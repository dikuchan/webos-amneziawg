# webos-amneziawg

AmneziaWG 3.1 client for rooted LG webOS TVs. Runs in userspace through `/dev/net/tun`; no kernel module is required.

- Remote-friendly interface in English and Russian.
- Configuration upload from a phone or computer on the same LAN, protected by a temporary code.
- Optional connection on device startup.
- Traffic counters and connection logs.
- Optional direct routing for Russian services using the automatically updated [RU Direct Standard](https://github.com/kyoresuas/ru-direct) IPv4 list.

ARMv7 binaries have been verified on an LG 55UN81006LB running webOS 5.6.2 / firmware 04.64.00. Root access, Homebrew Channel and `/dev/net/tun` are required.

## Install or update

Build the package using [BUILDING.md](BUILDING.md). Before updating, disconnect AmneziaWG and close the app. Install the new IPK over the existing app; the saved configuration, language and autoconnect preference are retained.

From a computer with SSH access to the TV:

```sh
scp -O dist/webos-amneziawg_0.3.1_all.ipk root@192.168.0.64:/tmp/amneziawg.ipk
ssh root@192.168.0.64
```

In the TV's root shell:

```sh
luna-send -i -f luna://com.webos.appInstallService/dev/install \
  '{"id":"com.github.dikuchan.amneziawgtest","ipkUrl":"/tmp/amneziawg.ipk","subscribe":true}'
```

Wait for `"state":"installed"`, then press Ctrl+C. Replace the example TV address with your own.

Open **AmneziaWG**. The power button connects and disconnects the VPN. The three lower buttons open configuration, logs and settings. Back closes the current panel; closing the app leaves the VPN running.

## Import a configuration

1. Disconnect AmneziaWG and open **Configuration**.
2. Open the displayed address on a device on the same network.
3. Enter the code, select the provider's `.conf` file and import it.
4. Connect using the power button on the TV.

The upload server binds to the TV's private LAN IPv4 address on port 8089. It closes after one successful upload, five incorrect codes, ten minutes, or leaving the import panel. Uploads use HTTP on the local network. Files are validated before replacing the saved configuration; an active AWG session cannot be overwritten.

Profiles must contain one peer, an IPv4 address and `AllowedIPs` including `0.0.0.0/0`. Up to three IPv4 DNS servers are supported. Physical-interface IPv6 is suspended while connected and restored on disconnect. DNS is applied through a reversible resolver bind mount.

The ordinary WireGuard app can remain installed. Disconnect its `wg0` interface before starting AmneziaWG.

## Settings

**Connect on device startup** installs a Homebrew Channel boot hook. It waits up to 120 seconds for the network, endpoint resolution and the system clock, then attempts one connection. Manual connection, disconnection or import cancels a pending boot attempt. Disable ordinary WireGuard autostart before enabling this option.

**Russian services directly** routes the RU Direct Standard IPv4 networks through the physical gateway. The bundled snapshot contains 635 subnets. The list is checked once a day while this mode and the VPN are active; failed updates retry after an hour. The refresh button updates it manually. Downloads are validated against the published checksum, size and count; a failed update keeps the previous list. Switching modes does not reconnect the tunnel.

This is subnet routing, not an application or domain exclusion rule. Services using addresses outside the list may still see the VPN. DNS servers remain pinned to the tunnel. The list's coverage and native TV applications need to be checked on the device.

## CLI and recovery

```sh
AWG=/media/developer/apps/usr/palm/applications/com.github.dikuchan.amneziawgtest/payload/bin/awg-test
"$AWG" status
"$AWG" logs
"$AWG" start
"$AWG" stop
"$AWG" autostart enable
"$AWG" autostart disable
"$AWG" routing ru-direct
"$AWG" routing all
"$AWG" routing update
```

For SSH-only installation, unpack `dist/webos-amneziawg-0.3.1-armv7.tar.gz` into `/var/lib/webosbrew/amneziawg-test` and use its `bin/awg-test`. Do not install both variants. A profile can also be imported with `"$AWG" import /path/to/profile.conf`.

The app ID, `awg-test` executable, `awgtest0` interface, `/var/lib/webosbrew/amneziawg-test` state directory and `75-amneziawg-test` boot hook retain their original names for compatibility with existing installations. The project and package names are `webos-amneziawg`.

If a connection fails, the controller rolls back its routes and daemon. If recovery fails, keep the state directory and retry **Disconnect** or `"$AWG" stop`. Disable autoconnect and disconnect successfully before uninstalling or downgrading. Only remove the private state directory after recovery has completed.

The log contains controller operations, not packet traces or configuration contents. There is no kill switch or continuous reconnection supervisor. Connection status currently checks the daemon and the presence of a handshake; it does not verify handshake freshness or general Internet reachability.

## Development

The controller uses the Go standard library. The UI uses strict TypeScript with no runtime framework. Build and test commands are in [BUILDING.md](BUILDING.md).

This fork is based on [RodriguezCF/webos-wireguard](https://github.com/RodriguezCF/webos-wireguard); its DNS work is retained on the `codex/dns-support` branch. Upstream history and copyright notices are preserved.

The controller and adapted scripts are MIT-licensed; bundled components retain their own licenses. Sources and pins for the Amnezia icon, Lucide icons, fonts and routing data are listed in [licenses/ASSETS.md](licenses/ASSETS.md). This is an independent client, not an official Amnezia application.
