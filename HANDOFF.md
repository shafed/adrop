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

## Bugs found & fixed during bring-up

1. **Missing `gradle.properties`** → added `android.useAndroidX=true` (build failed
   on every AndroidX dep without it).
2. **`Icons.Default.ArrowBack`** in 3 Compose screens → `Icons.AutoMirrored.Filled.ArrowBack`.
3. **Missing `@OptIn(ExperimentalMaterial3Api::class)`** on 4 screens using `TopAppBar`.
4. **`endpointAlgorithmIdentifier`** typo in `PinnedTlsContext.kt` → `endpointIdentificationAlgorithm = null`.
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

## OPEN BUG (in progress) — wrong phone port stored on PC

After pairing, the PC stored the phone as:
```
"addr": "192.168.0.112:53127"   ← WRONG. Phone listens on 7777, not 53127.
```
So `adrop send SM-S721B ...` fails: `dial 192.168.0.112:53127: connection refused`.

- The phone listens on **7777** (`ReceiveForegroundService.LISTEN_PORT = 7777`,
  and `PairViewModel.DEFAULT_LISTEN_PORT = 7777`).
- `53127` is the **PC daemon's** own port. The Go side stored it via the
  *empty-advertised-addr fallback* in `internal/daemon/pairing.go`
  `tryCompletePairing` (lines ~139-145: `if addr == "" { ... d.port }`).
- That means the phone's pairing Hello arrived with an **empty `addr`** field
  (or it wasn't parsed), even though `PairViewModel` sets
  `addr = "${localLanIp()}:7777"` and `localLanIp()` never returns empty
  (falls back to `"0.0.0.0"`). **Root cause not yet confirmed** — the `addr`
  field is somehow not reaching / not being honored by the Go side.

### Manual workaround given to the user (to unblock sending now)
```sh
systemctl --user stop adrop
sed -i 's/:53127"/:7777"/' ~/.config/adrop/devices.json
systemctl --user start adrop
# then on phone: tap "Open to receive", then:
adrop send SM-S721B /path/to/file
```
(Must stop the daemon first — it caches devices in memory and rewrites the file.)

## Next steps (priority order)

1. **[BUG] Fix the stored-port root cause.** Confirm whether the phone's pairing
   Hello actually carries `addr=ip:7777`. Capture it: add a temporary log in the
   Go daemon `handlePeer`/`tryCompletePairing` to print `hello.Addr`, re-pair,
   read `journalctl --user -u adrop`. Likely fixes:
   - If `addr` IS arriving: honor it (Go already does — check parsing/`0.0.0.0`).
   - If `0.0.0.0:7777` is arriving: the Go side should substitute the connection's
     **source IP** for `0.0.0.0` but keep the advertised **port** (7777). Right now
     it stores `0.0.0.0:7777` verbatim or falls back entirely. Patch
     `tryCompletePairing` to merge `remote` host + advertised port when the
     advertised host is unspecified/`0.0.0.0`.
   - Also make the phone **re-advertise its real `addr` on every connect** (Hello
     already carries `addr`), and have the PC `UpdateAddr` from each inbound
     connection's `hello.Addr` so the port self-heals.
2. **[VERIFY] PC → phone transfer.** With the port fixed, send a file while the
   phone's receive window is open; confirm it lands in phone Downloads via
   MediaStore + notification fires. NOT yet verified end-to-end.
3. **[VERIFY] phone → PC transfer.** Use the app's Send screen (SAF file pick →
   choose `pc` → send). Confirm it lands in `~/Downloads` on the PC.
4. **[VERIFY] clipboard both directions.** PC `adrop clip <phone>` and the phone's
   clipboard-send button.
5. **[UX] Receive window duration.** Confirm the 5-min window + Stop action work;
   consider surfacing remaining time.
6. **[CLEANUP] Device naming.** Phone pairs as model `SM-S721B`; PC uses hostname.
   `ADROP_NAME` overrides PC name. Fine, but document it.
7. **[ROBUSTNESS] IP changes.** Stored addr is a fixed IP; on DHCP change, re-pair
   or self-heal via Hello `addr` (ties into step 1).

## Build & run quick reference

PC (Go):
```sh
cd /home/shafed/tmp && make build && make install
systemctl --user enable --now adrop
adrop pair show          # show QR
adrop devices            # list paired
adrop send <name> file…  # send (phone must have receive window open)
```

Android (needs the SDK env vars):
```sh
cd /home/shafed/tmp/android
JAVA_HOME=/opt/android-studio/jbr ANDROID_HOME=/home/shafed/Android/Sdk \
  ANDROID_SDK_ROOT=/home/shafed/Android/Sdk ./gradlew assembleDebug --no-daemon
# APK: app/build/outputs/apk/debug/app-debug.apk  (package com.adrop.debug)
/home/shafed/Android/Sdk/platform-tools/adb install -r app/build/outputs/apk/debug/app-debug.apk
/home/shafed/Android/Sdk/platform-tools/adb shell am start -n com.adrop.debug/com.adrop.ui.MainActivity
# logcat for crashes:
/home/shafed/Android/Sdk/platform-tools/adb logcat -d AndroidRuntime:E "*:S" | tail -50
# clear app data (forces fresh keystore identity — needed after identity changes):
/home/shafed/Android/Sdk/platform-tools/adb shell pm clear com.adrop.debug
```

### Gotchas learned
- Changing the PC identity (e.g. Ed25519→ECDSA) invalidates all existing QRs and
  phone pairings. Delete `~/.config/adrop/{identity.*,devices.json}`, restart the
  daemon, `pm clear` the phone app, and **re-pair with a freshly shown QR**.
- The running daemon caches `devices.json`; edit it only while the daemon is
  stopped.
- Android keystore keys are opaque (no `ECPrivateKey`); declare `.setDigests(...)`
  or TLS signing fails at handshake.
