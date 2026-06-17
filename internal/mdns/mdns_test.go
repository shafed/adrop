package mdns

import "testing"

func TestParseBrowseLine(t *testing.T) {
	line := `=;eth0;IPv4;thinkpad;_adrop._tcp;local;thinkpad.local;192.168.1.5;53127;"fp=abc123"`
	name, addr, fp, ok := parseBrowseLine(line)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if name != "thinkpad" {
		t.Errorf("name: got %q, want %q", name, "thinkpad")
	}
	if addr != "192.168.1.5:53127" {
		t.Errorf("addr: got %q, want %q", addr, "192.168.1.5:53127")
	}
	if fp != "abc123" {
		t.Errorf("fp: got %q, want %q", fp, "abc123")
	}
}

func TestParseBrowseLineSkipsNonResolved(t *testing.T) {
	for _, line := range []string{
		"+;eth0;IPv4;thinkpad;_adrop._tcp;local",
		"",
		"some random line",
	} {
		_, _, _, ok := parseBrowseLine(line)
		if ok {
			t.Errorf("expected ok=false for line %q", line)
		}
	}
}

func TestBrowseFiltersOwnFingerprint(t *testing.T) {
	const selfFP = "abc123"
	line := `=;eth0;IPv4;thinkpad;_adrop._tcp;local;thinkpad.local;192.168.1.5;53127;"fp=abc123"`

	called := false
	// Simulate the Browse callback logic used in daemon.startMDNS
	onRecord := func(name, addr, fp string) {
		if fp == selfFP {
			return // filtered
		}
		called = true
	}

	if _, addr, fp, ok := parseBrowseLine(line); ok {
		onRecord("thinkpad", addr, fp)
	}

	if called {
		t.Error("own fingerprint should have been filtered out")
	}
}

func TestParseBrowseLineMissingFP(t *testing.T) {
	// Line without fp= in TXT — should not parse
	line := `=;eth0;IPv4;thinkpad;_adrop._tcp;local;thinkpad.local;192.168.1.5;53127;"key=val"`
	_, _, _, ok := parseBrowseLine(line)
	if ok {
		t.Error("expected ok=false when TXT has no fp= entry")
	}
}
