//go:build gui

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/shafed/adrop/internal/ipc"
	"github.com/shafed/adrop/internal/pairing"
)

// guiAvailable tells main() a bare `adrop` can open a window in this build.
const guiAvailable = true

// displayAvailable reports whether there is a graphical session to open a
// window on. Fyne/GLFW abort the process when they can't reach a display
// instead of returning an error, so `adrop` over SSH has to be caught here —
// runGUI would never get to return.
func displayAvailable() bool {
	return os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("DISPLAY") != ""
}

// runGUI launches the Fyne drop window. It is a thin IPC client of the daemon:
// sends dial per-request (reusing roundtrip), and one long-lived CmdSubscribe
// connection feeds the inbound row.
func runGUI() error {
	a := app.NewWithID("dev.adrop.gui")
	a.Settings().SetTheme(adropTheme{}) // the phone's Material 3 look
	w := a.NewWindow("adrop")
	w.Resize(fyne.NewSize(columnWidth, 720)) // a phone-shaped column
	w.SetPadded(false)                       // the TopAppBar band runs edge to edge; the body pads itself

	g := newGUI(a, w)
	w.SetContent(g.content())

	// Native file drag-drop: the URIs are already discrete, so hand their paths
	// straight to the send path — no newline round-trip through whitespace
	// splitting, which would mangle paths containing spaces.
	w.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		paths := make([]string, 0, len(uris))
		for _, u := range uris {
			paths = append(paths, u.Path())
		}
		g.sendPaths(paths)
	})

	// Global Ctrl+V: paste a file:// path (or bare path) from the clipboard and
	// send immediately — no need to focus a text field first.
	w.Canvas().AddShortcut(&fyne.ShortcutPaste{}, func(fyne.Shortcut) {
		g.sendFromInput(a.Clipboard().Content())
	})

	// Refresh the device dropdown when the window regains focus.
	a.Lifecycle().SetOnEnteredForeground(func() { g.refreshPeers() })

	// Initial population + subscribe feed.
	g.refreshState()
	go g.subscribeLoop()

	w.ShowAndRun()
	g.stop()
	return nil
}

// gui holds the widgets and mutable UI state for the drop window.
type gui struct {
	app fyne.App
	win fyne.Window

	peerList  *fyne.Container // one selectable card per trusted device
	noPeers   fyne.CanvasObject
	manageBtn *widget.Button
	chooseBtn *m3Button
	clipBtn   *m3Button
	pairBtn   *m3Button

	// The receive card: shown only while receiving, after a failed receive, or
	// with the daemon down.
	recvBlock *fyne.Container // the card plus its gap, hidden at rest
	recvCard  *m3Card
	recvTitle *widget.RichText
	recvSub   *widget.RichText
	startBtn  *m3Button
	inBar     *m3Progress

	outLabel *widget.RichText
	outBar   *m3Progress
	fileList *widget.RichText // names of files queued/sending in the current batch
	retryBtn *m3Button

	mu         sync.Mutex
	peers      []string         // device names, in list order
	peer       string           // the selected send target
	devices    []ipc.DeviceInfo // full trusted-device list, for the manage dialog
	manage     func(error)      // redraws the open manage list; nil when closed
	staged     []string         // last batch's files, kept for Retry on failure
	pairing    bool             // true while a pairing dialog owns a pair-show request
	sending    bool             // a send is in flight; serializes the send entry points
	daemonDown bool             // the receive card is showing the daemon-not-running state

	stopCh   chan struct{}
	stopOnce sync.Once
}

func newGUI(a fyne.App, w fyne.Window) *gui {
	return &gui{app: a, win: w, stopCh: make(chan struct{})}
}

