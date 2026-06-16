package pairing

import (
	"strings"
	"testing"

	"github.com/shafed/adrop/internal/config"
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
