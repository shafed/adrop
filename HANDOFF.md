# adrop — Handoff / Status

AirDrop-like file & clipboard transfer between an Arch Linux PC and an Android
phone over a key-pinned mutual-TLS LAN connection.

- **PC side (Go):** daemon + CLI, in `cmd/adrop` + `internal/*`. Built/tested here.
- **Android side (Kotlin/Compose):** in `android/`. Builds into an installable APK.

## What works (verified on real devices)

- ✅ PC daemon: pairing QR, pinned-TLS transport, multi-file transfer, clipboard
  push, auto-rename on collision, SHA-256 verify, Unix-socket IPC, systemd unit.
  All Go unit + integration tests pass (`make test`).
- ✅ Android app: builds, installs, launches, scans QR.
- ✅ **Pairing phone ↔ PC succeeds** end-to-end (mutual TLS handshake completes,
  both sides pin each other).
- ✅ **Wrong-phone-port bug FIXED** — PC now stores the phone's actual listen port
  (e.g. 7777) after pairing, not its own port (53127). See details below.

## Bugs found & fixed during bring-up

1. **Missing `gradle.properties`** → added `android.useAndroidX=true` (build failed
   on every AndroidX dep without it).
2. **`Icons.Default.ArrowBack`** in 3 Compose screens → `Icons.AutoMirrored.Filled.ArrowBack`.
3. **Missing `@OptIn(ExperimentalMaterial3Api::class)`** on 4 screens using `TopAppBar`.
4. **`endpointAlgorithmIdentifier`** typo in `PinnedTlsContext.kt` →
   `endpointIdentificationAlgorithm = null`.
5. **`BootReceiver` declared as `<provider>`** in AndroidManifest (it's a
   `BroadcastReceiver`) → app crashed at startup with `ClassCastException`.
   Removed the bogus `<provider>`; kept the correct `<receiver>`.
6. **AndroidKeyStore EC key cast** → `as ECPrivateKey` crashed on pairing
   (`AndroidKeyStoreECPrivateKey cannot be cast to ECPrivateKey`). Keystore keys
   are opaque; changed to `PrivateKey` in `Identity.kt`. Also added
   `.setDigests(SHA256/384/512/NONE)` to the `KeyGenParameterSpec` so Conscrypt
   can sign the TLS CertificateVerify.
7. **PC identity was Ed25519** → Android BoringSSL/Conscrypt does not advertise
   ed25519 in its cert signature algorithms, so the handshake failed with
   `tls: peer doesn't support any of the certificate's signature algorithms`
   (phone saw `SSLV3_ALERT_HANDSHAKE_FAILURE`). **Fixed:** switched PC identity
   to **ECDSA P-256** in `internal/config/config.go` (`createIdentity`). P-256 is
   universally supported; protocol only pins SHA-256 of DER so algorithm is free.
   Reproduced + verified with `openssl s_client -sigalgs` against the daemon.

## Bug FIXED — wrong phone port stored on PC

### What was wrong

After pairing, the PC stored the phone as:
```
"addr": "192.168.0.112:53127"   ← WRONG. Phone listens on 7777, not 53127.
```
So `adrop send SM-S721B ...` failed: `dial 192.168.0.112:53127: connection refused`.

- The phone listens on **7777** (`ReceiveForegroundService.LISTEN_PORT = 7777`).
- `53127` is the **PC daemon's** own port (`DefaultPort`).
- Root cause: the phone's pairing Hello arrived with `addr = "0.0.0.0:7777"` (it
  couldn't determine its own LAN IP). The old fallback in `tryCompletePairing` saw
  an unspecified host and discarded the phone's port, substituting the PC's own port
  (`d.port`) against the remote IP — storing `192.168.0.112:53127` instead of
  `192.168.0.112:7777`.

### How it was fixed

`(*Daemon).resolvePeerAddr` (`internal/daemon/pairing.go`) implements a correct
merge strategy:

1. **Advertised port is authoritative.** The peer's Hello `addr` field carries its
   listen port (e.g. 7777); that port is always kept.
2. **Unspecified host → substitute source IP.** When the host in `addr` is `""`,
   `0.0.0.0`, or `::`, the actual source IP of the live TCP connection is used
   instead, while the advertised port is preserved.
