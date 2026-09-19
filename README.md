# adrop

AirDrop-like file & clipboard transfer over a key-pinned TLS LAN connection.
This repository implements the **PC side** (Arch Linux) from
[`SPEC.md`](SPEC.md): a single Go binary that is the **desktop app**, the
resident **daemon** and a thin **CLI**. Running `adrop` with no arguments opens
the drag-drop window; the CLI lives under explicit subcommands.

The Android side lives in `android/`. Because the daemon is symmetric — it both
serves and originates transfers — two PCs can pair and exchange files directly,
which is also how the protocol is integration-tested.

## Features (MVP)

- **Pairing via QR.** `adrop pair show` prints a scannable QR (and the raw
  `adrop://pair?d=...` URI). The QR carries the device name, its self-signed
  certificate + fingerprint, and a LAN address. The scanning peer pins the
  fingerprint; a back-connect lets both sides pin each other.
- **Pinned mutual TLS.** Every connection is TLS 1.3 with both sides presenting
  certificates; a peer is accepted only if its certificate fingerprint is in the
  trusted set. No CA, no passwords, MITM-resistant. Revocation is immediate.
- **Multi-file, single-session transfer.** `adrop send <peer> a.pdf b.jpg` sends
  all files in one session; each file's SHA-256 is verified by the receiver,
  which discards mismatches.
- **Safe receive.** Files land in `~/Downloads`; name collisions auto-rename
  (`file.pdf` → `file (1).pdf`). Never overwrites, never prompts.
- **Clipboard push.** `adrop clip <peer>` pushes the local Wayland clipboard
  (via `wl-paste`) to a peer, which silently sets it (via `wl-copy`).
- **Desktop notifications** via `notify-send` on receive.
- **Unix-socket IPC** between CLI and daemon; **systemd user service** for the
  daemon.

## Build & install

```sh
make build              # produces ./adrop (with the GUI)
make test               # unit + integration tests
make race               # tests under the race detector
make install            # installs both binaries to ~/.local/bin + systemd unit
make gui-install        # add the app-menu launcher
systemctl --user daemon-reload
systemctl --user enable --now adrop
```

`make install` installs **two** binaries from the same source:

- `adrop` — the window and the CLI. Built with the GUI, so it needs
  `CGO_ENABLED=1`, a C toolchain and Fyne's Linux prerequisites: `libgl`/`mesa`,
  `libxcursor`, `libxrandr`, `libxinerama`, `libxi`, `libxxf86vm` dev headers.
- `adrop-daemon` — what the systemd unit runs (`ExecStart=…/adrop-daemon
  daemon`). Static and `CGO_ENABLED=0`, so the resident service does not depend
  on the X11/GL libraries the GUI links and keeps running across a mesa/xorg
  change.

**Upgrading from a single-binary install:** the unit's `ExecStart` changed, so
run `systemctl --user daemon-reload && systemctl --user restart adrop` after
`make install`. A stale `~/.local/bin/adrop` left over from before is harmless
(`make uninstall` removes both).

On a machine with no display stack, build the daemon/CLI only:

```sh
make build-headless     # static, CGO_ENABLED=0, no Fyne dependency
make install-headless
```

That binary has no window, so a bare `adrop` prints usage there; everything
else — daemon, CLI, systemd unit — is identical. `install-headless` puts the one
CGO-free build under both names, so the unit's `ExecStart` works unchanged.

Runtime dependencies: `wl-clipboard` (`wl-copy`/`wl-paste`) for clipboard,
`libnotify` (`notify-send`) for notifications. Neither is required for file
transfer.

### FCM wake relay (optional)

When a direct dial to the phone fails, the daemon asks a local relay to send an
FCM push so the phone opens its receive window, then retries. The relay is
opt-in because it needs a Firebase service-account key:

```sh
make relay-install      # builds adrop-relay, installs the binary + user unit
cp <service-account>.json ~/.config/adrop/fcm-service-account.json
systemctl --user daemon-reload
systemctl --user enable --now adrop-relay
```

The relay listens on **127.0.0.1:18080** only — `/wake` is unauthenticated and
pushes to whatever FCM token it is given, so it must not be reachable from the
LAN. The daemon finds it via `ADROP_RELAY` (set to `http://localhost:18080` in
the packaged unit).

`adrop.service` declares `Wants=adrop-relay.service`, so the relay comes up with
the daemon once it is enabled. The dependency is deliberately weak: with no
relay installed the daemon still starts and simply has no wake fallback. The
relay unit carries `ConditionPathExists=` on the key, so a missing key makes
systemd skip it instead of crash-looping.

**If wake stops working,** check the relay first — `systemctl --user status
adrop-relay`. A stopped relay shows up in the daemon's journal as
`FCM wake failed: Post "http://localhost:18080/wake": … connection refused`.

### GUI

A small drag-drop window for the PC side — drop files onto it to send to the
last-used (or chosen) peer, paste a `file:///` path, push the clipboard, and
watch incoming transfers live. It's a thin IPC client of the daemon, exactly
like the CLI.

Launch it from the app menu (`make gui-install`) or by running `adrop` with no
arguments. If the daemon isn't running, the window says so and offers a
**Start daemon** button rather than failing.

