// Package ipc defines the control protocol between the adrop CLI (thin client)
// and the adrop daemon over a Unix-domain socket.
//
// The wire format is newline-delimited JSON: the client writes one Request
// object terminated by '\n', then the daemon streams zero or more Response
// objects (each newline-terminated), the last of which has Done=true.
// Streaming lets long operations (file send) report progress.
package ipc

import (
	"os"
	"path/filepath"
)

// SocketPath returns the daemon's Unix socket path, honoring XDG_RUNTIME_DIR.
func SocketPath() string {
	if p := os.Getenv("ADROP_SOCKET"); p != "" {
		return p
	}
	if r := os.Getenv("XDG_RUNTIME_DIR"); r != "" {
		return filepath.Join(r, "adrop.sock")
	}
	return filepath.Join(os.TempDir(), "adrop.sock")
}

// Command names the CLI action.
type Command string

const (
	CmdStatus       Command = "status"        // daemon health + identity
	CmdPairShow     Command = "pair-show"      // render this device's pairing QR
	CmdPairAdd      Command = "pair-add"       // trust a scanned/typed peer URI
	CmdDevices      Command = "devices"        // list trusted devices
	CmdRevoke       Command = "revoke"         // remove a trusted device
	CmdSendFiles    Command = "send-files"     // send files to a peer
	CmdSendClip     Command = "send-clipboard" // push local clipboard to a peer
)

// Request is a single CLI->daemon command.
type Request struct {
	Cmd Command `json:"cmd"`

	// Target device (name or fingerprint prefix) for send/revoke.
	Target string `json:"target,omitempty"`

	// PairAdd: the scanned "adrop://pair?d=..." URI and optional local name.
	PairURI string `json:"pair_uri,omitempty"`

	// SendFiles: absolute paths to send.
	Files []string `json:"files,omitempty"`

	// SendClipboard: optional explicit text (otherwise daemon reads wl-paste).
	Text string `json:"text,omitempty"`
}

// Response is one daemon->CLI message. Multiple may stream for one Request.
type Response struct {
	// Progress / log line for the user (printed as-is).
	Line string `json:"line,omitempty"`

	// QR holds terminal-rendered QR art (pair-show).
	QR string `json:"qr,omitempty"`

	// Devices is populated for the devices command.
	Devices []DeviceInfo `json:"devices,omitempty"`

	// Status is populated for the status command.
	Status *StatusInfo `json:"status,omitempty"`

	// Done marks the final response for a request.
	Done bool `json:"done,omitempty"`

	// Err is non-empty if the command failed.
	Err string `json:"err,omitempty"`
}

// DeviceInfo is a trusted-device summary for the CLI.
type DeviceInfo struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	Addr        string `json:"addr"`
	PairedAt    string `json:"paired_at"`
}

// StatusInfo summarizes the running daemon.
type StatusInfo struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	ListenAddr  string `json:"listen_addr"`
	NumDevices  int    `json:"num_devices"`
}
