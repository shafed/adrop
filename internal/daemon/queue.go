package daemon

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/shafed/adrop/internal/config"
	"github.com/shafed/adrop/internal/ipc"
	"github.com/shafed/adrop/internal/notify"
	"github.com/shafed/adrop/internal/rns"
)

// Outside the LAN without a direct channel (SPEC §4.3: both devices behind
// symmetric NATs), files cannot go through the hubs. They wait here, with
// the reason, and go as soon as a channel comes up: the queue is retried
// periodically and whenever the device signals that it is online. A short
// clipboard goes through the hubs right away; a long one waits like files.

const (
	queueFile  = "queue.json"
	queueRetry = 2 * time.Minute

	// hubClipMax is the largest clipboard sent through the hubs: a handful of
	// Reticulum packets, not a stream (hubs are volunteers' links).
	hubClipMax = 4 << 10
	// hubClipPart is the raw bytes per packet: base64 makes it 184, and the
	// JSON around it stays under 63, inside rns.MaxMessage (247).
	hubClipPart = 138
)

// queued is one send waiting for a channel to its device.
type queued struct {
	ID     string    `json:"id"`
	Peer   string    `json:"peer"` // fingerprint
	Name   string    `json:"name"`
	Paths  []string  `json:"paths,omitempty"`
	Clip   []byte    `json:"clip,omitempty"`
	Added  time.Time `json:"added"`
	Reason string    `json:"reason"`
	Tries  int       `json:"tries"`
}

func (d *Daemon) loadQueue() []queued {
	raw, err := os.ReadFile(d.store.Path(queueFile))
	if err != nil {
		return nil
	}
	var q []queued
	json.Unmarshal(raw, &q)
	return q
}

func (d *Daemon) saveQueueLocked(q []queued) {
	raw, _ := json.MarshalIndent(q, "", "  ")
	tmp := d.store.Path(queueFile + ".tmp")
	if os.WriteFile(tmp, raw, 0o600) == nil {
		os.Rename(tmp, d.store.Path(queueFile))
	}
}

func (d *Daemon) enqueue(item queued) queued {
	var idb [6]byte
	rand.Read(idb[:])
	item.ID = hex.EncodeToString(idb[:])
	item.Added = time.Now()
	d.queueMu.Lock()
	d.saveQueueLocked(append(d.loadQueue(), item))
	d.queueMu.Unlock()
	d.logger.Printf("queued for %s: %s", item.Name, item.Reason)
	return item
}

// Queue lists the sends waiting for a channel.
func (d *Daemon) Queue() []ipc.QueueItem {
	d.queueMu.Lock()
	q := d.loadQueue()
	d.queueMu.Unlock()
	var out []ipc.QueueItem
	for _, it := range q {
		what := fmt.Sprintf("%d file(s)", len(it.Paths))
		if it.Clip != nil {
			what = fmt.Sprintf("clipboard (%d bytes)", len(it.Clip))
		}
		out = append(out, ipc.QueueItem{
			ID: it.ID, Peer: it.Name, What: what, Paths: it.Paths,
			Added: it.Added.Format(time.RFC3339), Reason: it.Reason, Tries: it.Tries,
		})
	}
	return out
}

// Unqueue drops a waiting send.
func (d *Daemon) Unqueue(id string) error {
	d.queueMu.Lock()
	defer d.queueMu.Unlock()
	q := d.loadQueue()
	for i, it := range q {
		if it.ID == id {
			d.saveQueueLocked(append(q[:i], q[i+1:]...))
			return nil
		}
	}
	return fmt.Errorf("nothing queued with id %q", id)
}

// kickQueue asks the queue runner for an early retry.
func (d *Daemon) kickQueue() {
	select {
	case d.queueKick <- struct{}{}:
	default:
	}
}

// runQueue retries the queue until ctx ends.
func (d *Daemon) runQueue(ctx context.Context) {
	tick := time.NewTicker(queueRetry)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-d.queueKick:
		}
		d.retryQueue(ctx)
	}
}