// content lays the window out as the phone's Send screen: a TopAppBar, the
// receive card from the home screen, then Target Device, Files and Clipboard
// sections, in one scrolling column.
func (g *gui) content() fyne.CanvasObject {
	g.manageBtn = widget.NewButtonWithIcon("", theme.ComputerIcon(), g.openManageDialog)
	g.manageBtn.Importance = widget.LowImportance // M3 IconButton: no container

	// Receive card. The phone's card holds an "Open to receive" switch; the PC
	// daemon always listens, so this one only reports.
	g.recvTitle = m3Text("", m3TitleMedium, m3OnSurface, true)
	g.recvSub = m3Text("", m3BodySmall, m3OnSurfaceVariant, false)
	g.inBar = newM3Progress()
	g.inBar.inset = theme.InnerPadding() // text in the card is inset by RichText's padding
	g.inBar.Hide()
	g.startBtn = newM3Button(m3Tonal, "Start Daemon", theme.MediaPlayIcon(), g.startDaemon)
	g.startBtn.Hide()
	g.recvCard = newM3Card(m3SurfaceVariant, "", m3Inset(8, container.NewVBox(
		container.New(tightV{}, g.recvTitle, g.recvSub),
		g.inBar,
		g.startBtn,
	)))
	g.recvBlock = container.NewVBox(g.recvCard, vgap(8))
	g.setRecvIdle()

	// Target Device.
	g.peerList = container.NewVBox()
	g.pairBtn = newM3Button(m3Outlined, "Pair Device", theme.ContentAddIcon(), g.openPairDialog)
	g.noPeers = container.NewVBox(
		m3Text("No paired devices yet.", m3BodySmall, m3OnSurfaceVariant, false),
		g.pairBtn,
	)
	g.noPeers.Hide()

	// Files. Picking one file sends it straight away; several go by drag-drop.
	g.chooseBtn = newM3Button(m3Outlined, "Pick File", theme.DocumentIcon(), g.chooseFiles)

	// Clipboard.
	// Tonal, like the phone's main "Send File or Clipboard" button.
	g.clipBtn = newM3Button(m3Tonal, "Send Clipboard", theme.MailSendIcon(), g.sendClipboard)

	// Outbound status, below everything like the phone's snackbar.
	g.fileList = m3Text("", m3BodySmall, m3OnSurfaceVariant, false)
	g.fileList.Hide()
	g.outBar = newM3Progress()
	g.outBar.Hide()
	// A failed send explains itself in a sentence or two (see dialFailure), so
	// the label wraps rather than stretching the window.
	g.outLabel = m3Text("", m3BodySmall, m3OnSurfaceVariant, false)
	g.retryBtn = newM3Button(m3Outlined, "Retry", theme.ViewRefreshIcon(), g.retry)
	g.retryBtn.Hide()

	body := container.NewVBox(
		g.recvBlock,
		section("Target Device"),
		g.peerList,
		g.noPeers,
		vgap(8),
		section("Files"),
		g.chooseBtn,
		m3Text("Sends right away. Drop files onto this window to send several at once.",
			m3BodySmall, m3OnSurfaceVariant, false),
		vgap(8),
		section("Clipboard"),
		g.clipBtn,
		vgap(8),
		g.fileList,
		g.outBar,
		g.outLabel,
		g.retryBtn,
	)
	return container.NewBorder(topBar(g.manageBtn), nil, nil, nil,
		container.NewVScroll(column(m3Inset(16, body))))
}

// topBar is the phone's TopAppBar: the app name in titleLarge on a band of
// surface, a shade lighter than the screen below it, with the paired-devices
// action on the right.
func topBar(action fyne.CanvasObject) fyne.CanvasObject {
	title := m3Text("adrop", m3TitleLarge, m3OnSurface, false)
	bar := m3Inset(8, container.NewBorder(nil, nil, nil, container.NewCenter(action), title))
	return newM3Card(m3Surface, "", column(bar)).square()
}

// columnWidth is how wide the content gets: about a phone screen. A tiled or
// maximized window centers the column instead of stretching every button
// across the monitor.
const columnWidth = 600

// column centers content at no more than columnWidth.
func column(content fyne.CanvasObject) fyne.CanvasObject {
	return container.New(columnLayout{}, content)
}

type columnLayout struct{}

func (columnLayout) MinSize(objs []fyne.CanvasObject) fyne.Size { return objs[0].MinSize() }

func (columnLayout) Layout(objs []fyne.CanvasObject, s fyne.Size) {
	w := min(s.Width, columnWidth)
	objs[0].Move(fyne.NewPos((s.Width-w)/2, 0))
	objs[0].Resize(fyne.NewSize(w, s.Height))
}

// section is a titleMedium heading over a block, as on the phone's Send screen.
func section(title string) fyne.CanvasObject {
	return m3Text(title, m3TitleMedium, m3OnSurface, true)
}

// tightV stacks text rows without VBox's padding, so a title and its subtitle
// sit together as one block.
type tightV struct{}

func (tightV) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var s fyne.Size
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		m := o.MinSize()
		s.Width = max(s.Width, m.Width)
		s.Height += m.Height - theme.InnerPadding()
	}
	return s.AddWidthHeight(0, theme.InnerPadding())
}

func (tightV) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		h := o.MinSize().Height
		o.Move(fyne.NewPos(0, y))
		o.Resize(fyne.NewSize(size.Width, h))
		y += h - theme.InnerPadding()
	}
}

