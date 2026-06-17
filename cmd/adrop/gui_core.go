package main

// This file holds the GUI's non-Fyne logic so it can be unit-tested without a
// display: file:// URI decoding, send-request construction, and receive-event
// formatting. It is compiled into every build (no `gui` tag) but only used by
// the Fyne window in gui.go.

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/shafed/adrop/internal/ipc"
)

// decodeFileURIs parses one or more whitespace/newline-separated tokens, each
// expected to be a file:// URI (or a bare local path), into local filesystem
// paths. Percent-escapes are decoded and a leading "file://[host]" scheme is
// stripped. Any token with a non-file scheme, or that fails os.Stat, is
// reported in the returned error; the successfully-resolved paths are still
// returned so the caller can decide whether to proceed.
func decodeFileURIs(input string) (paths []string, err error) {
	var bad []string
	for _, tok := range strings.Fields(input) {
		p, derr := decodeFileURI(tok)
		if derr != nil {
			bad = append(bad, fmt.Sprintf("%s (%v)", tok, derr))
			continue
		}
		if _, serr := os.Stat(p); serr != nil {
			bad = append(bad, fmt.Sprintf("%s (%v)", tok, serr))
			continue
		}
		paths = append(paths, p)
	}
	if len(bad) > 0 {
		err = fmt.Errorf("cannot send: %s", strings.Join(bad, "; "))
	}
	return paths, err
}

// decodeFileURI turns a single token into a local path. A token without a
// scheme is treated as a bare path. A "file://host/path" URI keeps only the
// path (the host, if any, is dropped — adrop is local-only). Percent-escapes
// like %20 are decoded. Non-file schemes are rejected.
func decodeFileURI(tok string) (string, error) {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return "", fmt.Errorf("empty")
	}
	// No scheme separator → treat as a bare local path.
	if !strings.Contains(tok, "://") {
		return tok, nil
	}
	u, err := url.Parse(tok)
	if err != nil {
		return "", fmt.Errorf("not a valid URI")
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("not a file:// URI")
	}
	// u.Path is already percent-decoded by url.Parse. The host (e.g.
	// "localhost") is ignored for local files.
	if u.Path == "" {
		return "", fmt.Errorf("no path")
	}
	return u.Path, nil
}

// sendFilesRequest builds the IPC request for an auto-send to peer.
func sendFilesRequest(peer string, paths []string) ipc.Request {
	return ipc.Request{Cmd: ipc.CmdSendFiles, Target: peer, Files: paths}
}

// sendClipRequest builds the IPC request for a clipboard push to peer.
func sendClipRequest(peer string) ipc.Request {
	return ipc.Request{Cmd: ipc.CmdSendClip, Target: peer}
}

// recvStatus renders a one-line human summary of a receive Event for the
// inbound row. Returns ("", false) for events with nothing to show.
func recvStatus(e ipc.Event) (string, bool) {
	switch e.Kind {
	case "recv-start":
		return fmt.Sprintf("↓ %s: starting…", e.Peer), true
	case "recv-progress":
		pct := 0
		if e.Total > 0 {
			pct = int(e.BytesDone * 100 / e.Total)
		}
		return fmt.Sprintf("↓ %s: %s %d%%", e.Peer, e.File, pct), true
	case "recv-file-done":
		return fmt.Sprintf("↓ %s: %s ✓", e.Peer, e.File), true
	case "recv-done":
		return fmt.Sprintf("↓ received %d file(s) from %s", e.Count, e.Peer), true
	case "recv-error":
		return fmt.Sprintf("↓ %s: error: %s", e.Peer, e.Err), true
	}
	return "", false
}

// recvFraction returns the 0..1 progress fraction for a receive Event, or -1 if
// the event has no meaningful fraction (e.g. start/done/error).
func recvFraction(e ipc.Event) float64 {
	switch e.Kind {
	case "recv-progress", "recv-file-done":
		if e.Total > 0 {
			return float64(e.BytesDone) / float64(e.Total)
		}
	}
	return -1
}
