package daemon

import (
	"context"
	"net"
	"testing"
	"time"
)

// fakeAddr is a net.Addr with a fixed String() for resolvePeerAddr tests.
type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

func TestResolvePeerAddr(t *testing.T) {
	d := &Daemon{port: DefaultPort}
	const src = "192.168.0.112:48512" // ephemeral source of the live connection

	cases := []struct {
		name       string
		advertised string
		want       string
	}{
		{
			// The happy path: peer named a concrete IP and its real listen port.
			name:       "concrete host and port honored verbatim",
			advertised: "192.168.0.112:7777",
			want:       "192.168.0.112:7777",
		},
		{
			// The core bug: phone couldn't find its LAN IP so it sent 0.0.0.0,
			// but its listen port (7777) is still authoritative. We must keep the
			// port and fill in the source IP — NOT fall back to our own port.
			name:       "unspecified ipv4 host takes source ip keeps port",
			advertised: "0.0.0.0:7777",
			want:       "192.168.0.112:7777",
		},
		{
			name:       "unspecified ipv6 host takes source ip keeps port",
			advertised: "[::]:7777",
			want:       "192.168.0.112:7777",
		},
		{
			name:       "empty host keeps advertised port",
			advertised: ":7777",
			want:       "192.168.0.112:7777",
		},
		{
			// Only when there's no usable port at all do we fall back to our
			// default port against the source IP.
			name:       "no advertised addr falls back to source ip and default port",
			advertised: "",
			want:       net.JoinHostPort("192.168.0.112", itoa(DefaultPort)),
		},
		{
			name:       "zero port falls back to default port",
			advertised: "0.0.0.0:0",
			want:       net.JoinHostPort("192.168.0.112", itoa(DefaultPort)),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := d.resolvePeerAddr(tc.advertised, fakeAddr(src))
			if got != tc.want {
				t.Fatalf("resolvePeerAddr(%q) = %q, want %q", tc.advertised, got, tc.want)
			}
		})
	}
}

// TestPairingStoresAdvertisedPort is a regression test for the wrong-stored-port
// bug: a peer that pairs while advertising an unspecified host must be stored
// with its real advertised port and the connection's source IP, never the PC's
// own port.
func TestPairingStoresAdvertisedPort(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pc := newTestDaemon(t, ctx, "pc")
	phone := newTestDaemon(t, ctx, "phone")

	// Make the phone advertise an unspecified host on its real listen port,
	// mimicking a device that can't determine its own LAN IP. The host should
	// be rewritten to the loopback source IP, but the port must survive.
	_, phonePort, err := net.SplitHostPort(phone.d.tcpAddr)
	if err != nil {
		t.Fatalf("split phone addr %q: %v", phone.d.tcpAddr, err)
	}
	phone.d.tcpAddr = net.JoinHostPort("0.0.0.0", phonePort)

	pair(t, pc, phone)

	dev, ok := pc.store.Lookup(phone.store.Fingerprint())
	if !ok {
		t.Fatalf("pc did not store phone after pairing")
	}
	want := net.JoinHostPort("127.0.0.1", phonePort)
	if dev.Addr != want {
		t.Fatalf("stored phone addr = %q, want %q (must keep advertised port, not PC port %d)",
			dev.Addr, want, DefaultPort)
	}
}

func TestSelfHealAddrOnInboundConnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pc := newTestDaemon(t, ctx, "pc")
	phone := newTestDaemon(t, ctx, "phone")
	pair(t, pc, phone)

	// Corrupt the stored phone address (wrong port), as the old bug produced.
	fp := phone.store.Fingerprint()
	pc.store.UpdateAddr(fp, "127.0.0.1:"+itoa(DefaultPort))

	// An inbound connect from the phone (it sends a file) should self-heal the
	// stored address back to the phone's real advertised port.
	srcDir := t.TempDir()
	src := srcDir + "/x.bin"
	mustWrite(t, src, []byte("heal me"))
	if err := phone.d.SendFiles(ctx, "pc", []string{src}, nil); err != nil {
		t.Fatalf("phone send: %v", err)
	}

	_, phonePort, _ := net.SplitHostPort(phone.d.tcpAddr)
	want := net.JoinHostPort("127.0.0.1", phonePort)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if dev, ok := pc.store.Lookup(fp); ok && dev.Addr == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	dev, _ := pc.store.Lookup(fp)
	t.Fatalf("stored addr not self-healed: got %q want %q", dev.Addr, want)
}

// TestDialRecoversFromStaleAddr covers the same-LAN IP-change case: the PC has a
// stale stored address for the phone, so the first dial fails. While dialPeer is
// in its recovery loop, the stored address is corrected (as an mDNS resolve or a
// concurrent inbound connect would do); the send must then pick up the fresh
// address and succeed without any relay/FCM wake configured.
func TestDialRecoversFromStaleAddr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pc := newTestDaemon(t, ctx, "pc")
	phone := newTestDaemon(t, ctx, "phone")
	pair(t, pc, phone)

	fp := phone.store.Fingerprint()
	good, _ := pc.store.Lookup(fp)
	goodAddr := good.Addr

	// Point the PC at a dead port so the initial dial fails.
	pc.store.UpdateAddr(fp, "127.0.0.1:1") // port 1: nothing listening

	// Simulate mDNS/inbound correcting the address shortly after the send starts,
	// while dialPeer is polling in its recovery loop.
	go func() {
		time.Sleep(300 * time.Millisecond)
		pc.store.UpdateAddr(fp, goodAddr)
	}()

	srcDir := t.TempDir()
	src := srcDir + "/recover.bin"
	mustWrite(t, src, randomBytes(t, 64*1024))
	if err := pc.d.SendFiles(ctx, "phone", []string{src}, nil); err != nil {
		t.Fatalf("send did not recover from stale addr: %v", err)
	}
}

// TestDialAbortsOnContextCancel verifies the recovery/wake loops honor context
// cancellation instead of burning the full mdnsRecoverTimeout (and then the
// wake timeout). With a permanently dead stored address and no relay, a cancel
// must abort the dial promptly with the context error.
func TestDialAbortsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pc := newTestDaemon(t, ctx, "pc")
	phone := newTestDaemon(t, ctx, "phone")
	pair(t, pc, phone)

	fp := phone.store.Fingerprint()
	// Dead port, never corrected: the recovery loop would otherwise spin until
	// its deadline.
	pc.store.UpdateAddr(fp, "127.0.0.1:1")

	sendCtx, sendCancel := context.WithCancel(ctx)
	go func() {
		time.Sleep(100 * time.Millisecond)
		sendCancel()
	}()

	srcDir := t.TempDir()
	src := srcDir + "/cancel.bin"
	mustWrite(t, src, randomBytes(t, 1024))

	start := time.Now()
	err := pc.d.SendFiles(sendCtx, "phone", []string{src}, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("send to dead addr unexpectedly succeeded")
	}
	// Should abort well before the full mdnsRecoverTimeout (2s); allow slack for
	// the in-flight dial's TCP timeout, but it must not run to completion.
	if elapsed > mdnsRecoverTimeout {
		t.Fatalf("send did not abort on cancel: took %s (>= mdnsRecoverTimeout %s)", elapsed, mdnsRecoverTimeout)
	}
}
