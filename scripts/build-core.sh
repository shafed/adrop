#!/usr/bin/env bash
# Builds the Go core (core/) for the Android app as
# app/android/app/libs/core.aar with gomobile bind. Needs gomobile and gobind
# on PATH (go install golang.org/x/mobile/cmd/gomobile@latest and .../gobind),
# ANDROID_HOME and an NDK; ANDROID_NDK_HOME defaults to the newest one.
# ADROP_ANDROID_TARGET picks the ABIs (default android/arm64; add
# android/amd64 for the emulator).
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

: "${ANDROID_HOME:?ANDROID_HOME is not set}"
if [ -z "${ANDROID_NDK_HOME:-}" ]; then
  ANDROID_NDK_HOME=$(ls -d "$ANDROID_HOME"/ndk/* | sort -V | tail -1)
  export ANDROID_NDK_HOME
fi
mkdir -p app/android/app/libs
# pion/ice reads network interfaces through github.com/wlynxg/anet (Android
# 11+ hides them from net.Interfaces); anet reaches into the net package by
# linkname, which Go refuses without -checklinkname=0.
gomobile bind -trimpath -ldflags=-checklinkname=0 -target="${ADROP_ANDROID_TARGET:-android/arm64}" -androidapi 29 \
  -javapkg com.adrop -o app/android/app/libs/core.aar ./core
rm -f app/android/app/libs/core-sources.jar
