package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/shafed/adrop/internal/ipc"
)

// dialDaemon connects to the daemon's Unix socket.
func dialDaemon() (net.Conn, error) {
	path := ipc.SocketPath()
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, fmt.Errorf("cannot reach daemon at %s (is `adrop daemon` running?): %w", path, err)
	}
	return conn, nil
}

// roundtrip sends req and invokes onResp for each streamed response until Done.
// It returns an error if any response carries Err.
func roundtrip(req ipc.Request, onResp func(ipc.Response)) error {
	conn, err := dialDaemon()
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}
	dec := json.NewDecoder(bufio.NewReader(conn))
	for {
		var resp ipc.Response
		if err := dec.Decode(&resp); err != nil {
			return fmt.Errorf("daemon closed connection: %w", err)
		}
		if resp.Err != "" {
			return fmt.Errorf("%s", resp.Err)
		}
		onResp(resp)
		if resp.Done {
			return nil
		}
	}
}

func runStatus(_ []string) error {
	return roundtrip(ipc.Request{Cmd: ipc.CmdStatus}, func(r ipc.Response) {
		if r.Status != nil {
			s := r.Status
			fmt.Printf("device:      %s\n", s.Name)
			fmt.Printf("fingerprint: %s\n", s.Fingerprint)
			fmt.Printf("listen addr: %s\n", s.ListenAddr)
			fmt.Printf("devices:     %d trusted\n", s.NumDevices)
		}
		if r.Line != "" {
			fmt.Println(r.Line)
		}
	})
}

func runPair(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: adrop pair show | adrop pair add <uri>")
	}
	switch args[0] {
	case "show":
		return roundtrip(ipc.Request{Cmd: ipc.CmdPairShow}, func(r ipc.Response) {
			if r.QR != "" {
				fmt.Print(r.QR)
			}
			if r.Line != "" {
				fmt.Println(r.Line)
			}
		})
	case "add":
		if len(args) < 2 {
			return fmt.Errorf("usage: adrop pair add <adrop://pair?d=...>")
		}
		return roundtrip(ipc.Request{Cmd: ipc.CmdPairAdd, PairURI: args[1]}, printLines)
	default:
		return fmt.Errorf("unknown pair subcommand %q", args[0])
	}
}

func runDevices(_ []string) error {
	return roundtrip(ipc.Request{Cmd: ipc.CmdDevices}, func(r ipc.Response) {
		if len(r.Devices) == 0 {
			fmt.Println("no trusted devices")
			return
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tFINGERPRINT\tADDR\tPAIRED")
		for _, d := range r.Devices {
			fp := d.Fingerprint
			if len(fp) > 16 {
				fp = fp[:16]
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", d.Name, fp, d.Addr, d.PairedAt)
		}
		tw.Flush()
	})
}

func runRevoke(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: adrop revoke <name|fingerprint-prefix>")
	}
	return roundtrip(ipc.Request{Cmd: ipc.CmdRevoke, Target: args[0]}, printLines)
}

func runSend(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: adrop send <peer> <file> [file...]")
	}
	peer := args[0]
	paths := make([]string, 0, len(args)-1)
	for _, p := range args[1:] {
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		if _, err := os.Stat(abs); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		paths = append(paths, abs)
	}
	return roundtrip(ipc.Request{Cmd: ipc.CmdSendFiles, Target: peer, Files: paths}, printLines)
}

func runClip(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: adrop clip <peer> [text]")
	}
	req := ipc.Request{Cmd: ipc.CmdSendClip, Target: args[0]}
	if len(args) > 1 {
		req.Text = args[1]
	}
	return roundtrip(req, printLines)
}

// printLines prints any Line fields in streamed responses.
func printLines(r ipc.Response) {
	if r.Line != "" {
		fmt.Println(r.Line)
	}
}
