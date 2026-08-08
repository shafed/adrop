//go:build gui

package main

import (
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestManageDialogEscape covers the dialog's Escape handling: it closes the
// device list, it is suppressed while a rename/revoke/pair dialog is stacked on
// top, and it puts the canvas's previous key handler back afterwards.
//
// Runs under the `gui` tag only (CGO_ENABLED=1 go test -tags gui ./cmd/adrop),
// on Fyne's test driver — no display required.
func TestManageDialogEscape(t *testing.T) {
	// Point IPC at a socket that isn't there: the dialog's refresh should fail
	// fast rather than talk to a real daemon.
	t.Setenv("ADROP_SOCKET", filepath.Join(t.TempDir(), "absent.sock"))

	a := test.NewTempApp(t)
	w := test.NewTempWindow(t, widget.NewLabel(""))
	g := newGUI(a, w)
	w.SetContent(g.content())

	open := func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.manage != nil
	}

	g.openManageDialog()
	if !open() {
		t.Fatal("manage dialog did not register as open")
	}
	onKey := w.Canvas().OnTypedKey()
	if onKey == nil {
		t.Fatal("no key handler installed while the dialog is open")
	}

	esc := &fyne.KeyEvent{Name: fyne.KeyEscape}

	g.setNested(true)
	onKey(esc)
	if !open() {
		t.Error("Escape closed the device list from under a stacked dialog")
	}

	g.setNested(false)
	onKey(esc)
	if open() {
		t.Error("Escape did not close the manage dialog")
	}
	if w.Canvas().OnTypedKey() != nil {
		t.Error("canvas key handler was not restored on close")
	}
}
