package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"time"

	"github.com/shafed/adrop/internal/config"
	"github.com/shafed/adrop/internal/direct"
	"github.com/shafed/adrop/internal/transport"
)

// A direct channel (SPEC §4.3, internal/direct) is set up with signals over
// Reticulum: "ice-offer" from the side that sends, "ice-answer" back, and
// "ice-cand" for each ICE candidate both ways, all with the same ID.

// iceInbox returns the signal queue of a direct-channel setup, creating it:
// candidates may overtake the offer on the way.
func (d *Daemon) iceInbox(id string) chan direct.Msg {
	d.iceMu.Lock()
	defer d.iceMu.Unlock()
	ch, ok := d.iceSetups[id]
	if !ok {
		ch = make(chan direct.Msg, 64)
		d.iceSetups[id] = ch
		time.AfterFunc(2*time.Minute, func() { d.dropIceInbox(id) })
	}
	return ch
}

func (d *Daemon) dropIceInbox(id string) {
	d.iceMu.Lock()
	delete(d.iceSetups, id)
	d.iceMu.Unlock()
}

func (d *Daemon) iceSignal(ctx context.Context, dev config.Device, id string) direct.Signal {
	return func(m direct.Msg) error {
		_, err := d.sendSignal(ctx, dev.Fingerprint, signal{Type: "ice-" + m.Kind, ID: id, Ufrag: m.Ufrag, Pwd: m.Pwd, Cand: m.Cand})
		return err
	}
}

// dialDirect opens a direct channel to dev and returns the session stream
// with the fingerprint the peer proved inside QUIC.
func (d *Daemon) dialDirect(ctx context.Context, dev config.Device) (net.Conn, string, error) {
	var idb [8]byte
	rand.Read(idb[:])
	id := hex.EncodeToString(idb[:])
	in := d.iceInbox(id)
	defer d.dropIceInbox(id)
	conn, err := direct.Dial(ctx, d.iceSignal(ctx, dev, id), in,
		transport.PinnedConfig(d.store.Certificate(), d, false), d.directOpt)
	if err != nil {
		return nil, "", err
	}
	fp := config.Fingerprint(conn.PeerCertificate())
	if fp != dev.Fingerprint {
		conn.Close()
		return nil, "", fmt.Errorf("direct channel reached %.16s instead of %s", fp, dev.Name)
	}
	return conn, fp, nil
}

// acceptDirect answers an offer from dev and serves the session that comes
// over the channel.
func (d *Daemon) acceptDirect(ctx context.Context, dev config.Device, s signal) {
	in := d.iceInbox(s.ID)
	defer d.dropIceInbox(s.ID)
	conn, err := direct.Accept(ctx, direct.Msg{Kind: "offer", Ufrag: s.Ufrag, Pwd: s.Pwd},
		d.iceSignal(ctx, dev, s.ID), in, transport.PinnedConfig(d.store.Certificate(), d, true), d.directOpt)
	if err != nil {
		d.logger.Printf("direct channel from %s: %v", dev.Name, err)
		return
	}
	defer conn.Close()
	fp := config.Fingerprint(conn.PeerCertificate())
	if fp != dev.Fingerprint {
		d.logger.Printf("direct channel: %s signalled, but %.16s connected", dev.Name, fp)
		return
	}
	d.logger.Printf("direct channel from %s", dev.Name)
	d.servePeer(ctx, conn, fp, false)
}