// deviceOption is one row of the phone's Target Device list: a radio mark,
// the name, the address and the fingerprint tail, on a card that turns
// primaryContainer with a primary outline when selected.
func (g *gui) deviceOption(d ipc.DeviceInfo, selected bool) fyne.CanvasObject {
	fill, border := fyne.ThemeColorName(m3Surface), fyne.ThemeColorName(m3OutlineVariant)
	radio := theme.NewColoredResource(theme.RadioButtonIcon(), m3OnSurfaceVariant)
	if selected {
		fill, border = m3PrimaryContainer, theme.ColorNamePrimary
		radio = theme.NewColoredResource(theme.RadioButtonCheckedIcon(), theme.ColorNamePrimary)
	}
	icon := widget.NewIcon(radio)

	addr := d.Addr
	if addr == "" {
		addr = "No known address"
	}
	text := container.New(tightV{},
		m3Text(d.Name, m3TitleSmall, m3OnSurface, true),
		m3Text(addr, m3BodySmall, m3OnSurfaceVariant, false),
		m3Text("Fingerprint …"+fingerprintSuffix(d.Fingerprint), m3LabelSmall, m3OnSurfaceVariant, false),
	)
	row := container.NewBorder(nil, nil, container.NewCenter(icon), nil, text)
	card := newM3Card(fill, border, m3Inset(4, row))
	name := d.Name
	card.OnTapped = func() { g.selectPeer(name) }
	return card
}

// fingerprintSuffix is the last 12 hex digits in groups of four, as the phone
// shows them.
func fingerprintSuffix(fp string) string {
	if len(fp) > 12 {
		fp = fp[len(fp)-12:]
	}
	var parts []string
	for len(fp) > 4 {
		parts = append(parts, fp[:4])
		fp = fp[4:]
	}
	return strings.Join(append(parts, fp), " ")
}

// selectPeer makes name the send target and redraws the list.
func (g *gui) selectPeer(name string) {
	g.mu.Lock()
	g.peer = name
	g.mu.Unlock()
	g.renderPeers()
}

// renderPeers rebuilds the Target Device list from the cached devices.
func (g *gui) renderPeers() {
	g.mu.Lock()
	devs, sel := g.devices, g.peer
	g.mu.Unlock()
	g.peerList.RemoveAll()
	for _, d := range devs {
		g.peerList.Add(g.deviceOption(d, d.Name == sel))
	}
	g.peerList.Refresh()
}

// setRecvIdle takes the receive card away: at rest the daemon is simply
// listening, which needs no card, and a finished receive already raised a
// desktop notification.
func (g *gui) setRecvIdle() {
	g.recvBlock.Hide()
}

// showRecv puts the receive card up with a title, a detail line and a color.
func (g *gui) showRecv(title, sub string, fill fyne.ThemeColorName) {
	setM3Text(g.recvTitle, title)
	setM3Text(g.recvSub, sub)
	g.recvCard.SetColors(fill, "")
	g.recvBlock.Show()
}

// ----- state / peer list -----

// refreshState pulls both the device list and last-peer default in one pass.
func (g *gui) refreshState() {
	g.refreshPeers()
}

// refreshPeers re-queries CmdDevices + CmdStatus and repopulates the dropdown,
// defaulting the selection to the last-used peer.
func (g *gui) refreshPeers() {
	// The IPC round-trips block, so run them off the UI thread. This is also
	// reached from lifecycle callbacks that fire on the main thread, where a
	// hung daemon would otherwise freeze the window.
	go func() {
		var names []string
		var devs []ipc.DeviceInfo
		derr := roundtrip(ipc.Request{Cmd: ipc.CmdDevices}, func(r ipc.Response) {
			for _, d := range r.Devices {
				names = append(names, d.Name)
				devs = append(devs, d)
			}
		})
		if derr != nil {
			g.showDaemonDown(derr)
			g.notifyManage(derr) // an open manage dialog would otherwise show a stale list
			return
		}
		g.clearDaemonDown()

		last := ""
		_ = roundtrip(ipc.Request{Cmd: ipc.CmdStatus}, func(r ipc.Response) {
			if r.Status != nil {
				last = r.Status.LastPeer
			}
		})

		fyne.Do(func() {
			g.mu.Lock()
			g.peers = names
			g.devices = devs
			rebuild := g.manage
			g.mu.Unlock()

			if rebuild != nil {
				rebuild(nil) // keep an open manage dialog in step with the daemon
			}
			if len(names) == 0 {
				g.mu.Lock()
				g.peer = ""
				g.mu.Unlock()
				g.setSendEnabled(false)
				g.noPeers.Show()
				g.pairBtn.Show()
				if g.pairingActive() {
					g.pairBtn.Disable()
				} else {
					g.pairBtn.Enable()
				}
			} else {
				g.setSendEnabled(true)
				g.noPeers.Hide()
				// A refresh must not move the send target under the user: keep
				// their pick if it still exists, and only then fall back to the
				// last-used peer, then the first device.
				g.mu.Lock()
				sel := g.peer
				if sel == "" || !contains(names, sel) {
					sel = last
				}
				if sel == "" || !contains(names, sel) {
					sel = names[0]
				}
				g.peer = sel
				g.mu.Unlock()
			}
			g.renderPeers()
		})
	}()
}

