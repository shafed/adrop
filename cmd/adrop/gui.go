//go:build gui

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"net"
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

// runGUI launches the Fyne drop window. It is a thin IPC client of the daemon:
// sends dial per-request (reusing roundtrip), and one long-lived CmdSubscribe
// connection feeds the inbound row.
func runGUI(_ []string) error {
	a := app.NewWithID("dev.adrop.gui")
	a.Settings().SetTheme(adropTheme{}) // match the phone's Material 3 teal look
	w := a.NewWindow("adrop")
	w.Resize(fyne.NewSize(320, 380))

	g := newGUI(a, w)
	w.SetContent(g.content())

	// Native file drag-drop: hand dropped URIs to the same decode+send path as
	// the paste field.
	w.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		var b strings.Builder
		for _, u := range uris {
			b.WriteString(u.Path())
			b.WriteByte('\n')
		}
		g.sendFromInput(b.String())
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

	peerSelect *widget.Select
	dropLabel  *widget.Label
	chooseBtn  *widget.Button
	clipBtn    *widget.Button
	statusLbl  *widget.Label // daemon-not-running / hint line
	startBtn   *widget.Button
	pairBtn    *widget.Button

	outLabel *widget.Label
	outBar   *widget.ProgressBar
	fileList *widget.Label // names of files queued/sending in the current batch
	inLabel  *widget.Label
	inBar    *widget.ProgressBar
	retryBtn *widget.Button

	mu       sync.Mutex
	peers    []string // device names, in dropdown order
	staged   []string // last batch's files, kept for Retry on failure
	pairing  bool     // true while a pairing dialog owns a pair-show request
	stopCh   chan struct{}
	stopOnce sync.Once
}

func newGUI(a fyne.App, w fyne.Window) *gui {
	return &gui{app: a, win: w, stopCh: make(chan struct{})}
}

func (g *gui) content() fyne.CanvasObject {
	g.peerSelect = widget.NewSelect(nil, func(string) {})
	g.peerSelect.PlaceHolder = "(no devices)"

	g.dropLabel = widget.NewLabel("Drop files here")
	g.dropLabel.Alignment = fyne.TextAlignCenter

	// Icon + pill-style buttons echo the phone's Material 3 actions. The two
	// primary actions get HighImportance (teal fill); helpers stay plain.
	g.chooseBtn = widget.NewButtonWithIcon("Choose files…", theme.FolderOpenIcon(), g.chooseFiles)
	g.chooseBtn.Importance = widget.HighImportance
	g.clipBtn = widget.NewButtonWithIcon("Send clipboard", theme.ContentPasteIcon(), g.sendClipboard)
	g.clipBtn.Importance = widget.HighImportance

	g.statusLbl = widget.NewLabel("")
	g.statusLbl.Wrapping = fyne.TextWrapWord
	g.startBtn = widget.NewButtonWithIcon("Start daemon", theme.MediaPlayIcon(), g.startDaemon)
	g.startBtn.Hide()
	g.pairBtn = widget.NewButtonWithIcon("Pair device", theme.ContentAddIcon(), g.openPairDialog)
	g.pairBtn.Importance = widget.HighImportance
	g.pairBtn.Hide()

	g.outLabel = widget.NewLabel("")
	g.outBar = widget.NewProgressBar()
	g.outBar.Hide()

	// fileList lists every file in the current send batch, one per line.
	g.fileList = widget.NewLabel("")
	g.fileList.Hide()

	g.retryBtn = widget.NewButtonWithIcon("Retry", theme.ViewRefreshIcon(), g.retry)
	g.retryBtn.Hide()

	g.inLabel = widget.NewLabel("")
	g.inBar = widget.NewProgressBar()
	g.inBar.Hide()

	body := container.NewVBox(
		widget.NewLabel("Peer:"),
		g.peerSelect,
		g.dropLabel,
		g.chooseBtn,
		g.clipBtn,
		widget.NewSeparator(),
		g.statusLbl,
		g.startBtn,
		g.pairBtn,
		g.fileList,
		g.outLabel,
		g.outBar,
		g.retryBtn,
		g.inLabel,
		g.inBar,
	)
	// Pin a flat "adrop" header at the top (like the phone's TopAppBar — plain
	// text on the surface, no fill), with the padded body filling the rest.
	return container.NewBorder(topBar(), nil, nil, nil, container.NewPadded(body))
}

