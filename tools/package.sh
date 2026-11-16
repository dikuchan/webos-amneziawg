#!/bin/sh
set -eu
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
# The controller migrates the previous app ID after its VPN has been stopped.
APP_ID=com.github.dikuchan.amneziawg
APP="$ROOT/app/$APP_ID"
PACKAGER=${ARES_PACKAGE:-ares-package}
TSC=${TSC:-tsc}
cd "$ROOT"
VERSION=$(node -p "require('./package.json').version")
APP_VERSION=$(node -p "require('./app/com.github.dikuchan.amneziawg/appinfo.json').version")
if [ "$VERSION" != "$APP_VERSION" ]; then
  echo 'Package and app versions differ' >&2
  exit 1
fi
"$TSC" -p tsconfig.json
test -s "$APP/fonts/Inter-Regular.woff2"
test -s "$APP/fonts/Inter-Medium.woff2"
test -s "$APP/fonts/JetBrainsMono-Regular.woff2"
test -x kit/bin/awg
test -x kit/bin/amneziawg-go
test -x kit/bin/amneziawgctl
mkdir -p "$APP/payload" "$APP/licenses" dist
cp -R kit/bin kit/scripts "$APP/payload/"
cp LICENSE "$APP/licenses/webos-amneziawg-MIT.txt"
cp third_party/amneziawg-go/LICENSE "$APP/licenses/amneziawg-go-MIT.txt"
cp third_party/amneziawg-tools/COPYING "$APP/licenses/amneziawg-tools-GPL-2.0.txt"
cp third_party/musl-1.2.6/COPYRIGHT "$APP/licenses/musl-COPYRIGHT.txt"
cp licenses/* "$APP/licenses/"
cp README.md "$APP/"
cp -R "$APP/licenses" kit/
cp README.md kit/
"$PACKAGER" --outdir dist "$APP"
IPK="webos-amneziawg_${VERSION}_all.ipk"
ARCHIVE="webos-amneziawg-${VERSION}-armv7.tar.gz"
mv "dist/${APP_ID}_${VERSION}_all.ipk" "dist/$IPK"
tar czf "dist/$ARCHIVE" -C kit bin scripts licenses README.md
(
  cd dist
  shasum -a 256 "$IPK" "$ARCHIVE" > SHA256SUMS
)
