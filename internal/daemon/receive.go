package daemon

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/shafed/adrop/internal/ipc"
	"github.com/shafed/adrop/internal/notify"
	"github.com/shafed/adrop/internal/proto"
)

// handlePeer services an inbound pinned-TLS connection from a trusted peer
// (or an in-window pairing peer). It performs the Hello exchange, then either
// completes pairing or receives a transfer session.
func (d *Daemon) handlePeer(ctx context.Context, raw net.Conn) {
	defer raw.Close()
	conn, ok := raw.(*tls.Conn)
	if !ok {
		d.logger.Printf("peer: non-TLS connection")
		return
	}
	if err := conn.Handshake(); err != nil {
		d.logger.Printf("peer handshake: %v", err)
		return
	}
	fp, err := peerFP(conn)
	if err != nil {
		d.logger.Printf("peer fingerprint: %v", err)
		return
	}

	// Hello exchange: read their hello, send ours.
	hello, err := proto.ReadHeader(conn)
	if err != nil || hello.Type != proto.TypeHello {
		d.logger.Printf("peer: expected hello, got %v (%v)", hello.Type, err)
		return
	}
	if err := proto.WriteControl(conn, proto.Header{
		Type:        proto.TypeHello,
		Version:     proto.ProtocolVersion,
		Fingerprint: d.store.Fingerprint(),
		Name:        d.name,
		Addr:        d.advertiseAddr(),
	}); err != nil {
		d.logger.Printf("peer: send hello: %v", err)
		return
	}

	// If a pairing window is open and this is the expected peer, finalize it.
	if d.tryCompletePairing(fp, hello.Name, hello.Addr, conn.RemoteAddr()) {
		d.logger.Printf("paired with %s (%s)", hello.Name, fp[:16])
		return
	}

	peerName, trusted := d.store.IsTrusted(fp)
	if !trusted {
		d.logger.Printf("peer: rejecting untrusted %s", fp[:16])
		return
	}
	// Self-heal the last-known address from this connection: trust the peer's
	// advertised listen port, substituting the live source IP when the peer
	// didn't name a concrete host. This corrects a stale/wrong stored port
	// (e.g. after a DHCP change) on every inbound connect.
	d.store.UpdateAddr(fp, d.resolvePeerAddr(hello.Addr, conn.RemoteAddr()))
	// Persist the peer's FCM token so we can wake it later if direct dial fails.
	d.store.UpdateFcmToken(fp, hello.FcmToken)

	if err := d.receiveSession(ctx, conn, peerName); err != nil {
		d.logger.Printf("receive from %s: %v", peerName, err)
	}
}

// receiveSession reads one session (files or clipboard) and sends acks.
func (d *Daemon) receiveSession(ctx context.Context, conn *tls.Conn, peerName string) error {
	start, err := proto.ReadHeader(conn)
	if err != nil {
		return err
	}
	if start.Type != proto.TypeSessionStart {
		return fmt.Errorf("expected session_start, got %s", start.Type)
	}

	switch start.Kind {
	case proto.KindClipboard:
		return d.receiveClipboard(ctx, conn, peerName)
	case proto.KindFiles:
		return d.receiveFiles(ctx, conn, peerName, start.Files, start.Resume)
	default:
		return fmt.Errorf("unknown session kind %q", start.Kind)
	}
}

// progressCallback is called when a TypeProgress frame is received from a
// remote sender. fileIndex indexes into the session manifest; bytesDone and
// totalBytes reflect the remote sender's view of per-file progress.
// The default implementation just logs; callers may override this via
// receiveFilesWithProgress when they want to surface progress elsewhere.
type progressCallback func(fileIndex int, bytesDone, totalBytes int64)

