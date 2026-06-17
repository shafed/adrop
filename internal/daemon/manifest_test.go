package daemon

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestBuildManifest_Directory(t *testing.T) {
	root := t.TempDir()

	// Create a directory tree:
	//   root/docs/report.pdf
	//   root/docs/sub/notes.txt
	//   root/img.png
	dirs := []string{
		filepath.Join(root, "docs"),
		filepath.Join(root, "docs", "sub"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(root, "docs", "report.pdf"):     "report content",
		filepath.Join(root, "docs", "sub", "notes.txt"): "notes content",
		filepath.Join(root, "img.png"):                "png content",
	}
	for p, content := range files {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Pass the root directory to buildManifest.
	metas, fsPaths, err := buildManifest([]string{root})
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}

	if len(metas) != 3 {
		t.Fatalf("want 3 entries, got %d", len(metas))
	}
	if len(fsPaths) != len(metas) {
		t.Fatalf("fsPaths length %d != metas length %d", len(fsPaths), len(metas))
	}

	// Collect rel paths and verify no ".." components.
	var relPaths []string
	for i, m := range metas {
		if m.RelPath == "" {
			t.Errorf("entry %d (%s): RelPath is empty", i, m.Name)
		}
		for _, part := range filepath.SplitList(m.RelPath) {
			if part == ".." {
				t.Errorf("entry %d: RelPath %q contains ..", i, m.RelPath)
			}
		}
		relPaths = append(relPaths, m.RelPath)
	}

	// Verify the expected relative paths are present (order may vary).
	dirBase := filepath.Base(root)
	want := []string{
		dirBase + "/docs/report.pdf",
		dirBase + "/docs/sub/notes.txt",
		dirBase + "/img.png",
	}
	sort.Strings(relPaths)
	sort.Strings(want)
	for i, w := range want {
		if relPaths[i] != w {
			t.Errorf("relPaths[%d]: got %q, want %q", i, relPaths[i], w)
		}
	}

	// Verify fsPaths are the actual on-disk files.
	for i, p := range fsPaths {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("fsPaths[%d] %q: %v", i, p, err)
		}
	}
}

func TestBuildManifest_FlatFiles(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "hello.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	metas, fsPaths, err := buildManifest([]string{p})
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("want 1 entry, got %d", len(metas))
	}
	if metas[0].RelPath != "" {
		t.Errorf("flat file should have empty RelPath, got %q", metas[0].RelPath)
	}
	if metas[0].Name != "hello.txt" {
		t.Errorf("Name: got %q, want hello.txt", metas[0].Name)
	}
	if fsPaths[0] != p {
		t.Errorf("fsPath: got %q, want %q", fsPaths[0], p)
	}
}

func TestBuildManifest_Empty(t *testing.T) {
	_, _, err := buildManifest([]string{})
	if err == nil {
		t.Error("expected error for empty paths, got nil")
	}
}
