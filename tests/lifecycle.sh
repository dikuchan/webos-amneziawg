#!/bin/sh
# Behaviour tests in an isolated Linux network/mount namespace, never on the TV.
set -eu
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
CTL="$ROOT/.build/arm64/bin/amneziawgctl"
[ -x "$CTL" ] || {
  echo 'Expected test controller is missing' >&2
  exit 1
}
[ -f /.dockerenv ] || {
  echo 'This test must run inside its disposable Docker container' >&2
  exit 1
}
mkdir -p /tmp/amneziawg-kit/bin /tmp/amneziawg-kit/scripts /dev/net
cp "$ROOT"/.build/arm64/bin/* /tmp/amneziawg-kit/bin/
cp "$ROOT"/packaging/dns.sh /tmp/amneziawg-kit/scripts/
CTL=/tmp/amneziawg-kit/bin/amneziawgctl
[ -e /dev/net/tun ] || mknod /dev/net/tun c 10 200
mount --make-rprivate /
umount /etc/resolv.conf
printf 'nameserver 192.0.2.53\nsearch home.test\n' >/etc/resolv.conf
cp /etc/resolv.conf /tmp/resolv.expected
mkdir -p /var/lib/webosbrew/amneziawg
ip link add awglan type veth peer name awgpeer
ip addr add 172.31.222.1/24 dev awglan
ip link set awglan up
ip netns add awg-server
ip link set awgpeer netns awg-server
ip -n awg-server link set lo up
ip -n awg-server addr add 172.31.222.2/24 dev awgpeer
ip -n awg-server link set awgpeer up
"$ROOT/.build/arm64/bin/awg" genkey >/tmp/server.key
"$ROOT/.build/arm64/bin/awg" genkey >/tmp/client.key
"$ROOT/.build/arm64/bin/awg" genkey >/tmp/header.key
SERVER_PUBLIC=$("$ROOT/.build/arm64/bin/awg" pubkey </tmp/server.key)
CLIENT_PUBLIC=$("$ROOT/.build/arm64/bin/awg" pubkey </tmp/client.key)
HEADER=$(cat /tmp/header.key)
protocol() {
  printf 'S1 = 16\nS2 = 16\nS3 = 16\nS4 = 16\nH1 = 1\nH2 = 2\nH3 = 3\nH4 = 4\nHeaderProtectionKey = %s\nContentPaddingAddition = 0-32\nRandomTrailers = on\nDisableCookies = on\n' "$HEADER"
}
{
  printf '[Interface]\nPrivateKey = %s\nListenPort = 31820\n' "$(cat /tmp/server.key)"
  protocol
  printf '[Peer]\nPublicKey = %s\nAllowedIPs = 10.203.7.2/32\n' "$CLIENT_PUBLIC"
} >/tmp/server.conf
{
  printf '[Interface]\nPrivateKey = %s\nAddress = 10.203.7.2/32\nDNS = 10.203.7.1\nMTU = 1360\n' "$(cat /tmp/client.key)"
  protocol
  printf '[Peer]\nPublicKey = %s\nAllowedIPs = 0.0.0.0/0, ::/0\nEndpoint = 172.31.222.2:31820\nPersistentKeepalive = 10-20\n' "$SERVER_PUBLIC"
} >/tmp/client.conf
ip netns exec awg-server "$ROOT/.build/arm64/bin/amneziawg-go" -f awgsrv0 >/dev/null 2>&1 &
SERVER_PID=$!
cleanup() {
  "$CTL" stop >/dev/null 2>&1 || true
  kill "$SERVER_PID" 2>/dev/null || true
  ip netns del awg-server 2>/dev/null || true
}
trap cleanup EXIT
tries=0
until ip -n awg-server link show awgsrv0 >/dev/null 2>&1; do
  tries=$((tries + 1))
  [ "$tries" -lt 30 ]
  sleep 0.1
done
ip netns exec awg-server "$ROOT/.build/arm64/bin/awg" setconf awgsrv0 /tmp/server.conf
ip -n awg-server addr add 10.203.7.1/24 dev awgsrv0
ip -n awg-server link set awgsrv0 mtu 1360 up
ip route show default >/tmp/default.expected
ip route add 198.51.100.77/32 dev awglan metric 44
"$CTL" import /tmp/client.conf

# Arrange: an active ordinary WG interface, owned by another application.
ip link add wg0 type dummy
# Act / Assert: AWG refuses to start and does not delete it or change DNS/routes.
if "$CTL" start >/tmp/result 2>&1; then
  echo 'WG coexistence guard failed'
  exit 1
fi
ip link show wg0 >/dev/null
cmp /etc/resolv.conf /tmp/resolv.expected
ip route show default >/tmp/default.actual
cmp /tmp/default.actual /tmp/default.expected
ip link del wg0
echo 'PASS: active WG is left alone'

# Act: a real AWG 3.1 tunnel, not a substituted daemon or network command.
"$CTL" start
# Assert: encrypted data, provider MTU/DNS, existing routes and keys kept private.
ping -c 2 -W 2 10.203.7.1
"$CTL" status >/tmp/result
grep -q 'state=connected' /tmp/result
grep -q 'mtu=1360' /tmp/result
grep -q 'nameserver 10.203.7.1' /etc/resolv.conf
if grep -F "$HEADER" /tmp/result; then
  echo 'HeaderProtectionKey leaked'
  exit 1
fi
echo 'PASS: AWG 3.1 handshake, encrypted traffic, MTU and DNS'

# Act: try replacing a live config.
if "$CTL" import /tmp/client.conf >/tmp/result 2>&1; then
  echo 'Active import accepted'
  exit 1
fi
# Assert: the existing tunnel still works.
ping -c 1 -W 2 10.203.7.1
echo 'PASS: importing cannot replace a live config'

# Act: disconnect twice.
"$CTL" stop
"$CTL" stop
# Assert: original DNS/default route, foreign route untouched, own interface gone.
cmp /etc/resolv.conf /tmp/resolv.expected
ip route show default >/tmp/default.actual
cmp /tmp/default.actual /tmp/default.expected
ip route show 198.51.100.77/32 | grep -q 'metric 44'
if ip link show awg0 >/dev/null 2>&1; then
  echo 'Own interface left behind'
  exit 1
fi
echo 'PASS: Stop restores DNS and leaves foreign/default routes alone'

# Arrange: another application reuses our now-free interface name.
ip link add awg0 type dummy
# Act: stopping an absent session.
"$CTL" stop
# Assert: foreign interface survives.
ip link show awg0 >/dev/null
ip link del awg0
echo 'PASS: Stop cannot delete an unowned interface'

# Arrange: no reachable server.
kill "$SERVER_PID"
wait "$SERVER_PID" || true
# Act: failed handshake must roll back every network change automatically.
if "$CTL" start >/tmp/result 2>&1; then
  echo 'Unreachable server accepted'
  exit 1
fi
# Assert.
cmp /etc/resolv.conf /tmp/resolv.expected
ip route show default >/tmp/default.actual
cmp /tmp/default.actual /tmp/default.expected
if ip link show awg0 >/dev/null 2>&1; then
  echo 'Failed Start left interface'
  exit 1
fi
ip route show 198.51.100.77/32 | grep -q 'metric 44'
echo 'PASS: failed handshake rolls back without changing WG or default route'
