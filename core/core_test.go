package core

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shafed/adrop/internal/config"
	"github.com/shafed/adrop/internal/ipc"
	"github.com/shafed/adrop/internal/pairing"
)

// fakePlatform answers every tap pairing with yes and records the rest.
type fakePlatform struct {
	mu     sync.Mutex
	clip   string
	events []ipc.Event
	node   *Node
}

func (f *fakePlatform) SetClipboard(text string) {
	f.mu.Lock()
	f.clip = text
	f.mu.Unlock()
}

func (f *fakePlatform) Event(raw string) {
	var e ipc.Event
	json.Unmarshal([]byte(raw), &e)
	f.mu.Lock()
	f.events = append(f.events, e)
	node := f.node
	f.mu.Unlock()
	if e.Kind == "pair-request" && node != nil {
		go node.ConfirmPair(e.Pair.ID, true)
	}
}

func (f *fakePlatform) has(kind string) (ipc.Event, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.events {
		if e.Kind == kind {
			return e, true
		}
	}
	return ipc.Event{}, false
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func startNode(t *testing.T, name string) (*Node, *fakePlatform, int) {
	t.Helper()
	p := &fakePlatform{}
	port := freePort(t)
	n, err := Start(&Options{
		DataDir: t.TempDir(), DownloadDir: t.TempDir(), Name: name, Port: port, TapPair: true,
	}, p)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.node = n
	p.mu.Unlock()
	t.Cleanup(n.Stop)
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(port))); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return n, p, port
}

func itoa(i int) string { return jsonInt(i) }

func jsonInt(i int) string { b, _ := json.Marshal(i); return string(b) }

func TestTapPairThenSendBetweenCores(t *testing.T) {
	a, _, _ := startNode(t, "phone")
	b, pb, portB := startNode(t, "tablet")

	name, err := a.PairWith("127.0.0.1:" + itoa(portB))
	if err != nil || name != "tablet" {
		t.Fatalf("pair: %q %v", name, err)
	}
	if !strings.Contains(b.Devices(), a.Fingerprint()) {
		t.Fatal("b did not save a")
	}

	src := filepath.Join(t.TempDir(), "photo.jpg")
	os.WriteFile(src, []byte("jpeg"), 0o644)
	paths, _ := json.Marshal([]string{src})
	if reason, err := a.SendFiles("tablet", string(paths)); err != nil || reason != "" {
		t.Fatalf("send: %q %v", reason, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if e, ok := pb.has("recv-file-done"); ok {
			if got, err := os.ReadFile(e.Path); err != nil || string(got) != "jpeg" {
				t.Fatalf("event path %q: %q %v", e.Path, got, err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no recv-file-done event with the file's path")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if how, err := a.SendText("tablet", "hi"); err != nil || how != "clipboard sent" {
		t.Fatalf("text: %q %v", how, err)
	}
	pb.mu.Lock()
	clip := pb.clip
	pb.mu.Unlock()
	if clip != "hi" {
		t.Fatalf("clipboard %q", clip)
	}
}

func TestPairDropsPeerThatDidNotAccept(t *testing.T) {
	pc, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	uri, err := pairing.Encode(pairing.Payload{
		Name:        "pc",
		Fingerprint: pc.Fingerprint(),
		CertPEM:     string(pc.CertPEM()),
		Addr:        "127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	n, _, _ := startNode(t, "proto")
	if _, err := n.Pair(uri); err == nil {
		t.Fatal("pair succeeded although the peer never answered")
	}
	if n.Devices() != "null" {
		t.Fatalf("an unconfirmed peer stayed trusted: %s", n.Devices())
	}
}
