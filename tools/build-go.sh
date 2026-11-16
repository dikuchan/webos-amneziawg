#!/bin/sh
set -eu
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
GO_BIN=${GO_BIN:-go}
mkdir -p "$ROOT/kit/bin" "$ROOT/test-bin"
cd "$ROOT"
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 "$GO_BIN" build -trimpath -ldflags='-s -w' -o kit/bin/awg-test ./controller
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 "$GO_BIN" build -trimpath -ldflags='-s -w' -o test-bin/awg-test ./controller
cd "$ROOT/third_party/amneziawg-go"
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 "$GO_BIN" build -buildvcs=false -trimpath -ldflags='-s -w' -o "$ROOT/kit/bin/amneziawg-go" .
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 "$GO_BIN" build -buildvcs=false -trimpath -ldflags='-s -w' -o "$ROOT/test-bin/amneziawg-go" .
