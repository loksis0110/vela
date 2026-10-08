#!/usr/bin/env bash
# Builds the sing-box core + Vela shared logic for Android and iOS with gomobile.
#   ./scripts/build-mobile-libs.sh android   -> android/app/libs/libvela.aar
#   ./scripts/build-mobile-libs.sh ios       -> ios/Frameworks/{Libbox,Velacore}.xcframework
# Requirements: Go 1.25+, for Android: ANDROID_HOME + ANDROID_NDK_HOME (NDK r28), JDK 17;
# for iOS: macOS with Xcode.
set -euo pipefail
cd "$(dirname "$0")/.."

GOMOBILE_VERSION=v0.1.13
TAGS="with_gvisor,with_quic,with_utls,with_clash_api,badlinkname,tfogo_checklinkname0"
LDFLAGS="-s -w -buildid= -checklinkname=0"

./scripts/prepare-core.sh
go mod tidy
go install "github.com/sagernet/gomobile/cmd/gomobile@${GOMOBILE_VERSION}"
go install "github.com/sagernet/gomobile/cmd/gobind@${GOMOBILE_VERSION}"
export PATH="$PATH:$(go env GOPATH)/bin"

case "${1:-}" in
  android)
    mkdir -p android/app/libs
    gomobile bind -trimpath \
      -target android/arm64,android/arm \
      -androidapi 24 -javapkg=app.vela -libname=box \
      -tags "$TAGS" -ldflags "$LDFLAGS" \
      -o android/app/libs/libvela.aar \
      github.com/sagernet/sing-box/experimental/libbox \
      github.com/velaapp/vela/mobile
    ;;
  ios)
    mkdir -p ios/Frameworks
    # Network Extension: the core (memory-limited build)
    gomobile bind -trimpath -target ios,iossimulator -iosversion=16.0 -libname=box \
      -tags "$TAGS,with_low_memory" -ldflags "$LDFLAGS" \
      -o ios/Frameworks/Libbox.xcframework \
      github.com/sagernet/sing-box/experimental/libbox
    # App: link parsing and config generation only (small)
    gomobile bind -trimpath -target ios,iossimulator -iosversion=16.0 -libname=velacore \
      -tags "$TAGS" -ldflags "$LDFLAGS" \
      -o ios/Frameworks/Velacore.xcframework \
      github.com/velaapp/vela/mobile
    ;;
  *)
    echo "usage: $0 android|ios" >&2
    exit 1
    ;;
esac
