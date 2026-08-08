//go:build gui

package main

import (
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/shafed/adrop/internal/ipc"
)

// TestManageDialogEscape covers Escape on the device dialog: it closes it, and
// it puts the canvas's previous key handler back afterwards.
//
// Runs under the `gui` tag only (make test-gui), on Fyne's test driver — no
// display required.
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
	onKey(esc)
	if open() {
		t.Error("Escape did not close the manage dialog")
	}
	if w.Canvas().OnTypedKey() != nil {
		t.Error("canvas key handler was not restored on close")
	}
}

// TestEscEntryForwardsEscape checks the rename field hands Escape back to the
// dialog instead of swallowing it, while other keys still reach the Entry.
func TestEscEntryForwardsEscape(t *testing.T) {
	_ = test.NewTempApp(t)

	fired := 0
	e := newEscEntry(func() { fired++ })
	e.SetText("phone")

	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if fired != 1 {
		t.Fatalf("Escape fired %d times, want 1", fired)
	}
	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyBackspace})
	if fired != 1 {
		t.Errorf("a non-Escape key was treated as Escape (fired=%d)", fired)
	}
}

// TestEscapeStacksInnermostFirst checks the dialog stacking rule: with a
// revoke confirmation on top of the device list, Escape dismisses the
// confirmation and leaves the list open, then a second Escape closes the list.
func TestEscapeStacksInnermostFirst(t *testing.T) {
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
	g.confirmRevoke(ipc.DeviceInfo{Name: "phone", Fingerprint: "abcdef0123456789"}, widget.NewLabel(""))
	inner := w.Canvas().OnTypedKey()
	if inner == nil {
		t.Fatal("stacked dialog installed no key handler")
	}

	esc := &fyne.KeyEvent{Name: fyne.KeyEscape}
	inner(esc)
	if !open() {
		t.Fatal("Escape closed the device list from under the confirmation")
	}
	if w.Canvas().OnTypedKey() == nil {
		t.Fatal("the device list's key handler was not restored")
	}

	// The restored handler is the device list's own, so Escape closes it.
	w.Canvas().OnTypedKey()(esc)
	if open() {
		t.Error("second Escape did not close the device list")
	}
}
