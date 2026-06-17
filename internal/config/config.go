// Package config manages adrop's on-disk state: this device's TLS identity
// (a self-signed ECDSA P-256 certificate) and the list of paired/trusted peers.
//
// Layout (under $XDG_CONFIG_HOME/adrop or ~/.config/adrop):
//
//	identity.key   PEM-encoded ECDSA P-256 private key
//	identity.crt   PEM-encoded self-signed certificate (pinned by peers)
//	devices.json   list of trusted peers {name, pubkey-fingerprint, addr}
package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Device is a single trusted peer recorded after pairing.
type Device struct {
	// Name is a human-friendly label (e.g. "pixel-7", "thinkpad").
	Name string `json:"name"`
	// Fingerprint is the SHA-256 of the peer certificate's DER bytes,
	// hex-encoded. This is the value we pin for mutual-TLS verification.
	Fingerprint string `json:"fingerprint"`
	// Addr is the last known "host:port" used to dial the peer.
	Addr string `json:"addr"`
	// PairedAt records when the device was added.
	PairedAt time.Time `json:"paired_at"`
}

// Store holds this device's identity plus the trusted-device list and
// persists changes to disk. It is safe for concurrent use.
type Store struct {
	dir string

	mu       sync.RWMutex
	cert     tls.Certificate
	certDER  []byte
	devices  []Device
	lastPeer string // fingerprint of the most-recently-used send target
}

// Dir returns the config directory, honoring XDG_CONFIG_HOME.
func Dir() string {
	if d := os.Getenv("ADROP_CONFIG_DIR"); d != "" {
		return d
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "adrop")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "adrop")
}

// Open loads the store from dir, creating a fresh identity on first run.
func Open(dir string) (*Store, error) {
	if dir == "" {
		dir = Dir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	s := &Store{dir: dir}
	if err := s.loadOrCreateIdentity(); err != nil {
		return nil, err
	}
	if err := s.loadDevices(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) keyPath() string     { return filepath.Join(s.dir, "identity.key") }
func (s *Store) certPath() string    { return filepath.Join(s.dir, "identity.crt") }
func (s *Store) devicesPath() string { return filepath.Join(s.dir, "devices.json") }

func (s *Store) loadOrCreateIdentity() error {
	keyPEM, errKey := os.ReadFile(s.keyPath())
	crtPEM, errCrt := os.ReadFile(s.certPath())
	if errKey == nil && errCrt == nil {
		cert, err := tls.X509KeyPair(crtPEM, keyPEM)
		if err != nil {
			return fmt.Errorf("parse identity: %w", err)
		}
		s.cert = cert
		s.certDER = cert.Certificate[0]
		return nil
	}
	if !os.IsNotExist(errKey) && errKey != nil {
		return errKey
	}
	return s.createIdentity()
}

func (s *Store) createIdentity() error {
	// ECDSA P-256 rather than Ed25519: Android's TLS stack (BoringSSL/Conscrypt)
	// does not advertise ed25519 among its supported certificate signature
	// algorithms, so an Ed25519 server cert makes the handshake fail with
	// "peer doesn't support any of the certificate's signature algorithms".
	// P-256 is universally supported. The protocol only pins SHA-256 of the
	// DER cert, so the key algorithm is otherwise irrelevant.
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	pub := &priv.PublicKey
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "adrop-" + host},
		NotBefore:    time.Now().Add(-time.Hour),
		// Long-lived: the cert is pinned, not validated against a CA or
		// expiry in the usual sense, but we still bound it.
		NotAfter:              time.Now().Add(100 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		return fmt.Errorf("create certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	crtPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(s.certPath(), crtPEM, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(s.keyPath(), keyPEM, 0o600); err != nil {
		return err
	}
	cert, err := tls.X509KeyPair(crtPEM, keyPEM)
	if err != nil {
		return err
	}
	s.cert = cert
	s.certDER = der
	return nil
}

// Certificate returns this device's TLS certificate for serving and dialing.
func (s *Store) Certificate() tls.Certificate { return s.cert }

// Fingerprint returns this device's own certificate fingerprint.
func (s *Store) Fingerprint() string { return Fingerprint(s.certDER) }

// CertPEM returns the PEM-encoded certificate (shared in the pairing QR).
func (s *Store) CertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.certDER})
}

