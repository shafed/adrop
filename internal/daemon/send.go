package daemon

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/shafed/adrop/internal/config"
	"github.com/shafed/adrop/internal/proto"
	"github.com/shafed/adrop/internal/transport"
)

// progressFn receives human-readable progress lines during a send.
type progressFn func(string)

// dialPeer resolves a target (name or fingerprint prefix), dials it, performs
// the Hello exchange, and returns the open connection.
func (d *Daemon) dialPeer(target string) (*tls.Conn, config.Device, error) {
	dev, ok := d.store.Lookup(target)
	if !ok {
		return nil, config.Device{}, fmt.Errorf("no trusted device matching %q", target)
	}
	conn, fp, err := transport.Dial(dev.Addr, d.store.Certificate(), d)
	if err != nil {
		return nil, dev, fmt.Errorf("dial %s (%s): %w", dev.Name, dev.Addr, err)
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
		Addr:        d.tcpAddr,
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

// SendFiles transfers files to target as a single session. Each file's
// SHA-256 is computed and sent in the manifest for receiver verification.
func (d *Daemon) SendFiles(ctx context.Context, target string, paths []string, progress progressFn) error {
	manifest, err := buildManifest(paths)
	if err != nil {
		return err
	}
	conn, dev, err := d.dialPeer(target)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := proto.WriteControl(conn, proto.Header{
		Type:  proto.TypeSessionStart,
		Kind:  proto.KindFiles,
		Files: manifest,
	}); err != nil {
		return err
	}

	for i, m := range manifest {
		if progress != nil {
			progress(fmt.Sprintf("sending %s (%d/%d)…", m.Name, i+1, len(manifest)))
		}
		if err := d.sendOneFile(conn, i, paths[i], m); err != nil {
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
	if progress != nil {
		progress(fmt.Sprintf("sent %d file(s) to %s", len(manifest), dev.Name))
	}
	return nil
}

func (d *Daemon) sendOneFile(conn *tls.Conn, index int, path string, m proto.FileMeta) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := proto.WriteControl(conn, proto.Header{Type: proto.TypeFileHeader, FileIndex: index}); err != nil {
		return err
	}
	buf := make([]byte, proto.ChunkSize)
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			if err := proto.WriteMessage(conn, proto.Header{
				Type: proto.TypeChunk, FileIndex: index, Length: int64(n),
			}, bytesReader(buf[:n])); err != nil {
				return err
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
	conn, dev, err := d.dialPeer(target)
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
	_ = dev
	return nil
}

// buildManifest stats and hashes each path, producing the session manifest.
func buildManifest(paths []string) ([]proto.FileMeta, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("no files to send")
	}
	out := make([]proto.FileMeta, 0, len(paths))
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			return nil, fmt.Errorf("%s is a directory (not supported in MVP)", p)
		}
		sum, err := hashFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, proto.FileMeta{
			Name:   filepath.Base(p),
			Size:   info.Size(),
			SHA256: sum,
		})
	}
	return out, nil
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
