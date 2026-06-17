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
		Addr:        d.tcpAddr,
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
	var saved []string
	for {
		hdr, err := proto.ReadHeader(conn)
		if err != nil {
			return err
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
			continue
		}
		// TypeResumeQuery: sender asks how many bytes of this file we already have.
		// Reply with TypeResumeOffer carrying the .adrop-part size (0 if none).
		if hdr.Type == proto.TypeResumeQuery {
			var partBytes int64
			if resume && hdr.FileIndex >= 0 && hdr.FileIndex < len(manifest) {
				partBytes = d.partialFileBytes(manifest[hdr.FileIndex], hdr.SHA256)
			}
			_ = proto.WriteControl(conn, proto.Header{
				Type:      proto.TypeResumeOffer,
				FileIndex: hdr.FileIndex,
				BytesDone: partBytes,
			})
			continue
		}
		if hdr.Type != proto.TypeFileHeader {
			return fmt.Errorf("expected file_header, got %s", hdr.Type)
		}
		if hdr.FileIndex < 0 || hdr.FileIndex >= len(manifest) {
			return fmt.Errorf("file index %d out of range", hdr.FileIndex)
		}
		meta := manifest[hdr.FileIndex]
		var resumeOffset int64
		if resume {
			resumeOffset = d.partialFileBytes(meta, meta.SHA256)
		}
		path, err := d.receiveOneFile(conn, meta, resumeOffset)
		if err != nil {
			_ = proto.WriteControl(conn, proto.Header{
				Type: proto.TypeAck, FileIndex: hdr.FileIndex, OK: false, Error: err.Error(),
			})
			return fmt.Errorf("file %q: %w", meta.Name, err)
		}
		saved = append(saved, path)
		_ = proto.WriteControl(conn, proto.Header{
			Type: proto.TypeAck, FileIndex: hdr.FileIndex, OK: true,
		})
		d.logger.Printf("received %s (%d bytes) from %s", filepath.Base(path), meta.Size, peerName)
	}
	_ = proto.WriteControl(conn, proto.Header{Type: proto.TypeAck, OK: true})

	summary := fmt.Sprintf("Received %d file(s) from %s", len(saved), peerName)
	body := strings.Join(baseNames(saved), ", ")
	_ = notify.Send(ctx, summary, body)
	return nil
}

// partialFileBytes returns the size of a .adrop-part file for meta,
// verifying it belongs to the same original via sha256. Returns 0 if absent,
// already complete, or hash-mismatched.
func (d *Daemon) partialFileBytes(meta proto.FileMeta, sha256hash string) int64 {
	var dest string
	if meta.RelPath != "" {
		dest = filepath.Join(d.downloadDir, filepath.FromSlash(meta.RelPath))
	} else {
		dest = filepath.Join(d.downloadDir, sanitizeName(meta.Name))
	}
	tmp := dest + ".adrop-part"
	fi, err := os.Stat(tmp)
	if err != nil || fi.Size() == 0 || fi.Size() >= meta.Size {
		return 0
	}
	if sha256hash != "" && sha256hash != meta.SHA256 {
		_ = os.Remove(tmp) // stale partial from a different file
		return 0
	}
	return fi.Size()
}

// receiveOneFile streams chunks for a single file to a uniquely-named path in
// the download dir, verifying the SHA-256 from the manifest. On hash mismatch
// the partial file is removed.
//
// resumeOffset > 0 means a .adrop-part already has that many bytes; the
// function appends to it and seeds the hasher from the existing data.
func (d *Daemon) receiveOneFile(conn *tls.Conn, meta proto.FileMeta, resumeOffset int64) (string, error) {
	var dest string
	if meta.RelPath != "" {
		// Validate: reject any RelPath component that is "..".
		clean := filepath.FromSlash(meta.RelPath)
		for _, part := range strings.Split(clean, string(filepath.Separator)) {
			if part == ".." {
				return "", fmt.Errorf("unsafe rel_path %q", meta.RelPath)
			}
		}
		dest = uniquePath(d.downloadDir, clean)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", err
		}
	} else {
		dest = uniquePath(d.downloadDir, sanitizeName(meta.Name))
	}
	tmp := dest + ".adrop-part"

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

	w := io.MultiWriter(f, hasher)
	cleanup := func() { f.Close(); os.Remove(tmp) }
	for {
		hdr, err := proto.ReadHeader(conn)
		if err != nil {
			cleanup()
			return "", err
		}
		switch hdr.Type {
		case proto.TypeChunk:
			if got+hdr.Length > meta.Size {
				cleanup()
				return "", fmt.Errorf("payload exceeds declared size")
			}
			if _, err := io.CopyN(w, conn, hdr.Length); err != nil {
				cleanup()
				return "", err
			}
			got += hdr.Length
		case proto.TypeProgress:
			// Advisory frame from a newer sender — log it and continue.
			d.logger.Printf("progress: file[%d] %d/%d bytes", hdr.FileIndex, hdr.BytesDone, hdr.TotalBytes)
		case proto.TypeFileEnd:
			if err := f.Close(); err != nil {
				os.Remove(tmp)
				return "", err
			}
			if got != meta.Size {
				os.Remove(tmp)
				return "", fmt.Errorf("size mismatch: got %d want %d", got, meta.Size)
			}
			sum := hex.EncodeToString(hasher.Sum(nil))
			if !strings.EqualFold(sum, meta.SHA256) {
				os.Remove(tmp)
				return "", fmt.Errorf("sha256 mismatch")
			}
			if err := os.Rename(tmp, dest); err != nil {
				os.Remove(tmp)
				return "", err
			}
			return dest, nil
		default:
			cleanup()
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
