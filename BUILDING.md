# Building

Requirements: Go 1.26+, Node.js/npm and Docker. The bundled `awg` utility is built statically with musl for ARMv7. Node.js and Go are build tools; they are not required on the TV.

## Controller, daemon and UI

```sh
npm install --ignore-scripts --no-audit --no-fund
sh tools/build-go.sh
npm run build:ui
```

`tools/build-go.sh` produces ARMv7 controller/daemon binaries in `kit/bin` and ARM64 binaries in `test-bin` for the Linux integration stand. Their source pins are:

| Component | Version | Commit |
| --- | --- | --- |
| amneziawg-go | v3.1.20261116 | `08fe3d33bbb116be09a6183d02609311b8b0c9e8` |
| amneziawg-tools | v3.1.20260812 | `ee0f0a9aa34ff0a0da4b3433b9512781cfe02843` |
| musl | 1.2.6 | Included source distribution |

Sources and upstream licenses are retained in `third_party`. Static Inter and JetBrains Mono fonts and the original Amnezia icon are already included. Do not regenerate the icon from a drawing.

## Static ARMv7 awg

The builder does not need privileged mode, host mounts or TV access. From a committed checkout:

```sh
docker build -t webos-amneziawg-builder -f tests/Dockerfile .
docker create --name awg-arm-build webos-amneziawg-builder sh -c 'mkdir -p /source/kit/bin && exec sh /source/tools/build-musl-awg.sh'
git archive --format=tar --prefix=source/ HEAD | docker cp - awg-arm-build:/
docker start -a awg-arm-build
docker cp awg-arm-build:/source/kit/bin/awg kit/bin/awg
docker rm awg-arm-build
```

The build enables `-Wall -Wextra -Werror` and `--fatal-warnings`. It uses Linux UAPI headers with musl, without glibc runtime dependencies. Generated upstream version metadata is included; the protocol implementation is unchanged.

## Package

```sh
PATH="$PWD/node_modules/.bin:$PATH" sh tools/package.sh
```

This uses the pinned `@webos-tools/cli` packager and produces:

- `dist/webos-amneziawg_0.3.1_all.ipk`
- `dist/webos-amneziawg-0.3.1-armv7.tar.gz`
- `dist/SHA256SUMS`

The internal app ID remains `com.github.dikuchan.amneziawgtest` so an update reuses the existing installation. Generated binaries, payload copies and packages are excluded from Git.

## Checks

```sh
go test -race ./controller
go vet ./controller
npx tsc -p tsconfig.json
npx tsc -p tests/tsconfig.preview.json
npx tsc -p tests/tsconfig.i18n.json
node -e 'const fs=require("node:fs"),vm=require("node:vm");vm.runInThisContext(fs.readFileSync("tests/i18n-check/web/i18n.js","utf8")+"\n"+fs.readFileSync("tests/i18n-check/tests/i18n.test.js","utf8"));'
```

Go tests use a local HTTP listener. They cover profile validation, private operation logs, boot-hook ownership, one-time uploads, routing-source integrity, update intervals and exact ownership of routes. Run ShellCheck on changed shell scripts.

For a browser preview, serve the built app with the separately compiled `tests/mock-bridge.ts` loaded before `i18n.js` and `app.js`. Keep the mock outside the packaged app. Check remote navigation, focus, Back, both languages, import completion, routing settings and logs at 1920×1080 and 1280×720. Check the UI extracted from the final IPK after minification.

## Linux network stand

Use disposable ARM64 Linux containers on an internal Docker bridge `172.31.222.0/24`: server `.2`, client `.3`. Only `NET_ADMIN` and `/dev/net/tun` are required. Do not use a real provider profile.

Place native ARM64 `awg-test`, `amneziawg-go` and `awg` binaries in `/source/test-bin` and the scripts under `/source/tests`. Run `tests/server.sh` on the server and copy its synthetic `/tmp/awg-server/client.conf` to the client's `/tmp/client.conf`.

- `tests/network.sh` checks a real handshake, encrypted traffic, MTU, the ordinary WG guard and recovery.
- `tests/failed-handshake.sh` runs after stopping the server and checks failed-start rollback.
- `tests/split-network.sh` checks the bundled bypass list, direct and tunneled packets, live switching, route collisions, updater shutdown and cleanup. Add `5.45.192.1/32` and `203.0.113.1/32` to the server's loopback first. Run on a fresh client; the script ends with a LAN upload server ready for a multipart POST from the other container.
- `tests/autostart.sh` checks successful, failed and cancelled boot connections.

The ordinary network tests use a profile without DNS and do not exercise resolver bind mounts. `tests/lifecycle.sh` covers actual DNS mounts and needs a suitable disposable Linux environment. TV validation is still needed for HBC permissions, firmware compatibility, HTTPS trust roots, resolver recovery, cold boot and native video applications.
