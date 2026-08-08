//go:build !gui

package main

import "fmt"

// guiAvailable lets main() know a bare `adrop` cannot open a window in this
// build, so it prints usage instead of failing with the error below.
const guiAvailable = false

// runGUI is the stub used when the binary is built without the `gui` tag. The
// real Fyne implementation lives in gui.go behind `//go:build gui`.
func runGUI() error {
	return fmt.Errorf("this is a headless build; rebuild with `make build`")
}