func (d *Daemon) retryQueue(ctx context.Context) {
	d.queueMu.Lock()
	q := d.loadQueue()
	d.queueMu.Unlock()
	for _, it := range q {
		if ctx.Err() != nil {
			return
		}
		var err error
		if it.Clip != nil {
			err = d.SendClipboard(ctx, it.Peer, it.Clip, "text/plain")
		} else {
			err = d.SendFiles(ctx, it.Peer, it.Paths, func(string) {})
		}
		d.queueMu.Lock()
		cur := d.loadQueue()
		for i := range cur {
			if cur[i].ID != it.ID {
				continue
			}
			var ue *unreachableError
			switch {
			case err == nil:
				cur = append(cur[:i], cur[i+1:]...)
				d.logger.Printf("queued send to %s went through", it.Name)
				d.broadcast(ipc.Event{Kind: "queue-sent", Peer: it.Name, Count: len(it.Paths)})
				go notify.Send(context.Background(), "Sent to "+it.Name, "A queued send went through.")
			case errors.As(err, &ue):
				cur[i].Tries++
				cur[i].Reason = d.waitReason(ue.dev, err)
			default:
				// The peer was reached and refused, or a file is gone: retrying
				// will not help.
				cur = append(cur[:i], cur[i+1:]...)
				d.logger.Printf("queued send to %s failed: %v", it.Name, err)
				d.broadcast(ipc.Event{Kind: "queue-error", Peer: it.Name, Err: err.Error()})
			}
			break
		}
		d.saveQueueLocked(cur)
		d.queueMu.Unlock()
	}
}

func (d *Daemon) waitReason(dev config.Device, err error) string {
	name := dev.Name
	if node, _ := d.rnsState(); node == nil || dev.RNS == "" {
		return fmt.Sprintf("%s cannot be reached now; it goes when %s is back on this network. Last error: %v", name, name, err)
	}
	return fmt.Sprintf("%s is outside this network and no direct channel came up "+
		"(both sides are probably behind strict NATs, e.g. two phones on mobile data); "+
		"it goes as soon as one of them is reachable, for example on Wi-Fi. Last error: %v", name, err)
}

// queueable reports whether a failed send may wait in the queue: the peer
// was not reached, and it can be reached outside the LAN later.
func (d *Daemon) queueable(err error) (config.Device, bool) {
	var ue *unreachableError
	if !errors.As(err, &ue) {
		return config.Device{}, false
	}
	if d.queueAll {
		return ue.dev, true
	}
	node, _ := d.rnsState()
	return ue.dev, node != nil && ue.dev.RNS != ""
}

// SendFilesOrQueue sends now, or queues the send when the device cannot be
// reached but may be later. It returns a non-empty reason when queued.
func (d *Daemon) SendFilesOrQueue(ctx context.Context, target string, paths []string, progress progressFn) (string, error) {
	err := d.SendFiles(ctx, target, paths, progress)
	dev, ok := d.queueable(err)
	if !ok {
		return "", err
	}
	it := d.enqueue(queued{Peer: dev.Fingerprint, Name: dev.Name, Paths: paths, Reason: d.waitReason(dev, err)})
	return it.Reason, nil
}

// SendClipboardAnywhere sends the clipboard directly, else through the hubs
// when it is short, else queues it. It says how it went.
func (d *Daemon) SendClipboardAnywhere(ctx context.Context, target string, data []byte, mime string) (string, error) {
	err := d.SendClipboard(ctx, target, data, mime)
	if err == nil {
		return "clipboard sent", nil
	}
	dev, ok := d.queueable(err)
	if !ok {
		return "", err
	}
	if len(data) <= hubClipMax && (mime == "" || mime == "text/plain") {
		if herr := d.sendClipOverHubs(ctx, dev, data); herr == nil {
			d.store.SetLastPeer(dev.Fingerprint)
			return "clipboard sent through reticulum hubs", nil
		} else {
			d.logger.Printf("clipboard through hubs to %s: %v", dev.Name, herr)
		}
	}
	it := d.enqueue(queued{Peer: dev.Fingerprint, Name: dev.Name, Clip: data, Reason: d.waitReason(dev, err)})
	return "queued: " + it.Reason, nil
}

