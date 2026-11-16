#!/bin/sh
set -eu
rm -rf /tmp/musl-arm-source /tmp/musl-arm
cp -a /source/third_party/musl-1.2.6 /tmp/musl-arm-source
cd /tmp/musl-arm-source
CC=arm-linux-gnueabihf-gcc AR=arm-linux-gnueabihf-ar RANLIB=arm-linux-gnueabihf-ranlib \
  CFLAGS='-O2 -march=armv7-a -mfpu=vfpv3-d16 -mfloat-abi=hard' \
  ./configure --target=arm-linux-musleabihf --prefix=/tmp/musl-arm --disable-shared > /tmp/musl-configure.log
if ! make -j4 > /tmp/musl-build.log 2>&1; then tail -60 /tmp/musl-build.log; exit 1; fi
make install > /tmp/musl-install.log
exec /bin/sh /source/tools/link-awg-musl.sh
