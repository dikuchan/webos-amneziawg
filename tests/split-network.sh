#!/bin/sh
# Two internal-network containers, a real AWG peer and synthetic credentials.
# No DNS bind mounts or host networking. The caller adds server loopback aliases
# 5.45.192.1 (RU Direct) and 203.0.113.1 (outside the list).
set -eu
mkdir -p /tmp/awg-test-kit/bin /tmp/awg-test-kit/scripts
cp /source/test-bin/* /tmp/awg-test-kit/bin/
cp /source/kit/scripts/dns.sh /tmp/awg-test-kit/scripts/
CTL=/tmp/awg-test-kit/bin/awg-test
BASE=/var/lib/webosbrew/amneziawg-test
ip route add default via 172.31.222.2 dev eth0
ip route add 198.51.100.77/32 dev eth0 metric 44
cp /etc/resolv.conf /tmp/dns.expected
ip route show default > /tmp/default.expected
"$CTL" import /tmp/client.conf
"$CTL" start
"$CTL" status > /tmp/status-before
PID_BEFORE=$(sed -n 's/^pid=//p' /tmp/status-before)

# Act: enable the full bundled list on an already connected tunnel.
"$CTL" routing ru-direct
# Assert: distinct direct/VPN paths and actual packets on both paths.
ip -4 route get 5.45.192.1 | grep -q 'dev eth0'
ip -4 route get 203.0.113.1 | grep -q 'dev awgtest0'
ping -c 1 -W 2 5.45.192.1
ping -c 1 -W 2 203.0.113.1
COUNT=$(ip -4 route show proto 242 metric 42432 | wc -l)
[ "$COUNT" -eq 635 ]
[ "$(wc -c < "$BASE/run/split-session.json")" -gt 10000 ]
echo 'PASS: 635 live bypass routes; direct and encrypted packets pass'

# Act: disable split without reconnecting the VPN.
"$CTL" routing all
# Assert: all traffic again uses AWG, same daemon identity, no bypass remains.
ip -4 route get 5.45.192.1 | grep -q 'dev awgtest0'
[ -z "$(ip -4 route show proto 242 metric 42432)" ]
"$CTL" status > /tmp/status-after
[ "$(sed -n 's/^pid=//p' /tmp/status-after)" = "$PID_BEFORE" ]
echo 'PASS: switching routing keeps the connected daemon'

# Arrange: a different owner already has an equal prefix/metric.
ip route add 5.45.192.0/18 via 172.31.222.2 dev eth0 proto 99 metric 42432
# Act: a failed enable must roll back the batch.
if "$CTL" routing ru-direct > /tmp/split-error 2>&1; then echo 'Route collision accepted'; exit 1; fi
# Assert: keep that owner's route and the previous all-VPN setting.
ip route show proto 99 | grep -q '5.45.192.0/18'
[ -z "$(ip -4 route show proto 242 metric 42432)" ]
"$CTL" status | grep -q '^routing=all$'
ip route del 5.45.192.0/18 via 172.31.222.2 dev eth0 proto 99 metric 42432

# Arrange: even our protocol/metric cannot grant ownership by itself.
ip route add 5.45.192.0/18 via 172.31.222.2 dev eth0 proto 242 metric 42432
# Act / Assert: refuse adoption; do not delete the unrecorded route.
if "$CTL" routing ru-direct > /tmp/split-error 2>&1; then echo 'Unowned route adopted'; exit 1; fi
ip route show 5.45.192.0/18 | grep -q 'proto 242'
ip route del 5.45.192.0/18 via 172.31.222.2 dev eth0 proto 242 metric 42432
echo 'PASS: collisions roll back; foreign routes survive'

# Act: enable again and then stop the entire session twice.
"$CTL" routing ru-direct
"$CTL" stop
"$CTL" stop
# Assert: all owned networking is gone, original resolver/default preserved.
[ -z "$(ip -4 route show proto 242)" ]
if ip link show awgtest0 >/dev/null 2>&1; then echo 'AWG interface remained'; exit 1; fi
ip route show 198.51.100.77/32 | grep -q 'metric 44'
ip route show default > /tmp/default.actual
cmp /tmp/default.actual /tmp/default.expected
cmp /etc/resolv.conf /tmp/dns.expected
[ ! -e "$BASE/run/route-updater.json" ]
echo 'PASS: Stop cleans routes and updater, preserves default/foreign routes/DNS'

# Act: another connection starts an updater; Stop must cancel its network wait.
"$CTL" start > /tmp/restarted-status
tries=0
until [ -f "$BASE/run/routing-update.json" ]; do
  tries=$((tries+1)); [ "$tries" -lt 40 ]; sleep 0.1
done
"$CTL" status > /tmp/updater-status
grep -Eq '^routing_update=(updating|failed)$' /tmp/updater-status
"$CTL" stop
[ ! -e "$BASE/run/route-updater.json" ]
[ -z "$(ip -4 route show proto 242)" ]
echo 'PASS: offline list update retains routing; Stop cancels updater'

# A live upload process is tested separately from another container.
"$CTL" upload open en > /tmp/upload-status
chmod 600 /tmp/upload-status
[ "$(sed -n 's/^upload=//p' /tmp/upload-status)" = waiting ]
echo 'READY: one-time LAN upload'
