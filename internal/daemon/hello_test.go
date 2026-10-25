package daemon

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestHelloIsLoggedByPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pc := newTestDaemon(t, ctx, "pc")
	phone := newTestDaemon(t, ctx, "phone")
	pair(t, pc, phone)

	var log lockedBuffer
	pc.d.logger.SetOutput(&log)

	dev, err := phone.d.Hello(ctx, "pc")
	if err != nil {
		t.Fatalf("hello: %v", err)
	}
	if dev.Fingerprint != pc.store.Fingerprint() {
		t.Fatalf("hello reached %s, want pc", dev.Fingerprint[:16])
	}

	want := "hello from phone (" + phone.store.Fingerprint() + ")"
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(log.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("pc log %q does not contain %q", log.String(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if strings.Contains(log.String(), "receive from") {
		t.Fatalf("a Hello-only connection was logged as a failed receive: %q", log.String())
	}
}
