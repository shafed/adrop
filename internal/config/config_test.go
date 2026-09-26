package config

import (
	"os"
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

func TestRenameDevice(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	fp := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	other := "9999999999999999999999999999999999999999999999999999999999999999"
	_ = s.AddDevice(Device{Name: "phone", Fingerprint: fp, Addr: "10.0.0.2:1"})
	_ = s.AddDevice(Device{Name: "tablet", Fingerprint: other})

	// Rename by name, then by fingerprint prefix.
	if err := s.RenameDevice("phone", "pixel"); err != nil {
		t.Fatalf("rename by name: %v", err)
	}
	if err := s.RenameDevice(fp[:16], "pixel 9"); err != nil {
		t.Fatalf("rename by fingerprint prefix: %v", err)
	}

	// Trust is pinned to the fingerprint, so it must survive both renames.
	name, ok := s.IsTrusted(fp)
	if !ok {
		t.Fatal("device lost trust after rename")
	}
	if name != "pixel 9" {
		t.Fatalf("name = %q, want %q", name, "pixel 9")
	}
	if dev, ok := s.Lookup("pixel 9"); !ok || dev.Addr != "10.0.0.2:1" {
		t.Fatalf("lookup after rename: %+v ok=%v", dev, ok)
	}

	if err := s.RenameDevice("nosuchdevice", "x"); err == nil {
		t.Error("renaming an unknown device should fail")
	}
	if err := s.RenameDevice("pixel 9", "  "); err == nil {
		t.Error("renaming to a blank name should fail")
	}
	if err := s.RenameDevice("pixel 9", "tablet"); err == nil {
		t.Error("renaming onto another device's name should fail")
	}

	// The rename is persisted, not just held in memory.
	s2, _ := Open(dir)
	if n, ok := s2.IsTrusted(fp); !ok || n != "pixel 9" {
		t.Fatalf("rename did not persist: %q ok=%v", n, ok)
	}
}

// TestRenameDeviceSaveFailure checks a rename that can't be persisted leaves
// the in-memory name alone, so the daemon never serves a name that isn't on
// disk (it would silently revert on the next restart).
func TestRenameDeviceSaveFailure(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	fp := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	_ = s.AddDevice(Device{Name: "phone", Fingerprint: fp})

	// Read-only config dir: writing the devices.json temp file must fail.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := s.RenameDevice("phone", "pixel"); err == nil {
		t.Skip("rename succeeded despite a read-only config dir (running as root?)")
	}
	if name, _ := s.IsTrusted(fp); name != "phone" {
		t.Fatalf("name = %q after a failed save, want the original %q", name, "phone")
	}
}

func TestRevokeClearsLastPeer(t *testing.T) {
	dir := t.TempDir()
	s1, _ := Open(dir)
	phone := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	laptop := "2222222222222222222222222222222222222222222222222222222222222222"
	_ = s1.AddDevice(Device{Name: "phone", Fingerprint: phone, Addr: "10.0.0.2:1"})
	_ = s1.AddDevice(Device{Name: "laptop", Fingerprint: laptop, Addr: "10.0.0.4:1"})
	s1.SetLastPeer(phone)

	if _, err := s1.RemoveDevice("laptop"); err != nil {
		t.Fatal(err)
	}
	if s1.LastPeer() != phone {
		t.Fatalf("revoking another device changed last peer to %q", s1.LastPeer())
	}

	if _, err := s1.RemoveDevice("phone"); err != nil {
		t.Fatal(err)
	}
	if s1.LastPeer() != "" {
		t.Fatalf("last peer still %q after revoking it", s1.LastPeer())
	}
	s2, _ := Open(dir)
	if s2.LastPeer() != "" {
		t.Fatalf("cleared last peer did not persist: %q", s2.LastPeer())
	}
}

func TestOpenDropsStaleLastPeer(t *testing.T) {
	phone := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	gone := "3333333333333333333333333333333333333333333333333333333333333333"
	for _, tc := range []struct{ lastPeer, want string }{
		{gone, ""},
		{phone, phone},
	} {
		dir := t.TempDir()
		data := `{"devices":[{"name":"phone","fingerprint":"` + phone + `","addr":"10.0.0.2:1"}],"last_peer":"` + tc.lastPeer + `"}`
		if err := os.WriteFile(dir+"/devices.json", []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if s.LastPeer() != tc.want {
			t.Fatalf("last_peer %q loaded as %q, want %q", tc.lastPeer, s.LastPeer(), tc.want)
		}
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
