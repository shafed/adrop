// Command adrop is the desktop app, the resident daemon and the thin CLI client.
//
//	adrop                             open the drag-drop window (default)
//	adrop daemon                      run the resident daemon (systemd user service)
//	adrop status                      show daemon status
//	adrop pair show                   display this device's pairing QR
//	adrop pair add <uri>              trust a scanned pairing URI
//	adrop devices                     list trusted devices
//	adrop revoke <name|fp>            remove a trusted device
//	adrop send [<peer>] <files...>    send files (peer optional, uses last-used)
//	adrop clip [<peer>] [text]        push clipboard (peer optional, uses last-used)
//
// The GUI and the CLI both talk to the daemon over a Unix-domain socket; only
// `daemon` runs the long-lived process. Headless builds (`make build-headless`)
// have no window, so a bare `adrop` prints usage there.
package main

import (
	"fmt"
	"os"
)

func main() {
	// No arguments: open the window. A headless build has none, so fall back to
	// usage rather than failing with runGUI's "rebuild" error.
	if len(os.Args) < 2 {
		if !guiAvailable {
			usage()
			os.Exit(2)
		}
		if err := runGUI(); err != nil {
			fmt.Fprintf(os.Stderr, "adrop: %v\n", err)
			os.Exit(1)
		}
		return
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "daemon":
		err = runDaemon(args)
	case "status":
		err = runStatus(args)
	case "pair":
		err = runPair(args)
	case "devices":
		err = runDevices(args)
	case "revoke":
		err = runRevoke(args)
	case "send":
		err = runSend(args)
	case "clip", "clipboard":
		err = runClip(args)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "adrop: unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "adrop: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `adrop — AirDrop-like file & clipboard transfer over pinned-TLS LAN

Usage:
`)
	if guiAvailable {
		fmt.Fprint(os.Stderr, "  adrop                         open the drag-drop window (default)\n")
	} else {
		fmt.Fprint(os.Stderr, "  (headless build — no window; rebuild with `make build` for the GUI)\n")
	}
	fmt.Fprint(os.Stderr, `  adrop daemon                  run the resident daemon
  adrop status                  show daemon status & this device's identity
  adrop pair show               display this device's pairing QR (waits to pair)
  adrop pair add <uri>          trust a scanned "adrop://pair?d=..." URI
  adrop devices                 list trusted devices
  adrop revoke <name|fp-prefix> revoke (untrust) a device
  adrop send [<peer>] <file...>  send files to a peer (peer optional after first send)
  adrop clip [<peer>] [text]    push clipboard to a peer (peer optional after first send)

Environment:
  ADROP_CONFIG_DIR   override config dir (keys, devices)
  ADROP_SOCKET       override IPC socket path
  ADROP_PORT         override peer TLS port (daemon, default 53127)
  ADROP_NAME         this device's advertised name (daemon, default hostname)
  ADROP_ADVERTISE_IP LAN IP to put in the pairing QR (daemon, auto-detected)
  ADROP_DOWNLOAD_DIR where received files land (daemon, default ~/Downloads)
`)
}