// ----- dialogs: Escape to dismiss -----

// dismissible is the slice of Fyne's dialog types that escapeCloses needs. All
// of them (custom, form, confirm, error, file) satisfy it.
type dismissible interface {
	Hide()
	SetOnClosed(func())
}

// escapeCloses makes Escape dismiss d. Fyne has no dismiss key of its own, so
// the window canvas carries a key handler for as long as d is open, chaining to
// whatever handler was there and putting it back on close. Handlers therefore
// stack the way the dialogs do: Escape closes the innermost one, which restores
// the handler of the dialog underneath.
//
// The canvas only sees keys no focused widget consumed, so a dialog holding a
// widget that swallows Escape (an Entry) needs that widget's cooperation — see
// escEntry.
func (g *gui) escapeCloses(d dismissible) {
	prev := g.win.Canvas().OnTypedKey()
	g.win.Canvas().SetOnTypedKey(func(k *fyne.KeyEvent) {
		if k.Name == fyne.KeyEscape {
			d.Hide() // runs the dialog's OnClosed, restoring prev below
			return
		}
		if prev != nil {
			prev(k)
		}
	})
	d.SetOnClosed(func() { g.win.Canvas().SetOnTypedKey(prev) })
}

// escEntry is an Entry that hands Escape back instead of swallowing it, so a
// dialog stays Escape-dismissable while the user is typing in its field.
type escEntry struct {
	widget.Entry
	onEscape func()
}

func newEscEntry(onEscape func()) *escEntry {
	e := &escEntry{onEscape: onEscape}
	e.ExtendBaseWidget(e)
	return e
}

func (e *escEntry) TypedKey(k *fyne.KeyEvent) {
	if k.Name == fyne.KeyEscape {
		e.onEscape()
		return
	}
	e.Entry.TypedKey(k)
}

// ----- device management (§5.4) -----

// openManageDialog shows the trusted-device list with add / rename / revoke.
// The daemon stays the single source of truth: every action is an IPC
// round-trip, and the list is rebuilt from a fresh CmdDevices afterwards, so a
// rejected action (e.g. a daemon that doesn't know CmdRename) leaves the list
// untouched and only surfaces the error.
func (g *gui) openManageDialog() {
	list := container.NewVBox()
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord

	addBtn := newM3Button(m3Filled, "Pair Device", theme.ContentAddIcon(), g.openPairDialog)

	// rebuild redraws the list from the cached device set, or — when the refresh
	// that fed it failed — says so and leaves the previous list alone rather than
	// presenting a stale one as current.
	rebuild := func(err error) {
		if err != nil {
			status.SetText("⚠ " + err.Error())
			return
		}
		g.mu.Lock()
		devs := g.devices
		g.mu.Unlock()

		list.RemoveAll()
		if len(devs) == 0 {
			list.Add(widget.NewLabel("No paired devices yet."))
		}
		for _, d := range devs {
			name := widget.NewLabel(deviceLabel(d))
			renameBtn := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() {
				g.promptRename(d, status)
			})
			revokeBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				g.confirmRevoke(d, status)
			})
			actions := container.NewHBox(renameBtn, revokeBtn)
			list.Add(container.NewBorder(nil, nil, nil, actions, name))
			list.Add(widget.NewSeparator())
		}
		list.Refresh()
	}
	rebuild(nil)

	g.mu.Lock()
	g.manage = rebuild
	g.mu.Unlock()

	content := container.NewBorder(
		nil,
		container.NewVBox(addBtn, status),
		nil, nil,
		container.NewVScroll(list),
	)
	d := dialog.NewCustom("Devices", "Close", content, g.win)
	d.Resize(fyne.NewSize(400, 440))
	g.escapeCloses(d)
	d.SetOnClosed(func() {
		g.mu.Lock()
		g.manage = nil
		g.mu.Unlock()
	})
	d.Show()

	g.refreshPeers() // pull a fresh list behind the already-visible dialog
}