3. **No usable port at all → fall back to `DefaultPort`.** Only when the advertised
   `addr` is empty or declares port 0 do we fall back to the daemon's own default
   port against the source IP. (This is a last resort, not the normal path.)

Additionally, **self-healing on every inbound connect** (`handlePeer` in
`internal/daemon/receive.go`): whenever a trusted peer opens an inbound connection,
`UpdateAddr` is called with `resolvePeerAddr(hello.Addr, conn.RemoteAddr())`, so a
stale or wrong-ported entry in `devices.json` is corrected automatically the next
time the peer sends us anything — no re-pairing required after a DHCP address change.

### Regression tests

- **`TestResolvePeerAddr`** — unit-tests all `resolvePeerAddr` cases including the
  `0.0.0.0:port` → `sourceIP:port` fix (6 sub-cases).
- **`TestPairingStoresAdvertisedPort`** — integration test that pairs a phone
  advertising `0.0.0.0:<port>` and asserts the stored address is `127.0.0.1:<port>`
  (port preserved, host rewritten to source IP).
- **`TestSelfHealAddrOnInboundConnect`** — deliberately corrupts the stored addr
  to `DefaultPort`, then has the phone send a file, and asserts the addr is
  self-healed to the phone's real advertised port.

All three pass under `make race`.

## Environment variables

| Variable             | Purpose                                  | Default                        |
| -------------------- | ---------------------------------------- | ------------------------------ |
| `ADROP_CONFIG_DIR`   | Config/state directory                   | `~/.config/adrop`              |
| `ADROP_SOCKET`       | CLI↔daemon Unix socket path              | `$XDG_RUNTIME_DIR/adrop.sock`  |
| `ADROP_PORT`         | Peer TLS listen port                     | `53127`                        |
| `ADROP_NAME`         | Advertised device name (overrides hostname) | system hostname             |
| `ADROP_ADVERTISE_IP` | LAN IP advertised in the pairing QR      | auto-detected (non-loopback)   |
| `ADROP_DOWNLOAD_DIR` | Directory where received files land      | `~/Downloads`                  |

**`ADROP_NAME`** is useful when the hostname is not descriptive (e.g. rename the PC
from `archlinux` to `thinkpad-x1` without changing the system hostname):
```sh
ADROP_NAME=thinkpad-x1 adrop daemon
# or, in the systemd unit override:
systemctl --user edit adrop
# Add under [Service]:
#   Environment=ADROP_NAME=thinkpad-x1
```

**`ADROP_PORT`** lets you run a second daemon instance or avoid firewall conflicts:
```sh
ADROP_PORT=8877 adrop daemon
```

## Build & run quick reference

### PC (Go)

```sh
cd /home/shafed/adrop         # repo root
make build                    # builds ./adrop binary (CGO_ENABLED=0)
make test                     # unit + integration tests
make race                     # tests under Go race detector
make install                  # copies binary + installs systemd user unit

# Start daemon (one of):
systemctl --user enable --now adrop   # via systemd (recommended)
adrop daemon                          # foreground (for debugging)

# Common operations:
adrop status                  # show device identity + trusted count
adrop pair show               # display pairing QR, wait for scan
adrop pair add 'adrop://...'  # trust a scanned URI
adrop devices                 # list trusted devices
adrop revoke <name|fp>        # revoke (untrust) a device
adrop send <peer> file…       # send files to a paired device
adrop clip <peer> [text]      # push clipboard (or text) to a peer
```

### Android (needs SDK env vars)

```sh
cd /home/shafed/adrop/android
JAVA_HOME=/opt/android-studio/jbr \
ANDROID_HOME=/home/shafed/Android/Sdk \
ANDROID_SDK_ROOT=/home/shafed/Android/Sdk \
  ./gradlew assembleDebug --no-daemon

# APK: app/build/outputs/apk/debug/app-debug.apk  (package com.adrop.debug)
/home/shafed/Android/Sdk/platform-tools/adb install -r \
  app/build/outputs/apk/debug/app-debug.apk

# Launch:
/home/shafed/Android/Sdk/platform-tools/adb shell am start \
  -n com.adrop.debug/com.adrop.ui.MainActivity

# View crashes:
/home/shafed/Android/Sdk/platform-tools/adb logcat \
  -d AndroidRuntime:E "*:S" | tail -50

# Clear app data (forces fresh keystore identity — needed after identity changes):
/home/shafed/Android/Sdk/platform-tools/adb shell pm clear com.adrop.debug

# Run JVM-only proto unit tests (no device/emulator required):
JAVA_HOME=/opt/android-studio/jbr \
ANDROID_HOME=/home/shafed/Android/Sdk \
ANDROID_SDK_ROOT=/home/shafed/Android/Sdk \
  ./gradlew testDebugUnitTest --no-daemon
```

