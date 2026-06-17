package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/shafed/adrop/internal/ipc"
)

// handleIPC serves one CLI connection: read a Request, stream Responses.
func (d *Daemon) handleIPC(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(bufio.NewReader(conn))
	enc := json.NewEncoder(conn)

	var req ipc.Request
	if err := dec.Decode(&req); err != nil {
		_ = enc.Encode(ipc.Response{Err: "decode request: " + err.Error(), Done: true})
		return
	}

	send := func(r ipc.Response) { _ = enc.Encode(r) }
	progress := func(line string) { send(ipc.Response{Line: line}) }

	switch req.Cmd {
	case ipc.CmdStatus:
		lastPeerName := ""
		if fp := d.store.LastPeer(); fp != "" {
			if dev, ok := d.store.Lookup(fp); ok {
				lastPeerName = dev.Name
			}
		}
		send(ipc.Response{Status: &ipc.StatusInfo{
			Name:        d.name,
			Fingerprint: d.store.Fingerprint(),
			ListenAddr:  d.tcpAddr,
			NumDevices:  len(d.store.Devices()),
			LastPeer:    lastPeerName,
		}, Done: true})

	case ipc.CmdPairShow:
		uri, err := d.PairingURI()
		if err != nil {
			send(ipc.Response{Err: err.Error(), Done: true})
			return
		}
		art, err := renderQR(uri)
		if err != nil {
			send(ipc.Response{Err: err.Error(), Done: true})
			return
		}
		// Arm a pairing window admitting one inbound peer, held for as long as
		// the CLI keeps this connection open (the user is looking at the QR).
		cancelWin := d.OpenPairWindow("", "", 5*time.Minute)
		defer cancelWin()
		send(ipc.Response{QR: art, Line: uri})
		send(ipc.Response{Line: "Scan with the phone (or run `adrop pair add <uri>` on a peer). Waiting…"})
		d.waitPairOrDisconnect(ctx, conn, send)
		return

	case ipc.CmdPairAdd:
		dev, err := d.AddPeer(req.PairURI)
		if err != nil {
			send(ipc.Response{Err: err.Error(), Done: true})
			return
		}
		send(ipc.Response{Line: "paired with " + dev.Name + " (" + dev.Fingerprint[:16] + ")", Done: true})

	case ipc.CmdDevices:
		var out []ipc.DeviceInfo
		for _, dev := range d.store.Devices() {
			out = append(out, ipc.DeviceInfo{
				Name:        dev.Name,
				Fingerprint: dev.Fingerprint,
				Addr:        dev.Addr,
				PairedAt:    dev.PairedAt.Format(time.RFC3339),
			})
		}
		send(ipc.Response{Devices: out, Done: true})

	case ipc.CmdRevoke:
		n, err := d.store.RemoveDevice(req.Target)
		if err != nil {
			send(ipc.Response{Err: err.Error(), Done: true})
			return
		}
		if n == 0 {
			send(ipc.Response{Err: "no device matched " + req.Target, Done: true})
			return
		}
		send(ipc.Response{Line: "revoked", Done: true})

	case ipc.CmdSendFiles:
		target, err := d.resolveTarget(req.Target)
		if err != nil {
			send(ipc.Response{Err: err.Error(), Done: true})
			return
		}
		ctx, cancel := cancelOnDisconnect(ctx, conn)
		defer cancel()
		if err := d.SendFiles(ctx, target, req.Files, progress); err != nil {
			send(ipc.Response{Err: err.Error(), Done: true})
			return
		}
		send(ipc.Response{Done: true})

	case ipc.CmdSendClip:
		target, err := d.resolveTarget(req.Target)
		if err != nil {
			send(ipc.Response{Err: err.Error(), Done: true})
			return
		}
		mime := req.MIME
		if mime == "" {
			mime = "text/plain"
		}
		data := []byte(req.Text)
		if len(data) == 0 {
			data, err = d.clipboardGet(ctx, mime)
			if err != nil {
				send(ipc.Response{Err: "read clipboard: " + err.Error(), Done: true})
				return
			}
		}
		ctx, cancel := cancelOnDisconnect(ctx, conn)
		defer cancel()
		if err := d.SendClipboard(ctx, target, data, mime); err != nil {
			send(ipc.Response{Err: err.Error(), Done: true})
			return
		}
		send(ipc.Response{Line: "clipboard sent", Done: true})

	case ipc.CmdSubscribe:
		d.streamEvents(ctx, conn, send)

	default:
		send(ipc.Response{Err: "unknown command: " + string(req.Cmd), Done: true})
	}
}

