# adrop GUI — Feature Spec (v1)

A desktop GUI for the PC side of adrop: a small, always-available **drag-drop
target window** for sending files (and clipboard) to paired peers, plus a live
view of **incoming** transfers. It is a thin IPC client of the existing daemon,
mirroring how the CLI works — the daemon remains the single source of truth for
identity, trust, and transfer logic.

This document specifies v1 only. It assumes familiarity with `SPEC.md`,
`README.md`, and the IPC protocol in `internal/ipc/ipc.go`.

---

## 1. Goals & non-goals

### Goals
- A lightweight window you drop files onto; files send immediately to the
  last-used peer (AirDrop-like).
- Accept a **pasted `file:///...` URI string** and send the referenced file(s).
- Show **incoming** transfer activity (peer, filename, progress) live.
- Pick the send target from a dropdown of trusted devices.
- Send the local clipboard to the current peer.
- Be the *friendly entry point*: if the daemon isn't running, say so and offer
  to start it — don't just error like the CLI.

### Non-goals (explicitly out of scope for v1)
- **Pairing UI / QR display.** Pairing stays CLI-only (`adrop pair show/add`).
  No `CmdPairShow` rendering in the GUI.
- **Device management** (revoke, rename) in the GUI. CLI-only.
- **Online/offline liveness dots** in the peer dropdown. The dropdown lists
  trusted devices by name only; the daemon has no liveness probe and v1 does not
  add one. A send to an offline peer simply fails (handled gracefully, §6).
- **Persistent / multiplexed IPC connection.** The GUI dials per operation, like
  the CLI. (The receive feed is the *one* long-lived connection — see §4.)
- **Transfer history persistence.** The window shows the current/most-recent
  activity only; nothing is stored to disk.
- **Tray icon, full control-panel window, settings screen.** v1 is a single
  small drop window.

---

## 2. What it replaces / the workaround today

Today, to send from the desktop you either:
- run `adrop send <peer> <file...>` in a terminal, or
- right-click in Dolphin → **Send via adrop** (`packaging/dolphin/adrop.desktop`,
  `Exec=adrop send %F`), which fires a one-shot CLI send with no visible
  progress or peer choice.

The GUI gives a persistent visual drop target with peer selection, live
progress, and inline error recovery. **The Dolphin service menu and the CLI are
unchanged in v1** — they keep working exactly as before. (Re-pointing Dolphin at
the GUI is a possible v2 follow-up, deliberately deferred.)

---

## 3. Architecture & how it fits

```
            ┌──────────────────────────────┐
            │        adrop daemon          │
            │  (resident, systemd --user)  │
            │  identity · trust · transfer │
            └──────────────┬───────────────┘
                           │ Unix socket (newline-JSON IPC)
        ┌──────────────────┼──────────────────┐
        │                  │                  │
   adrop CLI          adrop GUI          adrop GUI
  (per-request)   send: per-request   recv: 1 long-lived
                                       CmdSubscribe conn
```

- The GUI is launched as **`adrop gui`** — a new subcommand of the existing
  single binary, alongside `daemon`, `send`, etc. (`cmd/adrop/main.go` switch).
- It speaks the **existing IPC protocol** in `internal/ipc`. Sends reuse
  `CmdSendFiles` / `CmdSendClip`; the device list reuses `CmdDevices`; status /
  last-peer reuses `CmdStatus`. **No changes to send-side IPC or daemon send
  logic.**
- The **only** new daemon-side work is a receive-event broadcast (§4).
- **Connection model:** the GUI dials a fresh socket connection per send (exactly
  mirroring `cmd/adrop/client.go:roundtrip`), and holds **one** separate
  long-lived connection for the `CmdSubscribe` receive feed. The daemon's
  existing one-request-per-connection assumption is preserved for everything
  except the subscribe stream, which is intentionally long-lived.

### 3.1 Build-system constraint (must respect) ⚠️

The daemon/CLI binary builds with **`CGO_ENABLED=0`** (`Makefile`) — a key
property (static, lean, no C deps). Choosing `adrop gui` as a *subcommand* of the
**same binary** means the GUI toolkit is linked into that binary.

