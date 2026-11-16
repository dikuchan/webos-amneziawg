#!/bin/sh
set -eu
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
DOCKER=${DOCKER:-docker}
IMAGE=${AWG_BUILDER_IMAGE:-webos-amneziawg-builder}
cd "$ROOT"
mkdir -p .build
if ! "$DOCKER" build --platform linux/arm64 -t "$IMAGE" -f tests/Dockerfile . >.build/builder.log 2>&1; then
  tail -60 .build/builder.log
  exit 1
fi
CONTAINER=$("$DOCKER" create --platform linux/arm64 "$IMAGE" sh -ec '
  sh /source/scripts/build-musl-awg.sh
  cd /source/dependencies/amneziawg-tools/src
  make -j2 CC=gcc CFLAGS="-O2 -Iuapi/linux -std=gnu99 -D_GNU_SOURCE -Wall -Wextra -Werror -DRUNSTATEDIR=\\\"/var/run\\\"" LDFLAGS="-Wl,--fatal-warnings"
  mkdir -p /source/.build/arm64/bin
  cp wg /source/.build/arm64/bin/awg
')
trap '"$DOCKER" rm -f "$CONTAINER" >/dev/null' EXIT HUP INT TERM
if ! "$DOCKER" start -a "$CONTAINER" >.build/awg-build.log 2>&1; then
  tail -60 .build/awg-build.log
  exit 1
fi
for arch in armv7 arm64; do
  mkdir -p ".build/$arch/bin"
  "$DOCKER" cp "$CONTAINER:/source/.build/$arch/bin/awg" ".build/$arch/bin/awg"
done