// promptRename asks for a new display name and issues CmdRename. The device is
// addressed by fingerprint, so the rename can't hit the wrong peer.
func (g *gui) promptRename(dev ipc.DeviceInfo, status *widget.Label) {
	var d *dialog.FormDialog
	entry := newEscEntry(func() { d.Hide() }) // Escape works while typing too
	entry.SetText(dev.Name)
	form := []*widget.FormItem{widget.NewFormItem("Name", entry)}
	d = dialog.NewForm("Rename device", "Rename", "Cancel", form, func(ok bool) {
		if !ok {
			return
		}
		newName := strings.TrimSpace(entry.Text)
		if newName == "" || newName == dev.Name {
			return
		}
		go g.runManageAction(renameRequest(dev.Fingerprint, newName),
			fmt.Sprintf("Renamed %s to %s.", dev.Name, newName), status)
	}, g.win)
	g.escapeCloses(d)
	d.Show()
}

// confirmRevoke asks before untrusting a device, since revoke is immediate and
// only undone by pairing again.
func (g *gui) confirmRevoke(dev ipc.DeviceInfo, status *widget.Label) {
	msg := fmt.Sprintf("Untrust %s? You won't be able to send to it until you pair again.", dev.Name)
	d := dialog.NewConfirm("Revoke device", msg, func(ok bool) {
		if !ok {
			return
		}
		go g.runManageAction(revokeRequest(dev.Fingerprint),
			fmt.Sprintf("Revoked %s.", dev.Name), status)
	}, g.win)
	g.escapeCloses(d)
	d.Show()
}

// notifyManage hands a refresh outcome to an open manage dialog: nil redraws
// the list, an error is shown in its status line. It is a no-op when no dialog
// is open.
func (g *gui) notifyManage(err error) {
	g.mu.Lock()
	rebuild := g.manage
	g.mu.Unlock()
	if rebuild != nil {
		fyne.Do(func() { rebuild(err) })
	}
}

// runManageAction performs one management round-trip off the UI thread and
// reports the outcome in the dialog's status line. It always refreshes
// afterwards: on success to pick up the change, on failure so that a dead
// daemon surfaces as the window's daemon-not-running state instead of just an
// error string in the dialog.
func (g *gui) runManageAction(req ipc.Request, okMsg string, status *widget.Label) {
	err := roundtrip(req, func(ipc.Response) {})

	g.mu.Lock()
	open := g.manage != nil
	g.mu.Unlock()
	fyne.Do(func() {
		switch {
		case open:
			if err != nil {
				status.SetText("⚠ " + err.Error())
				return
			}
			status.SetText(okMsg)
		case err != nil:
			// The dialog was closed mid-round-trip; a failure must not vanish
			// with its status label (a success is visible in the dropdown).
			e := dialog.NewError(err, g.win)
			g.escapeCloses(e)
			e.Show()
		}
	})
	g.refreshPeers()
}

// ----- pairing / first-run onboarding -----

