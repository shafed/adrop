package daemon

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/shafed/adrop/internal/proto"
)

// writeRawHeader frames hdr exactly as given. proto.WriteControl zeroes
// Length, so it cannot produce the frames a broken or hostile peer might send.
func writeRawHeader(t *testing.T, w io.Writer, hdr proto.Header) {
	t.Helper()
	raw, err := json.Marshal(hdr)
	if err != nil {
		t.Fatal(err)
	}
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(raw)))
	if _, err := w.Write(append(n[:], raw...)); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

// TestHostileLengthsDoNotCrashReceiver: a paired peer is trusted with files,
// not with the receiver's memory. A negative clipboard length used to reach
// make([]byte, n) and panic, taking the whole daemon down; a negative chunk
// length rewound the size check so a peer could stream past the declared size.
func TestHostileLengthsDoNotCrashReceiver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pc := newTestDaemon(t, ctx, "pc")
	phone := newTestDaemon(t, ctx, "phone")
	pair(t, pc, phone)

	cases := []struct {
		name  string
		start proto.Header
		frame proto.Header
	}{
		{"negative clipboard", proto.Header{Type: proto.TypeSessionStart, Kind: proto.KindClipboard},
			proto.Header{Type: proto.TypeClipboardData, MIME: "text/plain", Length: -1}},
		{"oversized clipboard", proto.Header{Type: proto.TypeSessionStart, Kind: proto.KindClipboard},
			proto.Header{Type: proto.TypeClipboardData, MIME: "text/plain", Length: 1 << 40}},
		{"negative chunk", proto.Header{Type: proto.TypeSessionStart, Kind: proto.KindFiles,
			Files: []proto.FileMeta{{Name: "neg.bin", Size: 4, SHA256: "00"}}},
			proto.Header{Type: proto.TypeChunk, FileIndex: 0, Length: -1 << 20}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, _, err := phone.d.dialPeer(ctx, "pc")
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer conn.Close()
			writeRawHeader(t, conn, tc.start)
			if tc.start.Kind == proto.KindFiles {
				writeRawHeader(t, conn, proto.Header{Type: proto.TypeFileHeader, FileIndex: 0})
			}
			writeRawHeader(t, conn, tc.frame)
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			ack, err := proto.ReadHeader(conn)
			if err == nil && ack.OK {
				t.Fatalf("receiver accepted a frame with length %d", tc.frame.Length)
			}
		})
	}

	// The receiver is still alive and still correct.
	if err := phone.d.SendClipboard(ctx, "pc", []byte("still here"), ""); err != nil {
		t.Fatalf("receiver unusable after hostile frames: %v", err)
	}
	pc.clipMu.Lock()
	defer pc.clipMu.Unlock()
	if string(pc.clip) != "still here" {
		t.Fatalf("clipboard = %q", pc.clip)
	}
}
