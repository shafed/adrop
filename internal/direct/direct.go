// Package direct opens a direct channel between two paired devices in
// different networks (SPEC §4.3): ICE finds a UDP path through the NATs
// (host and STUN candidates, exchanged over Reticulum by the caller), QUIC
// runs over it, and inside QUIC the same pinned TLS 1.3 as in the LAN. One
// QUIC stream then carries the usual adrop session.
package direct

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/stun/v4"
	"github.com/quic-go/quic-go"
)

func init() {
	// QUIC runs over the ICE connection, not a *net.UDPConn, so quic-go
	// cannot size the socket buffers and would warn about it on every run.
	if os.Getenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING") == "" {
		os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	}
}

// DefaultSTUN are public STUN servers used to learn the outside address.
var DefaultSTUN = []string{
	"stun:stun.l.google.com:19302",
	"stun:stun.cloudflare.com:3478",
	"stun:stun.nextcloud.com:443",
}

// ALPN of adrop sessions over QUIC.
const ALPN = "adrop/1"

// Msg is one signalling message of a direct-channel setup, carried over
// Reticulum. Kind is "offer" (from the dialer), "answer" or "cand".
type Msg struct {
	Kind  string
	Ufrag string
	Pwd   string
	Cand  string
}

// Signal sends one Msg to the peer.
type Signal func(Msg) error

// Options configure a direct channel. Zero values take the defaults.
type Options struct {
	STUN []string // STUN server URIs; nil means DefaultSTUN, empty slice none
	// Loopback adds loopback candidates, for tests on one machine.
	Loopback bool
	Timeout  time.Duration // ICE + QUIC setup (default 30 s)
}

// Conn is the session stream of a direct channel. Closing it closes the
// QUIC connection and the ICE agent.
type Conn struct {
	*quic.Stream
	qc      *quic.Conn
	release func()
	once    sync.Once
}

func (c *Conn) LocalAddr() net.Addr  { return c.qc.LocalAddr() }
func (c *Conn) RemoteAddr() net.Addr { return c.qc.RemoteAddr() }

// PeerCertificate is the leaf certificate the peer showed inside QUIC.
func (c *Conn) PeerCertificate() []byte {
	certs := c.qc.ConnectionState().TLS.PeerCertificates
	if len(certs) == 0 {
		return nil
	}
	return certs[0].Raw
}

func (c *Conn) Close() error {
	c.once.Do(func() {
		c.Stream.Close()
		// Let the peer read what was written before the connection goes.
		time.AfterFunc(2*time.Second, func() {
			c.qc.CloseWithError(0, "")
			c.release()
		})
	})
	return nil
}

// Dial sets up a direct channel as the controlling side: it sends an offer
// and its candidates through signal, takes the answer and the peer's
// candidates from incoming, and connects as the QUIC client with tlsConf.
func Dial(ctx context.Context, signal Signal, incoming <-chan Msg, tlsConf *tls.Config, opt Options) (*Conn, error) {
	return setup(ctx, true, Msg{}, signal, incoming, tlsConf, opt)
}

// Accept answers an offer as the controlled side and becomes the QUIC
// server with tlsConf.
func Accept(ctx context.Context, offer Msg, signal Signal, incoming <-chan Msg, tlsConf *tls.Config, opt Options) (*Conn, error) {
	return setup(ctx, false, offer, signal, incoming, tlsConf, opt)
}

