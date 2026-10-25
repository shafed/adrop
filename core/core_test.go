package core

import (
	"strings"
	"testing"
)

func TestStartKeepsIdentity(t *testing.T) {
	dir := t.TempDir()
	a, err := Start(dir, "proto")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	b, err := Start(dir, "proto")
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if a.Fingerprint() != b.Fingerprint() || len(a.Fingerprint()) != 64 {
		t.Fatalf("fingerprint %q changed to %q across restarts", a.Fingerprint(), b.Fingerprint())
	}
	if b.Peers() != "" {
		t.Fatalf("fresh core has peers: %q", b.Peers())
	}
}

func TestPairAndHelloRejectBadInput(t *testing.T) {
	n, err := Start(t.TempDir(), "proto")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := n.Pair("adrop://pair?d=garbage"); err == nil {
		t.Fatal("pair accepted a garbage URI")
	}
	_, err = n.Hello("pc")
	if err == nil || !strings.Contains(err.Error(), "no trusted device") {
		t.Fatalf("hello to an unpaired peer: %v", err)
	}
}
