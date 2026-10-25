# app — the adrop window and phone app

One Flutter app. On Linux it is the window, a client of the daemon over its
Unix socket (`lib/backend/ipc_backend.dart`), like the CLI. On Android it
talks to the Go core (`core/`) running in `AdropService`
(`lib/backend/channel_backend.dart`, Kotlin in `android/app/src/main/kotlin`).

```sh
make app-linux       # from the repository root; make app-install installs it
make app-android     # builds the core (scripts/build-core.sh), then the APK
```

`ADROP_ANDROID_TARGET=android/arm64,android/amd64` adds the emulator's ABI to
the core.
