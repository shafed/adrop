package daemon

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/shafed/adrop/internal/config"
	"github.com/shafed/adrop/internal/ipc"
	"github.com/shafed/adrop/internal/notify"
	"github.com/shafed/adrop/internal/pairing"
	"github.com/shafed/adrop/internal/proto"
	"github.com/shafed/adrop/internal/transport"
)

// Tap pairing (SPEC §4.2): the two devices meet over TLS without pinning,
// both screens show pairing.Code of the two fingerprints, and each user
// confirms on their own device. A device is saved only when both said yes.

// tapPairTimeout bounds how long a tap pairing waits for the two answers.
const tapPairTimeout = 2 * time.Minute

var (
	errPairDeclined     = errors.New("pairing declined on this device")
	errPeerDeclined     = errors.New("pairing declined on the other device")
	errPairTimeout      = errors.New("pairing timed out waiting for an answer")
	errNoPendingPairing = errors.New("no such pairing is waiting for an answer")
)

// pendingPair is one tap pairing between the code exchange and the outcome.
type pendingPair struct {
	info   ipc.PairInfo
	decide chan bool     // the local user's answer; buffered, first wins
	done   chan struct{} // closed once err is set
	err    error         // nil when the device was saved
}

// allowTapPair reports whether the peer listener admits tap-pairing
// connections (Options.TapPair).
func (d *Daemon) allowTapPair() bool { return d.tapPair }

// PendingPairs lists the tap pairings waiting for the local user's answer.
func (d *Daemon) PendingPairs() []ipc.PairInfo {
	d.tapMu.Lock()
	defer d.tapMu.Unlock()
	var out []ipc.PairInfo
	for _, p := range d.tapPairs {
		out = append(out, p.info)
	}
	return out
}

// ConfirmPair delivers the local user's answer to a tap pairing and waits
// for its outcome: nil means the device is now trusted on this side.
func (d *Daemon) ConfirmPair(ctx context.Context, id string, ok bool) (ipc.PairInfo, error) {
	d.tapMu.Lock()
	p := d.tapPairs[id]
	d.tapMu.Unlock()
	if p == nil {
		return ipc.PairInfo{}, errNoPendingPairing
	}
	select {
	case p.decide <- ok:
	default:
	}
	select {
	case <-p.done:
		return p.info, p.err
	case <-ctx.Done():
		return p.info, ctx.Err()
	}
}

