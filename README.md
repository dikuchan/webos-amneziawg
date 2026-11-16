# webos-amneziawg

AmneziaWG 3.1 client for rooted LG webOS TVs. Runs in userspace through `/dev/net/tun`; no kernel module is required.

- Remote-friendly interface in English and Russian.
- LAN configuration import and routing editor, protected by a temporary code.
- Optional connection on device startup, traffic counters and logs.
- IPv4 include/exclude routing with multiple TXT files or HTTPS sources and configurable automatic updates.

The ARMv7 engine has been verified on an LG 55UN81006LB running webOS 5.6.2 / firmware 04.64.00. Version 0.4.0 routing and migration have been checked on an isolated Linux AWG stand; cold boot, firmware integration and native apps still need validation on the TV. Root access, Homebrew Channel and `/dev/net/tun` are required.

## Install

Build the package using [BUILDING.md](BUILDING.md). From a computer with SSH access to the TV:

```sh
scp -O dist/webos-amneziawg_0.4.0_all.ipk root@192.168.0.64:/tmp/amneziawg.ipk
ssh root@192.168.0.64
```

In the TV's root shell:

```sh
luna-send -i -f luna://com.webos.appInstallService/dev/install \
  '{"id":"com.github.dikuchan.amneziawg","ipkUrl":"/tmp/amneziawg.ipk","subscribe":true}'
```

Wait for `"state":"installed"`, then press Ctrl+C. Replace the example TV address with your own.

Open **AmneziaWG**. The power button connects and disconnects the VPN. The lower buttons open configuration, logs and settings. Back closes the current panel; closing the app leaves the VPN running.

### Upgrade from 0.3.x

Version 0.4.0 uses a new app ID, `com.github.dikuchan.amneziawg`. It installs alongside the old `com.github.dikuchan.amneziawgtest` app.

1. **Disconnect in the old app**, leave its web settings, and close it. Keep the old app installed until migration is verified. If needed, use its SSH command:
   ```sh
   OLD=/media/developer/apps/usr/palm/applications/com.github.dikuchan.amneziawgtest/payload/bin/awg-test
   "$OLD" stop
   ```
2. Install the new IPK and launch it. The controller copies the saved profile and routing preference. If the old app had autoconnect enabled, its own boot hook is transferred to the new app. An active old session or unresolved network recovery blocks migration.
3. Connect in the new app and verify Internet access and routing. You can then remove the old app. Both tiles are named AmneziaWG; launch the new one from SSH to distinguish them:
   ```sh
   luna-send -n 1 luna://com.webos.applicationManager/launch '{"id":"com.github.dikuchan.amneziawg"}'
   ```

The old state directory is retained as a backup. The new app uses `awg0`, `/var/lib/webosbrew/amneziawg`, `amneziawgctl` and `75-amneziawg`. Browser-local preferences such as the language are not copied between app IDs; the new app initially follows the TV language. Do not run both versions. To return to the old version, disconnect and disable autoconnect in the new one first, then re-enable the old app's autoconnect if required.

## Import a VPN profile

1. Open **Configuration** on the TV.
2. Open the displayed address on a device on the same LAN and enter the code.
3. Select the provider's `.conf` file and import it. Disconnect the VPN before replacing its profile.
4. Connect using the power button on the TV.

Web settings bind to the TV's private LAN IPv4 address on port 8089. The session closes after importing a profile, five incorrect codes, ten minutes, or leaving the TV panel. It uses HTTP on the local network with an HttpOnly session cookie and checks Host and Origin. Profiles are validated before replacement, and their contents are never displayed in the editor.

Profiles must contain one peer, an IPv4 address and `AllowedIPs` including `0.0.0.0/0`. Up to three unicast IPv4 DNS servers are supported. Physical-interface IPv6 is suspended while connected and restored on disconnect. DNS is applied through a reversible resolver bind mount.

The ordinary WireGuard app can remain installed. Disconnect its `wg0` interface before starting AmneziaWG.

## Routing lists

Open **Settings → Routing** on the TV:

| Mode | Listed IPv4 destinations | Other IPv4 destinations |
| --- | --- | --- |
| All traffic | VPN | VPN |
| Exclude lists | Direct | VPN |
| Only lists | VPN | Direct |

