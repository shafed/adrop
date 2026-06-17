package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shafed/adrop/internal/ipc"
)

func TestDecodeFileURI(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"bare path", "/home/me/a.txt", "/home/me/a.txt", false},
		{"file scheme", "file:///home/me/a.txt", "/home/me/a.txt", false},
		{"file scheme with host", "file://localhost/home/me/a.txt", "/home/me/a.txt", false},
		{"percent space", "file:///home/me/my%20file.txt", "/home/me/my file.txt", false},
		{"percent multi", "file:///tmp/a%2Bb%23c.txt", "/tmp/a+b#c.txt", false},
		{"http rejected", "http://example.com/x", "", true},
		{"ftp rejected", "ftp://host/x", "", true},
		{"empty", "   ", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeFileURI(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %q", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("decodeFileURI(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDecodeFileURIsMultiple(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b file.txt") // space, to exercise escaping
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Build a realistic two-URI input; the second has a space escaped as %20.
	input := "file://" + a + "\n" + "file://" + strings.ReplaceAll(b, " ", "%20")

	paths, err := decodeFileURIs(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(paths) != 2 || paths[0] != a || paths[1] != b {
		t.Errorf("got %v, want [%q %q]", paths, a, b)
	}
}

// TestDecodeFileURIsLiteralSpace proves a path containing a literal (un-escaped)
// space survives — both as a file:// URI and as a bare path — because splitting
// is on newlines only, not all whitespace. This is the drop/paste-with-spaces
// regression: strings.Fields would have shredded "b file.txt" into two tokens.
func TestDecodeFileURIsLiteralSpace(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b file.txt") // literal space, not %20
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// file:// URI with a literal space alongside a normal one.
	input := "file://" + a + "\nfile://" + b
	paths, err := decodeFileURIs(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(paths) != 2 || paths[0] != a || paths[1] != b {
		t.Errorf("got %v, want [%q %q]", paths, a, b)
	}

	// Bare path with a literal space (paste of a plain filesystem path).
	paths, err = decodeFileURIs(b)
	if err != nil {
		t.Fatalf("unexpected error for bare path: %v", err)
	}
	if len(paths) != 1 || paths[0] != b {
		t.Errorf("bare path got %v, want [%q]", paths, b)
	}
}

func TestDecodeFileURIsReportsBad(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "ok.txt")
	if err := os.WriteFile(good, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "nope.txt")

	input := "file://" + good + "\nhttp://x/y\nfile://" + missing
	paths, err := decodeFileURIs(input)
	if err == nil {
		t.Fatal("expected error naming the bad URIs")
	}
	// The good path is still returned so the caller can choose to proceed/stage.
	if len(paths) != 1 || paths[0] != good {
		t.Errorf("good paths = %v, want [%q]", paths, good)
	}
	if !strings.Contains(err.Error(), "http://x/y") || !strings.Contains(err.Error(), missing) {
		t.Errorf("error should name both bad URIs, got: %v", err)
	}
}

func TestSendRequests(t *testing.T) {
	r := sendFilesRequest("thinkpad", []string{"/a", "/b"})
	if r.Cmd != ipc.CmdSendFiles || r.Target != "thinkpad" || len(r.Files) != 2 {
		t.Errorf("sendFilesRequest = %+v", r)
	}
	c := sendClipRequest("thinkpad")
	if c.Cmd != ipc.CmdSendClip || c.Target != "thinkpad" {
		t.Errorf("sendClipRequest = %+v", c)
	}
}

func TestRecvStatusAndFraction(t *testing.T) {
	prog := ipc.Event{Kind: "recv-progress", Peer: "phone", File: "p.jpg", BytesDone: 70, Total: 100}
	s, ok := recvStatus(prog)
	if !ok || !strings.Contains(s, "70%") || !strings.Contains(s, "p.jpg") {
		t.Errorf("recvStatus progress = %q (%v)", s, ok)
	}
	if f := recvFraction(prog); f < 0.69 || f > 0.71 {
		t.Errorf("recvFraction = %v, want ~0.7", f)
	}

	done := ipc.Event{Kind: "recv-done", Peer: "phone", Count: 3}
	s, ok = recvStatus(done)
	if !ok || !strings.Contains(s, "3 file") {
		t.Errorf("recvStatus done = %q", s)
	}
	if f := recvFraction(done); f != -1 {
		t.Errorf("recvFraction(done) = %v, want -1", f)
	}

	if _, ok := recvStatus(ipc.Event{Kind: "recv-clipboard"}); ok {
		t.Error("unknown kind should return ok=false")
	}
}
