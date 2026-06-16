# Orchestrator prompt for the next adrop session

Paste the block below into a fresh Claude Code session to coordinate parallel
work on adrop. It assumes the repo at `/home/shafed/tmp`.

---

You are orchestrating work on "adrop", an AirDrop-like file/clipboard transfer
app between an Arch Linux PC (Go daemon+CLI) and an Android phone (Kotlin/Compose).
Read /home/shafed/tmp/HANDOFF.md FIRST — it has full status, fixed bugs, the open
port bug, and the prioritized next-steps list. Also read the project memories
(adrop-project, adrop-tls-interop, adrop-phone-port-bug).

CURRENT STATE (verified): phone↔PC PAIRING WORKS over mutual TLS. PC identity is
ECDSA P-256 (must NOT be Ed25519 — Android BoringSSL rejects ed25519 sigalgs).
Open bug: PC's tryCompletePairing in internal/daemon/pairing.go falls back to the
PC's own port (53127) when the phone's advertised addr is empty, storing the wrong
phone port (should be 7777). A manual sed workaround was applied to devices.json
but the code is unfixed.

CONSTRAINTS:
- PC Go code (cmd/adrop, internal/*) CAN be built+tested here: `make build`,
  `make test`, `make race`. Keep all existing tests green.
- Android (android/) CAN be compiled here with:
  JAVA_HOME=/opt/android-studio/jbr ANDROID_HOME=/home/shafed/Android/Sdk
  ANDROID_SDK_ROOT=/home/shafed/Android/Sdk ./gradlew assembleDebug --no-daemon
  but CANNOT be run on a device by you — a human installs/tests via adb. Treat
  device behavior as unverified until the human confirms.
- THE WIRE PROTOCOL IS THE INTEROP CONTRACT. internal/proto (Go) and
  android/.../data/proto/Proto.kt MUST stay byte-compatible (same JSON keys,
  same 4-byte BE framing, same cert-fingerprint = lowercase hex SHA-256 of DER).
  Any agent touching the protocol on one side MUST mirror the other and keep the
  proto round-trip tests passing on both.

SUBAGENTS — you MAY use the voltagent specialized subagents for these tracks
(they were used successfully already on this project). Spawn agents ONLY if the
user asks for parallel/subagent work; otherwise do the tracks inline. Recommended
mapping:
- Go daemon / CLI / transport work  -> voltagent-lang:golang-pro
- Android Kotlin/Compose work        -> voltagent-core-dev:mobile-developer
                                        (or voltagent-lang:kotlin-specialist for
                                         pure-Kotlin/coroutine-heavy changes)
- TLS/pinning/keystore security work -> voltagent-infra:security-engineer
- Tests / CI / build hardening       -> voltagent-qa-sec:test-automator
- Docs                               -> voltagent-dev-exp:documentation-engineer
When you spawn a voltagent subagent it starts COLD: give it the exact files,
the build/test command for its side, the interop contract rules above, and tell
it to run its build+tests before reporting and to be honest about what needs
human device testing. To continue a still-running agent with its context intact,
use SendMessage with its agent id rather than spawning a fresh one.

HOW TO PARALLELIZE (these tracks have independent files — safe to run
concurrently; spawn each as its own agent only if the user asks for parallel
agents):

  Track A — FIX the phone-port bug (Go-only, highest priority, BLOCKS sending).
    Agent: voltagent-lang:golang-pro.
    Files: internal/daemon/pairing.go (tryCompletePairing), internal/daemon/receive.go
    (UpdateAddr on inbound Hello), maybe internal/daemon/*_test.go. When advertised
    host is "0.0.0.0"/unspecified, substitute the connection's source IP but KEEP
    the advertised PORT; and UpdateAddr from hello.Addr on every inbound connect so
    it self-heals. Add a regression test. Do NOT touch Android.

  Track B — Phase-2 feature: per-file progress + larger-file UX (protocol-adjacent).
    Agent: ONE agent owning BOTH sides (golang-pro + mobile-developer work, or
    drive it yourself) because it edits the proto contract.
    Touches internal/proto (add optional progress/ack fields, backward compatible),
    internal/daemon/send.go+receive.go, AND the Android session code. MUST coordinate
    the proto change on both sides in one agent (do not split A-side/B-side proto
    edits across agents). Lower priority; only if user wants it.

  Track C — Android UX polish (Android-only, no protocol changes).
    Agent: voltagent-core-dev:mobile-developer.
    Files: android/.../ui/*, feature/*/...Screen.kt, feature/receive/* (receive-window
    countdown display, transfer progress UI, error toasts). Must keep compiling.
    Independent of Tracks A/B as long as it doesn't edit data/proto or net/*.

  Track D — Docs/tests hardening (no source-behavior changes).
    Agent: voltagent-qa-sec:test-automator (tests) and/or
    voltagent-dev-exp:documentation-engineer (docs).
    Expand README/HANDOFF, add Go integration test for the full pair→send→receive
    loop, add JVM proto golden tests. Safe alongside everything.

CONFLICT RULES for parallel agents:
- Only ONE agent may edit internal/proto/* and android/.../data/proto/* (the proto
  contract). If two tracks both need a proto change, serialize them or give both
  to one agent.
- Track A (Go daemon pairing) and Track C (Android UI) do not overlap — safe in
  parallel. Give each its own git worktree (isolation: "worktree") if you want
  fully independent checkouts.
- Every agent runs the relevant build/test before reporting:
  Go agents: `make build && make test`. Android agents: the gradlew assembleDebug
  command above. Report honestly what compiled vs. what needs human device testing.

AFTER each track lands, the human must verify on devices (you cannot): re-pair if
identity changed, open the phone's "Open to receive" window, run
`adrop send <phone-name> <file>`, and check Downloads + notification. Tell the
human exactly which adb/CLI commands to run to verify each delivered track.

Start by reading HANDOFF.md, confirm whether Track A is still needed (check
`grep addr ~/.config/adrop/devices.json` and the tryCompletePairing fallback),
then propose which tracks to run, which voltagent subagent each gets, and in what
grouping — get the user's OK before spawning anything.
