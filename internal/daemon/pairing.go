package daemon

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/shafed/adrop/internal/config"
	"github.com/shafed/adrop/internal/pairing"
	"github.com/shafed/adrop/internal/proto"
	"github.com/shafed/adrop/internal/transport"
)

// pairSession tracks an open pairing window. While open, the listener admits a
// single inbound connection from an as-yet-untrusted peer and pins it. This is
// the side showing the QR: it doesn't know the scanner's fingerprint ahead of
// time, so expectFingerprint is empty and any first untrusted peer is accepted.
type pairSession struct {
	expectFingerprint string // "" = accept any untrusted peer (QR-display side)
	expectName        string
	once              sync.Once
	done              chan struct{}

	// paired is set to the device name once a peer completes pairing in this
	// window; read only after done is closed.
	paired string
}

// renderQR renders a pairing URI as terminal QR art.
func renderQR(uri string) (string, error) {
	return pairing.RenderTerminal(uri)
}

// PairingURI builds this device's pairing payload + URI (for QR rendering).
func (d *Daemon) PairingURI() (string, error) {
	p := pairing.Payload{
		Version:     1,
		Name:        d.name,
		Fingerprint: d.store.Fingerprint(),
		CertPEM:     string(d.store.CertPEM()),
		Addr:        d.tcpAddr,
	}
	return pairing.Encode(p)
}

// AddPeer trusts a peer described by a scanned pairing URI and connects back to
// it to complete a mutual exchange (so the peer also pins us, and we confirm
// reachability). The peer is recorded immediately; the back-connect is best
// effort and reported via the returned message.
func (d *Daemon) AddPeer(uri string) (config.Device, error) {
	p, err := pairing.Decode(uri)
	if err != nil {
		return config.Device{}, err
	}
	dev := config.Device{
		Name:        p.Name,
		Fingerprint: p.Fingerprint,
		Addr:        p.Addr,
		PairedAt:    time.Now(),
	}
	if err := d.store.AddDevice(dev); err != nil {
		return config.Device{}, fmt.Errorf("save device: %w", err)
	}
	// Best-effort hello so the peer records us too. Once dev is in the store,
	// transport pinning already admits it.
	if err := d.helloPeer(dev); err != nil {
		// Non-fatal: peer may pair from its side, or be momentarily offline.
		d.logger.Printf("pairing back-connect to %s failed (non-fatal): %v", dev.Name, err)
	}
	return dev, nil
}

// helloPeer dials a peer and performs only the Hello exchange, which is enough
// for the remote daemon (if in a pairing window) to learn and pin our cert.
func (d *Daemon) helloPeer(dev config.Device) error {
	conn, fp, err := transport.Dial(dev.Addr, d.store.Certificate(), d)
	if err != nil {
		return err
	}
	defer conn.Close()
	if fp != dev.Fingerprint {
		return fmt.Errorf("peer fingerprint changed: %s", fp[:16])
	}
	if err := proto.WriteControl(conn, proto.Header{
		Type:        proto.TypeHello,
		Version:     proto.ProtocolVersion,
		Fingerprint: d.store.Fingerprint(),
		Name:        d.name,
		Addr:        d.tcpAddr,
	}); err != nil {
		return err
	}
	_, err = proto.ReadHeader(conn) // their hello
	return err
}

// OpenPairWindow arms a pairing window for the peer with the given fingerprint
// and name, so the listener will admit one inbound connection from it even
// before it is in the trusted store. Returns a cancel func.
func (d *Daemon) OpenPairWindow(fingerprint, name string, ttl time.Duration) func() {
	ps := &pairSession{
		expectFingerprint: fingerprint,
		expectName:        name,
		done:              make(chan struct{}),
	}
	d.pairMu.Lock()
	d.pairWindow = ps
	d.pairMu.Unlock()

	timer := time.AfterFunc(ttl, func() { d.closePairWindow(ps) })
	return func() {
		timer.Stop()
		d.closePairWindow(ps)
	}
}

func (d *Daemon) closePairWindow(ps *pairSession) {
	d.pairMu.Lock()
	if d.pairWindow == ps {
		d.pairWindow = nil
	}
	d.pairMu.Unlock()
	ps.once.Do(func() { close(ps.done) })
}

// tryCompletePairing records a peer that connected during an open pairing
// window. Returns true if this connection completed a pairing.
func (d *Daemon) tryCompletePairing(fp, name, advertisedAddr string, remote net.Addr) bool {
	d.pairMu.Lock()
	ps := d.pairWindow
	d.pairMu.Unlock()
	if ps == nil || (ps.expectFingerprint != "" && ps.expectFingerprint != fp) {
		return false
	}
	if _, already := d.store.IsTrusted(fp); already {
		return false
	}
	addr := d.resolvePeerAddr(advertisedAddr, remote)
	dev := config.Device{
		Name:        name,
		Fingerprint: fp,
		Addr:        addr,
		PairedAt:    time.Now(),
	}
	if err := d.store.AddDevice(dev); err != nil {
		d.logger.Printf("pairing: save device: %v", err)
		return false
	}
	ps.paired = name
	d.closePairWindow(ps)
	return true
}

// resolvePeerAddr decides the address to store for a peer, given what it
// advertised in its Hello and the live connection it arrived on.
//
// The peer's advertised port is authoritative (it's the port the peer listens
// on, which is not the same as the ephemeral source port of this connection).
// The host, however, is only trustworthy if the peer named a concrete IP. When
// the advertised host is missing or unspecified ("0.0.0.0"/"::") — e.g. the
// phone couldn't determine its own LAN IP — we substitute the connection's
// source IP but keep the advertised port. Only if no usable port was advertised
// at all do we fall back to our own default port against the source IP.
func (d *Daemon) resolvePeerAddr(advertised string, remote net.Addr) string {
	remoteHost, _, _ := net.SplitHostPort(remote.String())

	host, port, err := net.SplitHostPort(advertised)
	if err != nil || port == "" || port == "0" {
		// No usable advertised port: best we can do is source IP + our port.
		return net.JoinHostPort(remoteHost, fmt.Sprint(d.port))
	}
	if ip := net.ParseIP(host); host == "" || ip == nil || ip.IsUnspecified() {
		host = remoteHost
	}
	return net.JoinHostPort(host, port)
}

// pairWindowDone returns the done channel of the current pairing window (or a
// nil channel if none), letting callers block until pairing completes.
func (d *Daemon) pairWindowDone() (<-chan struct{}, *pairSession) {
	d.pairMu.Lock()
	defer d.pairMu.Unlock()
	if d.pairWindow == nil {
		return nil, nil
	}
	return d.pairWindow.done, d.pairWindow
}
