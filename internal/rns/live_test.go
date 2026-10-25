package rns

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

// TestLivePublicHub sends a signed message between two nodes through a real
// public hub: ADROP_LIVE_HUB=host:port go test ./internal/rns -run Live
func TestLivePublicHub(t *testing.T) {
	hub := os.Getenv("ADROP_LIVE_HUB")
	if hub == "" {
		t.Skip("set ADROP_LIVE_HUB=host:port to run against a public hub")
	}
	var a *Node
	b := openNode(t, hub, func(pub []byte) bool { return bytes.Equal(pub, a.PublicKey()) })
	a = openNode(t, hub, nil)
	if err := b.Announce(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	start := time.Now()
	if err := a.Send(ctx, b.PublicKey(), []byte("through a public hub")); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case m := <-b.Inbox():
		t.Logf("delivered %q in %v", m.Body, time.Since(start))
	case <-ctx.Done():
		t.Fatal("message never arrived")
	}
}

// TestLiveHubManager runs the hub manager on the real network for a minute:
// ADROP_LIVE_HUBS=1 go test ./internal/rns -run LiveHubManager -v
func TestLiveHubManager(t *testing.T) {
	if os.Getenv("ADROP_LIVE_HUBS") == "" {
		t.Skip("set ADROP_LIVE_HUBS=1 to run against public hubs")
	}
	n, err := Open(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	catalog, err := FetchCatalog(ctx, CatalogURL, t.TempDir()+"/catalog.json")
	t.Logf("catalog: %d hubs (err %v)", len(catalog), err)
	m := NewHubManager(n, BuiltinHubs, HubPolicy{Silence: 30 * time.Second}, t.Logf)
	m.AddCandidates(catalog)
	go m.Run(ctx)
	<-ctx.Done()
	t.Logf("working after a minute: %v", m.Working())
	if len(m.Working()) == 0 {
		t.Fatal("no working hub")
	}
}
