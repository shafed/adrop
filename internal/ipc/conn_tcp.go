//go:build windows || ipctcp

package ipc

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// On Windows the window (Dart) cannot use Unix sockets, so the daemon listens
// on 127.0.0.1 and writes the address and a random token to a file in this
// user's profile. A client sends the token as its first line; a connection
// without it is closed. Other users on the machine cannot read the file.

type endpoint struct {
	Addr  string `json:"addr"`
	Token string `json:"token"`
}

// EndpointFile is where the daemon publishes its IPC address and token
// (ADROP_IPC_FILE overrides it).
func EndpointFile() string {
	if p := os.Getenv("ADROP_IPC_FILE"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "adrop", "ipc.json")
}

// SocketPath names the IPC endpoint in messages.
func SocketPath() string { return EndpointFile() }

type tokenListener struct {
	net.Listener
	token string
}

func (l *tokenListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		line, err := readLine(c)
		c.SetReadDeadline(time.Time{})
		if err != nil || subtle.ConstantTimeCompare([]byte(line), []byte(l.token)) != 1 {
			c.Close()
			continue
		}
		return c, nil
	}
}

// readLine reads up to '\n' one byte at a time, so nothing after the token
// is consumed from the connection.
func readLine(c net.Conn) (string, error) {
	var b strings.Builder
	one := make([]byte, 1)
	for b.Len() < 256 {
		if _, err := c.Read(one); err != nil {
			return "", err
		}
		if one[0] == '\n' {
			return b.String(), nil
		}
		b.WriteByte(one[0])
	}
	return "", fmt.Errorf("token line too long")
}

// Listen opens the daemon's IPC endpoint and publishes it.
func Listen() (net.Listener, string, func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", nil, err
	}
	var tb [24]byte
	rand.Read(tb[:])
	ep := endpoint{Addr: ln.Addr().String(), Token: hex.EncodeToString(tb[:])}
	raw, _ := json.Marshal(ep)
	file := EndpointFile()
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		ln.Close()
		return nil, "", nil, err
	}
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		ln.Close()
		return nil, "", nil, err
	}
	return &tokenListener{Listener: ln, token: ep.Token}, ep.Addr, func() { ln.Close(); os.Remove(file) }, nil
}

// Dial connects to the daemon and presents the token.
func Dial() (net.Conn, error) {
	raw, err := os.ReadFile(EndpointFile())
	if err != nil {
		return nil, fmt.Errorf("cannot reach daemon (is `adrop daemon` running?): %w", err)
	}
	var ep endpoint
	if err := json.Unmarshal(raw, &ep); err != nil {
		return nil, err
	}
	conn, err := net.Dial("tcp", ep.Addr)
	if err != nil {
		return nil, fmt.Errorf("cannot reach daemon at %s (is `adrop daemon` running?): %w", ep.Addr, err)
	}
	if _, err := conn.Write([]byte(ep.Token + "\n")); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

