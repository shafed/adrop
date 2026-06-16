package daemon

import (
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shafed/adrop/internal/transport"
)

// peerFP returns the pinned fingerprint of a handshaken TLS peer.
func peerFP(conn *tls.Conn) (string, error) {
	return transport.PeerFingerprint(conn)
}

// sanitizeName strips any path components and rejects traversal, leaving only
// a safe base file name. Empty/odd names fall back to "file".
func sanitizeName(name string) string {
	name = filepath.Base(filepath.Clean("/" + name))
	name = strings.TrimLeft(name, "/")
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	return name
}

// uniquePath returns a path in dir for name that does not collide with an
// existing file, auto-renaming "file.pdf" -> "file (1).pdf" and so on.
// It never overwrites and never prompts (per SPEC §7).
func uniquePath(dir, name string) string {
	candidate := filepath.Join(dir, name)
	if !exists(candidate) {
		return candidate
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		c := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		if !exists(c) {
			return c
		}
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