**Any real GUI toolkit needs CGO on Linux.** Fyne links OpenGL/X11; Gio also
requires CGO on Linux (Wayland/X11/EGL/GLES — there is *no* practical
`CGO_ENABLED=0` Gio build on Linux). So the choice is **not** about avoiding
CGO — CGO is unavoidable for the GUI. The real job is keeping the *default*
daemon/CLI build CGO-free and GUI-dependency-free.

**Decision for v1:** build the GUI with **Fyne** (richer widgets, native
file drag-drop via `Window.SetOnDropped`, much less UI code for the small drop
window than Gio's low-level API), isolated behind a Go **build tag**
(`//go:build gui`), so:
- `make build` keeps producing the lean **`CGO_ENABLED=0`** daemon/CLI binary
  with **no Fyne/CGO dependency** compiled in when the `gui` tag is absent;
- a new `make build-gui` target builds the binary **with** the `gui` tag and
  **`CGO_ENABLED=1`** (Fyne needs it);
- `adrop gui` is registered in `main.go` only under the `gui` tag; without the
  tag the subcommand prints "this build has no GUI; rebuild with `make
  build-gui`".

The build tag is what preserves both user decisions — "single binary /
`adrop gui` subcommand" **and** a static, CGO-free default daemon/CLI — even
though the GUI build itself uses CGO. If a single-binary build ever proves
unworkable, the fallback is a separate `cmd/adrop-gui` binary (rejected by the
user for v1, but the build-tag layout degrades to it cleanly).

**Build dependencies (GUI build only):** Fyne requires a C toolchain plus
`libgl`/`mesa`, `libxcursor`, `libxrandr`, `libxinerama`, `libxi`, `libxxf86vm`
dev headers (per Fyne's Linux prerequisites). These are *not* needed for the
default `make build`.

---

## 4. New IPC: receive-event subscription

To show incoming transfers, add **one** new command and a small event payload.
This is additive and backward-compatible: existing clients never send
`CmdSubscribe` and are unaffected.

### 4.1 `internal/ipc/ipc.go` additions

```go
const CmdSubscribe Command = "subscribe" // long-lived: stream receive events

// Event is a daemon->GUI push on a CmdSubscribe stream. Each Response on that
// stream carries exactly one Event in a new Response.Event field.
type Event struct {
    Kind     string `json:"kind"`      // "recv-start" | "recv-progress" | "recv-file-done" | "recv-done" | "recv-error"
    Peer     string `json:"peer"`      // sending peer's name
    File     string `json:"file"`      // basename of the current file (if any)
    Index    int    `json:"index"`     // 0-based file index within the session
    Count    int    `json:"count"`     // total files in the session (if known)
    BytesDone int64 `json:"bytes_done"`
    Total     int64 `json:"total"`     // total bytes for the current file (if known)
    Err      string `json:"err,omitempty"`
}
```

Add `Event *Event `json:"event,omitempty"`` to `ipc.Response`. The subscribe
stream sends `Response{Event: ...}` messages and **never** sets `Done` until the
daemon shuts down or the connection drops.

### 4.2 Daemon side (`internal/daemon`)

- Add a subscriber registry to `Daemon`: a mutex-guarded set of channels (or
  callback funcs), with `subscribe()` / `unsubscribe()` and a `broadcast(Event)`
  that is non-blocking (drops events to a slow/full subscriber rather than
  stalling a transfer).
- In `internal/daemon/ipc_handler.go`, handle `CmdSubscribe`: register a
  subscriber, stream each broadcast `Event` as `Response{Event: e}` until the
  client disconnects (reuse the EOF-watch pattern already in
  `cancelOnDisconnect` / `waitPairOrDisconnect`), then unsubscribe. This is the
  only handler arm that loops indefinitely.
- Wire broadcasts into the **existing** receive path
  (`internal/daemon/receive.go`), which already has the data:
  - `receiveSession` start → `recv-start` (peer, count).
  - `receiveFilesWithProgress` `onProgress` callback (already exists at
    `receive.go:141`/`159`) → `recv-progress` (index, bytesDone, total).
  - per-file completion (`receive.go:200` "received …") → `recv-file-done`.
  - session summary (`receive.go:204`/`206`, where `notify.Send` fires) →
    `recv-done`.
  - errors → `recv-error`.
- **No change to receive behavior or notifications.** The daemon still
  auto-saves to `~/Downloads` and still fires `notify-send`. The GUI feed is
  purely additive observability; the GUI does **not** approve/deny receives
  (receive is automatic, per existing design).

### 4.3 Clipboard receive
Out of scope to surface in the feed for v1 (clipboard sets silently today). The
event schema above covers files only. (Adding a `recv-clipboard` kind later is
trivial and non-breaking.)

---

## 5. GUI behavior & layout

```
┌─ adrop ────────────┐
│ Peer: [thinkpad ▾] │   ← dropdown from CmdDevices; default = LastPeer
│                    │
│  drop files here   │   ← drag-drop target (text/uri-list, file://)
│   ⬇ file:/// ok    │
│ [ paste file:// ]  │   ← paste-a-URI field
│ [ send clipboard ] │
│                    │
│ ↑ photo.jpg ▓▓░60% │   ← outbound progress
│ ↓ from phone:      │   ← inbound (from CmdSubscribe feed)
│   IMG_001.jpg ▓░70%│
└────────────────────┘
```

### 5.1 Peer selection
- The dropdown is populated by a `CmdDevices` round-trip on open (and refreshed
  when the window regains focus). Names only — **no liveness dots** (§1).
- Default selection is `StatusInfo.LastPeer` (from `CmdStatus`). If there is no
  last peer and ≥1 device exists, default to the first device. If **zero**
  trusted devices, show "No paired devices — pair with `adrop pair show`" and
  disable sending.

### 5.2 Sending (auto-send to current peer)
- **Drop files** onto the window → Fyne's `Window.SetOnDropped(pos, []fyne.URI)`
  delivers the dropped items as `fyne.URI` values. For each, take its file path
  (`URI.Path()` / strip the `file://[host]` scheme; URL-unescape `%XX`, e.g.
  `%20`→space; reject non-`file` schemes), `os.Stat` each, then immediately fire
  `CmdSendFiles{Target: <selected peer>, Files: [...]}` in one session. Multiple
  files dropped together → one session (matches CLI batch semantics). The
  `file://` decode logic lives in the same testable helper used by the paste
  path (§5.2 next bullet) — `SetOnDropped` is just the entry point.
- **Paste a `file:///` string** into the paste field → same decode path → send
  immediately. Accept one or many whitespace/newline-separated URIs.
- **Send clipboard** button → `CmdSendClip{Target: <selected peer>}` (daemon
  reads `wl-paste`, exactly like `adrop clip`).
- Progress: read streamed `Response.Line` values from the send round-trip; reuse
  the existing convention that a line ending in `%` is a transient per-file
  progress update (see `client.go:isProgressLine`). Render as a bar.

### 5.3 Receiving (display only)
- On open, the GUI dials the long-lived `CmdSubscribe` connection and renders
  incoming `Event`s as an inbound progress row. On `recv-done`, show a brief
  "received N file(s) from <peer>" line. Files still land in `~/Downloads` via
  the daemon; the GUI offers no save dialog (consistent with current design).
- If the subscribe connection drops (daemon restart), the GUI silently
  reconnects with backoff; falls back to the §7 "daemon not running" state if it
  can't.

---

## 6. Error & edge-case UX

| Situation | Behavior |
|---|---|
| Send fails (peer offline, dial error, mid-transfer abort) | Show inline error in the window (e.g. red "thinkpad offline"). **Keep the dropped/pasted files staged** so the user can switch the peer dropdown and click **Retry** without re-dropping. |
| No last peer & user hasn't picked one | Prompt to pick a peer from the dropdown before the auto-send proceeds; don't silently fail. |
| Zero trusted devices | Disable send controls; show "pair with `adrop pair show`". |
| Daemon not reachable on the IPC socket | Show **"⚠ daemon not running"** with a **[Start daemon]** button that runs `systemctl --user start adrop`, then retries the connection. Do **not** silently spawn `adrop daemon`. (Improves on the CLI's bare error.) |
| Dropped/pasted URI isn't a `file://` path, or path missing | Show a clear inline error naming the bad URI; do not send the others silently — surface which ones failed `os.Stat`. |
| Subscribe stream drops | Auto-reconnect with backoff; degrade to the daemon-not-running state if persistent. |

**Staging note:** §5.2 says "auto-send" for the happy path, but on *failure* the
files become *staged* so Retry works. The window holds the last drop's file list
in memory until a send succeeds or the user clears it.

---

## 7. Launch & packaging

- **Subcommand:** `adrop gui` (registered in `cmd/adrop/main.go`, under the
  `gui` build tag per §3.1).
- **App launcher entry:** add `packaging/desktop/adrop-gui.desktop`:
  ```ini
  [Desktop Entry]
  Type=Application
  Name=adrop
  Comment=Send files to paired devices
  Exec=adrop gui
  Icon=network-wireless
  Terminal=false
  Categories=Network;FileTransfer;
  ```
  Installed into `~/.local/share/applications/` so it appears in the KDE app
  menu and is pinnable. New Makefile targets `gui-install` / `gui-uninstall`
  (parallel to the existing `dolphin-install`).
- The existing `packaging/dolphin/adrop.desktop` (CLI `adrop send %F`) is **left
  unchanged**.
- `make build` stays `CGO_ENABLED=0` and GUI-free. `make build-gui` builds the
  GUI-enabled binary.

---

## 8. Backward compatibility & rollout

- **Fully additive.** The new `CmdSubscribe`/`Event` and `Response.Event` field
  are ignored by older clients (the CLI never sends `CmdSubscribe` and never
  reads `Event`). Existing daemon/CLI behavior is byte-for-byte unchanged.
- A new GUI talking to an **old daemon** (no `CmdSubscribe`): the subscribe dial
  gets an "unknown command" error and `Done:true`; the GUI degrades to
  **send-only** (no inbound feed) without crashing. Send still works against any
  daemon version.
- **Rollout:** ship behind the `gui` build tag, all at once (no runtime flag).
  Users opt in by running `make build-gui` + `make gui-install`. No migration,
  no config changes, no new state files. The daemon change ships in the normal
  binary and is inert until a GUI subscribes.

---

## 9. Touch list (for implementers)

- `internal/ipc/ipc.go` — add `CmdSubscribe`, `Event` type, `Response.Event`.
- `internal/daemon/daemon.go` — subscriber registry on `Daemon` (+ subscribe/
  unsubscribe/broadcast).
- `internal/daemon/ipc_handler.go` — `CmdSubscribe` arm (long-lived stream).
- `internal/daemon/receive.go` — emit broadcasts at start/progress/file-done/
  done/error (hook the existing `onProgress` callback and notify points; no
  behavior change).
- `cmd/adrop/main.go` — register `gui` subcommand (under `//go:build gui`).
- `cmd/adrop/gui.go` (new, `//go:build gui`) — Fyne window, `SetOnDropped`,
  file:// URI decode, send round-trips, subscribe loop, daemon-start button.
- `Makefile` — `build-gui`, `gui-install`, `gui-uninstall` targets.
- `packaging/desktop/adrop-gui.desktop` (new).
- `README.md` — document `adrop gui` and the build-gui/install flow.

### Tests
- `internal/ipc` round-trip of the new `Event`/`CmdSubscribe` JSON.
- `internal/daemon` subscriber broadcast: a subscribed connection receives
  `recv-*` events during a receive session; a slow subscriber is dropped without
  stalling the transfer; unsubscribe on disconnect.
- `file://` URI decoder unit tests (escapes, spaces, multiple URIs, non-file
  scheme rejection, missing path).
- The GUI rendering itself is not unit-tested; logic (URI decode, send request
  construction, event handling) lives in testable non-GUI helpers.

---

## 10. Note for the AI implementer

When building this, you may spawn or compose agents and use the **voltagent**
agent suite — create your own agent teams or delegate to individual specialists
as needed. Suggested fits for this feature:
- `voltagent-lang:golang-pro` — the daemon/IPC changes (§4) and the Go GUI code.
- `voltagent-core-dev:ui-designer` / `voltagent-core-dev:frontend-developer` —
  the Fyne window layout and interaction details (§5).
- `voltagent-dev-exp:build-engineer` — the `gui` build tag, `make build-gui`,
  and CGO/packaging wiring (§3.1, §7).
- `voltagent-qa-sec:code-reviewer` / `voltagent-qa-sec:test-automator` — review
  and the tests in the section above.

Coordinate them (e.g. via an orchestrator/`voltagent-meta:agent-organizer`) when
the work spans daemon + GUI + build at once. Use whichever agents make sense;
this list is guidance, not a constraint.
