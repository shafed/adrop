package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shafed/adrop/internal/config"
	"github.com/shafed/adrop/internal/proto"
	"github.com/shafed/adrop/internal/transport"
)

// progressFn receives human-readable progress lines during a send.
type progressFn func(string)

// fileProgressFn receives per-file byte-level progress: (fileIndex, bytesDone, totalBytes).
type fileProgressFn func(fileIndex int, bytesDone, totalBytes int64)

// dialPeer resolves a target (name or fingerprint prefix), dials it, performs
// the Hello exchange, and returns the open connection.
//
// If the initial dial fails and the target has a known FCM token and a relay
// address is configured, dialPeer sends a wake request to the relay (asking
// the phone to open its receive window) and retries once after a short wait.
// Wake retry tuning: after sending an FCM wake, poll the peer this often for up
// to this long, connecting as soon as the phone opens its receive window.
const (
	wakePollInterval = 500 * time.Millisecond
	wakeTimeout      = 15 * time.Second

	// mDNS address-recovery tuning. After a dial fails we first run a blocking
	// one-shot mDNS resolve (refreshAddrViaMDNS, up to its own 3s timeout), which
	// usually corrects the stored address before this loop even runs. The loop
	// then retries the dial while the stored address keeps changing, which also
	// catches a concurrent inbound connect self-healing the address. The timeout
	// is measured from after the resolve, so total recovery latency is the mDNS
	// resolve plus up to mdnsRecoverTimeout.
	mdnsRecoverPoll    = 200 * time.Millisecond
	mdnsRecoverTimeout = 2 * time.Second
)

func (d *Daemon) dialPeer(ctx context.Context, target string) (*tls.Conn, config.Device, error) {
	dev, ok := d.store.Lookup(target)
	if !ok {
		return nil, config.Device{}, fmt.Errorf("no trusted device matching %q", target)
	}
	conn, fp, err := transport.Dial(dev.Addr, d.store.Certificate(), d)
	if err != nil {
		// The peer may have changed IP on the same LAN. Actively refresh its
		// address via a one-shot mDNS resolve, then retry the dial whenever the
		// stored address changes, before falling back to the (slower, relay-
		// dependent) FCM wake path. This recovers a same-network IP change even
		// when no relay is configured. Bounded by mdnsRecoverTimeout (measured
		// from after the resolve) so a truly unreachable peer still proceeds to
		// the wake path / final error.
		d.refreshAddrViaMDNS(ctx)
		deadline := time.Now().Add(mdnsRecoverTimeout)
		for {
			if cur, ok := d.store.Lookup(target); ok && cur.Addr != dev.Addr {
				dev = cur
				d.logger.Printf("retrying %s at addr %s", dev.Name, dev.Addr)
				conn, fp, err = transport.Dial(dev.Addr, d.store.Certificate(), d)
				if err == nil {
					break
				}
			}
			if time.Now().After(deadline) {
				break
			}
			select {
			case <-ctx.Done():
				return nil, dev, ctx.Err()
			case <-time.After(mdnsRecoverPoll):
			}
		}
	}
	if err != nil {
		// Attempt FCM wake if we have the token and a relay is configured.
		if dev.FcmToken != "" && d.relayAddr != "" {
			d.logger.Printf("dial %s failed (%v); sending FCM wake via relay", dev.Name, err)
			if wakeErr := d.wakeViRelay(dev); wakeErr != nil {
				d.logger.Printf("FCM wake failed: %v", wakeErr)
			} else {
				// Poll for the phone to open its receive window instead of
				// blindly sleeping: retry the dial every wakePollInterval until
				// it succeeds or wakeTimeout elapses. This connects within a
				// second or two of the push arriving rather than always waiting
				// the full timeout.
				//
				// Re-Lookup the device each iteration so we pick up a fresh
				// address: when the phone wakes it re-announces over mDNS, which
				// updates the stored addr. Without this we'd keep dialing the
				// stale IP from before the phone moved networks / changed IP.
				d.logger.Printf("FCM wake sent; waiting up to %s for phone to open receive window…", wakeTimeout)
				deadline := time.Now().Add(wakeTimeout)
				for {
					select {
					case <-ctx.Done():
						return nil, dev, ctx.Err()
					case <-time.After(wakePollInterval):
					}
					if cur, ok := d.store.Lookup(target); ok {
						dev = cur
					}
					conn, fp, err = transport.Dial(dev.Addr, d.store.Certificate(), d)
					if err == nil {
						d.logger.Printf("phone %s woke up; connected at %s", dev.Name, dev.Addr)
						break
					}
					if time.Now().After(deadline) {
						break
					}
				}
			}
		}
		if err != nil {
			return nil, dev, fmt.Errorf("dial %s (%s): %w", dev.Name, dev.Addr, err)
		}
	}
	if fp != dev.Fingerprint {
		conn.Close()
		return nil, dev, fmt.Errorf("peer fingerprint mismatch for %s", dev.Name)
	}
	// Hello exchange.
	if err := proto.WriteControl(conn, proto.Header{
		Type:        proto.TypeHello,
		Version:     proto.ProtocolVersion,
		Fingerprint: d.store.Fingerprint(),
		Name:        d.name,
		Addr:        d.advertiseAddr(),
	}); err != nil {
		conn.Close()
		return nil, dev, err
	}
	if _, err := proto.ReadHeader(conn); err != nil { // their hello
		conn.Close()
		return nil, dev, err
	}
	return conn, dev, nil
}

