#!/bin/sh
set -eu
CTL=/tmp/amneziawg-kit/bin/amneziawgctl
# Arrange: the runner has stopped the server.
# Act.
if "$CTL" start >/tmp/failed-start-output 2>&1; then
  echo 'Unreachable server accepted'
  exit 1
fi
# Assert: error is specifically the timed-out handshake, all own changes removed.
grep -q 'no handshake in 20 seconds' /tmp/failed-start-output
cmp /etc/resolv.conf /tmp/resolv.expected
ip route show default >/tmp/default.actual
cmp /tmp/default.actual /tmp/default.expected
ip route show 198.51.100.77/32 | grep -q 'metric 44'
if ip link show awg0 >/dev/null 2>&1; then
  echo 'Failed Start left interface'
  exit 1
fi
test ! -f /var/lib/webosbrew/amneziawg/run/session.json
echo 'PASS: unreachable server rolls back routes and daemon automatically'
