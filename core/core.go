// Package core is the adrop core embedded in a UI process: the same daemon
// code the PC runs, exposed through a small API that gomobile can bind for
// Android and core/cshared exports to dart:ffi on Linux.
//
// Only types gomobile understands cross this boundary: strings, errors and
// pointers to exported structs.
package core

import (
	"context"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/shafed/adrop/internal/config"
	"github.com/shafed/adrop/internal/daemon"
)

// helloTimeout bounds one Hello round trip, including the daemon's mDNS
// address recovery, so a UI button never hangs on an unreachable peer.
const helloTimeout = 20 * time.Second

// Node is one running core: this device's identity and its trusted peers.
type Node struct {
	store *config.Store
	d     *daemon.Daemon
}

// Start opens (or creates on first run) the identity and peer list in
// dataDir and returns a core that can pair and talk to peers. It does not
// listen for inbound connections.
func Start(dataDir, name string) (*Node, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("core: empty data dir")
	}
	store, err := config.Open(dataDir)
	if err != nil {
		return nil, err
	}
	d, err := daemon.New(daemon.Options{
		Store:       store,
		Name:        name,
		DownloadDir: filepath.Join(dataDir, "Downloads"),
		Logger:      log.New(io.Discard, "", 0),
	})
	if err != nil {
		return nil, err
	}
	return &Node{store: store, d: d}, nil
}

// Fingerprint is this device's certificate fingerprint.
func (n *Node) Fingerprint() string { return n.store.Fingerprint() }

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
	ctx, cancel := context.WithTimeout(context.Background(), helloTimeout)
	defer cancel()
	if _, err := n.d.Hello(ctx, dev.Fingerprint); err != nil {
		n.store.RemoveDevice(dev.Fingerprint)
		return "", fmt.Errorf("%s did not accept this device (is `adrop pair show` still waiting there?): %w", dev.Name, err)
	}
	return dev.Name, nil
}

// Peers lists the names of trusted devices, one per line.
func (n *Node) Peers() string {
	var names []string
	for _, dev := range n.store.Devices() {
		names = append(names, dev.Name)
	}
	return strings.Join(names, "\n")
}

// Hello connects to a trusted peer (name or fingerprint prefix) and exchanges
// Hello. It returns "name fingerprint" of the peer that answered.
func (n *Node) Hello(peer string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), helloTimeout)
	defer cancel()
	dev, err := n.d.Hello(ctx, peer)
	if err != nil {
		return "", err
	}
	return dev.Name + " " + dev.Fingerprint, nil
}
