# app — Flutter prototype (PLAN.md, step 1)

One screen over the Go core (`core/`): pair with a PC daemon by its
`adrop://pair?d=…` URI (`adrop pair show`), then **Hello**. The daemon logs
`hello from <name> (<fingerprint>)`.

The core is not built by Flutter; build it first:

```sh
bash scripts/build-core.sh linux     # app/build/native/linux/libadrop_core.so
bash scripts/build-core.sh android   # app/android/app/libs/core.aar (gomobile)
cd app && flutter build linux        # or: flutter build apk
```

`ADROP_ANDROID_TARGET=android/arm64,android/amd64` adds the emulator's ABI.
On Android it installs as `com.adrop.proto`, next to the current `com.adrop`.
On Linux its identity lives in `$XDG_DATA_HOME/adrop-proto`, separate from
the daemon's.
