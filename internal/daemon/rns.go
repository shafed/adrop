package daemon

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/shafed/adrop/internal/config"
	"github.com/shafed/adrop/internal/direct"
	"github.com/shafed/adrop/internal/rns"
)

// Outside the LAN (SPEC §4.3) paired devices meet through public Reticulum
// hubs. The daemon holds a few hub connections (rns.HubManager) and exchanges
// small signed messages with paired devices over them.

// signal is one message between paired devices over Reticulum.
type signal struct {
	Type string `json:"t"`            // "ping" | "pong" | "ice-offer" | "ice-answer" | "ice-cand"
	ID   string `json:"id,omitempty"` // ties a pong to its ping, ICE signals to their setup

	Ufrag string `json:"u,omitempty"` // ice-offer, ice-answer
	Pwd   string `json:"p,omitempty"`
	Cand  string `json:"c,omitempty"` // ice-cand
}

// startRNS opens the Reticulum endpoint and the hub manager. Failures are
// logged: the daemon keeps working in the LAN without them.
func (d *Daemon) startRNS(ctx context.Context) {
	node, err := rns.Open(d.store.Path("rns"), d.trustedRNS)
	if err != nil {
		d.logger.Printf("rns: %v", err)
		return
	}
	go func() { <-ctx.Done(); node.Close() }()
	hubs := d.rnsHubs
	if len(hubs) == 0 {
		hubs = rns.BuiltinHubs
	}
	m := rns.NewHubManager(node, hubs, rns.HubPolicy{}, d.logger.Printf)
	if len(d.rnsHubs) == 0 {
		go func() {
			catalog, err := rns.FetchCatalog(ctx, rns.CatalogURL, d.store.Path("rns/catalog.json"))
			if err != nil {
				d.logger.Printf("rns: hub catalog: %v", err)
				return
			}
			m.AddCandidates(catalog)
		}()
	}
	d.rnsMu.Lock()
	d.rnsNode, d.rnsHubMgr = node, m
	d.rnsMu.Unlock()
	d.logger.Printf("rns: address %s", node.Address())
	go m.Run(ctx)
	go d.rnsInbox(ctx, node)
}

func (d *Daemon) rnsState() (*rns.Node, *rns.HubManager) {
	d.rnsMu.Lock()
	defer d.rnsMu.Unlock()
	return d.rnsNode, d.rnsHubMgr
}

// rnsKey is this device's Reticulum public key for Hello, pairing and QR,
// or "" when Reticulum is off.
func (d *Daemon) rnsKey() string {
	node, _ := d.rnsState()
	if node == nil {
		return ""
	}
	return hex.EncodeToString(node.PublicKey())
}

// RNSStatus reports this device's Reticulum address and working hubs.
func (d *Daemon) RNSStatus() (addr string, hubs []string) {
	node, m := d.rnsState()
	if node == nil {
		return "", nil
	}
	return node.Address(), m.Working()
}

func (d *Daemon) trustedRNS(pub []byte) bool {
	_, ok := d.deviceByRNS(pub)
	return ok
}

func (d *Daemon) deviceByRNS(pub []byte) (config.Device, bool) {
	key := hex.EncodeToString(pub)
	for _, dev := range d.store.Devices() {
		if dev.RNS == key {
			return dev, true
		}
	}
	return config.Device{}, false
}

// Signal sends a signed message to a paired device through the hubs.
func (d *Daemon) sendSignal(ctx context.Context, target string, s signal) (config.Device, error) {
	node, _ := d.rnsState()
	if node == nil {
		return config.Device{}, errors.New("reticulum is off on this device (ADROP_RNS=1 turns it on)")
	}
	dev, ok := d.store.Lookup(target)
	if !ok {
		return dev, fmt.Errorf("no trusted device matching %q", target)
	}
	if dev.RNS == "" {
		return dev, fmt.Errorf("%s has not shared its Reticulum identity yet; connect to it once in the LAN", dev.Name)
	}
	pub, err := hex.DecodeString(dev.RNS)
	if err != nil {
		return dev, err
	}
	body, _ := json.Marshal(s)
	return dev, node.Send(ctx, pub, body)
}

// Ping sends a signed ping to a paired device over Reticulum and waits for
// its pong; it returns the round trip time.
func (d *Daemon) Ping(ctx context.Context, target string) (time.Duration, error) {
	id := fmt.Sprintf("%x", time.Now().UnixNano())
	pong := make(chan struct{})
	d.pongMu.Lock()
	d.pongs[id] = pong
	d.pongMu.Unlock()
	defer func() {
		d.pongMu.Lock()
		delete(d.pongs, id)
		d.pongMu.Unlock()
	}()
	start := time.Now()
	dev, err := d.sendSignal(ctx, target, signal{Type: "ping", ID: id})
	if err != nil {
		return 0, err
	}
	select {
	case <-pong:
		return time.Since(start), nil
	case <-ctx.Done():
		return 0, fmt.Errorf("no answer from %s over reticulum", dev.Name)
	}
}

func (d *Daemon) rnsInbox(ctx context.Context, node *rns.Node) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-node.Inbox():
			dev, ok := d.deviceByRNS(m.From)
			if !ok {
				continue
			}
			var s signal
			if err := json.Unmarshal(m.Body, &s); err != nil {
				d.logger.Printf("rns: bad signal from %s: %v", dev.Name, err)
				continue
			}
			if s.Type == "ping" || s.Type == "pong" || s.Type == "ice-offer" {
				d.logger.Printf("rns: %s from %s", s.Type, dev.Name)
			}
			switch s.Type {
			case "ping":
				go func() {
					ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
					defer cancel()
					if _, err := d.sendSignal(ctx, dev.Fingerprint, signal{Type: "pong", ID: s.ID}); err != nil {
						d.logger.Printf("rns: pong to %s: %v", dev.Name, err)
					}
				}()
			case "ice-offer":
				go d.acceptDirect(ctx, dev, s)
			case "ice-answer", "ice-cand":
				select {
				case d.iceInbox(s.ID) <- direct.Msg{Kind: s.Type[len("ice-"):], Ufrag: s.Ufrag, Pwd: s.Pwd, Cand: s.Cand}:
				default:
				}
			case "pong":
				d.pongMu.Lock()
				if ch, ok := d.pongs[s.ID]; ok {
					close(ch)
					delete(d.pongs, s.ID)
				}
				d.pongMu.Unlock()
			}
		}
	}
}

// rnsFields are the Daemon's Reticulum state, embedded to keep daemon.go
// readable.
type rnsFields struct {
	rnsEnabled bool
	rnsHubs    []string

	rnsMu     sync.Mutex
	rnsNode   *rns.Node
	rnsHubMgr *rns.HubManager

	pongMu sync.Mutex
	pongs  map[string]chan struct{}

	// iceSetups queue the signals of direct-channel setups by ID; directOpt
	// tunes them (tests: no STUN, loopback candidates).
	iceMu     sync.Mutex
	iceSetups map[string]chan direct.Msg
	directOpt direct.Options
}