// topBar renders a flat "adrop" header echoing the phone's Material 3
// TopAppBar: the app name in bold foreground over the surface, no band. Using a
// bold Label (not a raw canvas.Text) keeps the proper line height so the
// glyph tops aren't clipped by the row's top edge.
func topBar() fyne.CanvasObject {
	title := widget.NewLabelWithStyle("adrop", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	// Double padding gives the header breathing room like the phone's TopAppBar.
	return container.NewPadded(container.NewPadded(title))
}

// ----- state / peer list -----

// refreshState pulls both the device list and last-peer default in one pass.
func (g *gui) refreshState() {
	g.refreshPeers()
}

// refreshPeers re-queries CmdDevices + CmdStatus and repopulates the dropdown,
// defaulting the selection to the last-used peer.
func (g *gui) refreshPeers() {
	var names []string
	derr := roundtrip(ipc.Request{Cmd: ipc.CmdDevices}, func(r ipc.Response) {
		for _, d := range r.Devices {
			names = append(names, d.Name)
		}
	})
	if derr != nil {
		g.showDaemonDown(derr)
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
		g.mu.Unlock()

		g.peerSelect.Options = names
		if len(names) == 0 {
			g.peerSelect.PlaceHolder = "(no devices)"
			g.peerSelect.ClearSelected()
			g.setSendEnabled(false)
			g.statusLbl.SetText("No paired devices yet.")
			g.pairBtn.Show()
			if g.pairingActive() {
				g.pairBtn.Disable()
			} else {
				g.pairBtn.Enable()
			}
		} else {
			g.setSendEnabled(true)
			g.statusLbl.SetText("")
			g.pairBtn.Hide()
			sel := last
			if sel == "" || !contains(names, sel) {
				sel = names[0]
			}
			g.peerSelect.SetSelected(sel)
		}
		g.peerSelect.Refresh()
	})
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

	copyBtn := widget.NewButtonWithIcon("Copy URI", theme.ContentCopyIcon(), func() {
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
		g.peerSelect.Enable()
	} else {
		g.chooseBtn.Disable()
		g.clipBtn.Disable()
	}
}

// chooseFiles opens a native file picker and sends the chosen file. (Fyne's
// open dialog is single-select; drop a batch onto the window for many files.)
func (g *gui) chooseFiles() {
	dialog.ShowFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil || r == nil {
			return // cancelled or error
		}
		path := r.URI().Path()
		_ = r.Close()
		g.sendFromInput(path)
	}, g.win)
}

// currentPeer returns the selected peer name, or "" if none.
func (g *gui) currentPeer() string {
	return g.peerSelect.Selected
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
		g.fileList.SetText(header + "\n" + strings.Join(names, "\n"))
		g.fileList.Show()
	})
}

// sendFromInput decodes input (drop or paste) into file paths and sends them in
// one session to the current peer. On any decode/stat failure it shows the bad
// URIs and does not send. On send failure it stages the files for Retry.
func (g *gui) sendFromInput(input string) {
	paths, err := decodeFileURIs(input)
	if err != nil {
		fyne.Do(func() { g.outLabel.SetText("⚠ " + err.Error()) })
		if len(paths) == 0 {
			return
		}
	}
	if len(paths) == 0 {
		return
	}
	peer := g.currentPeer()
	if peer == "" {
		fyne.Do(func() { g.outLabel.SetText("⚠ pick a peer first") })
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
	fyne.Do(func() { g.retryBtn.Hide() })
	g.showFiles(paths)
	go g.runSend(sendFilesRequest(peer, paths), paths)
}

func (g *gui) sendClipboard() {
	peer := g.currentPeer()
	if peer == "" {
		fyne.Do(func() { g.outLabel.SetText("⚠ pick a peer first") })
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
		g.outLabel.SetText("↑ sending…")
	})
	err := roundtrip(req, func(r ipc.Response) {
		if r.Line == "" {
			return
		}
		line := r.Line
		frac, ok := progressFraction(line)
		fyne.Do(func() {
			g.outLabel.SetText("↑ " + line)
			if ok {
				g.outBar.SetValue(frac)
			}
		})
	})
	fyne.Do(func() {
		if err != nil {
			g.outBar.Hide()
			g.outLabel.SetText("⚠ " + err.Error())
			if len(staged) > 0 {
				g.retryBtn.Show() // keep staged files for a retry
			}
			return
		}
		g.outBar.SetValue(1)
		g.outLabel.SetText("↑ done")
		g.fileList.Hide() // batch complete; clear the list
		g.mu.Lock()
		g.staged = nil // success: clear staging
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
		if err := g.subscribeOnce(); err != nil {
			// Couldn't connect or stream dropped — wait and retry.
			select {
			case <-g.stopCh:
				return
			case <-time.After(backoff):
			}
			if backoff < 10*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
	}
}

// subscribeOnce dials the daemon, sends CmdSubscribe, and renders events until
// the stream ends or stop is requested.
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
			return nil
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
		g.inLabel.SetText(text)
		if frac >= 0 {
			g.inBar.Show()
			g.inBar.SetValue(frac)
		} else if e.Kind == "recv-done" || e.Kind == "recv-error" {
			g.inBar.Hide()
		}
	})
}

// ----- daemon-not-running state -----

func (g *gui) showDaemonDown(err error) {
	fyne.Do(func() {
		g.statusLbl.SetText("⚠ daemon not running")
		g.startBtn.Show()
		if g.pairBtn != nil {
			g.pairBtn.Hide()
		}
		g.setSendEnabled(false)
	})
}

func (g *gui) clearDaemonDown() {
	fyne.Do(func() {
		g.startBtn.Hide()
	})
}

func (g *gui) startDaemon() {
	go func() {
		cmd := exec.Command("systemctl", "--user", "start", "adrop")
		out, err := cmd.CombinedOutput()
		if err != nil {
			fyne.Do(func() {
				g.statusLbl.SetText("⚠ start failed: " + strings.TrimSpace(string(out)))
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