func (g *gui) openPairDialog() {
	if !g.beginPairing() {
		return
	}
	g.pairBtn.Disable()

	intro := widget.NewLabel("Scan this QR with the device you want to pair.")
	intro.Wrapping = fyne.TextWrapWord

	status := widget.NewLabel("Preparing pairing code...")
	status.Wrapping = fyne.TextWrapWord

	waiting := widget.NewProgressBarInfinite()
	waiting.Start()
	qrBox := container.NewCenter(waiting)

	var pairURI string
	uriLabel := widget.NewLabel("")
	uriLabel.Wrapping = fyne.TextWrapBreak
	uriLabel.Hide()

	copyBtn := newM3Button(m3Outlined, "Copy URI", theme.ContentCopyIcon(), func() {
		if pairURI == "" {
			return
		}
		g.app.Clipboard().SetContent(pairURI)
		status.SetText("Pairing URI copied.")
	})
	copyBtn.Disable()

	content := container.NewVBox(
		intro,
		qrBox,
		copyBtn,
		uriLabel,
		status,
	)
	pairDialog := dialog.NewCustom("Pair a device", "Close", content, g.win)
	pairDialog.Resize(fyne.NewSize(380, 520))

	var connMu sync.Mutex
	var conn net.Conn
	closed := make(chan struct{})
	var closedOnce sync.Once
	closeConn := func() {
		connMu.Lock()
		defer connMu.Unlock()
		if conn != nil {
			_ = conn.Close()
			conn = nil
		}
	}
	g.escapeCloses(pairDialog)
	pairDialog.SetOnClosed(func() {
		closedOnce.Do(func() { close(closed) })
		closeConn()
		g.setPairing(false)
		if g.pairBtn != nil {
			g.pairBtn.Enable()
		}
		g.refreshPeers()
	})
	pairDialog.Show()

	go func() {
		c, err := net.Dial("unix", ipc.SocketPath())
		if err != nil {
			g.setPairing(false)
			fyne.Do(func() {
				waiting.Hide()
				status.SetText("Daemon is not running. Start the daemon, then try pairing again.")
				if g.pairBtn != nil {
					g.pairBtn.Enable()
				}
			})
			g.showDaemonDown(err)
			return
		}
		select {
		case <-closed:
			_ = c.Close()
			return
		default:
		}

		connMu.Lock()
		conn = c
		connMu.Unlock()
		defer closeConn()

		if err := json.NewEncoder(c).Encode(ipc.Request{Cmd: ipc.CmdPairShow}); err != nil {
			g.setPairing(false)
			fyne.Do(func() {
				waiting.Hide()
				status.SetText("Pairing failed: " + err.Error())
				if g.pairBtn != nil {
					g.pairBtn.Enable()
				}
			})
			return
		}

		dec := json.NewDecoder(bufio.NewReader(c))
		for {
			var resp ipc.Response
			if err := dec.Decode(&resp); err != nil {
				g.setPairing(false)
				fyne.Do(func() {
					waiting.Hide()
					status.SetText("Pairing stopped.")
					if g.pairBtn != nil {
						g.pairBtn.Enable()
					}
				})
				return
			}
			if resp.Err != "" {
				g.setPairing(false)
				fyne.Do(func() {
					waiting.Hide()
					status.SetText("Pairing failed: " + resp.Err)
					if g.pairBtn != nil {
						g.pairBtn.Enable()
					}
				})
				return
			}
			if resp.Line != "" && strings.HasPrefix(resp.Line, "adrop://pair?d=") {
				uri := resp.Line
				qr, err := pairingQR(uri)
				fyne.Do(func() {
					pairURI = uri
					waiting.Hide()
					if err != nil {
						status.SetText("Pairing code ready. Copy the URI to pair manually.")
					} else {
						qrBox.Objects = []fyne.CanvasObject{qr}
						qrBox.Refresh()
						status.SetText("Waiting for the other device...")
					}
					uriLabel.SetText(uri)
					uriLabel.Show()
					copyBtn.Enable()
				})
			} else if resp.Line != "" {
				line := resp.Line
				fyne.Do(func() { status.SetText(line) })
			}
			if resp.Done {
				g.setPairing(false)
				fyne.Do(func() {
					waiting.Hide()
					if g.pairBtn != nil {
						g.pairBtn.Enable()
					}
				})
				g.refreshPeers()
				return
			}
		}
	}()
}

func (g *gui) beginPairing() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pairing {
		return false
	}
	g.pairing = true
	return true
}

func (g *gui) setPairing(on bool) {
	g.mu.Lock()
	g.pairing = on
	g.mu.Unlock()
}

func (g *gui) pairingActive() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pairing
}

func pairingQR(uri string) (fyne.CanvasObject, error) {
	png, err := pairing.RenderPNG(uri)
	if err != nil {
		return nil, err
	}
	img := canvas.NewImageFromReader(bytes.NewReader(png), "adrop-pair.png")
	img.FillMode = canvas.ImageFillContain
	bg := canvas.NewRectangle(color.White)
	return container.NewGridWrap(fyne.NewSize(260, 260), container.NewStack(bg, img)), nil
}

func (g *gui) setSendEnabled(on bool) {
	if on {
		g.chooseBtn.Enable()
		g.clipBtn.Enable()
	} else {
		g.chooseBtn.Disable()
		g.clipBtn.Disable()
	}
}

// chooseFiles opens a native file picker and sends the chosen file. (Fyne's
// open dialog is single-select; drop a batch onto the window for many files.)
func (g *gui) chooseFiles() {
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil || r == nil {
			return // cancelled or error
		}
		path := r.URI().Path()
		_ = r.Close()
		g.sendFromInput(path)
	}, g.win)
	g.escapeCloses(d)
	d.Show()
}

// currentPeer returns the selected peer name, or "" if none.
func (g *gui) currentPeer() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.peer
}

// ----- sending -----

// showFiles displays the basenames of every file in the batch, one per line, so
// the user sees exactly what will be sent. Empty paths hide the list.
func (g *gui) showFiles(paths []string) {
	fyne.Do(func() {
		if len(paths) == 0 {
			g.fileList.Hide()
			return
		}
		names := make([]string, len(paths))
		for i, p := range paths {
			names[i] = "• " + filepath.Base(p)
		}
		header := fmt.Sprintf("%d file(s):", len(paths))
		setM3Text(g.fileList, header+"\n"+strings.Join(names, "\n"))
		g.fileList.Show()
	})
}