func (d *Daemon) receiveClipboard(ctx context.Context, conn *tls.Conn, peerName string) error {
	hdr, err := proto.ReadHeader(conn)
	if err != nil {
		return err
	}
	if hdr.Type != proto.TypeClipboardData {
		return fmt.Errorf("expected clipboard data, got %s", hdr.Type)
	}
	if hdr.Length < 0 || hdr.Length > proto.MaxClipboardSize {
		err := fmt.Errorf("clipboard payload of %d bytes (limit %d)", hdr.Length, proto.MaxClipboardSize)
		_ = proto.WriteControl(conn, proto.Header{Type: proto.TypeAck, OK: false, Error: err.Error()})
		return err
	}
	buf := make([]byte, hdr.Length)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return fmt.Errorf("read clipboard payload: %w", err)
	}
	// Consume the session_end.
	_, _ = proto.ReadHeader(conn)

	if err := d.clipboardSet(ctx, buf, hdr.MIME); err != nil {
		_ = proto.WriteControl(conn, proto.Header{Type: proto.TypeAck, OK: false, Error: err.Error()})
		return fmt.Errorf("set clipboard: %w", err)
	}
	_ = proto.WriteControl(conn, proto.Header{Type: proto.TypeAck, OK: true})

	preview := strings.TrimSpace(string(buf))
	if len(preview) > 60 {
		preview = preview[:60] + "…"
	}
	_ = notify.Send(ctx, "Clipboard from "+peerName, preview)
	d.logger.Printf("clipboard set from %s (%d bytes)", peerName, len(buf))
	return nil
}

func (d *Daemon) receiveFiles(ctx context.Context, conn *tls.Conn, peerName string, manifest []proto.FileMeta, resume bool) error {
	return d.receiveFilesWithProgress(ctx, conn, peerName, manifest, resume, nil)
}

func (d *Daemon) receiveFilesWithProgress(ctx context.Context, conn *tls.Conn, peerName string, manifest []proto.FileMeta, resume bool, onProgress progressCallback) error {
	if len(manifest) == 0 {
		return fmt.Errorf("empty file manifest")
	}
	count := len(manifest)
	d.broadcast(ipc.Event{Kind: "recv-start", Peer: peerName, Count: count})
	// emitErr broadcasts a recv-error before returning; wrap the error returns
	// below so the GUI feed reflects failures (notifications are unchanged).
	emitErr := func(err error) error {
		d.broadcast(ipc.Event{Kind: "recv-error", Peer: peerName, Count: count, Err: err.Error()})
		return err
	}
	// slots holds one exclusive destination claim per file index, taken at the
	// first frame naming that file (resume_query or file_header) and held until
	// the session ends. Two sessions receiving the same file name concurrently
	// therefore stream into different .adrop-part files instead of interleaving
	// their bytes in one.
	slots := make(map[int]*fileSlot)
	defer func() {
		for _, s := range slots {
			s.release()
		}
	}()
	slotFor := func(i int) (*fileSlot, error) {
		if s, ok := slots[i]; ok {
			return s, nil
		}
		s, err := d.reserveSlot(manifest[i])
		if err != nil {
			return nil, err
		}
		slots[i] = s
		return s, nil
	}
	var saved []string
	for {
		hdr, err := proto.ReadHeader(conn)
		if err != nil {
			return emitErr(err)
		}
		if hdr.Type == proto.TypeSessionEnd {
			break
		}
		// TypeProgress is advisory: log it and keep waiting for the next frame.
		if hdr.Type == proto.TypeProgress {
			if onProgress != nil {
				onProgress(hdr.FileIndex, hdr.BytesDone, hdr.TotalBytes)
			} else {
				d.logger.Printf("progress: file[%d] %d/%d bytes from %s",
					hdr.FileIndex, hdr.BytesDone, hdr.TotalBytes, peerName)
			}
			file := ""
			if hdr.FileIndex >= 0 && hdr.FileIndex < len(manifest) {
				file = manifest[hdr.FileIndex].Name
			}
			d.broadcast(ipc.Event{
				Kind: "recv-progress", Peer: peerName, File: file,
				Index: hdr.FileIndex, Count: count,
				BytesDone: hdr.BytesDone, Total: hdr.TotalBytes,
			})
			continue
		}
		// TypeResumeQuery: sender asks how many bytes of this file we already have.
		// Reply with TypeResumeOffer carrying the .adrop-part size (0 if none).
		if hdr.Type == proto.TypeResumeQuery {
			var partBytes int64
			if resume && hdr.FileIndex >= 0 && hdr.FileIndex < len(manifest) {
				// A reservation failure (unsafe rel_path) is reported when the
				// file_header for the same index arrives; here it only means
				// "nothing to resume".
				if slot, err := slotFor(hdr.FileIndex); err == nil {
					partBytes = partialBytes(slot, manifest[hdr.FileIndex])
				}
			}
			_ = proto.WriteControl(conn, proto.Header{
				Type:      proto.TypeResumeOffer,
				FileIndex: hdr.FileIndex,
				BytesDone: partBytes,
			})
			continue
		}
		if hdr.Type != proto.TypeFileHeader {
			return emitErr(fmt.Errorf("expected file_header, got %s", hdr.Type))
		}
		if hdr.FileIndex < 0 || hdr.FileIndex >= len(manifest) {
			return emitErr(fmt.Errorf("file index %d out of range", hdr.FileIndex))
		}
		meta := manifest[hdr.FileIndex]
		slot, err := slotFor(hdr.FileIndex)
		if err != nil {
			_ = proto.WriteControl(conn, proto.Header{
				Type: proto.TypeAck, FileIndex: hdr.FileIndex, OK: false, Error: err.Error(),
			})
			return emitErr(fmt.Errorf("file %q: %w", meta.Name, err))
		}
		var resumeOffset int64
		if resume {
			resumeOffset = partialBytes(slot, meta)
		}
		path, err := d.receiveOneFile(conn, meta, slot, resumeOffset)
		if err != nil {
			_ = proto.WriteControl(conn, proto.Header{
				Type: proto.TypeAck, FileIndex: hdr.FileIndex, OK: false, Error: err.Error(),
			})
			return emitErr(fmt.Errorf("file %q: %w", meta.Name, err))
		}
		saved = append(saved, path)
		_ = proto.WriteControl(conn, proto.Header{
			Type: proto.TypeAck, FileIndex: hdr.FileIndex, OK: true,
		})
		d.logger.Printf("received %s (%d bytes) from %s", filepath.Base(path), meta.Size, peerName)
		d.broadcast(ipc.Event{
			Kind: "recv-file-done", Peer: peerName, File: filepath.Base(path),
			Index: hdr.FileIndex, Count: count, BytesDone: meta.Size, Total: meta.Size,
		})
	}
	_ = proto.WriteControl(conn, proto.Header{Type: proto.TypeAck, OK: true})

	summary := fmt.Sprintf("Received %d file(s) from %s", len(saved), peerName)
	body := strings.Join(baseNames(saved), ", ")
	_ = notify.Send(ctx, summary, body)
	d.broadcast(ipc.Event{Kind: "recv-done", Peer: peerName, Count: len(saved)})
	return nil
}

