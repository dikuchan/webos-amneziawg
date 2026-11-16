#!/bin/sh
# Called while the controller holds operation.lock. Never edits ConnMan settings.
set -eu
umask 077
BASE=/var/lib/webosbrew/amneziawg
RUN="$BASE/run"
STATE="$RUN/dns"
CONF="$BASE/conf/awg0.conf"
IFACE=awg0
CANDIDATE=
STAGING=
APPLY_IN_PROGRESS=0

mount_id() {
  awk -v target="$1" '$5 == target {id=$1} END {print id}' /proc/self/mountinfo
}

restore_dns() {
  [ -d "$STATE" ] || return 0
  dns_boot="$(cat /proc/sys/kernel/random/boot_id)" || return 1
  saved_boot="$(cat "$STATE/boot-id")" || return 1
  if [ "$dns_boot" != "$saved_boot" ]; then
    # Bind mounts cannot survive a reboot. Do not replay an old DHCP snapshot.
    rm -rf "$STATE" || return 1
    return 0
  fi
  dns_target="$(cat "$STATE/target")" || return 1
  case "$dns_target" in
  /*) ;;
  *)
    echo 'ERROR: invalid saved DNS target' >&2
    return 1
    ;;
  esac
  saved_id="$(cat "$STATE/mount-id" 2>/dev/null || true)"
  current_id="$(mount_id "$dns_target")" || return 1
  if [ -n "$saved_id" ]; then
    if ! awk -v id="$saved_id" '$1 == id {found=1} END {exit !found}' /proc/self/mountinfo; then
      # Our mount was already removed; leave any replacement mount alone.
      rm -rf "$STATE" || return 1
      return 0
    fi
    if [ "$current_id" != "$saved_id" ]; then
      echo 'ERROR: another mount covers VPN DNS; keeping recovery state' >&2
      return 1
    fi
  fi
  source_inode="$(stat -c '%d:%i' "$STATE/resolv.conf")" || return 1
  target_inode="$(stat -Lc '%d:%i' "$dns_target")" || return 1
  if [ -n "$current_id" ] && [ "$source_inode" = "$target_inode" ]; then
    umount "$dns_target" || return 1
  elif [ -n "$saved_id" ] || [ "$current_id" != "$(cat "$STATE/original-mount-id")" ]; then
    echo 'ERROR: cannot identify VPN DNS mount; keeping recovery state' >&2
    return 1
  fi
  rm -rf "$STATE" || return 1
  echo 'Original DNS resolver restored'
}

cleanup() {
  rc=$?
  trap - EXIT HUP INT TERM
  if [ "$APPLY_IN_PROGRESS" -eq 1 ]; then
    if ! restore_dns; then
      echo 'ERROR: DNS rollback failed; retry Stop VPN before uninstalling' >&2
      rc=1
    fi
  fi
  [ -z "$CANDIDATE" ] || rm -f "$CANDIDATE"
  [ -z "$STAGING" ] || rm -rf "$STAGING"
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

case "${1:-}" in
restore)
  restore_dns
  exit 0
  ;;
apply) ;;
*)
  echo 'Usage: dns.sh apply|restore' >&2
  exit 1
  ;;
esac

mkdir -p "$RUN"
chmod 700 "$RUN"
CANDIDATE="$(mktemp "$RUN/dns-config.XXXXXX")"
# Validate hand-edited configs too. This app currently provides an IPv4 tunnel.
awk '
  function trim(s) {gsub(/^[[:space:]]+|[[:space:]]+$/, "", s); return s}
  function fail(message) {print "ERROR: DNS " message > "/dev/stderr"; exit 1}
  {
    sub(/\r$/, ""); sub(/#.*/, ""); line=trim($0)
    if (line ~ /^\[.*\]$/) {section=tolower(line); next}
    equals=index(line, "=")
    if (!equals || tolower(trim(substr(line, 1, equals-1))) != "dns") next
    if (section != "[interface]") fail("is only supported in [Interface]")
    value=trim(substr(line, equals+1))
    n=split(value, servers, ",")
    if (!n) fail("requires IPv4 addresses")
    for (i=1; i<=n; i++) {
      ip=trim(servers[i]); parts=split(ip, octets, ".")
      if (parts != 4) fail("requires IPv4 addresses; IPv6 and search domains are not supported")
      for (j=1; j<=4; j++) {
        if (octets[j] !~ /^[0-9]+$/ || octets[j]+0 > 255 || (length(octets[j])>1 && substr(octets[j],1,1)=="0"))
          fail("requires valid IPv4 addresses")
      }
      if (octets[1]+0 == 0 || octets[1]+0 == 127 || octets[1]+0 >= 224 || (octets[1]+0 == 169 && octets[2]+0 == 254))
        fail("requires unicast IPv4 addresses")
      if (++count > 3) fail("supports at most three servers")
      print "nameserver " ip
    }
  }
' "$CONF" >"$CANDIDATE"

while read -r _ dns_ip; do
  route="$(ip route get "$dns_ip")"
  if ! printf '%s\n' "$route" | awk -v iface="$IFACE" '{for (i=1;i<NF;i++) if ($i=="dev" && $(i+1)==iface) ok=1} END {exit !ok}'; then
    echo 'ERROR: DNS server is not routed through awg0; check AllowedIPs and LAN routes' >&2
    exit 1
  fi
done <"$CANDIDATE"

APPLY_IN_PROGRESS=1
restore_dns
if [ ! -s "$CANDIDATE" ]; then
  APPLY_IN_PROGRESS=0
  exit 0
fi

dns_target="$(readlink -f /etc/resolv.conf)"
# mountinfo escapes whitespace; standard webOS resolver paths contain none.
case "$dns_target" in
'' | *[[:space:]\\]*)
  echo 'ERROR: unsupported DNS resolver path' >&2
  exit 1
  ;;
esac
[ -f "$dns_target" ] || {
  echo 'ERROR: DNS resolver file is missing' >&2
  exit 1
}
STAGING="$(mktemp -d "$RUN/dns-state.XXXXXX")"
cat /proc/sys/kernel/random/boot_id >"$STAGING/boot-id"
printf '%s\n' "$dns_target" >"$STAGING/target"
mount_id "$dns_target" >"$STAGING/original-mount-id"
cp "$CANDIDATE" "$STAGING/resolv.conf"
chmod 644 "$STAGING/resolv.conf"
mv "$STAGING" "$STATE"
STAGING=
# Record recovery state before mounting, so an interrupted apply can be undone.
mount --bind "$STATE/resolv.conf" "$dns_target"
mount_id "$dns_target" >"$STATE/mount-id"
[ -s "$STATE/mount-id" ] || {
  echo 'ERROR: DNS mount was not recorded' >&2
  exit 1
}
mount -o remount,bind,ro "$dns_target"
cmp -s "$CANDIDATE" /etc/resolv.conf || {
  echo 'ERROR: DNS override did not take effect' >&2
  exit 1
}
APPLY_IN_PROGRESS=0
echo 'VPN DNS resolver applied'