// beginSend claims the single-send slot. It returns false (and shows a brief
// inline notice) if a send is already in flight, so the caller should bail out
// without starting a second one. On success it marks a send as in flight; the
// matching clear happens in runSend's final fyne.Do block.
func (g *gui) beginSend() bool {
	g.mu.Lock()
	busy := g.sending
	if !busy {
		g.sending = true
	}
	g.mu.Unlock()
	if busy {
		fyne.Do(func() { setM3Text(g.outLabel, "⚠ a send is in progress") })
		return false
	}
	return true
}

// sendFromInput decodes input (paste) into file paths and sends them in one
// session to the current peer. On any decode/stat failure it shows the bad URIs
// and does not send. Splitting is newline-only so pasted paths with spaces work.
func (g *gui) sendFromInput(input string) {
	paths, err := decodeFileURIs(input)
	if err != nil {
		fyne.Do(func() { setM3Text(g.outLabel, "⚠ "+err.Error()) })
		if len(paths) == 0 {
			return
		}
	}
	g.sendPaths(paths)
}

// sendPaths stages already-resolved file paths and sends them to the current
// peer. It is the shared tail for both drop and paste: pick a peer, guard
// against a concurrent send, stage for Retry, list the batch, and fire runSend.
func (g *gui) sendPaths(paths []string) {
	if len(paths) == 0 {
		return
	}
	peer := g.currentPeer()
	if peer == "" {
		fyne.Do(func() { setM3Text(g.outLabel, "⚠ pick a peer first") })
		return
	}
	if !g.beginSend() {
		return
	}
	g.mu.Lock()
	g.staged = paths
	g.mu.Unlock()
	g.showFiles(paths) // list all files before/while sending
	go g.runSend(sendFilesRequest(peer, paths), paths)
}

// retry re-sends the staged files to the currently-selected peer.
func (g *gui) retry() {
	g.mu.Lock()
	paths := g.staged
	g.mu.Unlock()
	peer := g.currentPeer()
	if len(paths) == 0 || peer == "" {
		return
	}
	if !g.beginSend() {
		return
	}
	fyne.Do(func() { g.retryBtn.Hide() })
	g.showFiles(paths)
	go g.runSend(sendFilesRequest(peer, paths), paths)
}

func (g *gui) sendClipboard() {
	peer := g.currentPeer()
	if peer == "" {
		fyne.Do(func() { setM3Text(g.outLabel, "⚠ pick a peer first") })
		return
	}
	if !g.beginSend() {
		return
	}
	g.showFiles(nil) // clipboard isn't a file batch
	go g.runSend(sendClipRequest(peer), nil)
}

// runSend performs a send round-trip, rendering streamed progress lines onto the
// outbound row. staged is the file list kept for Retry on failure (nil for clip).
func (g *gui) runSend(req ipc.Request, staged []string) {
	fyne.Do(func() {
		g.outBar.Show()
		g.outBar.SetValue(0)
		g.retryBtn.Hide()
		setM3Text(g.outLabel, "↑ sending…")
	})
	err := roundtrip(req, func(r ipc.Response) {
		if r.Line == "" {
			return
		}
		line := r.Line
		frac, ok := progressFraction(line)
		fyne.Do(func() {
			setM3Text(g.outLabel, "↑ "+line)
			if ok {
				g.outBar.SetValue(frac)
			}
		})
	})
	fyne.Do(func() {
		if err != nil {
			g.outBar.Hide()
			setM3Text(g.outLabel, "⚠ "+err.Error())
			if len(staged) > 0 {
				g.retryBtn.Show() // keep staged files for a retry
			}
			g.mu.Lock()
			g.sending = false // release the slot for a retry
			g.mu.Unlock()
			return
		}
		g.outBar.SetValue(1)
		setM3Text(g.outLabel, "↑ done")
		g.fileList.Hide() // batch complete; clear the list
		g.mu.Lock()
		g.staged = nil    // success: clear staging
		g.sending = false // release the slot
		g.mu.Unlock()
	})
}

