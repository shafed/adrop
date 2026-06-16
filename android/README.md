# adrop Android Client

Native Kotlin + Jetpack Compose Android client for adrop — an AirDrop-like P2P
file and clipboard transfer app that interoperates with the Go PC daemon.

## Project layout

```
android/
├── app/
│   └── src/main/kotlin/com/adrop/
│       ├── AdropApplication.kt              # App class, creates NotificationChannels
│       ├── data/
│       │   ├── proto/Proto.kt               # Wire-protocol codec (pure Kotlin/JVM)
│       │   ├── identity/Identity.kt         # TLS identity: EC P-256 cert via AndroidKeyStore
│       │   └── trust/TrustStore.kt          # Room DB: TrustedDevice, DAO, repository
│       ├── net/
│       │   ├── tls/PinnedTlsContext.kt      # SSLContext + PinningTrustManager + DeviceKeyManager
│       │   ├── transport/Transport.kt       # dial() + listen() + peerFingerprint()
│       │   └── session/Session.kt           # sendFiles, sendClipboard, receiveSession state machines
│       ├── feature/
│       │   ├── pair/
│       │   │   ├── PairDecoder.kt           # QR URI parser + anti-tamper validation
│       │   │   ├── PairViewModel.kt         # pairing flow: decode -> store -> back-connect Hello
│       │   │   └── ScanScreen.kt            # CameraX + ML Kit QR scanning UI
│       │   ├── devices/
│       │   │   ├── DevicesViewModel.kt      # observe + revoke trusted devices
│       │   │   └── DevicesScreen.kt         # Compose UI: list paired devices with revoke
│       │   ├── send/
│       │   │   ├── SendViewModel.kt         # SAF file picking, send files/clipboard
│       │   │   └── SendScreen.kt            # Compose UI: pick device, pick files, send clipboard
│       │   └── receive/
│       │       ├── ReceiveForegroundService.kt  # Bounded 5-min TLS listener foreground service
│       │       └── BootReceiver.kt          # Placeholder for future FCM wake
│       └── ui/
│           ├── MainActivity.kt              # Single-activity host + NavGraph
│           ├── screens/HomeScreen.kt        # Receive toggle + navigation buttons
│           └── theme/Theme.kt               # Material 3 + dynamic color theme
├── jvm-tests/                               # Gradle module: pure-JVM codec tests
│   └── src/
│       ├── main/kotlin/com/adrop/proto/Proto.kt    # Same codec, no Android deps
│       └── test/kotlin/com/adrop/proto/ProtoTest.kt # 24 JUnit 4 tests
└── jvm-tests-mvn/                           # Maven project: same tests, ran with JDK 26
    ├── pom.xml
    └── src/ (mirrors jvm-tests)
```

## Protocol interop

Transport: TLS 1.3, mutual authentication, certificate pinning.

Neither side trusts a CA. Trust is established once at pairing time by pinning
the SHA-256 of the peer's leaf certificate DER bytes (lowercase hex).

### Wire framing (exact match with Go daemon)

```
[4-byte big-endian uint32: JSON header length]
[JSON header bytes]
[raw payload bytes  — exactly header.length bytes, 0 for control messages]
```

### JSON header keys (Go json tags, verbatim)

| Field       | JSON key     | Go type   | Notes                          |
|-------------|--------------|-----------|--------------------------------|
| type        | `type`       | string    | always present                 |
| version     | `version`    | int       | omitempty                      |
| fingerprint | `fingerprint`| string    | omitempty                      |
| name        | `name`       | string    | omitempty                      |
| addr        | `addr`       | string    | omitempty                      |
| kind        | `kind`       | string    | omitempty                      |
| files       | `files`      | []FileMeta| omitempty                      |
| file_index  | `file_index` | int       | omitempty — **snake_case**     |
| mime        | `mime`       | string    | omitempty                      |
| ok          | `ok`         | bool      | omitempty (false = absent)     |
| error       | `error`      | string    | omitempty                      |
| length      | `length`     | int64     | omitempty                      |

`file_index` uses snake_case exactly as Go's json tag.
All optional fields use `omitempty` semantics: zero/false/empty are omitted.

### Session flow (initiator sends first)