func (d *Daemon) sendClipOverHubs(ctx context.Context, dev config.Device, data []byte) error {
	var idb [8]byte
	rand.Read(idb[:])
	id := hex.EncodeToString(idb[:])
	ack := make(chan struct{})
	d.pongMu.Lock()
	d.pongs[id] = ack
	d.pongMu.Unlock()
	defer func() {
		d.pongMu.Lock()
		delete(d.pongs, id)
		d.pongMu.Unlock()
	}()
	parts := (len(data) + hubClipPart - 1) / hubClipPart
	if parts == 0 {
		parts = 1
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	for n := 0; n < parts; n++ {
		end := min((n+1)*hubClipPart, len(data))
		part := base64.StdEncoding.EncodeToString(data[n*hubClipPart : end])
		if _, err := d.sendSignal(ctx, dev.Fingerprint, signal{Type: "clip", ID: id, N: n, Of: parts, Data: part}); err != nil {
			return err
		}
	}
	select {
	case <-ack:
		return nil
	case <-ctx.Done():
		return errors.New("no acknowledgement through the hubs")
	}
}

// clipParts collects a clipboard text arriving through the hubs.
type clipParts struct {
	parts [][]byte
	got   int
}

func (d *Daemon) receiveClipPart(ctx context.Context, dev config.Device, s signal) {
	if s.Of <= 0 || s.Of*hubClipPart > hubClipMax+hubClipPart || s.N < 0 || s.N >= s.Of {
		return
	}
	part, err := base64.StdEncoding.DecodeString(s.Data)
	if err != nil || len(part) > hubClipPart {
		return
	}
	key := dev.Fingerprint + "/" + s.ID
	d.clipMu.Lock()
	cp, ok := d.clips[key]
	if !ok {
		cp = &clipParts{parts: make([][]byte, s.Of)}
		d.clips[key] = cp
		time.AfterFunc(time.Minute, func() {
			d.clipMu.Lock()
			delete(d.clips, key)
			d.clipMu.Unlock()
		})
	}
	if len(cp.parts) != s.Of || cp.parts[s.N] != nil {
		d.clipMu.Unlock()
		return
	}
	cp.parts[s.N] = part
	cp.got++
	complete := cp.got == len(cp.parts)
	if complete {
		delete(d.clips, key)
	}
	d.clipMu.Unlock()
	if !complete {
		return
	}
	var text []byte
	for _, p := range cp.parts {
		text = append(text, p...)
	}
	if err := d.clipboardSet(ctx, text, "text/plain"); err != nil {
		d.logger.Printf("clipboard from %s (hubs): %v", dev.Name, err)
		return
	}
	d.logger.Printf("clipboard set from %s through the hubs (%d bytes)", dev.Name, len(text))
	d.broadcast(ipc.Event{Kind: "recv-done", Peer: dev.Name, File: "clipboard"})
	go notify.Send(context.Background(), "Clipboard from "+dev.Name, "Received through the Reticulum hubs.")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		d.sendSignal(ctx, dev.Fingerprint, signal{Type: "clip-ack", ID: s.ID})
	}()
}

func init() {
	// The largest clip signal must fit one Reticulum message.
	worst, _ := json.Marshal(signal{Type: "clip", ID: "0123456789abcdef", N: hubClipMax / hubClipPart, Of: hubClipMax/hubClipPart + 1,
		Data: base64.StdEncoding.EncodeToString(make([]byte, hubClipPart))})
	if len(worst) > rns.MaxMessage {
		panic(fmt.Sprintf("clip signal of %d bytes exceeds rns.MaxMessage %d", len(worst), rns.MaxMessage))
	}
}