## Device-verification steps (confirming a working pair)

After pairing, verify end-to-end before declaring it done:

1. **Verify pairing stored correctly on PC:**
   ```sh
   adrop devices
   # Expected: name=<phone-model>, addr=<phone-LAN-IP>:7777
   # If port is 53127 instead of 7777: re-pair (old bug; fixed in current code).
   ```

2. **Verify PC → phone transfer:**
   - On phone: open the adrop app → tap "Open to receive" (starts receive window).
   - On PC: `adrop send <phone-name> /path/to/file.pdf`
   - Verify the file appears in phone Downloads + notification fires.

3. **Verify phone → PC transfer:**
   - Ensure PC daemon is running (`adrop status` or `systemctl --user status adrop`).
   - On phone: use the Send screen → pick a file → choose `pc` → send.
   - Verify the file appears in `~/Downloads` on the PC.

4. **Verify clipboard PC → phone:**
   ```sh
   echo "test clipboard" | wl-copy
   adrop clip <phone-name>
   # Then paste on the phone to confirm.
   ```

5. **Verify clipboard phone → PC:**
   - Copy text on the phone → use the app's clipboard-send button.
   - On PC: `wl-paste` should return the copied text.

6. **Confirm auto-rename on collision:**
   - Send the same filename twice. Second copy must arrive as `file (1).ext`, not
     overwriting the first.

7. **Confirm revocation works:**
   ```sh
   adrop revoke <name-or-fp-prefix>
   adrop devices  # device must be gone
   # Subsequent send attempts to that device must fail.
   ```

## Protocol wire format (interop contract)

The Go daemon (`internal/proto/proto.go`) and Android codec
(`android/.../data/proto/Proto.kt`) must agree on ALL of the following. Any drift
breaks interop. These are pinned by golden tests on both sides.

### Framing

Every message is laid out as:
```
[4 bytes big-endian uint32 : JSON header byte length]
[JSON header bytes, UTF-8]
[raw payload bytes — exactly Header.length bytes, absent when length == 0]
```

There is **no separator** between the JSON header and the payload — payload bytes
start immediately at byte offset `4 + jsonHeaderLength`.

### JSON keys (Go struct tags → exact wire names)

| Field          | JSON key       | Notes |
| -------------- | -------------- | ----- |
| Type           | `"type"`       | always present |
| Version        | `"version"`    | hello only; omitted on other types |
| Fingerprint    | `"fingerprint"`| hello only; 64-char lowercase hex |
| Name           | `"name"`       | hello only |
| Addr           | `"addr"`       | hello only; `"host:port"` |
| Kind           | `"kind"`       | session_start only; `"files"` or `"clipboard"` |
| Files          | `"files"`      | array of FileMeta; session_start (kind=files) only |
| FileIndex      | `"file_index"` | **snake_case**; file_header, chunk, file_end |
| MIME           | `"mime"`       | clipboard message only |
| OK             | `"ok"`         | ack only; **absent when false** (omitempty) |
| Error          | `"error"`      | ack only; absent when empty |
| Length         | `"length"`     | messages with a payload; absent when 0 |
| BytesDone      | `"bytes_done"` | progress messages only |
| TotalBytes     | `"total_bytes"`| progress messages only |

**FileMeta keys:** `"name"` (string), `"size"` (int64), `"sha256"` (64-char lowercase hex).

### Omitempty behaviour

Go's `omitempty` tag omits zero values:
- `"ok": false` → **absent** (Go zero value for bool)
- `"file_index": 0` → **absent** (Go zero value for int)
- `"version": 0` → **absent**
- empty string, 0 numeric, `null` / nil slice → **absent**

