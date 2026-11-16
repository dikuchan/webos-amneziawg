#!/bin/sh
# Host-side integration test: synthetic AWG peers and a TLS list server, no Internet.
set -eu
umask 077
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
DOCKER=${DOCKER:-docker}
IMAGE=${AWG_TEST_IMAGE:-webos-amneziawg-nettools}
GO_BIN=${GO_BIN:-go}
WORK=$(mktemp -d)
NETWORK=
SERVER=
CLIENT=
cleanup() {
    if [ -n "$CLIENT" ]; then "$DOCKER" rm -f "$CLIENT" >/dev/null; fi
    if [ -n "$SERVER" ]; then "$DOCKER" rm -f "$SERVER" >/dev/null; fi
    if [ -n "$NETWORK" ]; then "$DOCKER" network rm "$NETWORK" >/dev/null; fi
    rm -rf "$WORK"
}
trap cleanup EXIT HUP INT TERM
cd "$ROOT"
for component in fixture lan-client; do
    GOOS=linux GOARCH=arm64 CGO_ENABLED=0 "$GO_BIN" build -trimpath -o "test-bin/$component" "./tests/$component"
done
for component in awg amneziawg-go amneziawgctl; do test -x "test-bin/$component"; done
NETWORK=$("$DOCKER" network create --internal --subnet 172.31.222.0/24 "awg-routing-$$")
SERVER=$("$DOCKER" create --pull never --platform linux/arm64 --network "$NETWORK" --ip 172.31.222.2 --cap-add NET_ADMIN --device /dev/net/tun --sysctl net.ipv6.conf.all.disable_ipv6=1 --sysctl net.ipv6.conf.default.disable_ipv6=1 "$IMAGE" sleep infinity)
CLIENT=$("$DOCKER" create --pull never --platform linux/arm64 --network "$NETWORK" --ip 172.31.222.3 --cap-add NET_ADMIN --device /dev/net/tun --sysctl net.ipv6.conf.all.disable_ipv6=1 --sysctl net.ipv6.conf.default.disable_ipv6=1 "$IMAGE" sleep infinity)
"$DOCKER" start "$SERVER" "$CLIENT" >/dev/null
"$DOCKER" exec "$SERVER" mkdir -p /source/test-bin /source/tests
"$DOCKER" exec "$CLIENT" mkdir -p /tmp/amneziawg-kit/bin /tmp/amneziawg-kit/scripts
for component in awg amneziawg-go amneziawgctl; do
    "$DOCKER" cp "test-bin/$component" "$SERVER:/source/test-bin/$component"
    "$DOCKER" cp "test-bin/$component" "$CLIENT:/tmp/amneziawg-kit/bin/$component"
done
"$DOCKER" cp tests/server.sh "$SERVER:/source/tests/server.sh"
"$DOCKER" cp test-bin/fixture "$SERVER:/tmp/fixture"
"$DOCKER" cp test-bin/lan-client "$CLIENT:/tmp/lan-client"
"$DOCKER" cp kit/scripts/. "$CLIENT:/tmp/amneziawg-kit/scripts/"
"$DOCKER" exec "$SERVER" sh /source/tests/server.sh
"$DOCKER" exec "$SERVER" sh -ec 'ip addr add 5.45.192.1/32 dev lo; ip addr add 203.0.113.1/32 dev lo; ip addr add 203.0.113.2/32 dev lo; mkdir -p /tmp/awg-feed; printf "203.0.113.1/32\n" > /tmp/awg-feed/rules.txt'
"$DOCKER" exec -d "$SERVER" /tmp/fixture
"$DOCKER" cp "$SERVER:/tmp/awg-server/client.conf" "$WORK/client.conf"
# Wait for the fixture to create its CA, without disabling TLS verification.
attempt=0
until "$DOCKER" cp "$SERVER:/tmp/awg-feed/ca.pem" "$WORK/ca.pem" 2>/dev/null; do
    attempt=$((attempt+1)); test "$attempt" -lt 30; sleep 0.1
