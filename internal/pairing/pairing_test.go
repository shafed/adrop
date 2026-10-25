package pairing

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/shafed/adrop/internal/config"
	"rsc.io/qr"
)

func TestEncodeDecodeRoundtrip(t *testing.T) {
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := Payload{
		Name:        "pc",
		Fingerprint: store.Fingerprint(),
		CertPEM:     string(store.CertPEM()),
		Addr:        "192.168.1.5:53127",
	}
	uri, err := Encode(p)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.HasPrefix(uri, URIScheme) {
		t.Fatalf("missing scheme: %s", uri)
	}
	got, err := Decode(uri)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Fingerprint != p.Fingerprint || got.Addr != p.Addr || got.Name != p.Name {
		t.Fatalf("mismatch: %+v", got)
	}
}

func TestDecodeLegacyJSONPayload(t *testing.T) {
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := Payload{
		Version:     1,
		Name:        "pc",
		Fingerprint: store.Fingerprint(),
		CertPEM:     string(store.CertPEM()),
		Addr:        "192.168.1.5:53127",
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	uri := URIScheme + base64.RawURLEncoding.EncodeToString(raw)
	got, err := Decode(uri)
	if err != nil {
		t.Fatalf("decode legacy: %v", err)
	}
	if got.Fingerprint != p.Fingerprint || got.Addr != p.Addr || got.Name != p.Name {
		t.Fatalf("mismatch: %+v", got)
	}
}

func TestCompactEncodeIsSmallerThanLegacyJSON(t *testing.T) {
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := Payload{
		Version:     2,
		Name:        "pc",
		Fingerprint: store.Fingerprint(),
		CertPEM:     string(store.CertPEM()),
		Addr:        "192.168.1.5:53127",
	}
	compactURI, err := Encode(p)
	if err != nil {
		t.Fatalf("encode compact: %v", err)
	}
	p.Version = 1
	legacyRaw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	legacyURI := URIScheme + base64.RawURLEncoding.EncodeToString(legacyRaw)
	if len(compactURI) >= len(legacyURI) {
		t.Fatalf("compact URI length %d >= legacy length %d", len(compactURI), len(legacyURI))
	}
}

func TestDecodeRejectsTamperedFingerprint(t *testing.T) {
	store, _ := config.Open(t.TempDir())
	p := Payload{
		Name:        "pc",
		Fingerprint: strings.Repeat("0", 64), // wrong fingerprint for the cert
		CertPEM:     string(store.CertPEM()),
		Addr:        "10.0.0.1:1",
	}
	uri, err := Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(uri); err == nil {
		t.Fatal("expected fingerprint-mismatch error")
	}
}

func TestRenderTerminalNonEmpty(t *testing.T) {
	art, err := RenderTerminal("adrop://pair?d=AAAA")
	if err != nil {
		t.Fatal(err)
	}
	if len(art) == 0 || !strings.Contains(art, "\n") {
		t.Fatal("expected multi-line QR art")
	}
}

func TestEncodeDecodeWithReticulumKey(t *testing.T) {
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := Payload{
		Name:        "pc",
		Fingerprint: store.Fingerprint(),
		CertPEM:     string(store.CertPEM()),
		Addr:        "192.168.1.5:53127",
		RNS:         strings.Repeat("ab", 64),
	}
	uri, err := Encode(p)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := Decode(uri)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.RNS != p.RNS || got.Version != compactRNSVersion || got.Fingerprint != p.Fingerprint {
		t.Fatalf("mismatch: %+v", got)
	}
	// The QR must stay scannable by a phone camera from a screen: keep it
	// at or below version 25 (117x117 modules).
	code, err := qr.Encode(uri, qr.M)
	if err != nil {
		t.Fatal(err)
	}
	version := (code.Size - 17) / 4
	t.Logf("URI with Reticulum key: %d bytes, QR version %d", len(uri), version)
	if version > 25 {
		t.Fatalf("QR version %d is too dense for a phone camera", version)
	}
	if _, err := RenderTerminal(uri); err != nil {
		t.Fatalf("render: %v", err)
	}
}
