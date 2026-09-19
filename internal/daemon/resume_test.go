package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
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
	partPath := filepath.Join(phone.download, "bigfile.bin"+partSuffix)
	mustWrite(t, partPath, payload[:alreadyDone])
	// The sidecar records which original the partial belongs to; without it the
	// receiver treats the partial as stale and restarts from zero, so seeding it
	// is what makes this a resume test at all.
	writePartMeta(partPath, proto.FileMeta{SHA256: sha})

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

	// A four-chunk file; seed the receiver with three chunks already done, so a
	// resumed send reports >= 75% on its first progress line while a restarted
	// one would report 25%.
	const chunkSz = proto.ChunkSize
	payload := randomBytes(t, chunkSz*4)

	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "two_chunks.bin")
	mustWrite(t, src, payload)

	// Seed partial file (first three chunks) plus the sidecar naming its origin.
	partPath := filepath.Join(phone.download, "two_chunks.bin"+partSuffix)
	mustWrite(t, partPath, payload[:chunkSz*3])
	h := sha256.New()
	h.Write(payload)
	writePartMeta(partPath, proto.FileMeta{SHA256: hex.EncodeToString(h.Sum(nil))})

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

	// The FIRST percentage line must already be >= 75%: the sender started from
	// offset=3*chunkSz. Checking "some line is >= 75%" would pass without resume
	// too, since every transfer ends at 100%.
	mu.Lock()
	defer mu.Unlock()
	first := ""
	for _, l := range lines {
		if strings.HasSuffix(l, "%") {
			first = l
			break
		}
	}
	if first == "" {
		t.Fatalf("no progress percentage lines at all, got: %v", lines)
	}
	if !containsPct(first, 75) {
		t.Errorf("first progress line %q < 75%%: the transfer restarted instead of resuming (lines: %v)", first, lines)
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