func setup(ctx context.Context, controlling bool, offer Msg, signal Signal, incoming <-chan Msg, tlsConf *tls.Config, opt Options) (*Conn, error) {
	if opt.Timeout == 0 {
		opt.Timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()

	agent, err := newAgent(opt)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			agent.Close()
		}
	}()
	if err := agent.OnCandidate(func(c ice.Candidate) {
		if c != nil {
			signal(Msg{Kind: "cand", Cand: c.Marshal()})
		}
	}); err != nil {
		return nil, err
	}
	ufrag, pwd, err := agent.GetLocalUserCredentials()
	if err != nil {
		return nil, err
	}
	kind := "answer"
	if controlling {
		kind = "offer"
	}
	if err := signal(Msg{Kind: kind, Ufrag: ufrag, Pwd: pwd}); err != nil {
		return nil, fmt.Errorf("direct: send %s: %w", kind, err)
	}
	if err := agent.GatherCandidates(); err != nil {
		return nil, err
	}

	// The peer's credentials: in the offer we answer, or in the answer.
	remote := make(chan Msg, 1)
	if !controlling {
		remote <- offer
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case m, open := <-incoming:
				if !open {
					return
				}
				switch m.Kind {
				case "answer":
					select {
					case remote <- m:
					default:
					}
				case "cand":
					if c, err := ice.UnmarshalCandidate(m.Cand); err == nil {
						agent.AddRemoteCandidate(c)
					}
				}
			}
		}
	}()
	var creds Msg
	select {
	case creds = <-remote:
	case <-ctx.Done():
		return nil, errors.New("direct: no answer from the peer")
	}

	var iceConn *ice.Conn
	if controlling {
		iceConn, err = agent.Dial(ctx, creds.Ufrag, creds.Pwd)
	} else {
		iceConn, err = agent.Accept(ctx, creds.Ufrag, creds.Pwd)
	}
	if err != nil {
		return nil, fmt.Errorf("direct: no path through the NATs: %w", err)
	}

	pc := &packetConn{c: iceConn}
	tr := &quic.Transport{Conn: pc}
	release := func() { tr.Close(); agent.Close() }
	qconf := &quic.Config{KeepAlivePeriod: 10 * time.Second, MaxIdleTimeout: 60 * time.Second}
	tlsConf = tlsConf.Clone()
	tlsConf.NextProtos = []string{ALPN}

	var qc *quic.Conn
	var stream *quic.Stream
	if controlling {
		qc, err = tr.Dial(ctx, iceConn.RemoteAddr(), tlsConf, qconf)
		if err == nil {
			stream, err = qc.OpenStreamSync(ctx)
		}
	} else {
		var ln *quic.Listener
		ln, err = tr.Listen(tlsConf, qconf)
		if err == nil {
			qc, err = ln.Accept(ctx)
			if err == nil {
				stream, err = qc.AcceptStream(ctx)
			}
		}
	}
	if err != nil {
		tr.Close()
		return nil, fmt.Errorf("direct: quic: %w", err)
	}
	ok = true
	return &Conn{Stream: stream, qc: qc, release: release}, nil
}

func newAgent(opt Options) (*ice.Agent, error) {
	servers := opt.STUN
	if servers == nil {
		servers = DefaultSTUN
	}
	var urls []*stun.URI
	for _, s := range servers {
		u, err := stun.ParseURI(s)
		if err != nil {
			return nil, err
		}
		urls = append(urls, u)
	}
	types := []ice.CandidateType{ice.CandidateTypeHost}
	if len(urls) > 0 {
		types = append(types, ice.CandidateTypeServerReflexive)
	}
	opts := []ice.AgentOption{
		ice.WithUrls(urls),
		ice.WithNetworkTypes([]ice.NetworkType{ice.NetworkTypeUDP4, ice.NetworkTypeUDP6}),
		ice.WithCandidateTypes(types),
	}
	if opt.Loopback {
		opts = append(opts, ice.WithIncludeLoopback())
	}
	return ice.NewAgentWithOptions(opts...)
}

// packetConn presents the single-peer ICE connection as the PacketConn
// QUIC wants.
type packetConn struct{ c *ice.Conn }

func (p *packetConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, err := p.c.Read(b)
	return n, p.c.RemoteAddr(), err
}

func (p *packetConn) WriteTo(b []byte, _ net.Addr) (int, error) { return p.c.Write(b) }
func (p *packetConn) Close() error                              { return p.c.Close() }
func (p *packetConn) LocalAddr() net.Addr                       { return p.c.LocalAddr() }
func (p *packetConn) SetDeadline(t time.Time) error             { return p.c.SetDeadline(t) }
func (p *packetConn) SetReadDeadline(t time.Time) error         { return p.c.SetReadDeadline(t) }
func (p *packetConn) SetWriteDeadline(t time.Time) error        { return p.c.SetWriteDeadline(t) }
