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
	CmdStatus    Command = "status"         // daemon health + identity
	CmdPairShow  Command = "pair-show"      // render this device's pairing QR
	CmdPairAdd   Command = "pair-add"       // trust a scanned/typed peer URI
	CmdDevices   Command = "devices"        // list trusted devices
	CmdRevoke    Command = "revoke"         // remove a trusted device
	CmdRename    Command = "rename"         // rename a trusted device (cosmetic)
	CmdSendFiles Command = "send-files"     // send files to a peer
	CmdSendClip  Command = "send-clipboard" // push local clipboard to a peer
	CmdSubscribe Command = "subscribe"      // long-lived: stream receive events

	CmdNearby      Command = "nearby"       // adrop devices seen on the LAN
	CmdPairWith    Command = "pair-with"    // tap pairing: connect and show the code
	CmdPairPending Command = "pair-pending" // tap pairings waiting for an answer
	CmdPairConfirm Command = "pair-confirm" // answer a tap pairing; waits for its outcome
	CmdPing        Command = "ping"         // signed ping to a paired device over Reticulum
	CmdQueue       Command = "queue"        // sends waiting for a channel
	CmdUnqueue     Command = "unqueue"      // drop a waiting send (ID)
)

// Event is a daemon->GUI push on a CmdSubscribe stream. Each Response on that
// stream carries exactly one Event in Response.Event.
type Event struct {
	Kind      string    `json:"kind"`  // "recv-start" | "recv-progress" | "recv-file-done" | "recv-done" | "recv-error" | "pair-request" | "pair-done" | "pair-error"
	Peer      string    `json:"peer"`  // sending peer's name
	File      string    `json:"file"`  // basename of the current file (if any)
	Index     int       `json:"index"` // 0-based file index within the session
	Count     int       `json:"count"` // total files in the session (if known)
	BytesDone int64     `json:"bytes_done"`
	Total     int64     `json:"total"` // total bytes for the current file (if known)
	Err       string    `json:"err,omitempty"`
	Pair      *PairInfo `json:"pair,omitempty"` // pair-* events
	Path      string    `json:"path,omitempty"` // recv-file-done: where the file landed
}

// Request is a single CLI->daemon command.
type Request struct {
	Cmd Command `json:"cmd"`

	// Target device (name or fingerprint prefix) for send/revoke.
	Target string `json:"target,omitempty"`

	// PairAdd: the scanned "adrop://pair?d=..." URI and optional local name.
	PairURI string `json:"pair_uri,omitempty"`

	// Rename: the new display name for the device named by Target.
	Name string `json:"name,omitempty"`

	// SendFiles: absolute paths to send.
	Files []string `json:"files,omitempty"`

	// SendClipboard: optional explicit text (otherwise daemon reads wl-paste).
	Text string `json:"text,omitempty"`

	// SendClipboard: MIME type of the clipboard data (default "text/plain").
	// When set to e.g. "image/png" and Text is empty, the daemon reads the
	// clipboard via wl-paste --type image/png instead of plain text.
	MIME string `json:"mime,omitempty"`

	// PairConfirm: which tap pairing (PairInfo.ID) and the user's answer.
	ID string `json:"id,omitempty"`
	OK bool   `json:"ok,omitempty"`
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

	// Event is populated on a CmdSubscribe stream (one per Response).
	Event *Event `json:"event,omitempty"`

	// Pair is a tap pairing waiting for the user's answer (pair-with).
	Pair *PairInfo `json:"pair,omitempty"`

	// Pending lists tap pairings waiting for an answer (pair-pending).
	Pending []PairInfo `json:"pending,omitempty"`

	// Queue lists sends waiting for a channel (queue).
	Queue []QueueItem `json:"queue,omitempty"`

	// Nearby lists adrop devices seen on the LAN (nearby).
	Nearby []NearbyInfo `json:"nearby,omitempty"`

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
	Name        string   `json:"name"`
	Fingerprint string   `json:"fingerprint"`
	ListenAddr  string   `json:"listen_addr"`
	NumDevices  int      `json:"num_devices"`
	LastPeer    string   `json:"last_peer,omitempty"` // name of last-used send target
	RNSAddr     string   `json:"rns_addr,omitempty"`  // Reticulum address, when it is on
	RNSHubs     []string `json:"rns_hubs,omitempty"`  // hubs that pass the measurement
}

// PairInfo is one tap pairing: the code both screens show and who is asking.
type PairInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	Code        string `json:"code"`
}

// NearbyInfo is an adrop device announced on the LAN over mDNS.
type NearbyInfo struct {
	Name        string `json:"name"`
	Addr        string `json:"addr"`
	Fingerprint string `json:"fingerprint"`
	Paired      bool   `json:"paired"`
}

// QueueItem is a send waiting for a channel to its device, and why.
type QueueItem struct {
	ID     string   `json:"id"`
	Peer   string   `json:"peer"`
	What   string   `json:"what"`
	Paths  []string `json:"paths,omitempty"`
	Added  string   `json:"added"`
	Reason string   `json:"reason"`
	Tries  int      `json:"tries"`
}
