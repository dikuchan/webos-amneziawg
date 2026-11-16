# WireGuard for LG webOS Homebrew

[English](#english) · [Español](#español)

---

# English

A WireGuard client for rooted LG webOS TVs using Homebrew Channel.

This project provides a simple webOS app that can install, configure, start, stop and monitor a WireGuard VPN tunnel on LG webOS. It uses WireGuard in userspace through `wireguard-go`, because LG webOS stock kernels usually do not include the WireGuard kernel module.

The app is designed for rooted TVs with Homebrew Channel installed.

---

## Features

- WireGuard userspace tunnel using `wireguard-go`
- Bundled `wg`, `wireguard-go` and `wg-upload` binaries
- TV-friendly webOS interface
- English and Spanish UI
- Manual **Install / update** workflow
- No automatic payload installation on app launch
- Temporary web upload server for `wg0.conf`
- Time-limited configuration upload protected by a random access code and failed-attempt lockout
- Start, stop, status, routes, config and log actions
- Optional autostart on boot
- Runtime uninstall action
- Experimental, reversible IPv4 DNS settings from provider configs

---

## Why userspace WireGuard?

On normal Linux systems, WireGuard usually runs as a kernel module.

On LG webOS TVs, the WireGuard kernel module is normally not available in the stock kernel. For that reason, this project uses:

- `wireguard-go` to create the WireGuard interface in userspace
- `wg` from `wireguard-tools` to configure the tunnel
- a TUN interface created from userspace

This makes it possible to run WireGuard without building or installing a custom webOS kernel.

---

## Requirements

- Rooted LG webOS TV
- Homebrew Channel installed
- Homebrew root service available
- `/dev/net/tun` support on the TV
- Compatible CPU architecture

The bundled binaries in the current release are built for:

```text
linux/armv7 (32-bit ARM)
```

Check your TV architecture with:

```sh
uname -m
```

The bundled ARMv7 binaries also run on supported 64-bit LG TVs.

---

## Installation

### 1. Download or build the IPK

Release package:

```text
com.github.cfernande1470.wireguard_1.0.7_all.ipk
```

To package manually:

```sh
make package
```

Packaging requires `ares-package` from [webOSBrew's `ares-cli-rs`](https://github.com/webosbrew/ares-cli-rs).
The script uses `--force-arch all`, which LG's `@webos-tools/cli` does not support.
Set `ARES_PACKAGE=/path/to/ares-package` when it is not available on `PATH`. The package is forced to architecture `all` because
the bundled ARMv7 binaries run on both 32-bit and 64-bit TVs.

Output:

```text
dist/com.github.cfernande1470.wireguard_1.0.7_all.ipk
```

### 2. Install using Homebrew Channel

Copy or upload the IPK to your TV and install it from Homebrew Channel.

### 3. Open the app

After launching the app, press:

```text
Install / update
```

This prepares persistent state under:

```text
/var/lib/webosbrew/wireguard
```

The packaged binaries and scripts stay inside the application directory. The state directory only contains the
configuration, logs, runtime files, and symlinks to the packaged payload, so updates take effect without duplicating
the binaries. The app does not prepare this state automatically; this is intentional.

---

## First-time setup

1. Open the WireGuard app on the TV.
2. Press **Install / update**.
3. Press **Upload config**.
4. Open the displayed URL from a computer or phone on the same LAN.
5. Enter the displayed access code.
6. Upload your `wg0.conf`.
7. Return to the TV.
8. Press **Start VPN**.
9. Use **Status** or **Log** to verify the tunnel.

---

## Example configuration

A safe example configuration is included at:

```text
examples/wg0.example.conf
```

Copy it, replace the placeholder keys and endpoint, then upload your real `wg0.conf` from the app.

Do not commit real WireGuard configuration files. They contain private keys and private VPN details.

Example:

```ini
[Interface]
PrivateKey = REPLACE_WITH_CLIENT_PRIVATE_KEY
Address = 10.10.10.2/32

[Peer]
PublicKey = REPLACE_WITH_SERVER_PUBLIC_KEY
AllowedIPs = 0.0.0.0/0
Endpoint = vpn.example.invalid:51820
PersistentKeepalive = 25
```

---

## Configuration handling

The uploader accepts normal `wg-quick` style configuration files. It preserves supported DNS settings in `wg0.conf`; the start script removes these before calling `wg setconf`.

Handled fields:

```text
PrivateKey
ListenPort
FwMark
PublicKey
PresharedKey
AllowedIPs
Endpoint
PersistentKeepalive
Address
DNS
```

Ignored fields:

```text
MTU
Table
SaveConfig
PreUp
PostUp
PreDown
PostDown
```

`Address` is extracted and stored separately in:

```text
/var/lib/webosbrew/wireguard/conf/address
```

The converted WireGuard config is stored as:

```text
/var/lib/webosbrew/wireguard/conf/wg0.conf
```

A backup of every uploaded config is stored in:

```text
/var/lib/webosbrew/wireguard/uploads
```

This fork supports `DNS = 10.10.10.1, 1.1.1.1` in `[Interface]` (one to three
unicast IPv4 addresses). IPv6 DNS, search domains and split DNS are not supported;
unsupported values are rejected instead of being silently discarded.

After the tunnel routes are installed, each DNS server must resolve to `dev wg0`
according to `ip route get`. The peer must also allow that address in `AllowedIPs`.
A LAN or endpoint route taking precedence causes startup to fail before the
resolver is changed. No DNS-specific routes are added or existing routes replaced.

The runtime bind-mounts a generated, read-only resolver file over the resolved
`/etc/resolv.conf` target. It does not edit the original file, restart ConnMan, or
change persistent network/DHCP settings. Stop, failed startup, profile replacement
and runtime uninstall remove the mount. A failed restore keeps recovery state and
prevents uninstall/update; retry **Stop VPN** before removing the app. A reboot
removes the mount; autostart reapplies DNS from the config.

Configs without `DNS` keep the original resolver. If an earlier app version
stripped DNS during upload, upload the original provider config again.

This implementation is experimental and has not been verified on a physical TV.
It covers clients using this resolver file in the same mount namespace. Services
with another resolver path, cached DNS settings, or their own DoH/DoT may behave
differently. Verify native apps on the target firmware; this is not a system-wide
DNS leak-prevention guarantee.

Uploaded `MTU` values are ignored by the uploader. The start script uses a default tunnel MTU of `1420`.

---

## Full-tunnel routing

If your config contains:

```ini
AllowedIPs = 0.0.0.0/0
```

the start script converts it into split default routes:

```text
0.0.0.0/1
128.0.0.0/1
```

This avoids replacing the default route directly and tends to behave better on webOS.

The VPN endpoint is pinned outside the tunnel using the original default route, so WireGuard can still reach the server after the tunnel starts.

---

## IPv6 status

IPv6 routes and IPv6 addresses in the WireGuard configuration are currently
ignored. While the tunnel is active, IPv6 is disabled on the original network
interface to prevent traffic from bypassing an IPv4-only VPN. The previous
interface setting is restored when WireGuard stops.

---

## Autostart

The app can enable WireGuard autostart.

When enabled, it creates:

```text
/var/lib/webosbrew/init.d/90-wireguard
```

This is a symlink to the packaged `boot.sh`. At boot, the script waits for a default route and then starts WireGuard.
If the app is removed, the link becomes non-executable and the Homebrew hook runner skips it.

---

## Uninstall

The **Uninstall** button stops WireGuard and removes:

```text
/var/lib/webosbrew/wireguard
/var/lib/webosbrew/init.d/90-wireguard
/var/run/wireguard/wg0.sock
```

After that, you can remove the app from Homebrew Channel.

---

## Build notes

### Build `wg-upload`

```sh
cd uploader

GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 \
  go build -trimpath -ldflags="-s -w" \
  -o ../app/com.github.cfernande1470.wireguard/payload/wireguard/bin/wg-upload \
  ./wg-upload.go

cd ..
```

### Build `wireguard-go`

```sh
cd wireguard-go

GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 \
  go build -trimpath -ldflags="-s -w" \
  -o ../app/com.github.cfernande1470.wireguard/payload/wireguard/bin/wireguard-go .

cd ..
```

### Build for ARMv7

For all supported TVs:

```sh
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build ...
```

All bundled binaries must remain 32-bit ARM to support both 32-bit and 64-bit LG TVs.

Check binaries with:

```sh
file app/com.github.cfernande1470.wireguard/payload/wireguard/bin/*
```

---

## Troubleshooting

### Updating from 1.0.1

Run **Install / update** once. The installer stops the old runtime, removes the copied payload, replaces it with
symlinks to the packaged files, and preserves the existing configuration.

### `ERROR luna-call`

This usually means the Homebrew root service failed or rejected the command.

Check that Homebrew Channel and its root service are working.

From the TV over SSH:

```sh
luna-send -n 1 'luna://org.webosbrew.hbchannel.service/exec' '{"command":"id; echo OK"}'
```

### `interface was not created`

Check TUN support:

```sh
ls -l /dev/net/tun
```

### VPN starts but traffic does not route

Open **Routes** and **Log** from the app.

Check:

```sh
ip route
ip addr show wg0
wg show wg0
```

### Uploaded config works but DNS does not change

Re-upload the original config if it was imported with an older version. Check
that it has IPv4 `DNS` entries in `[Interface]`, then stop and start the VPN.
The start log must contain `VPN DNS resolver applied`.

On the TV, compare `cat /etc/resolv.conf` with the provider DNS addresses and run
`ip route get <dns-ip>` (it must use `wg0`). Check both name lookup and playback
in the native apps you use. An app may keep old answers until restarted.
If Stop reports a DNS restore error, retry it before uninstalling the app.

---

## Security notes

Your private key is stored on the TV in:

```text
/var/lib/webosbrew/wireguard/conf/wg0.conf
```

The upload server uses a random eight-character access code. It stops after one successful upload, five incorrect
codes, or ten minutes. The connection is still plain HTTP, so it should only be used on a trusted LAN.

Do not publish real `wg0.conf` files or logs containing private data.

---

## Project layout

```text
app/com.github.cfernande1470.wireguard/
  appinfo.json
  index.html
  css/
  js/
  lib/
  payload/wireguard/
    install.sh
    bin/
      wg
      wireguard-go
      wg-upload
    scripts/
      dns.sh
      start.sh
      stop.sh
      status.sh
      upload-start.sh
      upload-stop.sh
      autostart.sh
      boot.sh
      uninstall.sh

examples/
  wg0.example.conf

uploader/
  wg-upload.go

wireguard-go/
wireguard-tools/
```

---

## Tests

Run `make test` with Go and Docker available. Import tests exercise persisted
config output and rejected DNS values. Linux integration tests use real bind
mounts in a disposable container without network access; only TV network commands
are substituted. They cover start/stop, reconnect, a profile without DNS, failure
rollback and retaining recovery state when uninstall cannot restore DNS.

The suite also runs the existing descriptor-launch regression test. Tests use
synthetic keys, endpoints and DNS settings; no provider credentials are needed.

Before enabling autostart on a TV, verify a normal WG handshake and traffic,
provider DNS and native-app playback, Stop restoring the original resolver,
reconnect with a hostname endpoint, and recovery after standby/reboot. Do not
publish provider configs, private keys or unredacted configuration output.

## Changelog

### Unreleased: experimental DNS support

- Preserve and validate IPv4 DNS from uploaded configs.
- Apply a reversible resolver override after routes are ready and restore it on stop or startup failure.
- Keep recovery state if DNS cannot be restored; block runtime removal/update in that case.

### 1.0.8

- Fix unresponsive UI on older webOS engines by using ES5 variable declarations (thanks @mastrogippo, PR #8)
- Confirmed working by two users on webOS 3.9.3, including installation, configuration upload, VPN start/stop, root status and autostart
- Stable release uses the same IPK tested as 1.0.8-rc.1; the official catalog minimum remains webOS 5.0

### 1.0.7

- Close inherited service descriptors before launching WireGuard and the upload server
- Prevent background daemons from keeping Homebrew Luna connections open after the service exits
- Preserve daemon PID tracking, logs, and upload-server startup settings

### 1.0.6

- Updated project links and maintainer attribution to RodriguezCF
- Simplified the TV interface

### 1.0.5

Fixed:

- Prevent IPv6 traffic from bypassing IPv4-only VPN configurations
- Preserve and restore the original IPv6 state across tunnel restarts and failed starts
- Serialize start and stop operations to prevent overlapping tunnel processes
- Track the long-lived `wireguard-go` process instead of its short-lived launcher
- Wait up to 60 seconds for networking during autostart and report a clear timeout

### 1.0.3

Fixed:

- Add explicit five-way remote navigation for arrow keys and OK/Enter
- Handle the Back key for upload dialog

### 1.0.2

Changed:

- Keep binaries and scripts in the application directory and link to them instead of copying the payload
- Use a symlinked boot hook so removing the app cannot keep starting the VPN
- Preserve only configuration, logs, and runtime state under `/var/lib/webosbrew/wireguard`
- Replace the four-digit PIN with a random eight-character access code
- Stop the upload server after one successful upload or ten minutes, and lock it after five incorrect codes
- Package releases with the standard `ares-package` tool instead of a custom IPK builder

### 1.0.1

Fixed:

- Renamed the application and package consistently to `com.github.cfernande1470.wireguard`
- Rebuilt all bundled executables for 32-bit ARMv7 compatibility
- Added an MIT licence and reproducible build, package and release verification scripts

### 1.0.0

Initial stable release.

Added:

- LG webOS Homebrew app for WireGuard
- Userspace WireGuard support through `wireguard-go`
- Bundled `wg`, `wireguard-go` and `wg-upload`
- Manual Install / update workflow
- No automatic payload installation on app startup
- Bilingual UI: English and Spanish
- Bilingual upload web server
- PIN-protected `wg0.conf` upload
- Start, stop, status, routes, config and log actions
- Optional autostart on boot
- Runtime uninstall action

Notes:

- WireGuard runs in userspace because the WireGuard kernel module is not normally available on LG webOS stock kernels.
- Current bundled binaries are built for 32-bit `linux/armv7`.
- IPv6 and DNS handling are not implemented in this release.

---

## License

The app-specific code is licensed under the [MIT License](LICENSE). Bundled WireGuard components retain their own licences:

```text
wireguard-go/LICENSE
wireguard-tools/COPYING
```

---

# Español

Cliente WireGuard para televisores LG webOS con root y Homebrew Channel.

Este proyecto proporciona una app sencilla para webOS que permite instalar, configurar, arrancar, parar y monitorizar un túnel VPN WireGuard en LG webOS. Usa WireGuard en userspace mediante `wireguard-go`, porque los kernels stock de LG webOS normalmente no incluyen el módulo WireGuard en el kernel.

La app está pensada para televisores con root y Homebrew Channel instalado.

---

## Funciones

- Túnel WireGuard en userspace usando `wireguard-go`
- Binarios incluidos: `wg`, `wireguard-go` y `wg-upload`
- Interfaz adaptada a TV
- Interfaz en inglés y español
- Flujo manual de **Instalar / actualizar**
- Sin instalación automática del payload al abrir la app
- Servidor web temporal para subir `wg0.conf`
- Subida temporal protegida por un código aleatorio, con caducidad y bloqueo tras varios intentos fallidos
- Acciones de arrancar, parar, estado, rutas, configuración y log
- Inicio automático opcional al arrancar
- Acción de desinstalación del runtime

---

## ¿Por qué WireGuard en userspace?

En Linux normal, WireGuard suele funcionar como módulo del kernel.

En televisores LG webOS, el módulo WireGuard normalmente no está disponible en el kernel de fábrica. Por eso este proyecto usa:

- `wireguard-go` para crear la interfaz WireGuard desde userspace
- `wg` de `wireguard-tools` para configurar el túnel
- una interfaz TUN creada desde userspace

Así se puede usar WireGuard sin compilar ni instalar un kernel personalizado para webOS.

---

## Requisitos

- TV LG webOS con root
- Homebrew Channel instalado
- Servicio root de Homebrew disponible
- Soporte para `/dev/net/tun` en la TV
- Arquitectura de CPU compatible

Los binarios incluidos actualmente están compilados para:

```text
linux/armv7 (32-bit ARM)
```

Comprueba la arquitectura de tu TV con:

```sh
uname -m
```

Los binarios ARMv7 incluidos también funcionan en televisores LG de 64 bits compatibles.

---

## Instalación

### 1. Descargar o compilar el IPK

Paquete de release:

```text
com.github.cfernande1470.wireguard_1.0.7_all.ipk
```

Para empaquetar manualmente:

```sh
make package
```

El empaquetado requiere `ares-package` de [webOSBrew `ares-cli-rs`](https://github.com/webosbrew/ares-cli-rs).
El script usa `--force-arch all`, que `@webos-tools/cli` de LG no admite.
Si no está disponible en `PATH`, usa `ARES_PACKAGE=/ruta/a/ares-package`.
Se fuerza la arquitectura `all` porque los binarios ARMv7 incluidos funcionan
en televisores de 32 y 64 bits.

Resultado:

```text
dist/com.github.cfernande1470.wireguard_1.0.7_all.ipk
```

### 2. Instalar usando Homebrew Channel

Copia o sube el IPK a la TV e instálalo desde Homebrew Channel.

### 3. Abrir la app

Después de abrir la app, pulsa:

```text
Instalar / actualizar
```

Esto prepara el estado persistente en:

```text
/var/lib/webosbrew/wireguard
```

Los binarios y scripts permanecen dentro del directorio de la aplicación. El directorio de estado solo contiene la
configuración, los logs, los datos de ejecución y enlaces al payload empaquetado, por lo que las actualizaciones se
aplican sin duplicar los binarios. La app no prepara este estado automáticamente; esto es intencionado.

---

## Primer uso

1. Abre la app WireGuard en la TV.
2. Pulsa **Instalar / actualizar**.
3. Pulsa **Subir config**.
4. Abre la URL mostrada desde un ordenador o móvil en la misma LAN.
5. Introduce el código de acceso mostrado.
6. Sube tu `wg0.conf`.
7. Vuelve a la TV.
8. Pulsa **Arrancar VPN**.
9. Usa **Estado** o **Log** para verificar el túnel.

---

## Configuración de ejemplo

Se incluye una configuración de ejemplo segura en:

```text
examples/wg0.example.conf
```

Cópiala, sustituye las claves y el endpoint de ejemplo, y luego sube tu `wg0.conf` real desde la app.

No subas configuraciones WireGuard reales al repositorio. Contienen claves privadas y datos privados de tu VPN.

Ejemplo:

```ini
[Interface]
PrivateKey = REPLACE_WITH_CLIENT_PRIVATE_KEY
Address = 10.10.10.2/32

[Peer]
PublicKey = REPLACE_WITH_SERVER_PUBLIC_KEY
AllowedIPs = 0.0.0.0/0
Endpoint = vpn.example.invalid:51820
PersistentKeepalive = 25
```

---

## Gestión de la configuración

El uploader acepta configuraciones normales de tipo `wg-quick`. Conserva el DNS compatible en `wg0.conf`; el script de arranque lo elimina antes de llamar a `wg setconf`.

Campos gestionados:

```text
PrivateKey
ListenPort
FwMark
PublicKey
PresharedKey
AllowedIPs
Endpoint
PersistentKeepalive
Address
DNS
```

Campos ignorados:

```text
MTU
Table
SaveConfig
PreUp
PostUp
PreDown
PostDown
```

`Address` se extrae y se guarda aparte en:

```text
/var/lib/webosbrew/wireguard/conf/address
```

La configuración convertida se guarda como:

```text
/var/lib/webosbrew/wireguard/conf/wg0.conf
```

Cada configuración subida se copia como backup en:

```text
/var/lib/webosbrew/wireguard/uploads
```

Este fork admite `DNS = 10.10.10.1, 1.1.1.1` en `[Interface]` (de una a tres
IPv4 unicast). Se rechazan DNS IPv6, dominios de búsqueda y más de tres servidores.
No hay soporte de DNS dividido.

Después de instalar las rutas, `ip route get` debe dirigir cada DNS por `wg0`.
El peer también debe incluir esa dirección en `AllowedIPs`. Una ruta LAN o de
endpoint que tenga prioridad provoca un error antes de cambiar el resolver.
No se añaden rutas especiales para DNS ni se sustituyen las existentes.

El runtime monta un archivo de resolver de solo lectura sobre el destino real de
`/etc/resolv.conf`, sin editar el original, reiniciar ConnMan ni cambiar DHCP.
Al parar, fallar el arranque, cambiar de perfil o desinstalar el runtime se retira
el montaje. Si falla la restauración, se conserva el estado y se impide
actualizar/desinstalar: vuelve a pulsar **Parar VPN** antes de eliminar la app.
El montaje desaparece al reiniciar; el inicio automático vuelve a aplicarlo.
Sin `DNS` se mantiene el resolver original. Vuelve a subir el archivo original
si una versión anterior había descartado su DNS.

Implementación experimental, aún sin verificar en una TV física. Cubre clientes
que usan este archivo en el mismo espacio de montajes. Servicios con otra ruta de
resolución, cachés o DoH/DoT propio pueden comportarse de forma distinta.
Hay que probar las apps nativas en el firmware de destino; no garantiza evitar
todas las fugas DNS.

Los valores `MTU` subidos se ignoran en el uploader. El script de arranque usa una MTU de túnel por defecto de `1420`.

---

## Rutas full tunnel

Si tu configuración contiene:

```ini
AllowedIPs = 0.0.0.0/0
```

el script de arranque lo convierte en dos medias rutas:

```text
0.0.0.0/1
128.0.0.0/1
```

Esto evita reemplazar directamente la ruta por defecto y suele funcionar mejor en webOS.

La ruta hacia el endpoint VPN se fija fuera del túnel usando la ruta por defecto original, para que WireGuard pueda seguir llegando al servidor después de levantar el túnel.

---

## Estado de IPv6

Las rutas y direcciones IPv6 de la configuración de WireGuard se ignoran por
ahora. Mientras el túnel está activo, IPv6 se desactiva en la interfaz de red
original para evitar que el tráfico se escape de una VPN que sólo admite IPv4.
Al detener WireGuard se restaura el valor anterior de la interfaz.

---

## Inicio automático

La app puede activar WireGuard al arrancar.

Cuando está activado, crea:

```text
/var/lib/webosbrew/init.d/90-wireguard
```

Es un enlace al `boot.sh` incluido en la app. Durante el arranque, el script espera a que exista una ruta por defecto
y luego inicia WireGuard. Si se elimina la app, el enlace deja de ser ejecutable y Homebrew lo ignora.

---

## Desinstalación

El botón **Desinstalar** detiene WireGuard y elimina:

```text
/var/lib/webosbrew/wireguard
/var/lib/webosbrew/init.d/90-wireguard
/var/run/wireguard/wg0.sock
```

Después puedes eliminar la app desde Homebrew Channel.

---

## Notas de compilación

### Compilar `wg-upload`

```sh
cd uploader

GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 \
  go build -trimpath -ldflags="-s -w" \
  -o ../app/com.github.cfernande1470.wireguard/payload/wireguard/bin/wg-upload \
  ./wg-upload.go

cd ..
```

### Compilar `wireguard-go`

```sh
cd wireguard-go

GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 \
  go build -trimpath -ldflags="-s -w" \
  -o ../app/com.github.cfernande1470.wireguard/payload/wireguard/bin/wireguard-go .

cd ..
```

### Compilar para ARMv7

Para todos los televisores compatibles:

```sh
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build ...
```

Todos los binarios incluidos deben seguir siendo ARM de 32 bits para admitir televisores LG de 32 y 64 bits.

Comprueba los binarios con:

```sh
file app/com.github.cfernande1470.wireguard/payload/wireguard/bin/*
```

---

## Solución de problemas

### Actualizar desde 1.0.1

Pulsa **Instalar / actualizar** una vez. El instalador detiene el runtime antiguo, elimina la copia del payload, la
sustituye por enlaces a los archivos incluidos en la app y conserva la configuración existente.

### `ERROR luna-call`

Normalmente significa que el servicio root de Homebrew ha fallado o ha rechazado el comando.

Comprueba que Homebrew Channel y su servicio root funcionan.

Desde SSH en la TV:

```sh
luna-send -n 1 'luna://org.webosbrew.hbchannel.service/exec' '{"command":"id; echo OK"}'
```

### `interface was not created`

Comprueba soporte TUN:

```sh
ls -l /dev/net/tun
```

### La VPN arranca pero el tráfico no va por el túnel

Abre **Rutas** y **Log** desde la app.

Comprueba:

```sh
ip route
ip addr show wg0
wg show wg0
```

### La configuración sube bien pero DNS no cambia

Vuelve a subir el archivo original si se importó con una versión anterior.
Comprueba las entradas IPv4 `DNS` en `[Interface]` y para/arranca la VPN.
El log debe mostrar `VPN DNS resolver applied`.

Compara `cat /etc/resolv.conf` con el DNS del proveedor y ejecuta
`ip route get <dns-ip>` (debe usar `wg0`). Prueba resolución y reproducción en las
apps nativas; reinícialas si conservan respuestas antiguas. Si falla la
restauración de DNS, vuelve a parar la VPN antes de eliminar la app.

---

## Seguridad

Tu clave privada se guarda en la TV en:

```text
/var/lib/webosbrew/wireguard/conf/wg0.conf
```

El servidor de subida usa un código de acceso aleatorio de ocho caracteres. Se detiene tras una subida correcta,
cinco códigos incorrectos o diez minutos. La conexión sigue siendo HTTP sin cifrar, por lo que solo debe usarse en
una LAN de confianza.

No publiques configuraciones reales ni logs con datos privados.

---

## Estructura del proyecto

```text
app/com.github.cfernande1470.wireguard/
  appinfo.json
  index.html
  css/
  js/
  lib/
  payload/wireguard/
    install.sh
    bin/
      wg
      wireguard-go
      wg-upload
    scripts/
      dns.sh
      start.sh
      stop.sh
      status.sh
      upload-start.sh
      upload-stop.sh
      autostart.sh
      boot.sh
      uninstall.sh

examples/
  wg0.example.conf

uploader/
  wg-upload.go

wireguard-go/
wireguard-tools/
```

---

## Pruebas

Ejecuta `make test` con Go y Docker disponibles. Se comprueban la importación,
los montajes reales de Linux y su restauración, reconexión, errores de arranque y
la conservación del estado si no se puede restaurar DNS. El contenedor no tiene
acceso a la red y usa datos sintéticos. También se ejecuta la prueba de
lanzamiento sin descriptores heredados.

Antes de activar el inicio automático, prueba en la TV el handshake, DNS y apps
nativas, la restauración al parar, reconexión con un endpoint por nombre y
recuperación después de standby/reinicio.

## Registro de cambios

### Sin publicar: DNS experimental

- Conserva y valida DNS IPv4 de los archivos subidos.
- Aplica un resolver reversible y lo restaura al parar o fallar el arranque.
- Impide eliminar/actualizar el runtime si no se ha podido restaurar DNS.

### 1.0.8

- Corregida la interfaz que no respondía en motores antiguos de webOS usando declaraciones de variables ES5 (gracias a @mastrogippo, PR #8)
- Dos usuarios confirman su funcionamiento en webOS 3.9.3, incluida la instalación, subida de configuración, inicio/parada de VPN, estado root y arranque automático
- La versión estable usa el mismo IPK probado como 1.0.8-rc.1; el mínimo oficial del catálogo sigue siendo webOS 5.0

### 1.0.7

- Cerrados los descriptores heredados del servicio antes de iniciar WireGuard y el servidor de subida
- Evitado que los procesos en segundo plano mantengan abiertas las conexiones Luna tras salir el servicio de Homebrew
- Conservados el seguimiento del PID, los logs y los ajustes de arranque del servidor de subida

### 1.0.6

- Actualizados los enlaces del proyecto y la atribución a RodriguezCF
- Simplificada la interfaz de la TV

### 1.0.5

Corregido:

- Evitado que el tráfico IPv6 escape de configuraciones VPN que sólo admiten IPv4
- Conservado y restaurado el estado IPv6 original tras reinicios del túnel y arranques fallidos
- Serializadas las operaciones de inicio y parada para evitar procesos de túnel solapados
- Registrado el proceso persistente de `wireguard-go` en lugar de su lanzador temporal
- Ampliada a 60 segundos la espera de red del inicio automático, con un error claro al agotarse

### 1.0.3

Corregido:

- Añadida navegación explícita con las flechas del mando y activación con OK/Enter

### 1.0.2

Cambios:

- Los binarios y scripts permanecen en la aplicación y se enlazan en lugar de copiar el payload
- El hook de arranque es un enlace, así que eliminar la app no puede seguir iniciando la VPN
- Solo la configuración, los logs y el estado de ejecución permanecen en `/var/lib/webosbrew/wireguard`
- El PIN de cuatro cifras se sustituye por un código de acceso aleatorio de ocho caracteres
- El servidor se detiene tras una subida correcta o diez minutos y se bloquea tras cinco códigos incorrectos
- Las releases se empaquetan con la herramienta estándar `ares-package` en lugar de un generador IPK propio

### 1.0.1

Corregido:

- Renombrado coherente de la aplicación y el paquete a `com.github.cfernande1470.wireguard`
- Recompilación de todos los ejecutables incluidos para ARMv7 de 32 bits
- Añadida la licencia MIT y scripts reproducibles de compilación, empaquetado y verificación

### 1.0.0

Primera versión estable.

Añadido:

- App LG webOS Homebrew para WireGuard
- Soporte WireGuard en userspace mediante `wireguard-go`
- Binarios incluidos: `wg`, `wireguard-go` y `wg-upload`
- Flujo manual de Instalar / actualizar
- Sin instalación automática del payload al abrir la app
- Interfaz bilingüe: inglés y español
- Servidor web de subida bilingüe
- Subida de `wg0.conf` protegida por PIN
- Acciones de arrancar, parar, estado, rutas, configuración y log
- Inicio automático opcional al arrancar
- Acción de desinstalación del runtime

Notas:

- WireGuard se ejecuta en userspace porque el módulo WireGuard del kernel no suele estar disponible en kernels stock de LG webOS.
- Los binarios incluidos actualmente están compilados para `linux/armv7` de 32 bits.
- IPv6 y gestión de DNS no están implementados en esta versión.

---

## Licencia

El código específico de la app está publicado bajo la [licencia MIT](LICENSE). Los componentes de WireGuard incluidos conservan sus propias licencias:

```text
wireguard-go/LICENSE
wireguard-tools/COPYING
```
