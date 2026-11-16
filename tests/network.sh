#!/bin/sh
# Client half: a real AWG server is in a separate container on an internal bridge.
# No mount calls, SYS_ADMIN, AppArmor overrides, host mounts or external network.
set -eu
ROOT=/source
mkdir -p /tmp/amneziawg-kit/bin /tmp/amneziawg-kit/scripts
cp "$ROOT"/.build/arm64/bin/* /tmp/amneziawg-kit/bin/
cp "$ROOT"/packaging/dns.sh /tmp/amneziawg-kit/scripts/
CTL=/tmp/amneziawg-kit/bin/amneziawgctl
ip route add default via 172.31.222.1 dev eth0 2>/dev/null || true
cp /etc/resolv.conf /tmp/resolv.expected
ip route show default >/tmp/default.expected
ip route add 198.51.100.77/32 dev eth0 metric 44
"$CTL" import /tmp/client.conf

# Arrange: another application's ordinary WG interface.
ip link add wg0 type dummy
# Act / Assert: starting the test refuses without modifying WG, routes or DNS.
if "$CTL" start >/tmp/result 2>&1; then
  echo 'WG guard failed'
  exit 1
fi
ip link show wg0 >/dev/null
cmp /etc/resolv.conf /tmp/resolv.expected
ip route show default >/tmp/default.actual
cmp /tmp/default.actual /tmp/default.expected
ip link del wg0
echo 'PASS: active WG is left alone'

# Act: actual AWG 3.1 settings, daemon, control socket and encrypted IP packets.
"$CTL" start
# Assert.
ping -c 2 -W 2 10.203.7.1
"$CTL" status >/tmp/result
grep -q 'state=connected' /tmp/result
grep -q 'mtu=1360' /tmp/result
cmp /etc/resolv.conf /tmp/resolv.expected
echo 'PASS: AWG 3.1 handshake, encrypted traffic and provider MTU'

# Act: import must not replace a live config.
if "$CTL" import /tmp/client.conf >/tmp/result 2>&1; then
  echo 'Live config replaced'
  exit 1
fi
# Assert: data still passes.
ping -c 1 -W 2 10.203.7.1
echo 'PASS: active config cannot be overwritten'

# Act: Stop is idempotent.
"$CTL" stop
"$CTL" stop
# Assert: original/default and foreign routes stay, test interface is gone.
cmp /etc/resolv.conf /tmp/resolv.expected
ip route show default >/tmp/default.actual
cmp /tmp/default.actual /tmp/default.expected
ip route show 198.51.100.77/32 | grep -q 'metric 44'
if ip link show awg0 >/dev/null 2>&1; then
  echo 'Test interface remained'
  exit 1
fi
echo 'PASS: Stop leaves default/foreign routes and DNS unchanged'

# Arrange: the interface name was reused after Stop.
ip link add awg0 type dummy
# Act.
"$CTL" stop
# Assert: Stop must not delete an unowned interface.
ip link show awg0 >/dev/null
ip link del awg0
echo 'PASS: foreign interface is left alone'

# Arrange: pretend an earlier DNS mount is covered by another manager's mount.
# Existing mount IDs are read; no mount is created or removed by this test.
STATE=/var/lib/webosbrew/amneziawg/run/dns
mkdir -p "$STATE"
cat /proc/sys/kernel/random/boot_id >"$STATE/boot-id"
printf '/etc/resolv.conf\n' >"$STATE/target"
awk '$5=="/proc" {print $1; exit}' /proc/self/mountinfo >"$STATE/mount-id"
# Act / Assert: refuse unmounting the foreign resolver and retain recovery state.
if "$CTL" stop >/tmp/result 2>&1; then
  echo 'Foreign DNS guard failed'
  exit 1
fi
test -d "$STATE"
cmp /etc/resolv.conf /tmp/resolv.expected
rm -rf "$STATE"
echo 'PASS: foreign DNS mount is not unmounted; recovery state retained'
