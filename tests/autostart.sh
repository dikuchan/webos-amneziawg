#!/bin/sh
set -eu
umask 077
CTL=/tmp/amneziawg-kit/bin/amneziawgctl
BASE=/var/lib/webosbrew/amneziawg
HOOK=/var/lib/webosbrew/init.d/75-amneziawg
WG_HOOK=/var/lib/webosbrew/init.d/90-wireguard

case "${1:-}" in
success)
  # Arrange: a synthetic provider profile, independent of Red Shield/TV.
  # An internal Docker network deliberately has no default route by default.
  ip -4 route add default via 172.31.222.1 dev eth0
  "$CTL" import /tmp/client.conf >/dev/null
  "$CTL" autostart enable >/tmp/autostart-result
  grep -q '^autostart=on$' /tmp/autostart-result
  test "$(readlink "$HOOK")" = /tmp/amneziawg-kit/scripts/boot.sh
  cp /etc/resolv.conf /tmp/boot-resolv.expected
  ip -4 route show default >/tmp/boot-default.expected
  # Simulate the next cold boot; import intentionally suppresses this boot.
  rm -f "$BASE/run/boot-cancelled" "$BASE/run/boot-attempt"

  # Act: invoke the installed HBC hook, not the Start command.
  "$HOOK"
  tries=0
  until "$CTL" status >/tmp/boot-status 2>/dev/null && grep -q '^state=connected$' /tmp/boot-status; do
    tries=$((tries + 1))
    test "$tries" -lt 60
    sleep 0.5
  done

  # Assert: real handshake/data, logs, and a single persistent hook.
  grep -q '^autostart=on$' /tmp/boot-status
  ip -4 route get 1.1.1.1 | grep -q 'dev awg0'
  ping -c 1 -W 2 10.203.7.1 >/dev/null
  "$CTL" logs >/tmp/boot-journal
  grep -q 'Boot autoconnect: connected.' /tmp/boot-journal
  awk -F ' = ' '$1 == "PrivateKey" || $1 == "HeaderProtectionKey" {print $2}' /tmp/client.conf >/tmp/private-patterns
  if grep -Fq -f /tmp/private-patterns /tmp/boot-journal; then
    echo 'FAIL: journal contains a configuration secret'
    exit 1
  fi
  test "$(stat -c %a "$BASE/run/events.log")" = 600
  echo 'PASS: installed boot hook connects; real AWG handshake and traffic; private bounded journal'

  # Manual Stop takes precedence for the rest of this boot.
  "$CTL" stop >/dev/null
  "$HOOK"
  sleep 1
  "$CTL" status >/tmp/boot-status
  grep -q '^state=stopped$' /tmp/boot-status
  if ip link show awg0 >/dev/null 2>&1; then
    echo 'FAIL: boot hook undid manual Stop'
    exit 1
  fi
  cmp /etc/resolv.conf /tmp/boot-resolv.expected
  ip -4 route show default >/tmp/boot-default.actual
  cmp /tmp/boot-default.actual /tmp/boot-default.expected
  echo 'PASS: manual Stop suppresses later boot-hook invocation; original network preserved'

  "$CTL" autostart disable >/dev/null
  test ! -L "$HOOK"
  printf '#!/bin/sh\n# Existing ordinary WireGuard hook\n' >"$WG_HOOK"
  chmod 700 "$WG_HOOK"
  cp "$WG_HOOK" /tmp/foreign-wg-hook.expected
  if "$CTL" autostart enable >/tmp/wg-conflict 2>&1; then
    echo 'FAIL: competing WG autostart accepted'
    exit 1
  fi
  grep -q 'ordinary WireGuard autostart is enabled' /tmp/wg-conflict
  cmp "$WG_HOOK" /tmp/foreign-wg-hook.expected
  test ! -L "$HOOK"
  rm "$WG_HOOK"
  echo 'PASS: conflicting ordinary WG autostart is refused and left untouched'
  ;;
failure)
  # Arrange: server is stopped by the runner; simulate a new cold boot.
  "$CTL" autostart enable >/dev/null
  rm -f "$BASE/run/boot-cancelled" "$BASE/run/boot-attempt"
  # Act.
  "$HOOK"
  tries=0
  until grep -q 'Boot autoconnect failed:.*no handshake in 20 seconds' "$BASE/run/events.log"; do
    tries=$((tries + 1))
    test "$tries" -lt 80
    sleep 0.5
  done
  # Assert: bounded failed attempt, automatic rollback, retained diagnosis.
  "$CTL" status >/tmp/boot-status
  grep -q '^state=stopped$' /tmp/boot-status
  if ip link show awg0 >/dev/null 2>&1; then
    echo 'FAIL: failed boot left a tunnel'
    exit 1
  fi
  cmp /etc/resolv.conf /tmp/boot-resolv.expected
  ip -4 route show default >/tmp/boot-default.actual
  cmp /tmp/boot-default.actual /tmp/boot-default.expected
  if ip -4 route show proto 242 | grep -q .; then
    echo 'FAIL: failed boot left routes'
    exit 1
  fi
  "$CTL" autostart disable >/dev/null
  echo 'PASS: unavailable boot server logs failure and rolls back tunnel/routes; original network preserved'
  ;;
cancel)
  # Arrange: a pending boot worker without a default route.
  "$CTL" autostart enable >/dev/null
  rm -f "$BASE/run/boot-cancelled" "$BASE/run/boot-attempt"
  ip -4 route del default via 172.31.222.1 dev eth0
  "$HOOK"
  tries=0
  until test -f "$BASE/run/boot-attempt"; do
    tries=$((tries + 1))
    test "$tries" -lt 20
    sleep 0.1
  done
  # Act: the user's Stop must cancel the pending connection, not only a TUN.
  "$CTL" stop >/dev/null
  ip -4 route add default via 172.31.222.1 dev eth0
  tries=0
  until grep -q 'Boot autoconnect cancelled.' "$BASE/run/events.log"; do
    tries=$((tries + 1))
    test "$tries" -lt 20
    sleep 0.5
  done
  # Assert: network becoming ready cannot undo the manual Stop.
  "$CTL" status >/tmp/boot-status
  grep -q '^state=stopped$' /tmp/boot-status
  grep -q '^autostart=on$' /tmp/boot-status
  if ip link show awg0 >/dev/null 2>&1; then
    echo 'FAIL: cancelled boot still connected'
    exit 1
  fi
  "$CTL" autostart disable >/dev/null
  echo 'PASS: manual Stop cancels a pending boot worker without disabling the next boot preference'
  ;;
*)
  echo 'Use success, failure or cancel'
  exit 2
  ;;
esac
