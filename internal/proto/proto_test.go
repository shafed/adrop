package proto

import (
	"bytes"
	"io"
	"testing"
)

func TestWriteReadRoundtrip(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte("hello world payload")
	hdr := Header{Type: TypeChunk, FileIndex: 2, Length: int64(len(payload))}
	if err := WriteMessage(&buf, hdr, bytes.NewReader(payload)); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := ReadHeader(&buf)
	if err != nil {
		t.Fatalf("read header: %v", err)
	}
	if got.Type != TypeChunk || got.FileIndex != 2 || got.Length != int64(len(payload)) {
		t.Fatalf("header mismatch: %+v", got)
	}
	out := make([]byte, got.Length)
	if _, err := io.ReadFull(&buf, out); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if !bytes.Equal(out, payload) {
		t.Fatalf("payload mismatch: %q", out)
	}
}

func TestControlNoPayload(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteControl(&buf, Header{Type: TypeSessionEnd}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadHeader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != TypeSessionEnd || got.Length != 0 {
		t.Fatalf("unexpected: %+v", got)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected empty buffer, %d bytes left", buf.Len())
	}
}

func TestRejectOversizeHeader(t *testing.T) {
	var buf bytes.Buffer
	// 4-byte length claiming > MaxHeaderSize.
	buf.Write([]byte{0xff, 0xff, 0xff, 0xff})
	if _, err := ReadHeader(&buf); err != ErrTooLarge {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}
