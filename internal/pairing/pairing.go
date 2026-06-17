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
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shafed/adrop/internal/config"
	"rsc.io/qr"
)

// URIScheme prefixes the QR payload.
const URIScheme = "adrop://pair?d="

// Payload is the data carried in a pairing QR code.
type Payload struct {
	Version     int    `json:"v"`
	Name        string `json:"n"`    // device name offering the pairing
	Fingerprint string `json:"fp"`   // SHA-256 of cert DER (the pin)
	CertPEM     string `json:"cert"` // full PEM cert, so scanner can verify fp
	Addr        string `json:"addr"` // host:port to dial back
}

// Encode builds the "adrop://pair?d=..." URI for a payload.
func Encode(p Payload) (string, error) {
	if p.Version == 0 {
		p.Version = 1
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
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
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return Payload{}, fmt.Errorf("pairing: parse payload: %w", err)
	}
	der, err := config.CertDERFromPEM([]byte(p.CertPEM))
	if err != nil {
		return Payload{}, fmt.Errorf("pairing: bad cert: %w", err)
	}
	if got := config.Fingerprint(der); got != p.Fingerprint {
		return Payload{}, fmt.Errorf("pairing: fingerprint mismatch (cert=%s claimed=%s)", got[:16], p.Fingerprint[:16])
	}
	return p, nil
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
