// Package core is the adrop core embedded in the phone app: the same daemon
// the PC runs (internal/daemon), with the platform doing what Go cannot do
// there — discovery through NsdManager, the clipboard, notifications and
// MediaStore. gomobile binds it for Android.
//
// Only types gomobile understands cross this boundary: strings, ints, bools,
// errors, pointers to exported structs and interfaces. Lists go as JSON.
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shafed/adrop/internal/config"
	"github.com/shafed/adrop/internal/daemon"
	"github.com/shafed/adrop/internal/ipc"
)

// Platform is implemented by the app (Kotlin).
type Platform interface {
	// SetClipboard puts received text on the system clipboard.
	SetClipboard(text string)
	// Event delivers a daemon event as JSON (ipc.Event): receives with the
	// landed file's path, pairing requests with the code, the queue.
	Event(json string)
}

// callTimeout bounds calls that reach another device, so a UI action never
// hangs on one that does not answer.
const callTimeout = 2 * time.Minute

// Node is one running core.
type Node struct {
	store  *config.Store
	d      *daemon.Daemon
	p      Platform
	cancel context.CancelFunc
}

// Options configure Start. TapPair and Reticulum switch the features of
// PLAN.md steps 2 and 3 on.
type Options struct {
	DataDir     string // identity, devices, queue
	DownloadDir string // where received files land before MediaStore
	Name        string
	Port        int // 0: the default port
	TapPair     bool
	Reticulum   bool
}

// Start opens (or creates on first run) the identity and starts receiving.
func Start(opt *Options, p Platform) (*Node, error) {
	if opt == nil || opt.DataDir == "" {
		return nil, errors.New("core: no data dir")
	}
	if p == nil {
		return nil, errors.New("core: no platform")
	}
	store, err := config.Open(opt.DataDir)
	if err != nil {
		return nil, err
	}
	d, err := daemon.New(daemon.Options{
		Store:       store,
		Name:        opt.Name,
		Port:        opt.Port,
		DownloadDir: opt.DownloadDir,
		TapPair:     opt.TapPair,
		Reticulum:   opt.Reticulum,
		// The phone's outbox: sends to a PC that is away wait for it.
		QueueUnreachable: true,
		Logger:           log.New(os.Stderr, "adrop: ", 0),
		ClipboardSet: func(_ context.Context, data []byte, _ string) error {
			p.SetClipboard(string(data))
			return nil
		},
		ClipboardGet: func(context.Context, string) ([]byte, error) {
			return nil, errors.New("pass the text to SendText")
		},
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	n := &Node{store: store, d: d, p: p, cancel: cancel}
	events, unsub := d.Subscribe()
	go func() {
		defer unsub()
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-events:
				n.emit(e)
			}
		}
	}()
	go func() {
		if err := d.Serve(ctx); err != nil && ctx.Err() == nil {
			n.emit(ipc.Event{Kind: "core-error", Err: err.Error()})
		}
	}()
	return n, nil
}

func (n *Node) emit(e ipc.Event) {
	raw, _ := json.Marshal(e)
	n.p.Event(string(raw))
}

func asJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func timeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), callTimeout)
}

// Stop ends receiving and closes the hub connections.
func (n *Node) Stop() { n.cancel() }

// Fingerprint is this device's certificate fingerprint.
func (n *Node) Fingerprint() string { return n.store.Fingerprint() }

// Name is the name this device announces.
func (n *Node) Name() string { return n.d.Name() }

// Status is ipc.StatusInfo as JSON.
func (n *Node) Status() string {
	addr, hubs := n.d.RNSStatus()
	return asJSON(ipc.StatusInfo{
		Name: n.d.Name(), Fingerprint: n.store.Fingerprint(),
		NumDevices: len(n.store.Devices()), RNSAddr: addr, RNSHubs: hubs,
	})
}

// PairingURI is this device's "adrop://pair?d=..." for its QR.
func (n *Node) PairingURI() (string, error) { return n.d.PairingURI() }

// OpenPairWindow lets the next unknown device that scans this device's QR
// pair with it, for ttlSeconds.
func (n *Node) OpenPairWindow(ttlSeconds int) {
	n.d.OpenPairWindow("", "", time.Duration(ttlSeconds)*time.Second)
}

