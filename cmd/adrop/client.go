package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
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
			if s.LastPeer != "" {
				fmt.Printf("last peer:   %s\n", s.LastPeer)
			}
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
	if len(args) < 1 {
		return fmt.Errorf("usage: adrop send [<peer>] <file> [file...]")
	}
	// Heuristic: if the first arg looks like a file path (exists on disk), treat
	// it as a file and leave peer empty so the daemon uses the last-used device.
	var peer string
	var filePaths []string
	if _, err := os.Stat(args[0]); err == nil {
		// First arg is a file — no peer specified.
		filePaths = args
	} else {
		// First arg is the peer name.
		if len(args) < 2 {
			return fmt.Errorf("usage: adrop send [<peer>] <file> [file...]")
		}
		peer = args[0]
		filePaths = args[1:]
	}
	paths := make([]string, 0, len(filePaths))
	for _, p := range filePaths {
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
	// Usage: adrop clip [--mime <type>] [<peer>] [text]
	// If peer is omitted the daemon uses the last-used device.
	req := ipc.Request{Cmd: ipc.CmdSendClip}
	mime := "text/plain"

	// Strip --mime flag from the front.
	remaining := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--mime" && i+1 < len(args) {
			mime = args[i+1]
			i++
		} else if strings.HasPrefix(args[i], "--mime=") {
			mime = strings.TrimPrefix(args[i], "--mime=")
		} else {
			remaining = append(remaining, args[i])
		}
	}
	if mime != "text/plain" {
		req.MIME = mime
	}

	switch len(remaining) {
	case 0:
		// no peer, no text — daemon resolves peer; reads wl-paste
	case 1:
		req.Target = remaining[0]
	default:
		req.Target = remaining[0]
		req.Text = remaining[1]
	}
	return roundtrip(req, printLines)
}

// printLines prints any Line fields in streamed responses, rendering transient
// progress updates (lines ending in "%") in place on a single terminal line.
func printLines(r ipc.Response) {
	defaultProgressPrinter.print(r)
}

var defaultProgressPrinter = &progressPrinter{out: os.Stdout}

// progressPrinter renders streamed Line responses, overwriting percentage
// progress lines on a single terminal line instead of scrolling the terminal.
type progressPrinter struct {
	out     *os.File
	inline  bool // a progress line is currently held on the terminal line
	lastLen int  // visible length of the held progress line, for clearing
}

func (p *progressPrinter) print(r ipc.Response) {
	if r.Line == "" {
		return
	}
	if isProgressLine(r.Line) && isTerminal(p.out) {
		// Overwrite the current line: carriage return, content, then pad with
		// spaces to erase any leftover from a previously longer line.
		pad := ""
		if n := p.lastLen - len(r.Line); n > 0 {
			pad = strings.Repeat(" ", n)
		}
		fmt.Fprintf(p.out, "\r%s%s", r.Line, pad)
		p.inline = true
		p.lastLen = len(r.Line)
		return
	}
	// A non-progress line finalizes any held progress line first.
	if p.inline {
		fmt.Fprintln(p.out)
		p.inline = false
		p.lastLen = 0
	}
	fmt.Fprintln(p.out, r.Line)
}

// isProgressLine reports whether line is a transient per-file progress update
// (e.g. "photo.jpg: 42%") that should overwrite in place.
func isProgressLine(line string) bool {
	return strings.HasSuffix(line, "%")
}

// isTerminal reports whether f is an interactive terminal (so we can use
// carriage-return overwriting); otherwise progress lines scroll normally.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
