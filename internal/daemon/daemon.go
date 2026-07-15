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
	"time"

	"github.com/shafed/adrop/internal/clipboard"
	"github.com/shafed/adrop/internal/config"
	"github.com/shafed/adrop/internal/ipc"
	"github.com/shafed/adrop/internal/mdns"
	"github.com/shafed/adrop/internal/transport"
)

// DefaultPort is the TCP port the daemon listens on for peer connections.
const DefaultPort = 53127

// Daemon ties together the store, the TLS peer listener, and the IPC socket.
type Daemon struct {
	store    *config.Store
	name     string
	listenIP string // bind address for the TLS listener ("" = all)
	port     int

	// tcpAddr is the host:port advertised in pairing (LAN IP:port). When
	// autoIP is true it was auto-detected and may be refreshed later (see
	// refreshAdvertiseAddr); guarded by addrMu since refresh can race with
	// concurrent reads from in-flight Hello exchanges.
	addrMu  sync.RWMutex
	tcpAddr string
	autoIP  bool // true when Options.AdvertiseIP was empty (auto-detect)

	// relayAddr is the base URL of an adrop-relay server (e.g.
	// "http://relay.example.com:8080"). When non-empty and a direct dial
	// fails, the daemon POSTs a wake request to the relay so the phone opens
	// its receive window via FCM, then retries the dial.
	relayAddr string

	logger *log.Logger

	// pairWindow, when non-nil, allows the next inbound connection from an
	// as-yet-untrusted peer to complete pairing. Guarded by pairMu.
	pairMu     sync.Mutex
	pairWindow *pairSession

	downloadDir string

	// clipboardSet writes received clipboard data; overridable in tests.
	clipboardSet func(ctx context.Context, data []byte, mime string) error
	// clipboardGet reads local clipboard for outgoing push; overridable.
	clipboardGet func(ctx context.Context, mime string) ([]byte, error)

	// subscribers receive broadcast receive-events (GUI feed). Guarded by subMu.
	subMu sync.Mutex
	subs  map[chan ipc.Event]struct{}
}

