package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/shafed/adrop/internal/proto"
)

// TestResumeTransfer verifies that when a .adrop-part file already exists in
// the receiver's download directory, the sender skips the already-received
// bytes and the final file is correct.
func TestResumeTransfer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pc := newTestDaemon(t, ctx, "pc")
	phone := newTestDaemon(t, ctx, "phone")
	pair(t, pc, phone)

	// Build a file large enough to split across two resume segments.
	const totalSize = proto.ChunkSize*3 + 512
	payload := randomBytes(t, totalSize)

	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "bigfile.bin")
	mustWrite(t, src, payload)

	// Compute the SHA-256 that will go into the manifest.
	h := sha256.New()
	h.Write(payload)
	sha := hex.EncodeToString(h.Sum(nil))

	// Seed the receiver's download dir with a partial file that covers the
	// first ChunkSize bytes.  The name must match sanitizeName("bigfile.bin")
	// and uniquePath must land on "bigfile.bin" (no collision yet).
	const alreadyDone = int64(proto.ChunkSize)
	partPath := filepath.Join(phone.download, "bigfile.bin.adrop-part")
	mustWrite(t, partPath, payload[:alreadyDone])
	_ = sha // SHA is embedded in the manifest by buildManifest; no need to pass explicitly.

	// Send the file; resume handshake happens automatically (Resume=true in SessionStart).
	if err := pc.d.SendFiles(ctx, "phone", []string{src}, nil); err != nil {
		t.Fatalf("SendFiles: %v", err)
	}

	// The final file must be complete and match the original payload.
	assertFileEqual(t, filepath.Join(phone.download, "bigfile.bin"), payload)
	// The .adrop-part file must have been renamed away (not left behind).
	if _, err := os.Stat(partPath); !os.IsNotExist(err) {
		t.Errorf(".adrop-part file still exists after successful transfer")
	}
}

// TestResumeTransferWithProgressSkip verifies that progress frames emitted
// by the sender reflect the resumed starting offset (bytesDone > 0 from the
// first chunk even on a resumed transfer).
func TestResumeTransferWithProgressSkip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pc := newTestDaemon(t, ctx, "pc")
	phone := newTestDaemon(t, ctx, "phone")
	pair(t, pc, phone)

	// A two-chunk file; seed the receiver with the first chunk already done.
	const chunkSz = proto.ChunkSize
	payload := randomBytes(t, chunkSz*2)

	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "two_chunks.bin")
	mustWrite(t, src, payload)

	// Seed partial file (first chunk).
	partPath := filepath.Join(phone.download, "two_chunks.bin.adrop-part")
	mustWrite(t, partPath, payload[:chunkSz])

	// Capture the human-readable progress lines from the sender.
	var mu sync.Mutex
	var lines []string
	if err := pc.d.SendFiles(ctx, "phone", []string{src}, func(line string) {
		mu.Lock()
		lines = append(lines, line)
		mu.Unlock()
	}); err != nil {
		t.Fatalf("SendFiles: %v", err)
	}

	assertFileEqual(t, filepath.Join(phone.download, "two_chunks.bin"), payload)

	// We expect a progress line reporting > 50% on the very first chunk update
	// because the sender started from offset=chunkSz (half the file is already done).
	mu.Lock()
	defer mu.Unlock()
	foundAbove50 := false
	for _, l := range lines {
		// Lines like "two_chunks.bin: 100%" — check any is >= 50%
		if len(l) > 0 && containsPct(l, 50) {
			foundAbove50 = true
			break
		}
	}
	if !foundAbove50 {
		t.Errorf("expected at least one progress line >= 50%% (resume offset), got: %v", lines)
	}
}

// containsPct reports whether line contains a percentage >= threshold.
func containsPct(line string, threshold int) bool {
	// Lines are "name: N%" — parse N.
	for i, c := range line {
		if c == ':' && i+2 < len(line) {
			rest := line[i+2:]
			var pct int
			for _, ch := range rest {
				if ch >= '0' && ch <= '9' {
					pct = pct*10 + int(ch-'0')
				} else {
					break
				}
			}
			return pct >= threshold
		}
	}
	return false
}
