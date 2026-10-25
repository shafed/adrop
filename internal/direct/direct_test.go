package direct

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/shafed/adrop/internal/config"
	"github.com/shafed/adrop/internal/transport"
)

type pinned map[string]bool

func (p pinned) IsTrusted(fp string) (string, bool) { return "peer", p[fp] }

func TestDirectChannelCarriesPinnedSession(t *testing.T) {
	a, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	toA, toB := make(chan Msg, 64), make(chan Msg, 64)
	opt := Options{STUN: []string{}, Loopback: true, Timeout: 20 * time.Second}
	ctx := context.Background()

	type result struct {
		c   *Conn
		err error
	}
	acc := make(chan result, 1)
	go func() {
		offer := <-toB
		c, err := Accept(ctx, offer, func(m Msg) error { toA <- m; return nil }, toB,
			transport.PinnedConfig(b.Certificate(), pinned{a.Fingerprint(): true}, true), opt)
		acc <- result{c, err}
	}()
	ca, err := Dial(ctx, func(m Msg) error { toB <- m; return nil }, toA,
		transport.PinnedConfig(a.Certificate(), pinned{b.Fingerprint(): true}, false), opt)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ca.Close()
	if _, err := ca.Write([]byte("hello direct")); err != nil {
		t.Fatal(err)
	}
	r := <-acc
	if r.err != nil {
		t.Fatalf("accept: %v", r.err)
	}
	defer r.c.Close()
	buf := make([]byte, 12)
	if _, err := io.ReadFull(r.c, buf); err != nil || string(buf) != "hello direct" {
		t.Fatalf("read %q: %v", buf, err)
	}
	if config.Fingerprint(r.c.PeerCertificate()) != a.Fingerprint() {
		t.Fatal("accepting side saw the wrong certificate")
	}
}

func TestDirectChannelRefusesUntrustedCertificate(t *testing.T) {
	a, _ := config.Open(t.TempDir())
	b, _ := config.Open(t.TempDir())
	toA, toB := make(chan Msg, 64), make(chan Msg, 64)
	opt := Options{STUN: []string{}, Loopback: true, Timeout: 10 * time.Second}
	ctx := context.Background()
	go func() {
		offer := <-toB
		// b trusts nobody.
		Accept(ctx, offer, func(m Msg) error { toA <- m; return nil }, toB,
			transport.PinnedConfig(b.Certificate(), pinned{}, true), opt)
	}()
	c, err := Dial(ctx, func(m Msg) error { toB <- m; return nil }, toA,
		transport.PinnedConfig(a.Certificate(), pinned{b.Fingerprint(): true}, false), opt)
	if err == nil {
		// TLS 1.3: the client learns about the rejected certificate on use.
		c.Write([]byte("x"))
		buf := make([]byte, 1)
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err = c.Read(buf); err == nil {
			t.Fatal("a session ran with an untrusted certificate")
		}
	}
}
