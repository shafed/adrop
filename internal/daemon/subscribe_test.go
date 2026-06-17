package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/shafed/adrop/internal/ipc"
)

// TestSubscribeRegistry checks the basic broadcast/unsubscribe mechanics in
// isolation: a subscriber receives events, and after unsubscribe its channel is
// closed and no longer delivered to.
func TestSubscribeRegistry(t *testing.T) {
	d := &Daemon{subs: make(map[chan ipc.Event]struct{})}

	ch, unsub := d.subscribe()
	d.broadcast(ipc.Event{Kind: "recv-start", Peer: "x"})

	select {
	case e := <-ch:
		if e.Kind != "recv-start" {
			t.Fatalf("got %q, want recv-start", e.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive event")
	}

	unsub()
	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed after unsubscribe")
	}
	// Broadcasting after unsubscribe must not panic or deliver.
	d.broadcast(ipc.Event{Kind: "recv-done"})
}

// TestBroadcastDropsSlowSubscriber verifies broadcast never blocks on a full
// subscriber: after filling the buffer, further broadcasts return immediately
// and the slow subscriber simply misses events.
func TestBroadcastDropsSlowSubscriber(t *testing.T) {
	d := &Daemon{subs: make(map[chan ipc.Event]struct{})}
	_, _ = d.subscribe() // never drained

	done := make(chan struct{})
	go func() {
		for i := 0; i < 10_000; i++ {
			d.broadcast(ipc.Event{Kind: "recv-progress"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("broadcast blocked on a full/slow subscriber")
	}
}

// TestSubscribeDuringTransfer runs a real file transfer and asserts the receiver
// broadcasts the expected lifecycle events to a subscriber, without stalling the
// transfer itself.
func TestSubscribeDuringTransfer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pc := newTestDaemon(t, ctx, "pc")
	phone := newTestDaemon(t, ctx, "phone")
	pair(t, pc, phone)

	ch, unsub := phone.d.subscribe()
	defer unsub()

	// Drain events in the background into a slice.
	var got []ipc.Event
	collected := make(chan struct{})
	go func() {
		timeout := time.After(5 * time.Second)
		for {
			select {
			case e := <-ch:
				got = append(got, e)
				if e.Kind == "recv-done" {
					close(collected)
					return
				}
			case <-timeout:
				close(collected)
				return
			}
		}
	}()

	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "report.pdf")
	mustWrite(t, src, randomBytes(t, 700*1024))

	if err := pc.d.SendFiles(ctx, "phone", []string{src}, nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	<-collected

	if !hasKind(got, "recv-start") {
		t.Error("missing recv-start event")
	}
	if !hasKind(got, "recv-file-done") {
		t.Error("missing recv-file-done event")
	}
	if !hasKind(got, "recv-done") {
		t.Error("missing recv-done event")
	}
}

func hasKind(events []ipc.Event, kind string) bool {
	for _, e := range events {
		if e.Kind == kind {
			return true
		}
	}
	return false
}
