package daemon

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/shafed/adrop/internal/config"
	"github.com/shafed/adrop/internal/ipc"
	"github.com/shafed/adrop/internal/transport"
)

func newTapDaemon(t *testing.T, ctx context.Context, name string) *testDaemon {
	return newTestDaemonWith(t, ctx, name, func(o *Options) { o.TapPair = true })
}

// waitPending returns the first tap pairing waiting on td.
func waitPending(t *testing.T, td *testDaemon) ipc.PairInfo {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if p := td.d.PendingPairs(); len(p) > 0 {
			return p[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s: no pending pairing", td.d.name)
	return ipc.PairInfo{}
}

type pairResult struct {
	dev config.Device
	err error
}

// startPairWith runs a.PairWith(addr) in the background, answering ok on a's
// side as soon as the code is shown, and returns the code a showed.
func startPairWith(ctx context.Context, a *testDaemon, addr string, ok bool) (<-chan pairResult, <-chan ipc.PairInfo) {
	res := make(chan pairResult, 1)
	shown := make(chan ipc.PairInfo, 1)
	go func() {
		dev, err := a.d.PairWith(ctx, addr, func(p ipc.PairInfo) {
			shown <- p
			go a.d.ConfirmPair(ctx, p.ID, ok)
		})
		res <- pairResult{dev, err}
	}()
	return res, shown
}

func TestTapPairingSavesBothSides(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newTapDaemon(t, ctx, "a")
	b := newTapDaemon(t, ctx, "b")

	res, shown := startPairWith(ctx, a, b.d.tcpAddr, true)
	pending := waitPending(t, b)
	if pending.Name != "a" || pending.Fingerprint != a.store.Fingerprint() {
		t.Fatalf("b sees %+v, want a", pending)
	}
	if got := <-shown; got.Code != pending.Code {
		t.Fatalf("codes differ without a man in the middle: %s vs %s", got.Code, pending.Code)
	}
	if _, err := b.d.ConfirmPair(ctx, pending.ID, true); err != nil {
		t.Fatalf("b confirm: %v", err)
	}
	if r := <-res; r.err != nil || r.dev.Name != "b" {
		t.Fatalf("a pair: %+v", r)
	}
	if _, ok := a.store.IsTrusted(b.store.Fingerprint()); !ok {
		t.Fatal("a did not save b")
	}
	if _, ok := b.store.IsTrusted(a.store.Fingerprint()); !ok {
		t.Fatal("b did not save a")
	}
	// The pinned path works right away.
	if _, err := a.d.Hello(ctx, "b"); err != nil {
		t.Fatalf("hello after tap pairing: %v", err)
	}
}

func TestTapPairingRefusedOnOneSideSavesNeither(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newTapDaemon(t, ctx, "a")
	b := newTapDaemon(t, ctx, "b")

	res, _ := startPairWith(ctx, a, b.d.tcpAddr, true)
	pending := waitPending(t, b)
	if _, err := b.d.ConfirmPair(ctx, pending.ID, false); !errors.Is(err, errPairDeclined) {
		t.Fatalf("b decline: %v", err)
	}
	if r := <-res; !errors.Is(r.err, errPeerDeclined) {
		t.Fatalf("a after b declined: %v", r.err)
	}
	if _, ok := a.store.IsTrusted(b.store.Fingerprint()); ok {
		t.Fatal("a saved b although b declined")
	}
	if _, ok := b.store.IsTrusted(a.store.Fingerprint()); ok {
		t.Fatal("b saved a although b declined")
	}
}

func TestTapPairingOffRefusesUnpinnedPeers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newTapDaemon(t, ctx, "a")
	b := newTestDaemon(t, ctx, "b") // TapPair off, as by default

	res, _ := startPairWith(ctx, a, b.d.tcpAddr, true)
	if r := <-res; !errors.Is(r.err, transport.ErrNoTapPairing) {
		t.Fatalf("pairing with a device that has tap pairing off: %v", r.err)
	}
	if len(b.d.PendingPairs()) != 0 {
		t.Fatal("b registered a pairing although tap pairing is off")
	}
	if _, err := b.d.PairWith(ctx, a.d.tcpAddr, func(ipc.PairInfo) {}); err == nil {
		t.Fatal("PairWith ran with tap pairing off")
	}
}

// TestTapPairingManInTheMiddleChangesCodes puts a relay between a and b that
// terminates TLS on both sides with its own certificate, as an attacker on
// the LAN would. The two screens must then show different codes.
func TestTapPairingManInTheMiddleChangesCodes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newTapDaemon(t, ctx, "a")
	b := newTapDaemon(t, ctx, "b")

	mitm, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := transport.Listen("127.0.0.1:0", mitm.Certificate(), nobody{}, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		in, err := ln.Accept()
		if err != nil {
			return
		}
		defer in.Close()
		if err := in.(*tls.Conn).Handshake(); err != nil {
			return
		}
		out, _, err := transport.DialPair(ctx, b.d.tcpAddr, mitm.Certificate())
		if err != nil {
			return
		}
		defer out.Close()
		go io.Copy(out, in)
		io.Copy(in, out)
	}()

	res, shown := startPairWith(ctx, a, ln.Addr().String(), true)
	pending := waitPending(t, b)
	codeA := (<-shown).Code
	if codeA == pending.Code {
		t.Fatalf("both screens show %s through a man in the middle", codeA)
	}
	if pending.Fingerprint == a.store.Fingerprint() {
		t.Fatal("b saw a's fingerprint through the relay")
	}
	// The user sees the mismatch and declines on b.
	b.d.ConfirmPair(ctx, pending.ID, false)
	<-res
	if _, ok := b.store.IsTrusted(mitm.Fingerprint()); ok {
		t.Fatal("b trusted the man in the middle")
	}
}

type nobody struct{}

func (nobody) IsTrusted(string) (string, bool) { return "", false }
