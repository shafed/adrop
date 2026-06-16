package config

import (
	"testing"
)

func TestIdentityPersists(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	fp1 := s1.Fingerprint()

	s2, err := Open(dir) // reopen: should load same identity
	if err != nil {
		t.Fatal(err)
	}
	if s2.Fingerprint() != fp1 {
		t.Fatalf("identity changed across reopen: %s vs %s", fp1, s2.Fingerprint())
	}
}

func TestAddRevokeDevice(t *testing.T) {
	s, _ := Open(t.TempDir())
	fp := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	if err := s.AddDevice(Device{Name: "phone", Fingerprint: fp, Addr: "10.0.0.2:1"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.IsTrusted(fp); !ok {
		t.Fatal("device not trusted after add")
	}
	if name, ok := s.IsTrusted(fp); !ok || name != "phone" {
		t.Fatalf("lookup wrong: %q %v", name, ok)
	}
	// Revoke by fingerprint prefix.
	n, err := s.RemoveDevice(fp[:16])
	if err != nil || n != 1 {
		t.Fatalf("revoke: n=%d err=%v", n, err)
	}
	if _, ok := s.IsTrusted(fp); ok {
		t.Fatal("device still trusted after revoke")
	}
}

func TestAddDevicePersists(t *testing.T) {
	dir := t.TempDir()
	s1, _ := Open(dir)
	fp := "1111111111111111111111111111111111111111111111111111111111111111"
	_ = s1.AddDevice(Device{Name: "tablet", Fingerprint: fp, Addr: "10.0.0.3:1"})

	s2, _ := Open(dir)
	if _, ok := s2.IsTrusted(fp); !ok {
		t.Fatal("device did not persist across reopen")
	}
}
