//go:build !windows

package main

import "errors"

func runAutostart([]string) error {
	return errors.New("on Linux the daemon starts with systemd: systemctl --user enable --now adrop")
}
