// Package rns is adrop's Reticulum endpoint (SPEC §4.3): a device keeps a
// Reticulum identity, holds TCP connections to a few public hubs and is
// reachable at the hash of its "adrop.signal" destination. Paired devices
// exchange small signed messages through it: requests to transfer, ICE
// candidates, the clipboard.
//
// Messages are encrypted to the receiver's identity by Reticulum and signed by
// the sender's identity here, so the receiver knows which paired device sent
// them. The hubs see only who talks to whom and when.
package rns

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"git.quad4.io/Networks/Reticulum-Go/pkg/common"
	"git.quad4.io/Networks/Reticulum-Go/pkg/debug"
	"git.quad4.io/Networks/Reticulum-Go/pkg/destination"
	"git.quad4.io/Networks/Reticulum-Go/pkg/identity"
	"git.quad4.io/Networks/Reticulum-Go/pkg/packet"
	"git.quad4.io/Networks/Reticulum-Go/pkg/transport"
)

const (
	appName = "adrop"
	aspect  = "signal"

	// MaxMessage bounds one signed message: a single encrypted Reticulum
	// packet (packet.EncryptedMDU) minus the envelope (public key, time,
	// signature). A longer packet is dropped on the way, not refused.
	MaxMessage = packet.EncryptedMDU - pubLen - 8 - sigLen

	// maxAge rejects replayed messages.
	maxAge = 5 * time.Minute

	identityFile = "reticulum.id"
)

// quietLibrary silences Reticulum-Go's own logging (INFO and up by default)
// unless ADROP_RNS_DEBUG is set.
var quietLibrary sync.Once

// ErrNoPath means no hub knows a route to the peer yet: it is offline, or
// has not announced itself on any hub this device is connected to.
var ErrNoPath = errors.New("rns: no path to the peer")

// Message is one verified signal from a paired device.
type Message struct {
	From []byte // sender's public key (identity.GetPublicKey)
	Body []byte
}

// Node is this device's Reticulum endpoint.
type Node struct {
	tr   *transport.Transport
	id   *identity.Identity
	dest *destination.Destination

	mu      sync.Mutex
	hubs    map[string]*hubConn
	trusted func(pub []byte) bool
	inbox   chan Message
}

// Open loads (or creates on first run) the identity in dir and starts a
// transport with no hubs yet. trusted decides whose signed messages are
// accepted.
func Open(dir string, trusted func(pub []byte) bool) (*Node, error) {
	quietLibrary.Do(func() {
		if os.Getenv("ADROP_RNS_DEBUG") == "" {
			debug.SetDebugLevel(debug.DebugCritical)
		}
	})
	id, err := loadOrCreateIdentity(filepath.Join(dir, identityFile))
	if err != nil {
		return nil, err
	}
	cfg := common.NewReticulumConfig()
	cfg.EnableTransport = false
	// The transport keeps its own identity in ConfigPath, or in ~/.reticulum
	// when it is empty.
	cfg.ConfigPath = dir
	tr := transport.NewTransport(cfg)
	dest, err := destination.New(id, destination.In, destination.Single, appName, tr, aspect)
	if err != nil {
		tr.Close()
		return nil, err
	}
	n := &Node{
		tr:      tr,
		id:      id,
		dest:    dest,
		hubs:    make(map[string]*hubConn),
		trusted: trusted,
		inbox:   make(chan Message, 64),
	}
	dest.SetPacketCallback(func(data []byte, _ common.NetworkInterface) { n.receive(data) })
	return n, nil
}

func loadOrCreateIdentity(path string) (*identity.Identity, error) {
	if _, err := os.Stat(path); err == nil {
		return identity.FromFile(path)
	}
	// NewIdentity, not New: New leaves the identity hash unset, and the hash
	// is the salt of every key derived for packets to this device.
	id, err := identity.NewIdentity()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := id.ToFile(path); err != nil {
		return nil, err
	}
	return id, nil
}

// PublicKey is this device's Reticulum identity as peers pin it.
func (n *Node) PublicKey() []byte { return n.id.GetPublicKey() }

// Address is the hash peers send to, hex-encoded.
func (n *Node) Address() string { return hex.EncodeToString(n.dest.GetHash()) }

// AddressOf is the destination hash of the device with public key pub.
func AddressOf(pub []byte) ([]byte, error) {
	id := identity.FromPublicKey(pub)
	if id == nil {
		return nil, errors.New("rns: bad public key")
	}
	d, err := destination.New(id, destination.Out, destination.Single, appName, nullTransport{}, aspect)
	if err != nil {
		return nil, err
	}
	return d.GetHash(), nil
}

