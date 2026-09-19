package daemon

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shafed/adrop/internal/proto"
)

// TestInterruptedTransferLeavesResumablePartial covers the case the two
// hand-seeded resume tests cannot: a transfer that dies mid-file. The receiver
// used to delete its .adrop-part on any read error, so a dropped connection
// left nothing to resume and the next attempt re-sent the whole file (and, with
// the first attempt's bytes gone, landed under a fresh name).
func TestInterruptedTransferLeavesResumablePartial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pc := newTestDaemon(t, ctx, "pc")
	phone := newTestDaemon(t, ctx, "phone")
	pair(t, pc, phone)

	const chunks = 8
	payload := randomBytes(t, proto.ChunkSize*chunks)
	src := filepath.Join(t.TempDir(), "interrupted.bin")
	mustWrite(t, src, payload)

	// Abort the send after a couple of chunks: sendOneFile checks ctx between
	// chunks, so cancelling here closes the connection mid-file exactly as a
	// phone walking out of Wi-Fi range would.
	sendCtx, abort := context.WithCancel(ctx)
	sent := 0
	err := pc.d.SendFiles(sendCtx, "phone", []string{src}, func(line string) {
		if strings.HasSuffix(line, "%") {
			if sent++; sent == 2 {
				abort()
			}
		}
	})
	abort()
	if err == nil {
		t.Fatalf("aborted SendFiles returned nil error")
	}

	// The receiving side is a separate goroutine: it finishes draining the
	// socket a moment after the sender goes away, so poll rather than race it.
	partPath := filepath.Join(phone.download, "interrupted.bin"+partSuffix)
	fi := waitForFile(t, partPath)
	if fi.Size() == 0 || fi.Size() >= int64(len(payload)) {
		t.Fatalf("partial has %d bytes, want 0 < n < %d", fi.Size(), len(payload))
	}
	if _, err := os.Stat(partMetaPath(partPath)); err != nil {
		t.Fatalf("no partial sidecar after an interrupted transfer: %v", err)
	}

	// Second attempt: it must resume (first progress line past the partial's
	// share) and land on the original name, not a "(1)" duplicate.
	var mu sync.Mutex
	var lines []string
	if err := pc.d.SendFiles(ctx, "phone", []string{src}, func(line string) {
		mu.Lock()
		lines = append(lines, line)
		mu.Unlock()
	}); err != nil {
		t.Fatalf("resumed SendFiles: %v", err)
	}

	assertFileEqual(t, filepath.Join(phone.download, "interrupted.bin"), payload)
	if _, err := os.Stat(filepath.Join(phone.download, "interrupted (1).bin")); err == nil {
		t.Errorf("resumed transfer created a duplicate interrupted (1).bin")
	}
	if _, err := os.Stat(partPath); !os.IsNotExist(err) {
		t.Errorf("%s still exists after the resumed transfer completed", partPath)
	}

	wantPct := int(fi.Size() * 100 / int64(len(payload)))
	mu.Lock()
	defer mu.Unlock()
	first := ""
	for _, l := range lines {
		if strings.HasSuffix(l, "%") {
			first = l
			break
		}
	}
	if first == "" || !containsPct(first, wantPct) {
		t.Errorf("first progress line %q below the %d%% already on disk: the transfer restarted instead of resuming (lines: %v)",
			first, wantPct, lines)
	}
}

// TestConcurrentSameNameReceives sends the same file name twice at the same
// time. Both sessions used to compute the same .adrop-part path and stream into
// it at once: the bytes interleaved, yet each session verified the SHA-256 of
// its own stream, so a corrupted file was renamed into place and acked OK.
func TestConcurrentSameNameReceives(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pc := newTestDaemon(t, ctx, "pc")
	phone := newTestDaemon(t, ctx, "phone")
	pair(t, pc, phone)

	payload := randomBytes(t, proto.ChunkSize*6)
	// Two sources with identical base names, in separate directories.
	srcA := filepath.Join(t.TempDir(), "same.bin")
	srcB := filepath.Join(t.TempDir(), "same.bin")
	mustWrite(t, srcA, payload)
	mustWrite(t, srcB, payload)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, src := range []string{srcA, srcB} {
		wg.Add(1)
		go func(i int, src string) {
			defer wg.Done()
			errs[i] = pc.d.SendFiles(ctx, "phone", []string{src}, nil)
		}(i, src)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent SendFiles[%d]: %v", i, err)
		}
	}

	// Both files must exist and both must be intact — the second one under the
	// auto-renamed name.
	assertFileEqual(t, filepath.Join(phone.download, "same.bin"), payload)
	assertFileEqual(t, filepath.Join(phone.download, "same (1).bin"), payload)
}

// waitForFile polls until path exists (the receiver runs asynchronously) and
// returns its stat, failing the test if it never appears.
func waitForFile(t *testing.T, path string) fs.FileInfo {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		fi, err := os.Stat(path)
		if err == nil {
			return fi
		}
		if time.Now().After(deadline) {
			entries, _ := os.ReadDir(filepath.Dir(path))
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Fatalf("%s never appeared; directory holds %v", path, names)
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}
