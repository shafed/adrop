// Package transport provides key-pinned, mutually-authenticated TLS for adrop.
//
// Neither side trusts a CA. Instead each connection presents this device's
// self-signed certificate, and the peer's certificate is accepted only if its
// SHA-256 fingerprint is in the pinned set (the trusted-device list). This is
// the SPEC's "pinned-TLS" model: protection against MITM without passwords.
package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/shafed/adrop/internal/config"
)

// ErrUntrustedPeer is returned when the peer cert is not pinned.
var ErrUntrustedPeer = errors.New("transport: peer certificate not in trusted set")

// PeerVerifier reports whether a fingerprint is currently trusted.
// It is consulted at handshake time so revocation takes effect immediately.
type PeerVerifier interface {
	IsTrusted(fingerprint string) (name string, ok bool)
}

// pinnedTLSConfig builds a tls.Config that requires a client cert (when
// serving) and verifies the peer's leaf certificate against the pinned set.
// If allowAnyPeer is true (used only during pairing), peer verification is
// skipped — the fingerprint is captured out of band instead.
func pinnedTLSConfig(cert tls.Certificate, verify PeerVerifier, server, allowAnyPeer bool) *tls.Config {
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		// We verify the peer ourselves via the pinned fingerprint, so the
		// standard chain/hostname checks are disabled.
		InsecureSkipVerify: true,
	}
	if server {
		cfg.ClientAuth = tls.RequireAnyClientCert
	}
	if !allowAnyPeer {
		cfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return ErrUntrustedPeer
			}
			fp := config.Fingerprint(rawCerts[0])
			if _, ok := verify.IsTrusted(fp); !ok {
				return fmt.Errorf("%w (fingerprint %s)", ErrUntrustedPeer, fp[:16])
			}
			return nil
		}
	}
	return cfg
}

// PairALPN is the ALPN protocol of a tap-pairing connection: TLS without
// pinning, on the same port as pinned sessions. A client that offers it gets
// an unpinned handshake only while allowPair reports true; everyone else,
// old peers included, goes through the pinned config as before.
const PairALPN = "adrop-pair/1"

// Listen returns a TLS listener on addr that enforces peer pinning. When
// allowPair is non-nil and returns true, a client offering PairALPN is
// admitted without pinning; the caller tells such connections apart by
// ConnectionState().NegotiatedProtocol and must not run a session on them.
func Listen(addr string, cert tls.Certificate, verify PeerVerifier, allowPair func() bool) (net.Listener, error) {
	cfg := pinnedTLSConfig(cert, verify, true, false)
	if allowPair != nil {
		pairCfg := pinnedTLSConfig(cert, verify, true, true)
		pairCfg.NextProtos = []string{PairALPN}
		cfg.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			if slices.Contains(hello.SupportedProtos, PairALPN) && allowPair() {
				return pairCfg, nil
			}
			return nil, nil
		}
	}
	return tls.Listen("tcp", addr, cfg)
}

// ErrNoTapPairing is returned by DialPair when the peer did not take the
// tap-pairing protocol: an old version, or tap pairing is off there.
var ErrNoTapPairing = errors.New("transport: peer does not accept tap pairing")

// DialPair opens a tap-pairing connection to addr: both sides present their
// certificates, neither is checked. It returns the peer's fingerprint, which
// the users then compare through pairing.Code.
func DialPair(ctx context.Context, addr string, cert tls.Certificate) (*tls.Conn, string, error) {
	cfg := pinnedTLSConfig(cert, nil, false, true)
	cfg.NextProtos = []string{PairALPN}
	d := tls.Dialer{Config: cfg}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		if strings.Contains(err.Error(), "bad certificate") {
			return nil, "", ErrNoTapPairing
		}
		return nil, "", err
	}
	conn := raw.(*tls.Conn)
	if conn.ConnectionState().NegotiatedProtocol != PairALPN {
		conn.Close()
		return nil, "", ErrNoTapPairing
	}
	fp, err := PeerFingerprint(conn)
	if err != nil {
		conn.Close()
		return nil, "", err
	}
	return conn, fp, nil
}

// Dial connects to addr, enforcing peer pinning, and returns the established
// connection along with the peer's certificate fingerprint.
func Dial(addr string, cert tls.Certificate, verify PeerVerifier) (*tls.Conn, string, error) {
	return DialTimeout(addr, cert, verify, 0)
}

// DialTimeout is Dial giving up on the TCP connect after timeout (0: the
// system's own, about two minutes for a host that does not answer).
func DialTimeout(addr string, cert tls.Certificate, verify PeerVerifier, timeout time.Duration) (*tls.Conn, string, error) {
	cfg := pinnedTLSConfig(cert, verify, false, false)
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: timeout}, "tcp", addr, cfg)
	if err != nil {
		return nil, "", err
	}
	fp, err := PeerFingerprint(conn)
	if err != nil {
		conn.Close()
		return nil, "", err
	}
	return conn, fp, nil
}

// PeerFingerprint extracts the leaf-cert fingerprint from a handshaken conn.
func PeerFingerprint(conn *tls.Conn) (string, error) {
	if err := conn.Handshake(); err != nil {
		return "", err
	}
	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return "", ErrUntrustedPeer
	}
	return config.Fingerprint(state.PeerCertificates[0].Raw), nil
}

// PinnedConfig is the pinned TLS config of this device for transports other
// than TCP (QUIC over a direct channel): the peer must present a trusted
// certificate, exactly as on the LAN listener and dialer.
func PinnedConfig(cert tls.Certificate, verify PeerVerifier, server bool) *tls.Config {
	return pinnedTLSConfig(cert, verify, server, false)
}