// streamEvents serves a long-lived CmdSubscribe connection: it registers a
// subscriber and forwards each broadcast Event as Response{Event: e} until the
// client disconnects or the daemon shuts down. Unlike every other IPC arm it
// loops indefinitely and never sets Done (until shutdown).
func (d *Daemon) streamEvents(ctx context.Context, conn net.Conn, send func(ipc.Response)) {
	events, unsub := d.subscribe()
	defer unsub()

	// Detect client disconnect by watching for EOF on the connection. The GUI
	// never writes on this stream after the initial request, so any read result
	// (EOF or stray byte) means it's time to stop.
	gone := make(chan struct{})
	go func() {
		var buf [1]byte
		_, _ = conn.Read(buf[:])
		close(gone)
	}()

	for {
		select {
		case e, ok := <-events:
			if !ok {
				return
			}
			ev := e
			send(ipc.Response{Event: &ev})
		case <-gone:
			return
		case <-ctx.Done():
			send(ipc.Response{Done: true})
			return
		}
	}
}

// cancelOnDisconnect returns a child context that is cancelled when the CLI
// closes its IPC connection (e.g. the user hits Ctrl-C on `adrop send`). It
// spawns a reader on conn; once no more responses will be read by the client
// the only event on the socket is its close, which unblocks the Read and
// cancels. Use this only for commands that do not otherwise read from conn,
// to avoid concurrent reads on the same connection.
func cancelOnDisconnect(parent context.Context, conn net.Conn) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		var buf [1]byte
		_, _ = conn.Read(buf[:]) // returns on client close (EOF) or any stray byte
		cancel()
	}()
	return ctx, cancel
}

// resolveTarget returns target if non-empty, or falls back to the last-used
// peer fingerprint. Returns an error if neither is available.
func (d *Daemon) resolveTarget(target string) (string, error) {
	if target != "" {
		return target, nil
	}
	fp := d.store.LastPeer()
	if fp == "" {
		return "", fmt.Errorf("no target specified and no previous send on record; use: adrop send <peer> ...")
	}
	dev, ok := d.store.Lookup(fp)
	if !ok {
		return "", fmt.Errorf("last-used device is no longer paired; use: adrop send <peer> ...")
	}
	return dev.Name, nil
}

// waitPairOrDisconnect blocks during a pair-show until pairing completes, the
// CLI disconnects, or the daemon shuts down, then sends a final Done response.
func (d *Daemon) waitPairOrDisconnect(ctx context.Context, conn net.Conn, send func(ipc.Response)) {
	done, ps := d.pairWindowDone()
	if done == nil {
		send(ipc.Response{Done: true})
		return
	}
	// Detect CLI disconnect by watching for EOF on the control connection.
	closed := make(chan struct{})
	go func() {
		buf := make([]byte, 1)
		_, _ = conn.Read(buf) // blocks until CLI closes/sends
		close(closed)
	}()

	select {
	case <-done:
		msg := "pairing complete"
		if ps.paired != "" {
			msg = "paired with " + ps.paired
		}
		send(ipc.Response{Line: msg, Done: true})
	case <-closed:
		// CLI went away; let the deferred cancel close the window.
	case <-ctx.Done():
		send(ipc.Response{Line: "daemon shutting down", Done: true})
	}
}