// Options configures a Daemon.
type Options struct {
	Store       *config.Store
	Name        string // device name; defaults to hostname
	Port        int    // TLS listen port; defaults to DefaultPort
	AdvertiseIP string // LAN IP to advertise in pairing; auto-detected if ""
	ListenIP    string // bind address for the TLS listener ("" = all interfaces)
	DownloadDir string // where received files land; defaults to ~/Downloads
	// RelayAddr is the base URL of an adrop-relay server. When set, a failed
	// direct dial triggers an FCM wake via the relay before retrying once.
	// Reads from env ADROP_RELAY if empty. Example: "http://relay.example.com:8080"
	RelayAddr string
	Logger    *log.Logger

	// ClipboardSet/Get override the system clipboard (used in tests). When nil
	// the wl-clipboard-backed defaults are used.
	ClipboardSet func(ctx context.Context, data []byte, mime string) error
	ClipboardGet func(ctx context.Context, mime string) ([]byte, error)
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
	relay := opt.RelayAddr
	if relay == "" {
		relay = os.Getenv("ADROP_RELAY")
	}
	d := &Daemon{
		store:        opt.Store,
		name:         name,
		port:         port,
		listenIP:     opt.ListenIP,
		tcpAddr:      net.JoinHostPort(ip, fmt.Sprint(port)),
		autoIP:       opt.AdvertiseIP == "",
		relayAddr:    relay,
		logger:       logger,
		downloadDir:  dl,
		clipboardSet: opt.ClipboardSet,
		clipboardGet: opt.ClipboardGet,
		subs:         make(map[chan ipc.Event]struct{}),
	}
	if d.clipboardSet == nil {
		d.clipboardSet = clipboard.Copy
	}
	if d.clipboardGet == nil {
		d.clipboardGet = clipboard.Get
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

	d.startMDNS(ctx)

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

// startMDNS launches avahi-based advertisement and discovery goroutines.
// If avahi tools are not installed both goroutines log a warning and exit
// cleanly — the daemon continues without mDNS.
func (d *Daemon) startMDNS(ctx context.Context) {
	selfFP := d.store.Fingerprint()

	go func() {
		if err := mdns.Advertise(ctx, d.name, d.port, selfFP); err != nil {
			d.logger.Printf("mdns: advertise: %v", err)
		}
	}()

	go func() {
		err := mdns.Browse(ctx, func(name, addr, fp string) {
			if fp == selfFP {
				return
			}
			peerName, trusted := d.store.IsTrusted(fp)
			if !trusted {
				return
			}
			d.store.UpdateAddr(fp, addr)
			d.logger.Printf("mDNS: updated addr for %s to %s", peerName, addr)
		})
		if err != nil {
			d.logger.Printf("mdns: browse: %v", err)
		}
	}()
}

// refreshAddrViaMDNS runs a single active mDNS resolve and updates the stored
// address of any trusted peer it finds. It is used as a fallback when a direct
// dial fails (e.g. the peer changed IP on the same LAN): the continuous Browse
// loop is passive and may not have observed the new address yet, so we kick off
// one resolve pass on demand. Best-effort and bounded — errors are logged, not
// returned, and a missing avahi just no-ops.
func (d *Daemon) refreshAddrViaMDNS(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	selfFP := d.store.Fingerprint()
	err := mdns.ResolveOnce(ctx, func(name, addr, fp string) {
		if fp == selfFP {
			return
		}
		if _, trusted := d.store.IsTrusted(fp); !trusted {
			return
		}
		d.store.UpdateAddr(fp, addr)
		d.logger.Printf("mDNS: refreshed addr for %s to %s", name, addr)
	})
	if err != nil {
		d.logger.Printf("mdns: resolve: %v", err)
	}
}

// subscribe registers a new receive-event channel and returns it along with an
// unsubscribe func. The channel is buffered so a momentarily-busy subscriber
// doesn't stall broadcast; broadcast drops events when the buffer is full.
func (d *Daemon) subscribe() (<-chan ipc.Event, func()) {
	ch := make(chan ipc.Event, 32)
	d.subMu.Lock()
	d.subs[ch] = struct{}{}
	d.subMu.Unlock()
	return ch, func() {
		d.subMu.Lock()
		if _, ok := d.subs[ch]; ok {
			delete(d.subs, ch)
			close(ch)
		}
		d.subMu.Unlock()
	}
}

// broadcast delivers e to every subscriber without blocking: a subscriber whose
// buffer is full simply misses the event rather than stalling a transfer.
func (d *Daemon) broadcast(e ipc.Event) {
	d.subMu.Lock()
	for ch := range d.subs {
		select {
		case ch <- e:
		default:
		}
	}
	d.subMu.Unlock()
}

// advertiseAddr returns the host:port to put in Hello/pairing messages.
func (d *Daemon) advertiseAddr() string {
	d.addrMu.RLock()
	defer d.addrMu.RUnlock()
	return d.tcpAddr
}

// refreshAdvertiseAddr re-detects the LAN IP when it was auto-detected
// (ADROP_ADVERTISE_IP unset) and updates tcpAddr if a real address is now
// found. This matters because the daemon commonly starts under systemd at
// login, before the network is up; detectLANIP falls back to 127.0.0.1 at
// that point and, without this refresh, the daemon would advertise loopback
// in every pairing QR until restarted. Called from the user-driven pairing
// entry points (PairingURI, AddPeer), by which time the network has usually
// come up. An explicit ADROP_ADVERTISE_IP, or a previously found real
// address, is never overwritten with the loopback fallback.
func (d *Daemon) refreshAdvertiseAddr() {
	if !d.autoIP {
		return
	}
	ip := detectLANIP()
	if ip == "127.0.0.1" {
		return
	}
	d.addrMu.Lock()
	d.tcpAddr = net.JoinHostPort(ip, fmt.Sprint(d.port))
	d.addrMu.Unlock()
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
