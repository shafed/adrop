package main

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// runAutostart starts the daemon at login on Windows (SPEC §3): a value
// under HKCU\...\Run pointing at adrop-daemon.exe (built without a console
// window), or at this binary when it is not there.
func runAutostart(args []string) error {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		return fmt.Errorf("usage: adrop autostart on|off")
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if args[0] == "off" {
		if err := k.DeleteValue("adrop"); err != nil && err != registry.ErrNotExist {
			return err
		}
		fmt.Println("adrop no longer starts at login")
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	exe := self
	if d := filepath.Join(filepath.Dir(self), "adrop-daemon.exe"); fileExists(d) {
		exe = d
	}
	if err := k.SetStringValue("adrop", fmt.Sprintf("%q daemon", exe)); err != nil {
		return err
	}
	fmt.Printf("adrop starts at login: %s daemon\n", exe)
	return nil
}