done
"$DOCKER" cp "$WORK/client.conf" "$CLIENT:/tmp/client.conf"
"$DOCKER" cp "$WORK/ca.pem" "$CLIENT:/tmp/feed-ca.pem"
cat > "$WORK/clear.json" <<'JSON'
{"Mode":"all","Sources":[]}
JSON
cat > "$WORK/remote.json" <<'JSON'
{"Mode":"include","Sources":[{"ID":"remote-one","Kind":"url","URL":"https://5.45.192.1:8443/rules.txt","Checksum":"https://5.45.192.1:8443/rules.sha256","Hours":6}]}
JSON
cat > "$WORK/multiple.json" <<'JSON'
{"Mode":"exclude","Sources":[{"ID":"one","Kind":"file","Text":"203.0.113.1/32\n"},{"ID":"two","Kind":"url","URL":"https://5.45.192.1:8443/rules.txt","Hours":12},{"ID":"three","Kind":"url","URL":"https://5.45.192.1:8443/rules.txt","Checksum":"https://5.45.192.1:8443/rules.sha256","Hours":48}]}
JSON
cat > "$WORK/everything.json" <<'JSON'
{"Mode":"exclude","Sources":[{"ID":"all","Kind":"file","Text":"0.0.0.0/0\n"}]}
JSON
cat > "$WORK/conflict.json" <<'JSON'
{"Mode":"exclude","Sources":[{"ID":"collision","Kind":"file","Text":"203.0.113.0/24\n"}]}
JSON
"$DOCKER" cp "$WORK/." "$CLIENT:/tmp/policies"
client() { "$DOCKER" exec -i -e SSL_CERT_FILE=/tmp/feed-ca.pem "$CLIENT" sh -e; }
client <<'CLIENT'
CTL=/tmp/amneziawg-kit/bin/amneziawgctl
ip route add default via 172.31.222.2 dev eth0
cp /etc/resolv.conf /tmp/dns.expected
ip route show default > /tmp/default.expected
ip route add 198.51.100.77/32 dev eth0 metric 44
"$CTL" import /tmp/client.conf > /dev/null
"$CTL" lan open en > /tmp/lan-status
/tmp/lan-client /tmp/policies/clear.json
ip link add wg0 type dummy
if "$CTL" start > /tmp/wg-guard 2>&1; then exit 1; fi
ip link show wg0 > /dev/null
ip link del wg0
"$CTL" start > /tmp/connected-before
"$CTL" lan open en > /tmp/lan-status
/tmp/lan-client /tmp/policies/remote.json
ip route get 203.0.113.1 | grep -q "dev awg0"
ip route get 5.45.192.1 | grep -q "dev eth0"
ping -c 1 -W 2 203.0.113.1 > /dev/null
"$CTL" routing update > /dev/null
ip route get 5.45.192.1 | grep -q "dev eth0"
echo "PASS: LAN authentication, IPv4 include mode, source download through VPN, temporary route cleanup"
CLIENT
"$DOCKER" exec "$SERVER" sh -ec 'printf "203.0.113.2/32\n" > /tmp/awg-feed/rules.txt'
client <<'CLIENT'
CTL=/tmp/amneziawg-kit/bin/amneziawgctl
"$CTL" routing update > /dev/null
ip route get 203.0.113.1 | grep -q "dev eth0"
ip route get 203.0.113.2 | grep -q "dev awg0"
ping -c 1 -W 2 203.0.113.2 > /dev/null
"$CTL" status > /tmp/connected-after
[ "$(sed -n "s/^pid=//p" /tmp/connected-before)" = "$(sed -n "s/^pid=//p" /tmp/connected-after)" ]
echo "PASS: update replaces routes without reconnecting"
CLIENT
"$DOCKER" exec "$SERVER" sh -ec 'touch /tmp/awg-feed/bad-sha; printf "203.0.113.1/32\n" > /tmp/awg-feed/rules.txt'
client <<'CLIENT'
CTL=/tmp/amneziawg-kit/bin/amneziawgctl
if "$CTL" routing update > /tmp/checksum-result 2>&1; then exit 1; fi
ip route get 203.0.113.1 | grep -q "dev eth0"
ip route get 203.0.113.2 | grep -q "dev awg0"
"$CTL" status | grep -q "^routing_update=failed$"
echo "PASS: SHA-256 mismatch preserves previous snapshot and routes"
CLIENT
"$DOCKER" exec "$SERVER" rm /tmp/awg-feed/bad-sha
client <<'CLIENT'
CTL=/tmp/amneziawg-kit/bin/amneziawgctl
/tmp/lan-client /tmp/policies/multiple.json
"$CTL" status | grep -q "^routing_sources=3$"
ip route get 203.0.113.1 | grep -q "dev eth0"
"$CTL" routing include > /dev/null
ip route get 203.0.113.1 | grep -q "dev awg0"
ip route get 203.0.113.2 | grep -q "dev eth0"
/tmp/lan-client /tmp/policies/everything.json
ip route get 203.0.113.1 | grep -q "dev eth0"
"$CTL" routing include > /dev/null
ip route get 203.0.113.1 | grep -q "dev awg0"
ip route get 172.31.222.2 | grep -q "dev eth0"
"$CTL" routing exclude > /dev/null
ip route get 203.0.113.1 | grep -q "dev eth0"
echo "PASS: multiple URLs plus local TXT, opposite include/exclude paths, /0 and LAN protection"
"$CTL" routing all > /dev/null
for protocol in 99 242; do
    ip route add 203.0.113.0/24 via 172.31.222.2 dev eth0 proto "$protocol" metric 42432
    if /tmp/lan-client /tmp/policies/conflict.json > /tmp/conflict-result 2>&1; then exit 1; fi
    "$CTL" status | grep -q "^routing=all$"
    ip route show 203.0.113.0/24 | grep -q "metric 42432"
    ip route del 203.0.113.0/24 via 172.31.222.2 dev eth0 proto "$protocol" metric 42432
done
[ "$(ip route show proto 242 metric 42432 | wc -l)" -eq 2 ]
/tmp/lan-client /tmp/policies/remote.json
"$CTL" stop > /dev/null
"$CTL" stop > /dev/null
[ -z "$(ip route show proto 242)" ]
if ip link show awg0 > /dev/null 2>&1; then exit 1; fi
ip route show default > /tmp/default.actual
cmp /tmp/default.expected /tmp/default.actual
cmp /tmp/dns.expected /etc/resolv.conf
ip route show 198.51.100.77/32 | grep -q "metric 44"
echo "PASS: route collision rollback, foreign ownership, idempotent Stop, network restoration"
# Keep a valid cached list, but mark it overdue for the next connection.
sed -i 's/"attempted":"[^"]*"/"attempted":"2020-01-01T00:00:00Z"/g' /var/lib/webosbrew/amneziawg/conf/routing.json
CLIENT
"$DOCKER" exec "$SERVER" touch /tmp/awg-feed/slow
client <<'CLIENT'
CTL=/tmp/amneziawg-kit/bin/amneziawgctl
"$CTL" start > /dev/null
attempt=0
until ip route show 5.45.192.1/32 | grep -q "dev awg0"; do
    attempt=$((attempt+1)); test "$attempt" -lt 50; sleep 0.1
done
"$CTL" stop > /dev/null
[ ! -e /var/lib/webosbrew/amneziawg/run/route-updater.json ]
[ -z "$(ip route show proto 242)" ]
echo "PASS: cached include-mode startup and Stop cancelling an in-flight automatic update"
CLIENT
"$DOCKER" exec -i "$SERVER" sh -e <<'SERVER'
[ "$(sort -u /tmp/awg-feed/peers.log)" = "10.203.7.2" ]
SERVER
echo 'PASS: all HTTPS feed requests used the encrypted tunnel'
