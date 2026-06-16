package daemon

import (
	"bufio"
	"context"
	"encoding/json"
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
		send(ipc.Response{Status: &ipc.StatusInfo{
			Name:        d.name,
			Fingerprint: d.store.Fingerprint(),
			ListenAddr:  d.tcpAddr,
			NumDevices:  len(d.store.Devices()),
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
		if err := d.SendFiles(ctx, req.Target, req.Files, progress); err != nil {
			send(ipc.Response{Err: err.Error(), Done: true})
			return
		}
		send(ipc.Response{Done: true})

	case ipc.CmdSendClip:
		data := []byte(req.Text)
		if len(data) == 0 {
			var err error
			data, err = d.clipboardGet(ctx)
			if err != nil {
				send(ipc.Response{Err: "read clipboard: " + err.Error(), Done: true})
				return
			}
		}
		if err := d.SendClipboard(ctx, req.Target, data, "text/plain"); err != nil {
			send(ipc.Response{Err: err.Error(), Done: true})
			return
		}
		send(ipc.Response{Line: "clipboard sent", Done: true})

	default:
		send(ipc.Response{Err: "unknown command: " + string(req.Cmd), Done: true})
	}
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