// ConnectHub adds a TCP connection to a hub ("host:port") and announces this
// device through it.
func (n *Node) ConnectHub(addr string) error {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.hubs[addr]; ok {
		return nil
	}
	iface := newHubConn(addr)
	if err := n.tr.RegisterInterface(addr, iface); err != nil {
		return err
	}
	if err := iface.Start(); err != nil {
		n.tr.UnregisterInterface(addr)
		return err
	}
	n.hubs[addr] = iface
	return nil
}

// DropHub closes the connection to a hub.
func (n *Node) DropHub(addr string) {
	n.mu.Lock()
	iface := n.hubs[addr]
	delete(n.hubs, addr)
	n.mu.Unlock()
	if iface != nil {
		iface.Stop()
		n.tr.UnregisterInterface(addr)
	}
}

// Hubs lists the hubs this node is connected to, and whether each link is up.
func (n *Node) Hubs() map[string]bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make(map[string]bool, len(n.hubs))
	for addr, iface := range n.hubs {
		out[addr] = iface.connected()
	}
	return out
}

// HubStats reports a hub's traffic counters, for measuring it.
func (n *Node) HubStats(addr string) (connected bool, rx, tx uint64, ok bool) {
	n.mu.Lock()
	iface := n.hubs[addr]
	n.mu.Unlock()
	if iface == nil {
		return false, 0, 0, false
	}
	return iface.connected(), iface.GetRxBytes(), iface.GetTxBytes(), true
}

// Announce tells the network where this device is. Hubs forget paths, so it
// is repeated periodically.
func (n *Node) Announce() error { return n.dest.Announce(false, nil, nil) }

// Inbox delivers verified messages from trusted devices.
func (n *Node) Inbox() <-chan Message { return n.inbox }

// Send signs body and sends it to the device with public key peer. It asks
// the hubs for a path first and fails with ErrNoPath if none came in time.
func (n *Node) Send(ctx context.Context, peer []byte, body []byte) error {
	if len(body) > MaxMessage {
		return fmt.Errorf("rns: message of %d bytes exceeds %d", len(body), MaxMessage)
	}
	hash, err := AddressOf(peer)
	if err != nil {
		return err
	}
	if err := n.waitPath(ctx, hash); err != nil {
		return err
	}
	out, err := destination.New(identity.FromPublicKey(peer), destination.Out, destination.Single, appName, n.tr, aspect)
	if err != nil {
		return err
	}
	sealed, err := out.Encrypt(n.envelope(body))
	if err != nil {
		return err
	}
	p := packet.NewPacket(destination.Single, sealed, packet.PacketTypeData, packet.ContextNone,
		0, packet.HeaderType1, nil, false, packet.FlagUnset)
	p.DestinationHash = hash
	return n.tr.SendPacket(p)
}

func (n *Node) waitPath(ctx context.Context, hash []byte) error {
	for i := 0; !n.tr.HasPath(hash); i++ {
		if i%20 == 0 { // every 2 s
			for name := range n.Hubs() {
				n.tr.RequestPath(hash, name, nil, false)
			}
		}
		select {
		case <-ctx.Done():
			return ErrNoPath
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil
}

// envelope: public key (64) | unix time (8) | body | signature (64) over
// everything before it.
func (n *Node) envelope(body []byte) []byte {
	var buf bytes.Buffer
	buf.Write(n.id.GetPublicKey())
	binary.Write(&buf, binary.BigEndian, time.Now().Unix())
	buf.Write(body)
	sig, _ := n.id.Sign(buf.Bytes())
	buf.Write(sig)
	return buf.Bytes()
}

const (
	pubLen = 64
	sigLen = 64
)

func (n *Node) receive(data []byte) {
	if len(data) < pubLen+8+sigLen {
		return
	}
	signed, sig := data[:len(data)-sigLen], data[len(data)-sigLen:]
	pub := signed[:pubLen]
	sender := identity.FromPublicKey(pub)
	if sender == nil || !sender.Verify(signed, sig) {
		return
	}
	at := time.Unix(int64(binary.BigEndian.Uint64(signed[pubLen:pubLen+8])), 0)
	if d := time.Since(at); d > maxAge || d < -maxAge {
		return
	}
	if n.trusted != nil && !n.trusted(pub) {
		return
	}
	msg := Message{From: append([]byte(nil), pub...), Body: append([]byte(nil), signed[pubLen+8:]...)}
	select {
	case n.inbox <- msg:
	default: // a full inbox drops, like a lost packet
	}
}

// Close drops every hub and stops the transport.
func (n *Node) Close() error {
	for addr := range n.Hubs() {
		n.DropHub(addr)
	}
	return n.tr.Close()
}

// nullTransport lets AddressOf compute a hash without registering anything.
type nullTransport struct{}

func (nullTransport) GetConfig() *common.ReticulumConfig                { return common.NewReticulumConfig() }
func (nullTransport) GetInterfaces() map[string]common.NetworkInterface { return nil }
func (nullTransport) RegisterDestination([]byte, any)                   {}
