//go:build !gui

package main

import "fmt"

// runGUI is the stub used when the binary is built without the `gui` tag. The
// real Fyne implementation lives in gui.go behind `//go:build gui`.
func runGUI(_ []string) error {
	return fmt.Errorf("this build has no GUI; rebuild with `make build-gui`")
}
