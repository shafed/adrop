// Package transport provides key-pinned, mutually-authenticated TLS for adrop.
//
// Neither side trusts a CA. Instead each connection presents this device's
// self-signed certificate, and the peer's certificate is accepted only if its
// SHA-256 fingerprint is in the pinned set (the trusted-device list). This is
// the SPEC's "pinned-TLS" model: protection against MITM without passwords.
package transport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"

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

// Listen returns a TLS listener on addr that enforces peer pinning.
func Listen(addr string, cert tls.Certificate, verify PeerVerifier) (net.Listener, error) {
	cfg := pinnedTLSConfig(cert, verify, true, false)
	return tls.Listen("tcp", addr, cfg)
}

// Dial connects to addr, enforcing peer pinning, and returns the established
// connection along with the peer's certificate fingerprint.
func Dial(addr string, cert tls.Certificate, verify PeerVerifier) (*tls.Conn, string, error) {
	cfg := pinnedTLSConfig(cert, verify, false, false)
	conn, err := tls.Dial("tcp", addr, cfg)
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
