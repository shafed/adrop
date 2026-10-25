// Command adrop is the desktop app, the resident daemon and the thin CLI client.
//
//	adrop                             open the window (adrop-gui)
//	adrop daemon                      run the resident daemon (systemd user service)
//	adrop status                      show daemon status
//	adrop pair show                   display this device's pairing QR
//	adrop pair add <uri>              trust a scanned pairing URI
//	adrop pair with <device>          pair with a nearby device by code
//	adrop pair accept                 answer a pairing started on another device
//	adrop nearby                      list adrop devices seen on the LAN
//	adrop ping <peer>                 signed ping to a paired device over Reticulum
//	adrop queue [rm <id>]             sends waiting for a channel to their device
//	adrop devices                     list trusted devices
//	adrop revoke <name|fp>            remove a trusted device
//	adrop send [<peer>] <files...>    send files (peer optional, uses last-used)
//	adrop clip [<peer>] [text]        push clipboard (peer optional, uses last-used)
//
// The window (adrop-gui) and the CLI both talk to the daemon over a
// Unix-domain socket; only `daemon` runs the long-lived process.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func main() {
	// No arguments: open the window (adrop-gui, the Flutter app in app/), or
	// print usage where there is none to open.
	if len(os.Args) < 2 {
		if err := openWindow(); err != nil {
			fmt.Fprintf(os.Stderr, "adrop: %v\n\n", err)
			usage()
			os.Exit(2)
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
	case "nearby":
		err = runNearby(args)
	case "ping":
		err = runPing(args)
	case "queue":
		err = runQueue(args)
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
	fmt.Fprint(os.Stderr, `adrop — files and clipboard between your own devices over pinned TLS

Usage:
`)
	fmt.Fprint(os.Stderr, "  adrop                         open the window (adrop-gui)\n")
	fmt.Fprint(os.Stderr, `  adrop daemon                  run the resident daemon
  adrop status                  show daemon status & this device's identity
  adrop pair show               display this device's pairing QR (waits to pair)
  adrop pair add <uri>          trust a scanned "adrop://pair?d=..." URI
  adrop pair with <device>      pair with a nearby device by comparing a code
  adrop pair accept             answer a pairing started on another device
  adrop nearby                  list adrop devices seen on the LAN
  adrop ping <peer>             ping a paired device over Reticulum hubs (outside the LAN)
  adrop queue [rm <id>]         list (or drop) sends waiting for a channel to their device
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
  ADROP_TAP_PAIR     1 = accept pairing by code from nearby devices (daemon, default off)
  ADROP_RNS          1 = connect to public Reticulum hubs for use outside the LAN (daemon, default off)
  ADROP_RNS_HUBS     comma-separated host:port hubs instead of the built-in list and catalog
`)
}

// openWindow replaces this process with adrop-gui, found next to this binary
// or on PATH.
func openWindow() error {
	if os.Getenv("WAYLAND_DISPLAY") == "" && os.Getenv("DISPLAY") == "" {
		return errors.New("no display (DISPLAY/WAYLAND_DISPLAY unset), no window to open")
	}
	gui := ""
	if self, err := os.Executable(); err == nil {
		if p := filepath.Join(filepath.Dir(self), "adrop-gui"); fileExists(p) {
			gui = p
		}
	}
	if gui == "" {
		p, err := exec.LookPath("adrop-gui")
		if err != nil {
			return errors.New("the window is not installed (make app-install)")
		}
		gui = p
	}
	return syscall.Exec(gui, []string{gui}, os.Environ())
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
