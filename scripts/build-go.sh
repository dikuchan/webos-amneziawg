#!/bin/sh
set -eu
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
GO_BIN=${GO_BIN:-go}
BUN=${BUN:-bun}
cd "$ROOT"
"$BUN" run build:ui
VERSION=$(cat VERSION)
DAEMON_VERSION=v3.1.20261116
SOURCE="$ROOT/.build/src/amneziawg-go"
mkdir -p .build/src
rm -rf "$SOURCE"
cp -R dependencies/amneziawg-go "$SOURCE"
rm -rf "$SOURCE/.git"
printf 'package main\n\nconst Version = "%s"\n' "$DAEMON_VERSION" >"$SOURCE/version.go"
for arch in armv7 arm64; do
  case "$arch" in
  armv7)
    GOARCH=arm
    GOARM=7
    ;;
  arm64)
    GOARCH=arm64
    GOARM=
    ;;
  esac
  export GOARCH GOARM
  OUTPUT="$ROOT/.build/$arch/bin"
  mkdir -p "$OUTPUT"
  GOOS=linux CGO_ENABLED=0 "$GO_BIN" build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o "$OUTPUT/amneziawgctl" .
  (
    cd "$SOURCE"
    GOOS=linux CGO_ENABLED=0 "$GO_BIN" build -buildvcs=false -trimpath -ldflags='-s -w' -o "$OUTPUT/amneziawg-go" .
  )
done
