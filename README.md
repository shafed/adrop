# adrop

AirDrop-like file & clipboard transfer over a key-pinned TLS LAN connection.
This repository implements the **PC side** (Arch Linux) from
[`SPEC.md`](SPEC.md): a single Go binary that is both the resident **daemon**
and a thin **CLI**.

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
make build              # produces ./adrop
make test               # unit + integration tests
make race               # tests under the race detector
make install            # installs to ~/.local/bin + systemd user unit
systemctl --user daemon-reload
systemctl --user enable --now adrop
```

Runtime dependencies: `wl-clipboard` (`wl-copy`/`wl-paste`) for clipboard,
`libnotify` (`notify-send`) for notifications. Neither is required for file
transfer.

## Usage

```sh
adrop daemon                  # run the resident daemon (normally via systemd)
adrop status                  # show this device's identity & trusted count
adrop pair show               # display pairing QR, wait for a peer to pair
adrop pair add <uri>          # trust a scanned adrop://pair?d=... URI
adrop devices                 # list trusted devices
adrop revoke <name|fp-prefix> # revoke (untrust) a device
adrop send <peer> <file...>   # send files (one session) to a peer
adrop clip <peer> [text]      # push clipboard (or given text) to a peer
```

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