All sources are merged into one list. Include and exclude are alternative modes; version 0.4.0 does not mix opposite actions per source. Connected physical LAN networks stay reachable directly. The VPN endpoint remains direct, and the profile's DNS servers remain pinned to the tunnel in every mode. Other existing, more specific routes can still take precedence.

Use **Edit lists** to open web settings without disconnecting. In the browser's **Routing** tab, add local TXT files, HTTPS TXT URLs, or the bundled [RU Direct Standard](https://github.com/kyoresuas/ru-direct) preset. The preset contains 635 IPv4 subnets and has its own validated manifest. It is an ordinary removable source, used according to the selected mode.

TXT format is UTF-8, one IPv4 address or CIDR per line:

```text
# Example destinations
5.45.192.0/18
203.0.113.7  # treated as /32
```

Blank lines and `#` comments are accepted; duplicates and contained networks are removed. IPv6 addresses, domains, binary `.dat` files and empty lists are rejected. Limits: 8 sources, 256 KiB per source, 1 MiB combined and 16384 normalized subnets.

Each HTTPS source has its own update interval: **1–168 hours**, default **24**. An optional SHA-256 URL can contain a plain 64-digit hash or a `sha256sum` line naming the TXT file. URLs must be HTTPS without embedded credentials or fragments. Redirects are not followed; use the final raw TXT URL. Certificate validation stays enabled. ETag and Last-Modified are used when available, including checksum verification after a 304 response.

The updater runs in the background while the VPN is connected, even when the TV app is closed. Overdue sources are checked shortly after connection and then once a minute for their configured due time. A failed update retries after an hour and retains that source's last valid snapshot. On boot without Internet access, saved lists remain usable. A missing list must be downloaded successfully before activating a split mode. The refresh button forces an update; local files are changed only by uploading a replacement.

Saving or refreshing updates routes without restarting the tunnel. Conflicts with routes owned by other components roll back the change. Temporary routes carry public HTTPS list downloads through the VPN even in include mode, then are removed. Private LAN hosts and the VPN endpoint keep their direct path. Public list hostnames are resolved using the current system resolver before those routes are installed.

Subnet lists do not identify applications or domains. Services outside a list can use the opposite path. This release has no per-app rules, domain lists, kill switch or continuous reconnection supervisor. Connection status checks the daemon and the presence of a handshake; it does not verify handshake freshness or general Internet reachability.

## CLI and recovery

```sh
AWG=/media/developer/apps/usr/palm/applications/com.github.dikuchan.amneziawg/payload/bin/amneziawgctl
"$AWG" status
"$AWG" logs
"$AWG" start
"$AWG" stop
"$AWG" autostart enable
"$AWG" autostart disable
"$AWG" routing exclude
"$AWG" routing include
"$AWG" routing all
"$AWG" routing update
"$AWG" lan open ru
"$AWG" lan close
```

For SSH-only installation, unpack `dist/webos-amneziawg-0.4.0-armv7.tar.gz` into `/var/lib/webosbrew/amneziawg` and use its `bin/amneziawgctl`. Choose either the IPK or SSH-only installation. A profile can also be imported with `"$AWG" import /path/to/profile.conf`.

**Connect on device startup** installs a Homebrew Channel boot hook. It waits up to 120 seconds for the network, endpoint resolution and the system clock, then attempts one connection. Manual connection, disconnection or import cancels a pending boot attempt. Disable ordinary WireGuard autostart before enabling this option.

If a connection fails, the controller rolls back its routes and daemon. If recovery fails, keep the state directory and retry **Disconnect** or `"$AWG" stop`. Disable autoconnect and disconnect successfully before uninstalling or downgrading. Only remove the private state directory after recovery has completed. The bounded log contains controller operations, not packet traces or configuration contents.

## Development

The controller uses the Go standard library. The UI uses strict TypeScript with no runtime framework. Build and test commands are in [BUILDING.md](BUILDING.md).

This fork is based on [RodriguezCF/webos-wireguard](https://github.com/RodriguezCF/webos-wireguard); its DNS work is retained on the `codex/dns-support` branch. Upstream history and copyright notices are preserved.

The controller and adapted scripts are MIT-licensed; bundled components retain their own licenses. Sources and pins for the Amnezia icon, Lucide icons, fonts and routing data are listed in [web/assets/README.md](web/assets/README.md). This is an independent client, not an official Amnezia application.