// Fingerprint computes the canonical pin: hex SHA-256 of DER cert bytes.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// CertDERFromPEM extracts the DER bytes of the first CERTIFICATE block in pemBytes.
func CertDERFromPEM(pemBytes []byte) ([]byte, error) {
	for {
		var blk *pem.Block
		blk, pemBytes = pem.Decode(pemBytes)
		if blk == nil {
			return nil, fmt.Errorf("no CERTIFICATE block in PEM")
		}
		if blk.Type == "CERTIFICATE" {
			return blk.Bytes, nil
		}
	}
}

// devicesFile is the on-disk envelope for devices.json.
// The Devices field was previously written as a bare JSON array; we detect
// that legacy format and migrate transparently on first write.
type devicesFile struct {
	Devices  []Device `json:"devices"`
	LastPeer string   `json:"last_peer,omitempty"` // fingerprint of last-used send target
}

func (s *Store) loadDevices() error {
	data, err := os.ReadFile(s.devicesPath())
	if os.IsNotExist(err) {
		s.devices = nil
		return nil
	}
	if err != nil {
		return err
	}
	// Detect legacy bare-array format (starts with '[').
	trimmed := []byte{}
	for _, b := range data {
		if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
			continue
		}
		trimmed = append(trimmed, b)
		break
	}
	if len(trimmed) > 0 && trimmed[0] == '[' {
		return json.Unmarshal(data, &s.devices)
	}
	var f devicesFile
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	s.devices = f.Devices
	s.lastPeer = f.LastPeer
	return nil
}

func (s *Store) saveDevicesLocked() error {
	data, err := json.MarshalIndent(devicesFile{
		Devices:  s.devices,
		LastPeer: s.lastPeer,
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.devicesPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.devicesPath())
}

// Devices returns a copy of the trusted-device list.
func (s *Store) Devices() []Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Device, len(s.devices))
	copy(out, s.devices)
	return out
}

// AddDevice records (or updates, by fingerprint) a trusted peer.
func (s *Store) AddDevice(d Device) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d.PairedAt.IsZero() {
		d.PairedAt = time.Now()
	}
	for i := range s.devices {
		if s.devices[i].Fingerprint == d.Fingerprint {
			s.devices[i] = d
			return s.saveDevicesLocked()
		}
	}
	s.devices = append(s.devices, d)
	return s.saveDevicesLocked()
}

// UpdateAddr refreshes the last-known address for a fingerprint, if present.
func (s *Store) UpdateAddr(fingerprint, addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.devices {
		if s.devices[i].Fingerprint == fingerprint {
			if s.devices[i].Addr != addr {
				s.devices[i].Addr = addr
				_ = s.saveDevicesLocked()
			}
			return
		}
	}
}

// RemoveDevice revokes a trusted device by name or fingerprint prefix.
// Returns the number of devices removed.
func (s *Store) RemoveDevice(nameOrFp string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.devices[:0:0]
	removed := 0
	for _, d := range s.devices {
		if d.Name == nameOrFp || hasPrefix(d.Fingerprint, nameOrFp) {
			removed++
			continue
		}
		kept = append(kept, d)
	}
	if removed > 0 {
		s.devices = kept
		if err := s.saveDevicesLocked(); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

// TrustedFingerprints returns the set of pinned peer fingerprints.
func (s *Store) TrustedFingerprints() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m := make(map[string]string, len(s.devices))
	for _, d := range s.devices {
		m[d.Fingerprint] = d.Name
	}
	return m
}

// IsTrusted reports whether a fingerprint is pinned, and its device name.
func (s *Store) IsTrusted(fingerprint string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, d := range s.devices {
		if d.Fingerprint == fingerprint {
			return d.Name, true
		}
	}
	return "", false
}

// Lookup finds a device by exact name or fingerprint prefix.
func (s *Store) Lookup(nameOrFp string) (Device, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, d := range s.devices {
		if d.Name == nameOrFp || hasPrefix(d.Fingerprint, nameOrFp) {
			return d, true
		}
	}
	return Device{}, false
}

// LastPeer returns the fingerprint of the most-recently-used send target,
// or "" if none has been recorded yet.
func (s *Store) LastPeer() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastPeer
}

// SetLastPeer records fingerprint as the most-recently-used send target and
// persists the change to disk.
func (s *Store) SetLastPeer(fingerprint string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastPeer == fingerprint {
		return
	}
	s.lastPeer = fingerprint
	_ = s.saveDevicesLocked()
}

func hasPrefix(s, p string) bool {
	return len(p) >= 8 && len(s) >= len(p) && s[:len(p)] == p
}
