package daemon

import (
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

// startRnsd runs the reference Reticulum daemon as a local hub, or skips
// the test without it (ADROP_RNSD or rnsd on PATH; pip install rns).
func startRnsd(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("ADROP_RNSD")
	if bin == "" {
		var err error
		if bin, err = exec.LookPath("rnsd"); err != nil {
			t.Skip("rnsd not found (set ADROP_RNSD or pip install rns)")
		}
	}
	port := freePort(t)
	dir := t.TempDir()
	cfg := fmt.Sprintf("[reticulum]\n  enable_transport = Yes\n  share_instance = No\n[interfaces]\n  [[hub]]\n    type = TCPServerInterface\n    enabled = yes\n    listen_ip = 127.0.0.1\n    listen_port = %d\n", port)
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--config", dir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	waitListening(t, "127.0.0.1", port)
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

func TestPingOverReticulumAfterLANPairing(t *testing.T) {
	hub := startRnsd(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	withRNS := func(o *Options) { o.Reticulum, o.RNSHubs = true, []string{hub} }
	a := newTestDaemonWith(t, ctx, "a", withRNS)
	b := newTestDaemonWith(t, ctx, "b", withRNS)
	a.d.startRNS(ctx)
	b.d.startRNS(ctx)

	// Pairing in the LAN carries the Reticulum keys in Hello.
	pair(t, a, b)
	devB, _ := a.store.Lookup("b")
	devA, _ := b.store.Lookup("a")
	if devB.RNS == "" || devB.RNS != b.d.rnsKey() || devA.RNS != a.d.rnsKey() {
		t.Fatalf("reticulum keys not exchanged: a has %q for b, b has %q for a", devB.RNS, devA.RNS)
	}

	pctx, pcancel := context.WithTimeout(ctx, 60*time.Second)
	defer pcancel()
	rtt, err := a.d.Ping(pctx, "b")
	if err != nil {
		t.Fatalf("ping over reticulum: %v", err)
	}
	t.Logf("ping a -> b over the hub: %v", rtt)
}
