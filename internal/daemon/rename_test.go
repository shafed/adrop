package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"testing"

	"github.com/shafed/adrop/internal/config"
	"github.com/shafed/adrop/internal/ipc"
)

// ipcCall runs one Request through handleIPC over an in-memory pipe and returns
// the final Response, mirroring what a CLI/GUI client sees.
func ipcCall(t *testing.T, d *Daemon, req ipc.Request) ipc.Response {
	t.Helper()
	client, server := net.Pipe()
	go d.handleIPC(context.Background(), server)
	defer client.Close()

	if err := json.NewEncoder(client).Encode(req); err != nil {
		t.Fatalf("encode request: %v", err)
	}
	dec := json.NewDecoder(bufio.NewReader(client))
	for {
		var resp ipc.Response
		if err := dec.Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Done {
			return resp
		}
	}
}

// TestIPCRename drives the CmdRename arm end to end: a successful rename shows
// up in CmdDevices, an unknown target is reported as an error, and an unknown
// command still degrades to the "unknown command" error an old daemon returns.
func TestIPCRename(t *testing.T) {
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	fp := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	if err := store.AddDevice(config.Device{Name: "phone", Fingerprint: fp}); err != nil {
		t.Fatalf("add device: %v", err)
	}
	d := &Daemon{store: store, name: "pc"}

	if resp := ipcCall(t, d, ipc.Request{Cmd: ipc.CmdRename, Target: "phone", Name: "pixel"}); resp.Err != "" {
		t.Fatalf("rename failed: %s", resp.Err)
	}
	resp := ipcCall(t, d, ipc.Request{Cmd: ipc.CmdDevices})
	if len(resp.Devices) != 1 || resp.Devices[0].Name != "pixel" {
		t.Fatalf("devices after rename: %+v", resp.Devices)
	}
	if resp.Devices[0].Fingerprint != fp {
		t.Errorf("fingerprint changed by rename: %s", resp.Devices[0].Fingerprint)
	}

	if resp := ipcCall(t, d, ipc.Request{Cmd: ipc.CmdRename, Target: "ghost", Name: "x"}); resp.Err == "" {
		t.Error("renaming an unknown device should report an error")
	}
	if resp := ipcCall(t, d, ipc.Request{Cmd: "no-such-command"}); resp.Err == "" {
		t.Error("unknown command should report an error")
	}
}