// wakeViRelay POSTs a wake request to the configured relay server, which
// forwards an FCM push to the target device's registration token.
func (d *Daemon) wakeViRelay(dev config.Device) error {
	type wakeRequest struct {
		Fingerprint string `json:"fingerprint"`
		Sender      string `json:"sender"`
		FCMToken    string `json:"fcm_token"`
	}
	body, _ := json.Marshal(wakeRequest{
		Fingerprint: dev.Fingerprint,
		Sender:      d.name,
		FCMToken:    dev.FcmToken,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.relayAddr+"/wake", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("relay: status %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// SendFiles transfers files to target as a single session. Each file's
// SHA-256 is computed and sent in the manifest for receiver verification.
func (d *Daemon) SendFiles(ctx context.Context, target string, paths []string, progress progressFn) error {
	manifest, fsPaths, err := buildManifest(paths)
	if err != nil {
		return err
	}
	conn, dev, err := d.dialPeer(ctx, target)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := proto.WriteControl(conn, proto.Header{
		Type:   proto.TypeSessionStart,
		Kind:   proto.KindFiles,
		Files:  manifest,
		Resume: true,
	}); err != nil {
		return err
	}

	// fileProgress emits a human-readable progress line and sends a TypeProgress
	// frame to the peer so the remote side can show per-file progress too.
	fileProgress := func(fileIndex int, bytesDone, totalBytes int64) {
		if progress != nil {
			pct := int64(0)
			if totalBytes > 0 {
				pct = bytesDone * 100 / totalBytes
			}
			progress(fmt.Sprintf("%s: %d%%", manifest[fileIndex].Name, pct))
		}
		// Best-effort: ignore send errors for advisory progress frames.
		_ = proto.WriteControl(conn, proto.Header{
			Type:       proto.TypeProgress,
			FileIndex:  fileIndex,
			BytesDone:  bytesDone,
			TotalBytes: totalBytes,
		})
	}

	for i, m := range manifest {
		if err := ctx.Err(); err != nil {
			return err // client disconnected (Ctrl-C) — abort before the next file
		}
		if progress != nil {
			progress(fmt.Sprintf("sending %s (%d/%d)…", m.Name, i+1, len(manifest)))
		}
		// Resume query: ask receiver how many bytes of this file it already has.
		if err := proto.WriteControl(conn, proto.Header{
			Type:      proto.TypeResumeQuery,
			FileIndex: i,
			SHA256:    m.SHA256,
		}); err != nil {
			return fmt.Errorf("send resume query for %s: %w", m.Name, err)
		}
		offer, err := proto.ReadHeader(conn)
		if err != nil {
			return fmt.Errorf("read resume offer for %s: %w", m.Name, err)
		}
		if offer.Type != proto.TypeResumeOffer {
			return fmt.Errorf("expected resume_offer for %s, got %s", m.Name, offer.Type)
		}
		resumeOffset := offer.BytesDone

		if err := d.sendOneFile(ctx, conn, i, fsPaths[i], m, resumeOffset, fileProgress); err != nil {
			return fmt.Errorf("send %s: %w", m.Name, err)
		}
		ack, err := proto.ReadHeader(conn)
		if err != nil {
			return fmt.Errorf("read ack for %s: %w", m.Name, err)
		}
		if !ack.OK {
			return fmt.Errorf("peer rejected %s: %s", m.Name, ack.Error)
		}
	}

	if err := proto.WriteControl(conn, proto.Header{Type: proto.TypeSessionEnd}); err != nil {
		return err
	}
	if _, err := proto.ReadHeader(conn); err != nil { // final session ack
		return err
	}
	d.store.SetLastPeer(dev.Fingerprint)
	if progress != nil {
		progress(fmt.Sprintf("sent %d file(s) to %s", len(manifest), dev.Name))
	}
	return nil
}

func (d *Daemon) sendOneFile(ctx context.Context, conn *tls.Conn, index int, path string, m proto.FileMeta, resumeOffset int64, fp fileProgressFn) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if resumeOffset > 0 && resumeOffset < m.Size {
		if _, err := f.Seek(resumeOffset, io.SeekStart); err != nil {
			return fmt.Errorf("seek to resume offset %d: %w", resumeOffset, err)
		}
	} else {
		resumeOffset = 0
	}

	if err := proto.WriteControl(conn, proto.Header{Type: proto.TypeFileHeader, FileIndex: index}); err != nil {
		return err
	}
	buf := make([]byte, proto.ChunkSize)
	bytesSent := resumeOffset
	for {
		if err := ctx.Err(); err != nil {
			return err // client disconnected (Ctrl-C) — stop mid-file
		}
		n, rerr := f.Read(buf)
		if n > 0 {
			if err := proto.WriteMessage(conn, proto.Header{
				Type: proto.TypeChunk, FileIndex: index, Length: int64(n),
			}, bytesReader(buf[:n])); err != nil {
				return err
			}
			bytesSent += int64(n)
			if fp != nil {
				fp(index, bytesSent, m.Size)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	return proto.WriteControl(conn, proto.Header{Type: proto.TypeFileEnd, FileIndex: index})
}

// SendClipboard pushes text to target as a clipboard session.
func (d *Daemon) SendClipboard(ctx context.Context, target string, data []byte, mime string) error {
	if mime == "" {
		mime = "text/plain"
	}
	conn, dev, err := d.dialPeer(ctx, target)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := proto.WriteControl(conn, proto.Header{Type: proto.TypeSessionStart, Kind: proto.KindClipboard}); err != nil {
		return err
	}
	if err := proto.WriteMessage(conn, proto.Header{
		Type: proto.TypeClipboardData, MIME: mime, Length: int64(len(data)),
	}, bytesReader(data)); err != nil {
		return err
	}
	if err := proto.WriteControl(conn, proto.Header{Type: proto.TypeSessionEnd}); err != nil {
		return err
	}
	ack, err := proto.ReadHeader(conn)
	if err != nil {
		return err
	}
	if !ack.OK {
		return fmt.Errorf("peer rejected clipboard: %s", ack.Error)
	}
	d.store.SetLastPeer(dev.Fingerprint)
	return nil
}

// buildManifest stats and hashes each path, producing the session manifest.
// Directories are walked recursively; each file gets a RelPath relative to
// the directory's parent so the receiver can reconstruct the tree.
// Returns (manifest, fsPaths, error) where fsPaths[i] is the actual file path
// on disk for manifest[i].
func buildManifest(paths []string) ([]proto.FileMeta, []string, error) {
	if len(paths) == 0 {
		return nil, nil, fmt.Errorf("no files to send")
	}
	var metas []proto.FileMeta
	var fsPaths []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, nil, err
		}
		if !info.IsDir() {
			sum, err := hashFile(p)
			if err != nil {
				return nil, nil, err
			}
			metas = append(metas, proto.FileMeta{
				Name:   filepath.Base(p),
				Size:   info.Size(),
				SHA256: sum,
			})
			fsPaths = append(fsPaths, p)
			continue
		}
		// Directory: walk and collect all regular files with relative paths.
		// parentDir is the directory containing p, so that the walked files
		// get rel paths starting with the directory name itself.
		parentDir := filepath.Dir(p)
		walkErr := filepath.WalkDir(p, func(entry string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(parentDir, entry)
			if err != nil {
				return err
			}
			// Reject any path that escapes via "..".
			if strings.Contains(rel, "..") {
				return fmt.Errorf("unsafe relative path %q", rel)
			}
			fi, err := d.Info()
			if err != nil {
				return err
			}
			sum, err := hashFile(entry)
			if err != nil {
				return err
			}
			metas = append(metas, proto.FileMeta{
				Name:    filepath.Base(entry),
				Size:    fi.Size(),
				SHA256:  sum,
				RelPath: filepath.ToSlash(rel),
			})
			fsPaths = append(fsPaths, entry)
			return nil
		})
		if walkErr != nil {
			return nil, nil, walkErr
		}
	}
	if len(metas) == 0 {
		return nil, nil, fmt.Errorf("no files to send")
	}
	return metas, fsPaths, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// bytesReader avoids importing bytes in multiple files.
func bytesReader(b []byte) io.Reader { return &sliceReader{b: b} }

type sliceReader struct {
	b []byte
	i int
}

func (r *sliceReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}
