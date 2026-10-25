#!/usr/bin/env bash
# Builds the Go core for the Flutter app (app/):
#   linux    app/build/native/linux/libadrop_core.so  (c-shared, dart:ffi)
#   android  app/android/app/libs/core.aar             (gomobile bind)
# Android needs gomobile/gobind on PATH (go install golang.org/x/mobile/cmd/...),
# ANDROID_HOME and an NDK; ANDROID_NDK_HOME defaults to the newest one.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

for target in "${@:-linux}"; do
  case $target in
  linux)
    mkdir -p app/build/native/linux
    CGO_ENABLED=1 go build -trimpath -buildmode=c-shared \
      -o app/build/native/linux/libadrop_core.so ./core/cshared
    rm -f app/build/native/linux/libadrop_core.h
    ;;
  android)
    : "${ANDROID_HOME:?ANDROID_HOME is not set}"
    if [ -z "${ANDROID_NDK_HOME:-}" ]; then
      ANDROID_NDK_HOME=$(ls -d "$ANDROID_HOME"/ndk/* | sort -V | tail -1)
      export ANDROID_NDK_HOME
    fi
    mkdir -p app/android/app/libs
    gomobile bind -trimpath -target="${ADROP_ANDROID_TARGET:-android/arm64}" -androidapi 21 \
      -javapkg com.adrop.proto -o app/android/app/libs/core.aar ./core
    rm -f app/android/app/libs/core-sources.jar
    ;;
  *)
    echo "usage: $0 [linux] [android]" >&2
    exit 2
    ;;
  esac
done
