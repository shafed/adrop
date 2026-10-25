package rns

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// startHub runs the reference Reticulum daemon (Python rnsd) as a transport
// hub with a TCP server, as the public hubs adrop connects to are. A hub made
// of Reticulum-Go does not stand in for it: its TCP server is one interface
// for all clients and forwards announces without its transport ID.
//
// rnsd is taken from ADROP_RNSD or PATH (pip install rns); without it the
// test is skipped.
func startHub(t *testing.T) string {
	t.Helper()
	rnsd := os.Getenv("ADROP_RNSD")
	if rnsd == "" {
		var err error
		if rnsd, err = exec.LookPath("rnsd"); err != nil {
			t.Skip("rnsd not found (set ADROP_RNSD or pip install rns)")
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	dir := t.TempDir()
	cfg := fmt.Sprintf(`[reticulum]
  enable_transport = Yes
  share_instance = No
[logging]
  loglevel = 2
[interfaces]
  [[hub]]
    type = TCPServerInterface
    enabled = yes
    listen_ip = 127.0.0.1
    listen_port = %d
`, port)
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(rnsd, "--config", dir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(15 * time.Second)
	for {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			c.Close()
			return addr
		}
		if time.Now().After(deadline) {
			t.Fatalf("rnsd never listened on %s", addr)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func openNode(t *testing.T, hub string, trusted func([]byte) bool) *Node {
	t.Helper()
	n, err := Open(t.TempDir(), trusted)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.Close() })
	if err := n.ConnectHub(hub); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !n.Hubs()[hub] {
		if time.Now().After(deadline) {
			t.Fatal("hub connection never came up")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return n
}

func TestSignedMessageThroughHub(t *testing.T) {
	hub := startHub(t)
	var a, c *Node
	b := openNode(t, hub, func(pub []byte) bool { return bytes.Equal(pub, a.PublicKey()) })
	a = openNode(t, hub, nil)
	c = openNode(t, hub, nil) // a device b never paired with
	if err := b.Announce(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.Send(ctx, b.PublicKey(), []byte("from a stranger")); err != nil {
		t.Fatalf("stranger send: %v", err)
	}
	if err := a.Send(ctx, b.PublicKey(), []byte("hello over the hub")); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case m := <-b.Inbox():
		if string(m.Body) != "hello over the hub" || !bytes.Equal(m.From, a.PublicKey()) {
			t.Fatalf("got %q from %x", m.Body, m.From[:8])
		}
	case <-ctx.Done():
		t.Fatal("message never arrived")
	}
}

func TestAddressOfMatchesNode(t *testing.T) {
	n, err := Open(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	addr, err := AddressOf(n.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", addr) != n.Address() {
		t.Fatalf("AddressOf %x, node listens on %s", addr, n.Address())
	}
}
