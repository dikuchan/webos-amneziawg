#!/bin/sh
set -eu
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"
if [ -n "$(git status --porcelain --ignore-submodules=none)" ]; then
  echo 'Commit source changes before creating a release source archive.' >&2
  exit 1
fi
VERSION=$(cat VERSION)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT HUP INT TERM
NAME="webos-amneziawg-$VERSION"
mkdir -p "$WORK/$NAME"
git archive HEAD >"$WORK/root.tar"
tar xf "$WORK/root.tar" -C "$WORK/$NAME"
for name in amneziawg-go amneziawg-tools musl; do
  target="$WORK/$NAME/dependencies/$name"
  mkdir -p "$target"
  # Upstream export-ignore rules omit files from git archive; retain the full pin.
  git -C "dependencies/$name" ls-files -z >"$WORK/dependency-files"
  tar cf "$WORK/dependency.tar" -C "dependencies/$name" --null -T "$WORK/dependency-files"
  tar xf "$WORK/dependency.tar" -C "$target"
done
mkdir -p dist
ARCHIVE="webos-amneziawg-$VERSION-source.tar.gz"
tar czf "dist/$ARCHIVE" -C "$WORK" "$NAME"
(
  cd dist
  for name in "webos-amneziawg_${VERSION}_all.ipk" "webos-amneziawg-$VERSION-armv7.tar.gz" "$ARCHIVE"; do
    if [ -f "$name" ]; then shasum -a 256 "$name"; fi
  done >SHA256SUMS
)
