package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFolderTransfer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, b := newTestDaemon(t, ctx, "sender"), newTestDaemon(t, ctx, "receiver")
	pair(t, a, b)
	source := filepath.Join(t.TempDir(), "tree")
	for _, dir := range []string{"empty/nested", "docs", "images"} {
		if err := os.MkdirAll(filepath.Join(source, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"docs/same.txt", "images/same.txt", "dots..txt"} {
		if err := os.WriteFile(filepath.Join(source, path), []byte(path), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := a.d.SendFiles(ctx, b.store.Fingerprint(), []string{source + "/"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"docs/same.txt", "images/same.txt", "dots..txt"} {
		got, err := os.ReadFile(filepath.Join(b.download, "tree", path))
		if err != nil || string(got) != path {
			t.Fatalf("%s: %q %v", path, got, err)
		}
	}
	for _, path := range []string{"tree/empty/nested", "tree/docs/same (1).txt"} {
		if _, err := os.Stat(filepath.Join(b.download, path)); err != nil {
			t.Fatal(err)
		}
	}
	empty := filepath.Join(t.TempDir(), "only-empty")
	if err := os.Mkdir(empty, 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.d.SendFiles(ctx, b.store.Fingerprint(), []string{empty}, nil); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(b.download, "only-empty")); err != nil || !info.IsDir() {
		t.Fatalf("empty directory: %v", err)
	}
}

func TestDirectoryPathSafety(t *testing.T) {
	d := &Daemon{downloadDir: t.TempDir()}
	for _, path := range []string{"", ".", "..", "../escape", "/absolute", "a/../b", "a//b", "C:/drive", `a\b`, "a/"} {
		if err := d.createDirectory(path); err == nil {
			t.Errorf("accepted %q", path)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(d.downloadDir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := d.createDirectory("link/escape"); err == nil {
		t.Fatal("followed symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "escape")); !os.IsNotExist(err) {
		t.Fatal("wrote outside receive directory")
	}
}

func TestManifestRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := buildManifest([]string{dir}); err == nil {
		t.Fatal("accepted symlink")
	}
}