```
-> Hello {type:"hello", version:1, fingerprint, name, addr}
<- Hello {type:"hello", ...}
-> SessionStart {type:"session_start", kind:"files", files:[{name,size,sha256},...]}
   per file i:
   -> {type:"file_header", file_index:i}
   -> {type:"chunk", file_index:i, length:N}  +  N payload bytes  (repeat)
   -> {type:"file_end", file_index:i}
   <- {type:"ack", file_index:i, ok:true}
-> {type:"session_end"}
<- {type:"ack", ok:true}

Clipboard:
-> SessionStart {type:"session_start", kind:"clipboard"}
-> {type:"clipboard", mime:"text/plain", length:N}  +  N bytes
-> {type:"session_end"}
<- {type:"ack", ok:true}
```

### Certificate / fingerprint

- Android generates EC P-256 (secp256r1) self-signed cert via AndroidKeyStore.
- Go daemon uses Ed25519. Both compute: `sha256(cert.DER)` as lowercase hex.
- The algorithm does not need to match — only the fingerprint computation does.

### Pairing QR format

```
adrop://pair?d=<base64url-no-padding>
```
Base64url decodes to JSON:
```json
{"v":1,"n":"PC name","fp":"<hex-sha256>","cert":"<PEM>","addr":"host:port"}
```
Anti-tamper: parse PEM -> extract DER -> compute sha256 -> assert equals "fp".
Then store device, back-connect to addr, send Hello.

## Building in Android Studio

Prerequisites:
- Android Studio Meerkat or newer (Flamingo+ also works)
- JDK 17 (via Android Studio's embedded JDK)
- Android SDK 35 (install via SDK Manager)

Steps:
1. Open `/path/to/adrop/android/` in Android Studio.
2. Let Gradle sync complete.
3. Connect a physical device (Android 8+, API 26+) or start an emulator.
4. Run `app` configuration.

To build an APK from the command line (requires Android SDK in `$ANDROID_HOME`):
```bash
./gradlew assembleDebug
# APK: app/build/outputs/apk/debug/app-debug.apk
```

## Running JVM unit tests (no Android SDK needed)

The `jvm-tests-mvn` module contains all wire-protocol codec tests as pure JVM
(no Android dependencies). Run them with Maven:

```bash
# Requires JDK 17+ and Maven 3.9+
cd android/jvm-tests-mvn
mvn test
# All 24 tests should pass.
```

Alternatively, if Gradle can resolve dependencies (needs network + JDK 17):
```bash
./gradlew :jvm-tests:test
```
Note: On JDK 26 the Kotlin DSL Gradle scripts fail to compile because the
embedded Kotlin compiler in Gradle 8.x does not support JDK 26 yet. Use
Maven or a JDK 17/21 environment for Gradle.

## What was verified vs unverified

### Verified (ran on JDK 26, no Android SDK)

- Wire-protocol codec (Proto.kt): all 24 JUnit 4 tests pass.
  - Round-trip: Hello, SessionStart + manifest, Chunk + payload, Ack, Clipboard.
  - Golden JSON key tests: all field names match Go's json tags exactly
    (`file_index` snake_case, `ok` omitempty, all types lowercase).
  - Framing: big-endian 4-byte prefix, zero-length rejection, overflow rejection.
  - Full session simulation: multi-chunk file transfer + clipboard.
- Pairing QR decoder (PairDecoder.kt): pure Kotlin logic, correct base64url
  decoding and fingerprint anti-tamper check — compilation only, not executed.

### Unverified (requires a real Android build)

- AndroidKeyStore-backed identity generation.
- PinningTrustManager / SSLContext with mutual TLS 1.3.
- SSLServerSocket listener in ReceiveForegroundService.
- MediaStore file saving to shared Downloads.
- CameraX + ML Kit QR scanning.
- Jetpack Compose UI rendering.
- Room database migration.
- Foreground service lifecycle (start, countdown timer, stop).
- Actual TLS interop with the Go daemon on a LAN.

## Known platform notes

- `sun.security.x509.*` import in Identity.kt is replaced at runtime by Android's
  Conscrypt/BouncyCastle — the KeyGenParameterSpec path in `generateAndStore()`
  is the actual used path; the sun.security import is vestigial and would be
  removed in a final build.
- The `@androidx.camera.core.ExperimentalGetImage` annotation in ScanScreen.kt
  is required for `imageProxy.image` until the API stabilizes.
