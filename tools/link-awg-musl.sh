#!/bin/sh
set -eu
# Only Linux UAPI headers are copied, never glibc headers or runtime libraries.
for headers in linux asm asm-generic; do
  cp -a "/usr/arm-linux-gnueabihf/include/$headers" /tmp/musl-arm/include/
done
rm -rf /tmp/awg-tools-musl
cp -a /source/third_party/amneziawg-tools /tmp/awg-tools-musl
cd /tmp/awg-tools-musl/src
common='-O2 -Iuapi/linux -std=gnu99 -D_GNU_SOURCE -Wall -Wextra -Werror -DRUNSTATEDIR=\"/var/run\" -DWIREGUARD_TOOLS_VERSION=\"3.1.20260812\" -march=armv7-a -mfpu=vfpv3-d16 -mfloat-abi=hard'
make -j2 CC=/tmp/musl-arm/bin/musl-gcc CFLAGS="$common" LDFLAGS='-static -Wl,--fatal-warnings'
cp wg /source/kit/bin/awg
arm-linux-gnueabihf-strip /source/kit/bin/awg
file /source/kit/bin/awg