// partSuffix names the in-progress file that becomes the real one on success.
const partSuffix = ".adrop-part"

// fileSlot is an exclusive claim on one destination path (and its .adrop-part
// sibling) for the duration of a single file transfer.
type fileSlot struct {
	dest    string
	tmp     string
	release func()
}

// reserveSlot claims a free destination for meta, skipping both names that
// already exist on disk and names another in-flight transfer is streaming
// into. The returned slot's release func must be called when the transfer is
// done, successfully or not.
func (d *Daemon) reserveSlot(meta proto.FileMeta) (*fileSlot, error) {
	name := sanitizeName(meta.Name)
	if meta.RelPath != "" {
		// Validate: reject any RelPath component that is "..".
		clean := filepath.FromSlash(meta.RelPath)
		for _, part := range strings.Split(clean, string(filepath.Separator)) {
			if part == ".." {
				return nil, fmt.Errorf("unsafe rel_path %q", meta.RelPath)
			}
		}
		name = clean
	}

	d.slotMu.Lock()
	defer d.slotMu.Unlock()
	if d.slots == nil {
		d.slots = make(map[string]struct{})
	}
	dest := uniquePathExcept(d.downloadDir, name, func(candidate string) bool {
		_, claimed := d.slots[candidate+partSuffix]
		return claimed
	})
	if meta.RelPath != "" {
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, err
		}
	}
	tmp := dest + partSuffix
	d.slots[tmp] = struct{}{}
	var once sync.Once
	return &fileSlot{
		dest: dest,
		tmp:  tmp,
		release: func() {
			once.Do(func() {
				d.slotMu.Lock()
				delete(d.slots, tmp)
				d.slotMu.Unlock()
			})
		},
	}, nil
}

// partMetaPath names the sidecar recording which original a .adrop-part
// belongs to. It is needed because the SHA-256 a sender quotes in a
// resume_query is the hash of *its* file: comparing that to the manifest entry
// for the same file can never reveal that the partial on disk was left by a
// different file which happened to share the name.
func partMetaPath(tmp string) string { return tmp + ".meta" }

