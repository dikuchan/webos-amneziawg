#!/bin/sh
set -eu
AWG=/source/test-bin/awg
mkdir -p /tmp/awg-server
umask 077
"$AWG" genkey > /tmp/awg-server/server.key
"$AWG" genkey > /tmp/awg-server/client.key
"$AWG" genkey > /tmp/awg-server/header.key
SERVER_PUBLIC=$("$AWG" pubkey < /tmp/awg-server/server.key)
CLIENT_PUBLIC=$("$AWG" pubkey < /tmp/awg-server/client.key)
protocol() {
  printf 'S1 = 16\nS2 = 16\nS3 = 16\nS4 = 16\nH1 = 1\nH2 = 2\nH3 = 3\nH4 = 4\nHeaderProtectionKey = %s\nContentPaddingAddition = 0-32\nRandomTrailers = on\nDisableCookies = on\n' "$(cat /tmp/awg-server/header.key)"
}
{
  printf '[Interface]\nPrivateKey = %s\nListenPort = 31820\n' "$(cat /tmp/awg-server/server.key)"
  protocol
  printf '[Peer]\nPublicKey = %s\nAllowedIPs = 10.203.7.2/32\n' "$CLIENT_PUBLIC"
} > /tmp/awg-server/server.conf
{
  printf '[Interface]\nPrivateKey = %s\nAddress = 10.203.7.2/32\nMTU = 1360\n' "$(cat /tmp/awg-server/client.key)"
  protocol
  printf '[Peer]\nPublicKey = %s\nAllowedIPs = 0.0.0.0/0, ::/0\nEndpoint = 172.31.222.2:31820\nPersistentKeepalive = 10-20\n' "$SERVER_PUBLIC"
} > /tmp/awg-server/client.conf
LOG_LEVEL=error /source/test-bin/amneziawg-go -f awgsrv0 </dev/null >/dev/null 2>&1 &
echo "$!" > /tmp/awg-server/pid
tries=0
until [ -S /var/run/amneziawg/awgsrv0.sock ]; do tries=$((tries+1)); [ "$tries" -lt 50 ]; sleep 0.1; done
"$AWG" setconf awgsrv0 /tmp/awg-server/server.conf
ip addr add 10.203.7.1/24 dev awgsrv0
ip link set awgsrv0 mtu 1360 up
echo server-ready