// progressFraction parses a streamed send Line like "photo.jpg: 60%" into a
// 0..1 fraction. Returns ok=false for non-percentage lines.
func progressFraction(line string) (float64, bool) {
	if !strings.HasSuffix(line, "%") {
		return 0, false
	}
	i := strings.LastIndex(line, " ")
	num := strings.TrimSuffix(strings.TrimSpace(line[i+1:]), "%")
	var pct int
	if _, err := fmt.Sscanf(num, "%d", &pct); err != nil {
		return 0, false
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return float64(pct) / 100, true
}

// ----- receive feed -----

// errSubscribeUnsupported means the daemon rejected CmdSubscribe (e.g. an older
// daemon that predates the event stream). There's no point reconnecting: the
// GUI degrades to send-only for the rest of the session.
var errSubscribeUnsupported = errors.New("subscribe unsupported by daemon")

// minStreamUptime is how long a subscribe connection must stay up before we
// treat it as a healthy stream and reset the reconnect backoff. Without this,
// a daemon that accepts the connection but ends the stream immediately (e.g.
// Done on shutdown) would let the loop reset backoff and re-dial in a tight
// spin.
const minStreamUptime = 5 * time.Second

// subscribeLoop holds one long-lived CmdSubscribe connection and renders inbound
// events. It reconnects with backoff if the connection drops (daemon restart).
func (g *gui) subscribeLoop() {
	backoff := time.Second
	for {
		select {
		case <-g.stopCh:
			return
		default:
		}
		start := time.Now()
		err := g.subscribeOnce()
		if errors.Is(err, errSubscribeUnsupported) {
			// Daemon doesn't speak the event stream; stop retrying for good.
			return
		}
		// Reset backoff only if the stream actually stayed up for a while; a
		// near-instant return (error or clean Done) must keep backing off so we
		// don't spin re-dialing.
		if err == nil && time.Since(start) >= minStreamUptime {
			backoff = time.Second
			continue
		}
		// Couldn't connect or the stream dropped/ended quickly — wait and retry.
		select {
		case <-g.stopCh:
			return
		case <-time.After(backoff):
		}
		if backoff < 10*time.Second {
			backoff *= 2
		}
	}
}

// subscribeOnce dials the daemon, sends CmdSubscribe, and renders events until
// the stream ends or stop is requested. It returns errSubscribeUnsupported if
// the daemon rejects the command, nil on a clean stream end, or the underlying
// error otherwise.
func (g *gui) subscribeOnce() error {
	conn, err := net.Dial("unix", ipc.SocketPath())
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() {
		<-g.stopCh
		conn.Close()
	}()

	if err := json.NewEncoder(conn).Encode(ipc.Request{Cmd: ipc.CmdSubscribe}); err != nil {
		return err
	}
	dec := json.NewDecoder(bufio.NewReader(conn))
	for {
		var resp ipc.Response
		if err := dec.Decode(&resp); err != nil {
			return err
		}
		if resp.Err != "" {
			// Old daemon: "unknown command". Degrade to send-only silently.
			return errSubscribeUnsupported
		}
		if resp.Event != nil {
			g.renderEvent(*resp.Event)
		}
		if resp.Done {
			return nil
		}
	}
}

func (g *gui) renderEvent(e ipc.Event) {
	text, ok := recvStatus(e)
	if !ok {
		return
	}
	frac := recvFraction(e)
	fyne.Do(func() {
		switch e.Kind {
		case "recv-done":
			g.inBar.Hide()
			g.setRecvIdle()
			return
		case "recv-error":
			g.inBar.Hide()
			g.showRecv("Transfer failed", text, m3ErrorContainer) // stays until the next transfer
			return
		}
		g.showRecv("Receiving transfer…", text, m3PrimaryContainer)
		if frac >= 0 {
			g.inBar.Show()
			g.inBar.SetValue(frac)
		}
	})
}

// ----- daemon-not-running state -----

func (g *gui) showDaemonDown(err error) {
	fyne.Do(func() {
		g.mu.Lock()
		g.daemonDown = true
		g.mu.Unlock()
		g.showRecv("Daemon not running", "Start it to send and receive files.", m3ErrorContainer)
		g.inBar.Hide()
		g.startBtn.Show()
		if g.pairBtn != nil {
			g.pairBtn.Hide()
		}
		g.manageBtn.Disable() // every management action needs the daemon
		g.setSendEnabled(false)
	})
}

func (g *gui) clearDaemonDown() {
	fyne.Do(func() {
		g.mu.Lock()
		wasDown := g.daemonDown
		g.daemonDown = false
		g.mu.Unlock()
		if wasDown { // leave a receive in progress alone
			g.setRecvIdle()
		}
		g.startBtn.Hide()
		g.manageBtn.Enable()
	})
}

func (g *gui) startDaemon() {
	go func() {
		cmd := exec.Command("systemctl", "--user", "start", "adrop")
		out, err := cmd.CombinedOutput()
		if err != nil {
			fyne.Do(func() {
				setM3Text(g.recvSub, "Start failed: "+strings.TrimSpace(string(out)))
			})
			return
		}
		// Give the socket a moment, then retry the connection.
		time.Sleep(500 * time.Millisecond)
		g.refreshState()
	}()
}

func (g *gui) stop() {
	g.stopOnce.Do(func() { close(g.stopCh) })
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