func writePartMeta(tmp string, meta proto.FileMeta) {
	_ = os.WriteFile(partMetaPath(tmp), []byte(strings.ToLower(meta.SHA256)), 0o644)
}

func removePart(tmp string) {
	_ = os.Remove(tmp)
	_ = os.Remove(partMetaPath(tmp))
}

// partialBytes reports how many bytes of slot's file are already on disk and
// safe to resume onto: 0 when there is no partial, when it is already as large
// as the expected file, or when it came from a different original.
func partialBytes(slot *fileSlot, meta proto.FileMeta) int64 {
	fi, err := os.Stat(slot.tmp)
	if err != nil || fi.Size() == 0 || fi.Size() >= meta.Size {
		return 0
	}
	recorded, err := os.ReadFile(partMetaPath(slot.tmp))
	if err != nil || !strings.EqualFold(strings.TrimSpace(string(recorded)), meta.SHA256) {
		removePart(slot.tmp) // stale partial from a different file (or a pre-sidecar one)
		return 0
	}
	return fi.Size()
}

// receiveOneFile streams chunks for a single file into the slot's .adrop-part
// file, verifying the SHA-256 from the manifest before renaming it into place.
//
// resumeOffset > 0 means the .adrop-part already has that many bytes; the
// function appends to it and seeds the hasher from the existing data.
//
// A partial left behind by a dropped connection is deliberately kept so the
// next attempt can resume from it; only a partial that cannot be trusted
// (integrity failure, protocol violation) is removed.
func (d *Daemon) receiveOneFile(conn *tls.Conn, meta proto.FileMeta, slot *fileSlot, resumeOffset int64) (string, error) {
	dest, tmp := slot.dest, slot.tmp

	hasher := sha256.New()
	var got int64

	var f *os.File
	var err error
	if resumeOffset > 0 {
		// Seed the hasher from the existing partial so the final digest is correct.
		if existing, rerr := os.Open(tmp); rerr == nil {
			_, _ = io.Copy(hasher, existing)
			existing.Close()
		}
		f, err = os.OpenFile(tmp, os.O_WRONLY|os.O_APPEND, 0o644)
		got = resumeOffset
	} else {
		f, err = os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	}
	if err != nil {
		return "", err
	}
	writePartMeta(tmp, meta)

	w := io.MultiWriter(f, hasher)
	// keep closes the file but leaves the partial in place: the bytes written
	// so far are a valid prefix, so the next send can resume from them.
	keep := func() { f.Close() }
	discard := func() { f.Close(); removePart(tmp) }
	for {
		hdr, err := proto.ReadHeader(conn)
		if err != nil {
			keep()
			return "", err
		}
		switch hdr.Type {
		case proto.TypeChunk:
			if hdr.Length < 0 {
				discard()
				return "", fmt.Errorf("negative chunk length %d", hdr.Length)
			}
			if got+hdr.Length > meta.Size {
				discard()
				return "", fmt.Errorf("payload exceeds declared size")
			}
			if _, err := io.CopyN(w, conn, hdr.Length); err != nil {
				keep()
				return "", err
			}
			got += hdr.Length
		case proto.TypeProgress:
			// Advisory frame from a newer sender — log it and continue.
			d.logger.Printf("progress: file[%d] %d/%d bytes", hdr.FileIndex, hdr.BytesDone, hdr.TotalBytes)
		case proto.TypeFileEnd:
			if err := f.Close(); err != nil {
				removePart(tmp)
				return "", err
			}
			if got != meta.Size {
				removePart(tmp)
				return "", fmt.Errorf("size mismatch: got %d want %d", got, meta.Size)
			}
			sum := hex.EncodeToString(hasher.Sum(nil))
			if !strings.EqualFold(sum, meta.SHA256) {
				removePart(tmp)
				return "", fmt.Errorf("sha256 mismatch")
			}
			if err := os.Rename(tmp, dest); err != nil {
				removePart(tmp)
				return "", err
			}
			_ = os.Remove(partMetaPath(tmp))
			return dest, nil
		default:
			discard()
			return "", fmt.Errorf("unexpected %s during file body", hdr.Type)
		}
	}
}

func baseNames(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
}
