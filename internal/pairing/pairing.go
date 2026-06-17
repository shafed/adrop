// Package pairing implements one-time device pairing via QR codes.
//
// The PC shows a QR encoding its identity (certificate fingerprint + the full
// PEM cert so the scanner can pin it), a human name, and its current LAN
// address. The phone scans it, records the PC as trusted, and connects back to
// complete a mutual exchange so the PC learns the phone's fingerprint too.
//
// The QR payload is a compact JSON object, base64url-wrapped behind an
// "adrop://pair?d=" URI so generic QR scanners surface it sensibly.
package pairing

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/shafed/adrop/internal/config"
	"rsc.io/qr"
)

// URIScheme prefixes the QR payload.
const URIScheme = "adrop://pair?d="

const compactPayloadVersion = 2

// Payload is the data carried in a pairing QR code.
type Payload struct {
	Version     int    `json:"v"`
	Name        string `json:"n"`    // device name offering the pairing
	Fingerprint string `json:"fp"`   // SHA-256 of cert DER (the pin)
	CertPEM     string `json:"cert"` // full PEM cert, so scanner can verify fp
	Addr        string `json:"addr"` // host:port to dial back
}

// Encode builds the "adrop://pair?d=..." URI for a payload. Version 2 uses a
// compact binary payload to keep terminal QR codes small. Decode still accepts
// the legacy version-1 JSON/PEM payload for existing pairing URIs.
func Encode(p Payload) (string, error) {
	if p.Version == 0 {
		p.Version = compactPayloadVersion
	}
	if p.Version != compactPayloadVersion {
		return "", fmt.Errorf("pairing: unsupported encode version %d", p.Version)
	}
	der, err := config.CertDERFromPEM([]byte(p.CertPEM))
	if err != nil {
		return "", fmt.Errorf("pairing: bad cert: %w", err)
	}
	fp, err := hex.DecodeString(p.Fingerprint)
	if err != nil || len(fp) != 32 {
		return "", fmt.Errorf("pairing: fingerprint must be 32 bytes of lowercase hex")
	}
	name := []byte(p.Name)
	addr := []byte(p.Addr)
	if len(name) == 0 || len(name) > 255 {
		return "", fmt.Errorf("pairing: name length must be 1..255 bytes")
	}
	if len(addr) == 0 || len(addr) > 255 {
		return "", fmt.Errorf("pairing: addr length must be 1..255 bytes")
	}
	if len(der) > 65535 {
		return "", fmt.Errorf("pairing: cert too large")
	}

	raw := make([]byte, 0, 1+1+len(name)+1+len(addr)+32+2+len(der))
	raw = append(raw, compactPayloadVersion)
	raw = append(raw, byte(len(name)))
	raw = append(raw, name...)
	raw = append(raw, byte(len(addr)))
	raw = append(raw, addr...)
	raw = append(raw, fp...)
	raw = binary.BigEndian.AppendUint16(raw, uint16(len(der)))
	raw = append(raw, der...)
	return URIScheme + base64.RawURLEncoding.EncodeToString(raw), nil
}

// Decode parses a pairing URI back into a Payload and validates that the
// embedded certificate matches the advertised fingerprint (so a tampered QR
// can't pin a fingerprint that doesn't belong to the cert).
func Decode(uri string) (Payload, error) {
	s, ok := strings.CutPrefix(uri, URIScheme)
	if !ok {
		return Payload{}, fmt.Errorf("pairing: not an adrop pairing URI")
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Payload{}, fmt.Errorf("pairing: decode payload: %w", err)
	}
	if len(raw) > 0 && raw[0] != '{' {
		return decodeCompact(raw)
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return Payload{}, fmt.Errorf("pairing: parse payload: %w", err)
	}
	der, err := config.CertDERFromPEM([]byte(p.CertPEM))
	if err != nil {
		return Payload{}, fmt.Errorf("pairing: bad cert: %w", err)
	}
	if got := config.Fingerprint(der); got != p.Fingerprint {
		return Payload{}, fmt.Errorf("pairing: fingerprint mismatch (cert=%s claimed=%s)", short(got), short(p.Fingerprint))
	}
	return p, nil
}

func decodeCompact(raw []byte) (Payload, error) {
	if len(raw) < 1 || raw[0] != compactPayloadVersion {
		return Payload{}, fmt.Errorf("pairing: unsupported compact payload version")
	}
	i := 1
	readString := func(label string) (string, error) {
		if i >= len(raw) {
			return "", fmt.Errorf("pairing: compact payload missing %s length", label)
		}
		n := int(raw[i])
		i++
		if n == 0 || i+n > len(raw) {
			return "", fmt.Errorf("pairing: compact payload bad %s", label)
		}
		s := string(raw[i : i+n])
		i += n
		return s, nil
	}
	name, err := readString("name")
	if err != nil {
		return Payload{}, err
	}
	addr, err := readString("addr")
	if err != nil {
		return Payload{}, err
	}
	if i+32 > len(raw) {
		return Payload{}, fmt.Errorf("pairing: compact payload missing fingerprint")
	}
	fp := hex.EncodeToString(raw[i : i+32])
	i += 32
	if i+2 > len(raw) {
		return Payload{}, fmt.Errorf("pairing: compact payload missing cert length")
	}
	certLen := int(binary.BigEndian.Uint16(raw[i : i+2]))
	i += 2
	if certLen == 0 || i+certLen != len(raw) {
		return Payload{}, fmt.Errorf("pairing: compact payload bad cert length")
	}
	der := raw[i : i+certLen]
	if got := config.Fingerprint(der); got != fp {
		return Payload{}, fmt.Errorf("pairing: fingerprint mismatch (cert=%s claimed=%s)", short(got), short(fp))
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return Payload{
		Version:     compactPayloadVersion,
		Name:        name,
		Fingerprint: fp,
		CertPEM:     string(certPEM),
		Addr:        addr,
	}, nil
}

func short(s string) string {
	if len(s) <= 16 {
		return s
	}
	return s[:16]
}

// RenderTerminal returns an ASCII-art QR for the URI, suitable for a terminal
// with a dark-on-light or light-on-dark scheme. It uses half-block characters
// so the code stays roughly square in typical fonts.
func RenderTerminal(uri string) (string, error) {
	code, err := qr.Encode(uri, qr.L)
	if err != nil {
		return "", err
	}
	size := code.Size
	var b strings.Builder
	// Quiet zone + two rows per text line using upper/lower half blocks.
	const quiet = 1
	dim := size + quiet*2
	at := func(x, y int) bool {
		if x < quiet || y < quiet || x >= size+quiet || y >= size+quiet {
			return false // quiet zone = light
		}
		return code.Black(x-quiet, y-quiet)
	}
	for y := 0; y < dim; y += 2 {
		for x := 0; x < dim; x++ {
			top := at(x, y)
			bot := y+1 < dim && at(x, y+1)
			switch {
			case top && bot:
				b.WriteRune('█')
			case top && !bot:
				b.WriteRune('▀')
			case !top && bot:
				b.WriteRune('▄')
			default:
				b.WriteRune(' ')
			}
		}
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// RenderPNG returns PNG bytes for the URI (used by a GUI/window renderer).
func RenderPNG(uri string) ([]byte, error) {
	code, err := qr.Encode(uri, qr.L)
	if err != nil {
		return nil, err
	}
	return code.PNG(), nil
}