// Pair trusts the device behind a scanned "adrop://pair?d=..." URI and
// connects back so it pins this device too. It returns the peer's name.
//
// AddPeer treats the back-connect as best effort; here a Hello must pass, or
// the peer is dropped again: a peer that never pinned this device answers
// every later Hello with "tls: bad certificate".
func (n *Node) Pair(uri string) (string, error) {
	dev, err := n.d.AddPeer(strings.TrimSpace(uri))
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := n.d.Hello(ctx, dev.Fingerprint); err != nil {
		n.store.RemoveDevice(dev.Fingerprint)
		return "", fmt.Errorf("%s did not accept this device (is its pairing QR still open?): %w", dev.Name, err)
	}
	return dev.Name, nil
}

// Devices is the trusted devices as JSON ([]ipc.DeviceInfo).
func (n *Node) Devices() string {
	var out []ipc.DeviceInfo
	for _, dev := range n.store.Devices() {
		out = append(out, ipc.DeviceInfo{
			Name: dev.Name, Fingerprint: dev.Fingerprint, Addr: dev.Addr,
			PairedAt: dev.PairedAt.Format(time.RFC3339),
		})
	}
	return asJSON(out)
}

// Revoke untrusts a device by name or fingerprint prefix.
func (n *Node) Revoke(target string) error {
	removed, err := n.store.RemoveDevice(target)
	if err == nil && removed == 0 {
		err = fmt.Errorf("no device matched %q", target)
	}
	return err
}

// Rename changes a device's display name.
func (n *Node) Rename(target, name string) error { return n.store.RenameDevice(target, name) }

// Nearby is the adrop devices seen on the LAN as JSON ([]ipc.NearbyInfo).
func (n *Node) Nearby() string { return asJSON(n.d.Nearby()) }

// AddNearby records a device NsdManager resolved; fp is its TXT "fp".
func (n *Node) AddNearby(name, host string, port int, fp string) {
	n.d.AddNearby(name, net.JoinHostPort(host, strconv.Itoa(port)), fp)
}

// RemoveNearby forgets a device NsdManager lost.
func (n *Node) RemoveNearby(name string) { n.d.RemoveNearby(name) }

// PairWith starts a tap pairing with a nearby device (its name in Nearby, or
// "host:port"). The code arrives as a "pair-request" event; answer it with
// ConfirmPair. It returns the paired device's name.
func (n *Node) PairWith(target string) (string, error) {
	addr := target
	for _, near := range n.d.Nearby() {
		if near.Name == target {
			addr = near.Addr
		}
	}
	ctx, cancel := timeout()
	defer cancel()
	dev, err := n.d.PairWith(ctx, addr, func(ipc.PairInfo) {})
	if err != nil {
		return "", err
	}
	return dev.Name, nil
}

// PendingPairs is the tap pairings waiting for an answer, as JSON.
func (n *Node) PendingPairs() string { return asJSON(n.d.PendingPairs()) }

// ConfirmPair answers a tap pairing and waits for its outcome.
func (n *Node) ConfirmPair(id string, ok bool) error {
	ctx, cancel := timeout()
	defer cancel()
	_, err := n.d.ConfirmPair(ctx, id, ok)
	return err
}

// SendFiles sends the files and folders in pathsJSON (a JSON array of
// paths) as one session. Progress arrives as "send-progress" events. A
// non-empty result means the send is queued, and says why.
func (n *Node) SendFiles(target, pathsJSON string) (string, error) {
	var paths []string
	if err := json.Unmarshal([]byte(pathsJSON), &paths); err != nil {
		return "", fmt.Errorf("core: paths: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	return n.d.SendFilesOrQueue(ctx, target, paths, func(line string) {
		n.emit(ipc.Event{Kind: "send-progress", Peer: target, File: line})
	})
}

// SendText pushes text to a device's clipboard; the result says how it went
// (directly, through the hubs, or queued).
func (n *Node) SendText(target, text string) (string, error) {
	ctx, cancel := timeout()
	defer cancel()
	return n.d.SendClipboardAnywhere(ctx, target, []byte(text), "text/plain")
}

// Queue is the sends waiting for a channel, as JSON ([]ipc.QueueItem).
func (n *Node) Queue() string { return asJSON(n.d.Queue()) }

// Unqueue drops a waiting send.
func (n *Node) Unqueue(id string) error { return n.d.Unqueue(id) }

// Ping sends a signed ping over Reticulum and reports the round trip.
func (n *Node) Ping(target string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	rtt, err := n.d.Ping(ctx, target)
	if err != nil {
		return "", err
	}
	return rtt.Round(time.Millisecond).String(), nil
}

// Hello connects to a trusted peer and exchanges Hello; it returns
// "name fingerprint" of the device that answered.
func (n *Node) Hello(peer string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dev, err := n.d.Hello(ctx, peer)
	if err != nil {
		return "", err
	}
	return dev.Name + " " + dev.Fingerprint, nil
}
