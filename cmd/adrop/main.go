// Command adrop is both the resident daemon and the thin CLI client.
//
//	adrop daemon                 run the resident daemon (systemd user service)
//	adrop status                 show daemon status
//	adrop pair show              display this device's pairing QR
//	adrop pair add <uri>         trust a scanned pairing URI
//	adrop devices                list trusted devices
//	adrop revoke <name|fp>       remove a trusted device
//	adrop send <peer> <files...> send files to a peer
//	adrop clip <peer> [text]     push clipboard (or text) to a peer
//
// The CLI talks to the daemon over a Unix-domain socket; only `daemon` runs
// the long-lived process.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
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
  adrop daemon                  run the resident daemon
  adrop status                  show daemon status & this device's identity
  adrop pair show               display this device's pairing QR (waits to pair)
  adrop pair add <uri>          trust a scanned "adrop://pair?d=..." URI
  adrop devices                 list trusted devices
  adrop revoke <name|fp-prefix> revoke (untrust) a device
  adrop send <peer> <file...>   send one or more files to a peer (one session)
  adrop clip <peer> [text]      push clipboard (or given text) to a peer

Environment:
  ADROP_CONFIG_DIR   override config dir (keys, devices)
  ADROP_SOCKET       override IPC socket path
  ADROP_PORT         override peer TLS port (daemon, default 53127)
  ADROP_NAME         this device's advertised name (daemon, default hostname)
  ADROP_ADVERTISE_IP LAN IP to put in the pairing QR (daemon, auto-detected)
  ADROP_DOWNLOAD_DIR where received files land (daemon, default ~/Downloads)
`)
}
