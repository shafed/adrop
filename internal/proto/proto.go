// Package proto defines adrop's application-layer wire protocol that runs
// over a mutually-authenticated, key-pinned TLS connection.
//
// Framing: every message is
//
//	[4 bytes big-endian header length][JSON header][raw payload bytes]
//
// The header's Length field gives the payload size. For file chunks the
// payload is the raw file bytes; for control messages it is empty.
//
// A transfer session looks like:
//
//	-> Hello              (both sides identify by fingerprint + name)
//	<- Hello
//	-> SessionStart       (kind=files|clipboard, file manifest)
//	-> FileHeader, chunks..., FileEnd   (repeated per file, for kind=files)
//	-> ClipboardData                    (for kind=clipboard)
//	-> SessionEnd
//	<- Ack                (per file and/or session, carries ok + error)
package proto

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ProtocolVersion is bumped on incompatible wire changes.
const ProtocolVersion = 1

// MaxHeaderSize guards against absurd allocations from a hostile/corrupt peer.
const MaxHeaderSize = 1 << 20 // 1 MiB of JSON header is already huge

// ChunkSize is the file payload size per Chunk message.
const ChunkSize = 256 * 1024

// Type enumerates message kinds.
type Type string

const (
	TypeHello         Type = "hello"
	TypeSessionStart  Type = "session_start"
	TypeFileHeader    Type = "file_header"
	TypeChunk         Type = "chunk"
	TypeFileEnd       Type = "file_end"
	TypeClipboardData Type = "clipboard"
	TypeSessionEnd    Type = "session_end"
	TypeAck           Type = "ack"
	// TypeProgress is an optional advisory message sent by the sender after
	// each chunk to report per-file transfer progress. Old peers that don't
	// understand it will never receive it because the sender only emits it
	// in a dedicated message (not mixed into chunk frames). Receivers that
	// don't recognise the type should skip it; the existing
	// ignoreUnknownKeys / omitempty guards ensure backward compatibility.
	TypeProgress Type = "progress"

	// TypeResumeQuery is sent by the sender before TypeFileHeader when the
	// session was started with Resume=true. The receiver checks for a
	// pre-existing .adrop-part file and replies with TypeResumeOffer.
	TypeResumeQuery Type = "resume_query"

	// TypeResumeOffer is sent by the receiver in response to TypeResumeQuery.
	// BytesDone reports how many bytes of the partial file already exist
	// (0 if none). The sender seeks to that offset before streaming chunks.
	TypeResumeOffer Type = "resume_offer"
)

// SessionKind distinguishes a file-transfer session from a clipboard push.
type SessionKind string

const (
	KindFiles     SessionKind = "files"
	KindClipboard SessionKind = "clipboard"
)

// FileMeta describes one file in a session manifest.
type FileMeta struct {
	Name    string `json:"name"`              // base name only; no path components
	Size    int64  `json:"size"`              // bytes
	SHA256  string `json:"sha256"`            // hex digest for integrity check
	RelPath string `json:"rel_path,omitempty"` // relative path within a directory transfer
}

// Header is the JSON envelope prefixing every message.
type Header struct {
	Type    Type `json:"type"`
	Version int  `json:"version,omitempty"`

	// Hello
	Fingerprint string `json:"fingerprint,omitempty"`
	Name        string `json:"name,omitempty"`
	// Addr is the sender's own LAN listen address ("host:port"), so a peer
	// that learns about us via an inbound (e.g. pairing back-connect)
	// connection records a dialable address rather than our ephemeral source
	// port.
	Addr string `json:"addr,omitempty"`
	// FcmToken is the Firebase Cloud Messaging registration token of the
	// sending device. Android sends this in Hello so the PC can wake it via
	// FCM when a subsequent direct dial fails. Old peers ignore unknown fields.
	FcmToken string `json:"fcm_token,omitempty"`

	// SessionStart
	Kind   SessionKind `json:"kind,omitempty"`
	Files  []FileMeta  `json:"files,omitempty"`
	// Resume, when true, signals that the sender will emit a TypeResumeQuery
	// before each TypeFileHeader. Old receivers that don't set Resume never
	// see these frames, so the field is safe to add with omitempty.
	Resume bool        `json:"resume,omitempty"`

	// SHA256 carries the file hash in TypeResumeQuery so the receiver can
	// detect a partial file from a different original (hash mismatch → restart).
	SHA256 string `json:"sha256,omitempty"`

	// FileHeader / Chunk / FileEnd reference a file by index into Files.
	FileIndex int `json:"file_index,omitempty"`

	// ClipboardData
	MIME string `json:"mime,omitempty"` // e.g. "text/plain"

	// Ack
	OK    bool   `json:"ok,omitempty"`
	Error string `json:"error,omitempty"`

	// Length is the number of payload bytes that follow this header.
	Length int64 `json:"length,omitempty"`

	// Progress fields — carried in TypeProgress messages.
	// BytesDone is the number of bytes of the current file sent so far.
	// TotalBytes is the total size of the current file (mirrors FileMeta.Size).
	// Both fields use omitempty so old peers that receive these frames just
	// see an unknown type and can safely skip them.
	BytesDone  int64 `json:"bytes_done,omitempty"`
	TotalBytes int64 `json:"total_bytes,omitempty"`
}

// ErrTooLarge is returned when a frame exceeds protocol limits.
var ErrTooLarge = errors.New("proto: frame exceeds limit")

// WriteMessage writes a header followed by exactly hdr.Length payload bytes
// read from payload (which may be nil when Length==0).
func WriteMessage(w io.Writer, hdr Header, payload io.Reader) error {
	raw, err := json.Marshal(hdr)
	if err != nil {
		return err
	}
	if len(raw) > MaxHeaderSize {
		return ErrTooLarge
	}
	var lenbuf [4]byte
	binary.BigEndian.PutUint32(lenbuf[:], uint32(len(raw)))
	if _, err := w.Write(lenbuf[:]); err != nil {
		return err
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}
	if hdr.Length > 0 {
		if payload == nil {
			return fmt.Errorf("proto: header declares %d payload bytes but payload is nil", hdr.Length)
		}
		n, err := io.CopyN(w, payload, hdr.Length)
		if err != nil {
			return err
		}
		if n != hdr.Length {
			return io.ErrShortWrite
		}
	}
	return nil
}

// WriteControl is a convenience for messages with no payload.
func WriteControl(w io.Writer, hdr Header) error {
	hdr.Length = 0
	return WriteMessage(w, hdr, nil)
}

// ReadHeader reads and decodes the next message header. The caller is then
// responsible for consuming exactly hdr.Length payload bytes from the same
// reader before calling ReadHeader again.
func ReadHeader(r io.Reader) (Header, error) {
	var lenbuf [4]byte
	if _, err := io.ReadFull(r, lenbuf[:]); err != nil {
		return Header{}, err
	}
	n := binary.BigEndian.Uint32(lenbuf[:])
	if n == 0 || n > MaxHeaderSize {
		return Header{}, ErrTooLarge
	}
	raw := make([]byte, n)
	if _, err := io.ReadFull(r, raw); err != nil {
		return Header{}, err
	}
	var hdr Header
	if err := json.Unmarshal(raw, &hdr); err != nil {
		return Header{}, fmt.Errorf("proto: decode header: %w", err)
	}
	return hdr, nil
}
