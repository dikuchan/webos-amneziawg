#!/bin/sh
# Exercise real Linux bind mounts with only the TV network commands substituted.
set -eu
ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
if [ "${1:-}" != --container ]; then
  exec docker run --rm --cap-add SYS_ADMIN --security-opt apparmor=unconfined --network none \
    --mount "type=bind,src=$ROOT,dst=/source,readonly" \
    alpine:3.22 /bin/sh /source/scripts/test-dns.sh --container "$@"
fi
shift
[ -f /.dockerenv ] || { echo 'Run through the Docker test launcher' >&2; exit 1; }
mount --make-rprivate /
umount /etc/resolv.conf
rm -f /etc/resolv.conf
ln -s /tmp/resolv.original /etc/resolv.conf
BASE=/var/lib/webosbrew/wireguard
APP=/tmp/dns-test-app
TARGET=/tmp/resolv.original
mkdir -p "$APP/scripts" "$APP/bin" /tmp/dns-test-tools
cp "$ROOT"/app/com.github.cfernande1470.wireguard/payload/wireguard/scripts/*.sh "$APP/scripts/"
cat > /tmp/dns-test-tools/ip <<'SH'
#!/bin/sh
case "$*" in
  'route show default'|'route') echo 'default via 192.0.2.1 dev eth0' ;;
  route\ get\ *) echo "$3 dev ${DNS_TEST_ROUTE_DEV:-wg0} src 10.20.0.2" ;;
  'link set mtu 1420 up dev wg0') touch /tmp/dns-test-interface-up ;;
  'link del wg0') rm -f /tmp/dns-test-interface-up ;;
esac
SH
cat > "$APP/bin/wg" <<'SH'
#!/bin/sh
case "$*" in
  setconf\ *)
    # VPN DNS cannot resolve the new endpoint until setconf completes.
    if grep -q '^Endpoint = vpn.example.invalid:' "$3" && grep -q '^nameserver 10.20.0.1$' /etc/resolv.conf; then
      echo 'VPN endpoint DNS is unavailable while the tunnel is being restarted' >&2
      exit 1
    fi
    ! grep -qi '^[[:space:]]*DNS[[:space:]]*=' "$3"
    ;;
  'show wg0 endpoints') echo 'test-peer 203.0.113.1:51820' ;;
  'show wg0') [ "${DNS_TEST_FAIL_STATUS:-0}" -eq 0 ] ;;
esac
SH
for tool in killall sleep; do
  printf '#!/bin/sh\nexit 0\n' > "/tmp/dns-test-tools/$tool"
done
cat > /tmp/dns-test-tools/sysctl <<'SH'
#!/bin/sh
[ "$1" != -n ] || echo 0
exit 0
SH
printf '#!/bin/sh\nexit 0\n' > "$APP/bin/wireguard-go"
cat > /tmp/dns-test-tools/mount <<'SH'
#!/bin/sh
if [ "${DNS_TEST_FAIL_REMOUNT:-0}" -eq 1 ] && [ "$1" = -o ]; then exit 1; fi
exec /bin/mount "$@"
SH
cat > /tmp/dns-test-tools/umount <<'SH'
#!/bin/sh
[ "${DNS_TEST_FAIL_UMOUNT:-0}" -eq 0 ] || exit 1
exec /bin/umount "$@"
SH
chmod +x /tmp/dns-test-tools/* "$APP/bin/"* "$APP/scripts/"*
PATH="/tmp/dns-test-tools:$PATH"
export PATH
cleanup_mounts() {
  n=0
  while awk -v target="$TARGET" '$5 == target {found=1} END {exit !found}' /proc/self/mountinfo; do
    [ "$n" -lt 4 ] || return 1
    /bin/umount "$TARGET"
    n=$((n + 1))
  done
}
finish() {
  result=$?
  trap - EXIT
  if [ "$result" -ne 0 ]; then
    for log in /tmp/start.log /tmp/stop.log /tmp/uninstall.log; do
      [ ! -f "$log" ] || tail -n 40 "$log" >&2
    done
  fi
  cleanup_mounts || exit 1
  exit "$result"
}
trap finish EXIT
reset_fixture() {
  cleanup_mounts
  unset DNS_TEST_FAIL_STATUS DNS_TEST_FAIL_REMOUNT DNS_TEST_FAIL_UMOUNT DNS_TEST_ROUTE_DEV
  rm -rf "$BASE"
  rm -f /tmp/dns-test-interface-up /tmp/start.log /tmp/stop.log /tmp/uninstall.log
  mkdir -p "$BASE/conf" "$BASE/run"
  printf '# supplied by DHCP\nnameserver 192.0.2.53\nsearch home.test\n' > "$TARGET"
  cp "$TARGET" /tmp/resolv.expected
  write_config '10.20.0.1, 1.1.1.1'
}
write_config() {
  {
    printf '[Interface]\nPrivateKey = test-key\nAddress = 10.20.0.2/32\n'
    [ -z "$1" ] || printf 'DNS = %s\n' "$1"
    printf '[Peer]\nPublicKey = test-peer\nAllowedIPs = 0.0.0.0/0\nEndpoint = 203.0.113.1:51820\n'
  } > "$BASE/conf/wg0.conf"
}
start_vpn() { "$APP/scripts/start.sh" > /tmp/start.log 2>&1; }
stop_vpn() { "$APP/scripts/stop.sh" > /tmp/stop.log 2>&1; }
assert_original_dns() {
  cmp /tmp/resolv.expected /etc/resolv.conf
  [ -L /etc/resolv.conf ]
  [ ! -e "$BASE/run/dns" ]
}
test_start_uses_config_dns() {
  # Arrange.
  reset_fixture
  # Act.
  start_vpn
  # Assert: resolver order, no ISP fallback, and protection from ConnMan writes.
  printf 'nameserver 10.20.0.1\nnameserver 1.1.1.1\n' > /tmp/resolv.vpn
  cmp /tmp/resolv.vpn /etc/resolv.conf
  if (echo 'nameserver 192.0.2.53' >> /etc/resolv.conf) 2>/dev/null; then
    echo 'DNS override is writable' >&2; return 1
  fi
}
test_stop_restores_original_dns() {
  # Arrange.
  reset_fixture
  start_vpn
  # Act.
  stop_vpn
  # Assert.
  assert_original_dns
}
test_repeated_start_remains_reversible() {
  # Arrange.
  reset_fixture
  start_vpn
  write_config '8.8.8.8'
  # Act: reconnect and then disconnect.
  start_vpn
  stop_vpn
  # Assert: no stack of mounts and no replacement of the original resolver.
  assert_original_dns
}
test_reconnect_can_resolve_hostname_endpoint() {
  # Arrange: a previously connected profile with a hostname endpoint.
  reset_fixture
  start_vpn
  sed -i 's/203.0.113.1:51820/vpn.example.invalid:51820/' "$BASE/conf/wg0.conf"
  # Act.
  start_vpn
  # Assert: reconnect succeeded and VPN DNS was reapplied afterwards.
  grep -qx 'nameserver 10.20.0.1' /etc/resolv.conf
}
test_no_dns_leaves_resolver_untouched() {
  # Arrange.
  reset_fixture
  write_config ''
  # Act.
  start_vpn
  # Assert.
  assert_original_dns
}
test_profile_without_dns_removes_old_override() {
  # Arrange.
  reset_fixture
  start_vpn
  write_config ''
  # Act.
  start_vpn
  # Assert.
  assert_original_dns
}
test_failed_start_restores_dns() {
  # Arrange: fail after DNS has been applied, during the status check.
  reset_fixture
  export DNS_TEST_FAIL_STATUS=1
  # Act.
  if start_vpn; then echo 'Start unexpectedly succeeded' >&2; return 1; fi
  # Assert.
  assert_original_dns
}
test_failed_start_removes_tunnel() {
  # Arrange: a failure after the tunnel is configured and routes are installed.
  reset_fixture
  export DNS_TEST_FAIL_STATUS=1
  # Act.
  if start_vpn; then echo 'Start unexpectedly succeeded' >&2; return 1; fi
  # Assert: failed startup must not leave traffic routed into a half-started VPN.
  [ ! -f /tmp/dns-test-interface-up ]
}
test_remount_failure_rolls_back_dns() {
  # Arrange: a kernel refusing read-only bind remounts.
  reset_fixture
  export DNS_TEST_FAIL_REMOUNT=1
  # Act.
  if start_vpn; then echo 'Start unexpectedly succeeded' >&2; return 1; fi
  # Assert.
  assert_original_dns
}
test_dns_outside_tunnel_is_rejected() {
  # Arrange: a LAN route outranks the VPN routes for this resolver.
  reset_fixture
  export DNS_TEST_ROUTE_DEV=eth0
  # Act.
  if start_vpn; then echo 'Start unexpectedly succeeded' >&2; return 1; fi
  # Assert.
  assert_original_dns
}
test_uninstall_keeps_state_when_restore_fails() {
  # Arrange.
  reset_fixture
  start_vpn
  export DNS_TEST_FAIL_UMOUNT=1
  # Act.
  if "$APP/scripts/uninstall.sh" > /tmp/uninstall.log 2>&1; then
    echo 'Uninstall unexpectedly succeeded' >&2; return 1
  fi
  # Assert: retain the mounted source and recovery information for a retry.
  [ -d "$BASE/run/dns" ]
  grep -qx 'nameserver 10.20.0.1' /etc/resolv.conf
}
if [ "$#" -eq 0 ]; then
  set -- test_start_uses_config_dns test_stop_restores_original_dns \
    test_repeated_start_remains_reversible test_reconnect_can_resolve_hostname_endpoint \
    test_no_dns_leaves_resolver_untouched test_profile_without_dns_removes_old_override \
    test_failed_start_restores_dns test_failed_start_removes_tunnel \
    test_remount_failure_rolls_back_dns test_dns_outside_tunnel_is_rejected \
    test_uninstall_keeps_state_when_restore_fails
fi
for test in "$@"; do
  printf '%s\n' "$test"
  "$test"
done
/bin/sh "$ROOT/scripts/test-launch-fds.sh"
echo 'DNS lifecycle tests passed'
