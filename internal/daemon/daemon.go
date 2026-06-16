// Package daemon implements the resident adrop process: it listens for
// incoming pinned-TLS sessions from trusted peers (receiving files and
// clipboard pushes), serves the CLI over a Unix socket, and originates
// outgoing transfers and pairing.
package daemon

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"sync"

	"github.com/shafed/adrop/internal/clipboard"
	"github.com/shafed/adrop/internal/config"
	"github.com/shafed/adrop/internal/ipc"
	"github.com/shafed/adrop/internal/transport"
)

// DefaultPort is the TCP port the daemon listens on for peer connections.
const DefaultPort = 53127

// Daemon ties together the store, the TLS peer listener, and the IPC socket.
type Daemon struct {
	store    *config.Store
	name     string
	tcpAddr  string // host:port advertised in pairing (LAN IP:port)
	listenIP string // bind address for the TLS listener ("" = all)
	port     int

	logger *log.Logger

	// pairWindow, when non-nil, allows the next inbound connection from an
	// as-yet-untrusted peer to complete pairing. Guarded by pairMu.
	pairMu     sync.Mutex
	pairWindow *pairSession

	downloadDir string

	// clipboardSet writes received clipboard data; overridable in tests.
	clipboardSet func(ctx context.Context, data []byte, mime string) error
	// clipboardGet reads local clipboard for outgoing push; overridable.
	clipboardGet func(ctx context.Context) ([]byte, error)
}

// Options configures a Daemon.
type Options struct {
	Store       *config.Store
	Name        string // device name; defaults to hostname
	Port        int    // TLS listen port; defaults to DefaultPort
	AdvertiseIP string // LAN IP to advertise in pairing; auto-detected if ""
	ListenIP    string // bind address for the TLS listener ("" = all interfaces)
	DownloadDir string // where received files land; defaults to ~/Downloads
	Logger      *log.Logger

	// ClipboardSet/Get override the system clipboard (used in tests). When nil
	// the wl-clipboard-backed defaults are used.
	ClipboardSet func(ctx context.Context, data []byte, mime string) error
	ClipboardGet func(ctx context.Context) ([]byte, error)
}

// New constructs a Daemon from Options.
func New(opt Options) (*Daemon, error) {
	if opt.Store == nil {
		return nil, fmt.Errorf("daemon: nil store")
	}
	name := opt.Name
	if name == "" {
		name, _ = os.Hostname()
		if name == "" {
			name = "adrop-pc"
		}
	}
	port := opt.Port
	if port == 0 {
		port = DefaultPort
	}
	logger := opt.Logger
	if logger == nil {
		logger = log.New(os.Stderr, "adrop: ", log.LstdFlags)
	}
	dl := opt.DownloadDir
	if dl == "" {
		home, _ := os.UserHomeDir()
		dl = home + "/Downloads"
	}
	ip := opt.AdvertiseIP
	if ip == "" {
		ip = detectLANIP()
	}
	d := &Daemon{
		store:        opt.Store,
		name:         name,
		port:         port,
		listenIP:     opt.ListenIP,
		tcpAddr:      net.JoinHostPort(ip, fmt.Sprint(port)),
		logger:       logger,
		downloadDir:  dl,
		clipboardSet: opt.ClipboardSet,
		clipboardGet: opt.ClipboardGet,
	}
	if d.clipboardSet == nil {
		d.clipboardSet = clipboard.Copy
	}
	if d.clipboardGet == nil {
		d.clipboardGet = clipboard.Paste
	}
	return d, nil
}

// IsTrusted implements transport.PeerVerifier. During an open pairing window
// the expected (still-untrusted) fingerprint is also accepted.
func (d *Daemon) IsTrusted(fp string) (string, bool) {
	if name, ok := d.store.IsTrusted(fp); ok {
		return name, true
	}
	d.pairMu.Lock()
	ps := d.pairWindow
	d.pairMu.Unlock()
	if ps != nil && (ps.expectFingerprint == "" || ps.expectFingerprint == fp) {
		return ps.expectName, true
	}
	return "", false
}

// runPeerListener opens the pinned-TLS listener and serves inbound peer
// connections until ctx is canceled. It is used by Run and, standalone, by
// tests that run multiple daemons in one process.
func (d *Daemon) runPeerListener(ctx context.Context) error {
	if err := os.MkdirAll(d.downloadDir, 0o755); err != nil {
		return fmt.Errorf("create download dir: %w", err)
	}
	tlsLn, err := transport.Listen(net.JoinHostPort(d.listenIP, fmt.Sprint(d.port)), d.store.Certificate(), d)
	if err != nil {
		return fmt.Errorf("tls listen: %w", err)
	}
	defer tlsLn.Close()
	go func() { <-ctx.Done(); tlsLn.Close() }()
	d.acceptLoop(ctx, tlsLn, d.handlePeer)
	return ctx.Err()
}

// Run starts the TLS peer listener and the IPC socket and blocks until ctx is
// canceled or a fatal error occurs.
func (d *Daemon) Run(ctx context.Context) error {
	if err := os.MkdirAll(d.downloadDir, 0o755); err != nil {
		return fmt.Errorf("create download dir: %w", err)
	}

	tlsLn, err := transport.Listen(net.JoinHostPort(d.listenIP, fmt.Sprint(d.port)), d.store.Certificate(), d)
	if err != nil {
		return fmt.Errorf("tls listen: %w", err)
	}
	defer tlsLn.Close()

	sockPath := ipc.SocketPath()
	_ = os.Remove(sockPath) // clear a stale socket from a crashed run
	ipcLn, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("ipc listen: %w", err)
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		d.logger.Printf("warning: chmod socket: %v", err)
	}
	defer func() {
		ipcLn.Close()
		os.Remove(sockPath)
	}()

	d.logger.Printf("listening for peers on %s, IPC at %s", d.tcpAddr, sockPath)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); d.acceptLoop(ctx, tlsLn, d.handlePeer) }()
	go func() { defer wg.Done(); d.acceptLoop(ctx, ipcLn, d.handleIPC) }()

	<-ctx.Done()
	tlsLn.Close()
	ipcLn.Close()
	wg.Wait()
	return ctx.Err()
}

// acceptLoop accepts connections until ctx is done, dispatching each to handle.
func (d *Daemon) acceptLoop(ctx context.Context, ln net.Listener, handle func(context.Context, net.Conn)) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				// Listener closed or transient error; if ctx not done, log.
				if ctx.Err() == nil {
					d.logger.Printf("accept: %v", err)
				}
				return
			}
		}
		go handle(ctx, conn)
	}
}

// detectLANIP finds a non-loopback IPv4 address to advertise in pairing.
func detectLANIP() string {
	// Dialing a public IP (no packets sent for UDP) reveals the preferred
	// outbound interface address.
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err == nil {
		defer conn.Close()
		if a, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			return a.IP.String()
		}
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if v4 := ipnet.IP.To4(); v4 != nil {
				return v4.String()
			}
		}
	}
	return "127.0.0.1"
}
