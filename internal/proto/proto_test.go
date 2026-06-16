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

func TestProgressRoundtrip(t *testing.T) {
	var buf bytes.Buffer
	hdr := Header{
		Type:       TypeProgress,
		FileIndex:  1,
		BytesDone:  1024 * 512,
		TotalBytes: 1024 * 1024,
	}
	if err := WriteControl(&buf, hdr); err != nil {
		t.Fatalf("write progress: %v", err)
	}
	got, err := ReadHeader(&buf)
	if err != nil {
		t.Fatalf("read progress: %v", err)
	}
	if got.Type != TypeProgress {
		t.Fatalf("type: want %s got %s", TypeProgress, got.Type)
	}
	if got.FileIndex != 1 {
		t.Fatalf("file_index: want 1 got %d", got.FileIndex)
	}
	if got.BytesDone != 1024*512 {
		t.Fatalf("bytes_done: want %d got %d", 1024*512, got.BytesDone)
	}
	if got.TotalBytes != 1024*1024 {
		t.Fatalf("total_bytes: want %d got %d", 1024*1024, got.TotalBytes)
	}
}

func TestProgressOmitempty(t *testing.T) {
	// A chunk header with no progress fields should not include those JSON keys.
	var buf bytes.Buffer
	hdr := Header{Type: TypeChunk, FileIndex: 0, Length: 0}
	if err := WriteControl(&buf, hdr); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Parse the raw JSON and ensure bytes_done/total_bytes are absent.
	raw := buf.Bytes()
	// skip the 4-byte length prefix
	jsonBytes := raw[4:]
	if bytes.Contains(jsonBytes, []byte("bytes_done")) {
		t.Fatalf("bytes_done present in non-progress header: %s", jsonBytes)
	}
	if bytes.Contains(jsonBytes, []byte("total_bytes")) {
		t.Fatalf("total_bytes present in non-progress header: %s", jsonBytes)
	}
}