// PairWith starts a tap pairing with the device at addr. onCode is called
// once with the pairing to show (its ID is what ConfirmPair takes); PairWith
// returns when both sides answered, or on a refusal or timeout.
func (d *Daemon) PairWith(ctx context.Context, addr string, onCode func(ipc.PairInfo)) (config.Device, error) {
	if !d.tapPair {
		return config.Device{}, errors.New("tap pairing is off on this device (ADROP_TAP_PAIR=1 turns it on)")
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, fp, err := transport.DialPair(dialCtx, addr, d.store.Certificate())
	cancel()
	if err != nil {
		return config.Device{}, err
	}
	defer conn.Close()
	if fp == d.store.Fingerprint() {
		return config.Device{}, errors.New("that is this device")
	}
	if err := d.sendPairRequest(conn); err != nil {
		return config.Device{}, err
	}
	peer, err := readPairRequest(conn)
	if err != nil {
		return config.Device{}, err
	}
	return d.tapPairSession(ctx, conn, peer, fp, onCode)
}

// handleTapPair serves an inbound tap-pairing connection (ALPN PairALPN).
func (d *Daemon) handleTapPair(ctx context.Context, conn *tls.Conn, fp string) {
	peer, err := readPairRequest(conn)
	if err != nil {
		d.logger.Printf("tap pairing: %v", err)
		return
	}
	if err := d.sendPairRequest(conn); err != nil {
		d.logger.Printf("tap pairing: %v", err)
		return
	}
	dev, err := d.tapPairSession(ctx, conn, peer, fp, func(p ipc.PairInfo) {
		d.logger.Printf("tap pairing request from %s (%s), code %s", p.Name, fp[:16], p.Code)
		go notify.Send(context.Background(), "Pairing request from "+p.Name,
			"Code "+p.Code+". Compare it with the other screen; answer with `adrop pair accept`.")
	})
	if err != nil {
		d.logger.Printf("tap pairing with %s: %v", peer.Name, err)
		return
	}
	d.logger.Printf("paired with %s (%s) by code", dev.Name, fp[:16])
}

func (d *Daemon) sendPairRequest(conn net.Conn) error {
	return proto.WriteControl(conn, proto.Header{
		Type:    proto.TypePairRequest,
		Version: proto.ProtocolVersion,
		Name:    d.name,
		Addr:    d.advertiseAddr(),
	})
}

func readPairRequest(conn net.Conn) (proto.Header, error) {
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	defer conn.SetReadDeadline(time.Time{})
	h, err := proto.ReadHeader(conn)
	if err != nil {
		return h, fmt.Errorf("read pair request: %w", err)
	}
	if h.Type != proto.TypePairRequest || h.Name == "" {
		return h, fmt.Errorf("expected a pair request, got %q", h.Type)
	}
	return h, nil
}

// tapPairSession runs the part both roles share: register the pairing, wait
// for the local and the remote answer, save the peer if both are yes.
func (d *Daemon) tapPairSession(ctx context.Context, conn net.Conn, peer proto.Header, fp string, onCode func(ipc.PairInfo)) (config.Device, error) {
	var idb [8]byte
	rand.Read(idb[:])
	p := &pendingPair{
		info: ipc.PairInfo{
			ID:          hex.EncodeToString(idb[:]),
			Name:        sanitizeName(peer.Name),
			Fingerprint: fp,
			Code:        pairing.Code(d.store.Fingerprint(), fp),
		},
		decide: make(chan bool, 1),
		done:   make(chan struct{}),
	}
	d.tapMu.Lock()
	d.tapPairs[p.info.ID] = p
	d.tapMu.Unlock()

	info := p.info
	d.broadcast(ipc.Event{Kind: "pair-request", Peer: info.Name, Pair: &info})
	onCode(info)

	dev, err := d.awaitAnswers(ctx, conn, p, peer.Addr)
	d.tapMu.Lock()
	delete(d.tapPairs, p.info.ID)
	d.tapMu.Unlock()
	p.err = err
	close(p.done)
	if err != nil {
		d.broadcast(ipc.Event{Kind: "pair-error", Peer: info.Name, Pair: &info, Err: err.Error()})
	} else {
		d.broadcast(ipc.Event{Kind: "pair-done", Peer: info.Name, Pair: &info})
	}
	return dev, err
}

func (d *Daemon) awaitAnswers(ctx context.Context, conn net.Conn, p *pendingPair, peerAddr string) (config.Device, error) {
	remote := make(chan bool, 1)
	go func() {
		h, err := proto.ReadHeader(conn)
		remote <- err == nil && h.Type == proto.TypePairConfirm && h.OK
	}()
	timer := time.NewTimer(tapPairTimeout)
	defer timer.Stop()

	decide := p.decide
	localYes, remoteYes := false, false
	for !localYes || !remoteYes {
		select {
		case ok := <-decide:
			decide = nil // one answer per pairing
			if err := proto.WriteControl(conn, proto.Header{Type: proto.TypePairConfirm, OK: ok}); err != nil {
				return config.Device{}, err
			}
			if !ok {
				return config.Device{}, errPairDeclined
			}
			localYes = true
		case ok := <-remote:
			if !ok {
				return config.Device{}, errPeerDeclined
			}
			remoteYes = true
		case <-timer.C:
			return config.Device{}, errPairTimeout
		case <-ctx.Done():
			return config.Device{}, ctx.Err()
		}
	}
	dev := config.Device{
		Name:        p.info.Name,
		Fingerprint: p.info.Fingerprint,
		Addr:        d.resolvePeerAddr(peerAddr, conn.RemoteAddr()),
		PairedAt:    time.Now(),
	}
	if err := d.store.AddDevice(dev); err != nil {
		return config.Device{}, fmt.Errorf("save device: %w", err)
	}
	return dev, nil
}
