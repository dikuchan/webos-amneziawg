#!/bin/sh
set -eu
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
VERSION=0.11.0
case "$(uname -s):$(uname -m)" in
Darwin:arm64)
  PLATFORM=darwin.aarch64
  SHA256=339b930feb1ea764467013cc1f72d09cd6b869ebf1013296ba9055ab2ffbd26f
  ;;
Darwin:x86_64)
  PLATFORM=darwin.x86_64
  SHA256=c2c15e08df0e8fbc374c335b230a7ee958c313fa5714817a59aa59f1aa594f51
  ;;
Linux:aarch64)
  PLATFORM=linux.aarch64
  SHA256=68a8133197a50beb8803f8d42f9908d1af1c5540d4bb05fdfca8c1fa47decefc
  ;;
Linux:x86_64)
  PLATFORM=linux.x86_64
  SHA256=b7af85e41cc99489dcc21d66c6d5f3685138f06d34651e6d34b42ec6d54fe6f6
  ;;
*)
  echo 'Unsupported ShellCheck host platform' >&2
  exit 1
  ;;
esac
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT HUP INT TERM
ARCHIVE="$WORK/shellcheck.tar.gz"
curl --fail --location --silent --show-error --max-time 120 "https://github.com/koalaman/shellcheck/releases/download/v$VERSION/shellcheck-v$VERSION.$PLATFORM.tar.gz" -o "$ARCHIVE"
printf '%s  %s\n' "$SHA256" "$ARCHIVE" >"$WORK/checksum"
shasum -a 256 -c "$WORK/checksum"
tar xzf "$ARCHIVE" -C "$WORK"
mkdir -p "$ROOT/.build/tools"
cp "$WORK/shellcheck-v$VERSION/shellcheck" "$ROOT/.build/tools/shellcheck"