**Managing devices.** The gear button next to the peer dropdown opens the
device list: each trusted device with its fingerprint and last-known address,
plus **Add device…** (the pairing QR, same as `adrop pair show`), rename, and
revoke. Renaming only changes the display name — trust is pinned to the
fingerprint — and revoke asks for confirmation first. Every action is an IPC
round-trip to the daemon; if one fails, the dialog shows the error and the list
is left unchanged.

The GUI is isolated behind a `gui` build tag, which `make build` sets; only
`make build-headless` drops it. There is no `adrop gui` subcommand — a bare
`adrop` is the way to open the window.

**Upgrading:** the `gui` subcommand used to exist, and launchers installed by
an older `make gui-install` still run `adrop gui`, which now fails with
"unknown command" — silently, since the entry is `Terminal=false`. Re-run
`make gui-install` once to rewrite the launcher.

## Usage

```sh
adrop                         # open the drag-drop window (default)
adrop daemon                  # run the resident daemon (normally via systemd)
adrop status                  # show this device's identity & trusted count
adrop pair show               # display pairing QR, wait for a peer to pair
adrop pair add <uri>          # trust a scanned adrop://pair?d=... URI
adrop devices                 # list trusted devices
adrop revoke <name|fp-prefix> # revoke (untrust) a device
adrop send [<peer>] <file...> # send files (one session) to a peer
adrop clip [<peer>] [text]    # push clipboard (or given text) to a peer
```

`<peer>` is optional after the first send — it defaults to the last-used peer.

`<peer>` is a device name or a fingerprint prefix (≥ 8 hex chars).

### Pairing two devices

On device A:

```sh
adrop pair show      # shows QR + URI, then waits
```

On device B, scan the QR (or copy the URI) and run:

```sh
adrop pair add 'adrop://pair?d=...'
```

Both devices now trust each other and can `send`/`clip` in either direction.

## Configuration

State lives under `$XDG_CONFIG_HOME/adrop` (or `~/.config/adrop`):

- `identity.key` / `identity.crt` — this device's ECDSA P-256 TLS identity.
- `devices.json` — trusted peers (name, pinned fingerprint, last address).

### Environment variables

| Variable             | Purpose                            | Default                        |
| -------------------- | ---------------------------------- | ------------------------------ |
| `ADROP_CONFIG_DIR`   | config/state directory             | `~/.config/adrop`              |
| `ADROP_SOCKET`       | CLI↔daemon Unix socket path        | `$XDG_RUNTIME_DIR/adrop.sock`  |
| `ADROP_PORT`         | peer TLS listen port               | `53127`                        |
| `ADROP_NAME`         | advertised device name             | system hostname                |
| `ADROP_ADVERTISE_IP` | LAN IP in the pairing QR           | auto-detected (non-loopback)   |
| `ADROP_DOWNLOAD_DIR` | where received files land          | `~/Downloads`                  |

**`ADROP_NAME`** lets you give the PC a friendly name without changing the system
hostname:
```sh
ADROP_NAME=thinkpad-x1 adrop daemon
# or via a systemd unit override:
# [Service]
# Environment=ADROP_NAME=thinkpad-x1
```

**`ADROP_PORT`** is useful when running a second instance or when port 53127 is
taken:
```sh
ADROP_PORT=8877 adrop daemon
```

## Protocol

Application messages run over the pinned-TLS stream, framed as
`[4-byte big-endian length][JSON header][payload]`. A session is:

- `Hello` exchange (both sides identify by fingerprint + name + listen address)
- `SessionStart` (kind = `files` | `clipboard`)
- For files: `FileHeader` / `Chunk`… / `FileEnd` (repeated per file), `SessionEnd`
- For clipboard: `ClipboardData`, `SessionEnd`
- `Ack` messages carry success or error per file and per session.

The fingerprint exchanged in `Hello` is SHA-256 of the certificate DER bytes,
encoded as lowercase hex (64 chars). See [`internal/proto`](internal/proto/proto.go).

## Layout

```
cmd/adrop/            CLI + daemon entrypoint
internal/config/      identity (TLS cert) + trusted-device store
internal/proto/       wire framing & message types
internal/transport/   pinned mutual-TLS dial/listen
internal/pairing/     QR pairing payload encode/decode + terminal render
internal/clipboard/   wl-copy / wl-paste wrappers
internal/notify/      notify-send wrapper
internal/ipc/         CLI↔daemon Unix-socket control protocol
internal/daemon/      daemon: receive, send, pairing, IPC handling
android/              Android app (Kotlin/Compose)
packaging/systemd/    systemd user unit
```

## Status vs. SPEC

Implemented: pairing, pinned-TLS transport, bidirectional multi-file transfer
with SHA-256 verification, auto-rename on collision, clipboard push,
notifications, Unix-socket IPC, systemd user unit, device revocation,
self-healing stored peer address on every inbound connect.

Deferred (SPEC Phase 2): FCM wake, resume/chunked retransmit, folder transfer,
rich clipboard formats, mDNS discovery, relay fallback, per-file progress UI.
