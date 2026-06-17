package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/shafed/adrop/internal/config"
)

// testDaemon is a running daemon plus its captured clipboard state.
type testDaemon struct {
	d        *Daemon
	store    *config.Store
	download string
	clipMu   sync.Mutex
	clip     []byte
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func newTestDaemon(t *testing.T, ctx context.Context, name string) *testDaemon {
	t.Helper()
	cfgDir := t.TempDir()
	dlDir := t.TempDir()
	store, err := config.Open(cfgDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	td := &testDaemon{store: store, download: dlDir}
	port := freePort(t)
	d, err := New(Options{
		Store:       store,
		Name:        name,
		Port:        port,
		AdvertiseIP: "127.0.0.1",
		ListenIP:    "127.0.0.1",
		DownloadDir: dlDir,
		Logger:      log.New(io.Discard, "", 0),
		ClipboardSet: func(_ context.Context, data []byte, _ string) error {
			td.clipMu.Lock()
			td.clip = append([]byte(nil), data...)
			td.clipMu.Unlock()
			return nil
		},
		ClipboardGet: func(_ context.Context, _ string) ([]byte, error) {
			td.clipMu.Lock()
			defer td.clipMu.Unlock()
			return append([]byte(nil), td.clip...), nil
		},
	})
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	td.d = d

	// Run only the TLS peer listener (skip the IPC socket so two daemons in
	// one process don't collide on a shared socket path).
	go func() {
		if err := d.runPeerListener(ctx); err != nil && ctx.Err() == nil {
			t.Errorf("%s listener: %v", name, err)
		}
	}()
	waitListening(t, "127.0.0.1", port)
	return td
}

func waitListening(t *testing.T, host string, port int) {
	t.Helper()
	addr := net.JoinHostPort(host, itoa(port))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("daemon never listened on %s", addr)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}

// pair makes a and b mutually trusted using the QR/back-connect flow.
func pair(t *testing.T, a, b *testDaemon) {
	t.Helper()
	// a shows its QR (arms a pairing window admitting one inbound peer).
	cancel := a.d.OpenPairWindow("", "", 10*time.Second)
	defer cancel()

	uri, err := a.d.PairingURI()
	if err != nil {
		t.Fatalf("pairing uri: %v", err)
	}
	// b scans a's QR: trusts a and back-connects (Hello), so a pins b.
	if _, err := b.d.AddPeer(uri); err != nil {
		t.Fatalf("add peer: %v", err)
	}

	// Wait for a to record b.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := a.store.IsTrusted(b.store.Fingerprint()); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := a.store.IsTrusted(b.store.Fingerprint()); !ok {
		t.Fatalf("%s did not pin %s after pairing", a.d.name, b.d.name)
	}
	if _, ok := b.store.IsTrusted(a.store.Fingerprint()); !ok {
		t.Fatalf("%s did not pin %s after pairing", b.d.name, a.d.name)
	}
}

func TestPairingAndFileTransfer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pc := newTestDaemon(t, ctx, "pc")
	phone := newTestDaemon(t, ctx, "phone")
	pair(t, pc, phone)

	// Build two source files with known content.
	srcDir := t.TempDir()
	f1 := filepath.Join(srcDir, "report.pdf")
	f2 := filepath.Join(srcDir, "photo.jpg")
	data1 := randomBytes(t, 700*1024) // spans multiple chunks
	data2 := []byte("small text file contents\n")
	mustWrite(t, f1, data1)
	mustWrite(t, f2, data2)

	// pc sends both files to phone as one session.
	if err := pc.d.SendFiles(ctx, "phone", []string{f1, f2}, nil); err != nil {
		t.Fatalf("send files: %v", err)
	}

	// phone should have both files in its download dir with identical content.
	assertFileEqual(t, filepath.Join(phone.download, "report.pdf"), data1)
	assertFileEqual(t, filepath.Join(phone.download, "photo.jpg"), data2)
}

func TestCollisionAutoRename(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pc := newTestDaemon(t, ctx, "pc")
	phone := newTestDaemon(t, ctx, "phone")
	pair(t, pc, phone)

	// Pre-create a colliding file on the receiver.
	mustWrite(t, filepath.Join(phone.download, "note.txt"), []byte("existing"))

	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "note.txt")
	payload := []byte("freshly sent")
	mustWrite(t, src, payload)

	if err := pc.d.SendFiles(ctx, "phone", []string{src}, nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	// Original untouched, new file auto-renamed.
	assertFileEqual(t, filepath.Join(phone.download, "note.txt"), []byte("existing"))
	assertFileEqual(t, filepath.Join(phone.download, "note (1).txt"), payload)
}

func TestClipboardPush(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pc := newTestDaemon(t, ctx, "pc")
	phone := newTestDaemon(t, ctx, "phone")
	pair(t, pc, phone)

	text := []byte("clipboard payload 123 ✓")
	if err := pc.d.SendClipboard(ctx, "phone", text, "text/plain"); err != nil {
		t.Fatalf("send clipboard: %v", err)
	}
	phone.clipMu.Lock()
	got := phone.clip
	phone.clipMu.Unlock()
	if !bytes.Equal(got, text) {
		t.Fatalf("clipboard mismatch: got %q want %q", got, text)
	}
}

func TestUntrustedPeerRejected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pc := newTestDaemon(t, ctx, "pc")
	stranger := newTestDaemon(t, ctx, "stranger")
	// No pairing. stranger manually trusts pc (one-sided) but pc never trusts
	// stranger, so pc must reject stranger's connection at the TLS layer.
	uri, _ := pc.d.PairingURI()
	if _, err := stranger.d.AddPeer(uri); err != nil {
		// AddPeer's back-connect Hello is expected to fail since pc has no
		// pairing window; the device is still recorded locally.
		t.Logf("expected back-connect failure: %v", err)
	}
	// stranger attempts to send to pc; pc has no record of stranger -> reject.
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "x.bin")
	mustWrite(t, src, []byte("hi"))
	err := stranger.d.SendFiles(ctx, "pc", []string{src}, nil)
	if err == nil {
		t.Fatalf("expected send to untrusted peer to fail")
	}
}

// --- helpers ---

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertFileEqual(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s: content mismatch (got %d bytes, want %d)", path, len(got), len(want))
	}
}
