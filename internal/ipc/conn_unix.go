//go:build !windows && !ipctcp

package ipc

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// SocketPath returns the daemon's Unix socket path, honoring XDG_RUNTIME_DIR.
func SocketPath() string {
	if p := os.Getenv("ADROP_SOCKET"); p != "" {
		return p
	}
	if r := os.Getenv("XDG_RUNTIME_DIR"); r != "" {
		return filepath.Join(r, "adrop.sock")
	}
	return filepath.Join(os.TempDir(), "adrop.sock")
}

// Listen opens the daemon's socket, readable by this user only. The returned
// cleanup removes it.
func Listen() (net.Listener, string, func(), error) {
	path := SocketPath()
	_ = os.Remove(path) // a stale socket from a crashed run
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, "", nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, "", nil, fmt.Errorf("chmod socket: %w", err)
	}
	return ln, path, func() { ln.Close(); os.Remove(path) }, nil
}

// Dial connects to the daemon.
func Dial() (net.Conn, error) {
	path := SocketPath()
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, fmt.Errorf("cannot reach daemon at %s (is `adrop daemon` running?): %w", path, err)
	}
	return conn, nil
}
