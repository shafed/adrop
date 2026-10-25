package rns

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"git.quad4.io/Networks/Reticulum-Go/pkg/transport"
)

// fakeHub listens on localhost; closeNow makes it hang up right away (a
// hub that "accepts and closes"), otherwise it holds the connection and
// sends nothing (a hub that "holds and does not deliver").
func fakeHub(t *testing.T, closeNow bool) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			if closeNow {
				c.Close()
				continue
			}
			t.Cleanup(func() { c.Close() })
		}
	}()
	return ln.Addr().String()
}

type logBuf struct {
	mu    sync.Mutex
	lines []string
}

func (l *logBuf) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, strings.TrimSpace(fmt.Sprintf(format, args...)))
}

func (l *logBuf) has(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestHubManagerReplacesBadHubs(t *testing.T) {
	good := startHub(t)
	closer := fakeHub(t, true)
	silent := fakeHub(t, false)

	// Another device on the good hub keeps announcing, so the hub relays data.
	other := openNode(t, good, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			other.Announce()
			time.Sleep(time.Second)
		}
	}()

	n, err := Open(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	var log logBuf
	m := NewHubManager(n, []string{closer, silent, good}, HubPolicy{
		Want:         3,
		Check:        200 * time.Millisecond,
		ConnectGrace: time.Second,
		Silence:      5 * time.Second,
		Backoff:      time.Hour,
	}, log.logf)
	go m.Run(ctx)

	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if log.has("dropping hub "+closer) && log.has("dropping hub "+silent) && slices.Equal(m.Working(), []string{good}) {
			if _, connected := n.Hubs()[closer]; connected {
				t.Fatal("a dropped hub is still attached")
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("working %v, log %q", m.Working(), log.lines)
}

func TestParseCatalogKeepsOnlineClearnetTCP(t *testing.T) {
	raw := []byte(`{"data":[
		{"type":"tcp","network":"clearnet","host":"a.example","port":4242,"status":"online"},
		{"type":"backbone","network":"clearnet","host":"b.example","port":4343,"status":"online"},
		{"type":"tcp","network":"clearnet","host":"c.example","port":4242,"status":"offline"},
		{"type":"i2p","network":"i2p","host":"d.i2p","port":0,"status":"online"}]}`)
	got, err := parseCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"a.example:4242", "b.example:4343"}) {
		t.Fatalf("got %v", got)
	}
}

// A peer drops its path to this device PathRequestTTL after the last
// announce and cannot get it back by asking, so the default period must
// leave room for one lost announce.
func TestAnnounceEveryKeepsPeersPath(t *testing.T) {
	ttl := time.Duration(transport.PathRequestTTL) * time.Second
	if every := (HubPolicy{}).withDefaults().AnnounceEvery; 2*every >= ttl {
		t.Fatalf("announce every %v: a single lost announce lets the path expire after %v", every, ttl)
	}
}
