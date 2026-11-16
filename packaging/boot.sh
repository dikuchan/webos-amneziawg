#!/bin/sh
set -eu
SCRIPT=$(readlink -f "$0")
KIT=$(CDPATH='' cd -- "$(dirname -- "$SCRIPT")/.." && pwd)
# HBC run-parts must return promptly so boot/failsafe handling is not blocked.
(
  unset LD_PRELOAD
  exec "$KIT/bin/amneziawgctl" boot
) </dev/null >/dev/null 2>&1 &