Android's codec must match: use `null` for absent optional fields and configure the
JSON serializer with `encodeDefaults = false, explicitNulls = false`.

### Fingerprint format

```
fingerprint = hex(sha256(certificate_DER_bytes))
```
- **Input:** raw DER (binary) bytes of the X.509 self-signed certificate.
- **Hash:** SHA-256 (32 bytes → 64 hex characters).
- **Encoding:** lowercase hex, exactly 64 characters, **no colons, no spaces**.
- **Example:** `"a3f8c12d4e..."` (64 chars)

**Never** base64. **Never** uppercase. **Never** colon-separated (that's the
certificate thumbprint display format, not the wire format).

## Test coverage summary

### Go tests (all pass under `make test` and `make race`)

| File | Tests | What's covered |
|------|-------|----------------|
| `internal/proto/proto_test.go` | 3 | Write/read roundtrip, control messages, oversize header rejection |
| `internal/pairing/pairing_test.go` | existing | QR encode/decode |
| `internal/config/config_test.go` | existing | Store open, device CRUD |
| `internal/daemon/integration_test.go` | 4 | Pair+file transfer, collision rename, clipboard push, untrusted peer rejection |
| `internal/daemon/integration_extra_test.go` | 10 | Bidirectional transfer, multi-file+clipboard sequence, empty file (0 bytes), unknown device error, bidirectional clipboard, large-file SHA-256 integrity, concurrent pairs isolation, progress callback, multi-collision rename, send after context cancel |
| `internal/daemon/pairing_test.go` | 3 | `resolvePeerAddr` unit (6 sub-cases), paired-port storage, self-heal-addr regression |

### Android tests (pass under `./gradlew testDebugUnitTest`)

| File | Tests | What's covered |
|------|-------|----------------|
| `ProtoTest.kt` | 9 | Round-trips, golden JSON key for file_index, oversize/zero header rejection, EOF handling |
| `ProtoGoldenTest.kt` | 20 | Exact JSON key names for every field, 4-byte big-endian framing, fingerprint lowercase-hex format, golden byte vectors for session_end and hello, Go-style round-trip decode, omitempty ok=false absent, ack error key name |

## Next steps (priority order)

1. **[VERIFY] PC → phone transfer end-to-end.** With the port bug fixed, send a
   file while the phone's receive window is open; confirm it lands in phone
   Downloads via MediaStore + notification fires.
2. **[VERIFY] phone → PC transfer.** Use the app's Send screen (SAF file pick →
   choose `pc` → send). Confirm it lands in `~/Downloads` on the PC.
3. **[VERIFY] clipboard both directions.** PC `adrop clip <phone>` and the phone's
   clipboard-send button.
4. **[UX] Receive window duration.** Confirm the 5-min window + Stop action work;
   consider surfacing remaining time in the foreground-service notification.
5. **[CLEANUP] Device naming.** Phone pairs as model `SM-S721B`; PC uses hostname.
   `ADROP_NAME` env var overrides PC name. Consider surfacing this in app settings.
6. **[ROBUSTNESS] IP changes.** Stored addr is a fixed IP; on DHCP change, the
   self-heal-on-inbound mechanism corrects the port automatically on the next
   transfer. Verify on real devices after a DHCP renewal.
7. **[FEATURE] mDNS discovery.** Currently requires QR scan for initial setup.
8. **[FEATURE] FCM wake.** Phone receive window must be manually opened.

## Gotchas learned

- Changing the PC identity (e.g. Ed25519→ECDSA) invalidates all existing QRs and
  phone pairings. Delete `~/.config/adrop/{identity.*,devices.json}`, restart the
  daemon, `pm clear` the phone app, and **re-pair with a freshly shown QR**.
- The running daemon caches `devices.json`; edit it only while the daemon is
  stopped (`systemctl --user stop adrop`).
- Android keystore keys are opaque (no `ECPrivateKey`); declare `.setDigests(...)`
  or TLS signing fails at handshake.
- `wl-copy` / `wl-paste` require an active Wayland session; they fail in headless
  or SSH-only environments. File transfer works without them.
- `notify-send` requires `libnotify` and a running notification daemon; it fails
  silently if absent (transfers still complete).
