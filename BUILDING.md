# Building

Requirements: Go 1.26.3, Bun 1.4.4, Node.js for the webOS packager, and Docker for the C binaries and network stand. No build tools are installed on the TV.

## Checkout and setup

```sh
git clone --recurse-submodules https://github.com/dikuchan/webos-amneziawg.git
cd webos-amneziawg
make setup
```

For an existing checkout, run `git submodule update --init --recursive` first. `make setup` installs the pinned Go/shell tools into `.build/tools`, frontend packages from `bun.lock`, and builds the embedded LAN script. ShellCheck archives are verified against their release SHA-256. Dependency lifecycle scripts are disabled.

Bun 1.4.4 supports the scoped `fstream` → `mkdirp` override used here. That old packager dependency needs mkdirp's callback API, while the webOS CLI itself needs mkdirp 3. The override keeps both versions without patching upstream sources.

## Layout

- The controller is one Go package in the repository root; its unit tests are beside the source files.
- `web/` contains the TV UI, LAN editor, CSS, assets and frontend unit tests. `state.ts`, `protocol.ts` and `i18n.ts` have no browser I/O; `bridge.ts` owns the webOS API boundary.
- `tests/` contains browser tests and the isolated Linux network fixtures.
- `packaging/` contains app metadata and the TV boot/DNS scripts. `VERSION` is the application version; packaging and Go build metadata are generated from it.
- `scripts/` contains build and packaging commands; `.build/` contains all intermediate output; `dist/` contains release artifacts. Neither output directory is committed.
- `dependencies/` contains unmodified upstream submodules. Git records each dependency's exact commit. Build-generated version files are written to a copy under `.build/src`.

| Component | Version | Commit |
| --- | --- | --- |
| amneziawg-go | v3.1.20261116 | `08fe3d33bbb116be09a6183d02609311b8b0c9e8` |
| amneziawg-tools | v3.1.20260812 | `ee0f0a9aa34ff0a0da4b3433b9512781cfe02843` |
| musl | 1.2.6 | `9fa28ece75d8a2191de7c5bb53bed224c5947417` |

Keep application code and source dependencies in separate formatting scopes. Update a submodule explicitly and review its changed commit; builds do not update dependencies.

## Checks

```sh
make fmt
make check
bun x playwright install chromium webkit
make test-web
```

`make fmt` runs gofumpt/goimports, shfmt and Prettier, including Go HTML templates. It preserves Markdown wrapping and upstream assets. `make check` checks formatting, golangci-lint, ShellCheck, strict TypeScript/ESLint, Go tests with the race detector, and Bun unit tests. Warnings fail checks. Go commands target the application and `tests/` explicitly, excluding upstream examples and `node_modules`.

`make test-web` uses Chromium at 1920×1080 and 1280×720, plus mobile WebKit for the LAN editor. The TV tests replace only the external webOS bridge. The LAN tests use the actual Go handler and templates with an in-memory backend; they never change networking or import a real provider profile. Both fixture servers bind to localhost and are bounded. Screenshots and failure traces are stored under `.build/test-results`.

The unit suite protects profile validation, exact route ownership, independent source intervals, integrity checks, migration and recovery. Frontend tests protect status decoding, locale precedence, transfer units, remote focus/Back, startup status, web settings and LAN form submission. There is no coverage target or per-function test requirement.

Go analysis checks both the host platform and the TV's Linux/ARMv7 target, including the Linux-only netlink implementation.

## Build and package

```sh
make package
```

`make build` writes ARMv7 controller/daemon/awg binaries into `.build/armv7/bin` and ARM64 stand binaries into `.build/arm64/bin`. The TV `awg` is statically linked with musl; the ARM64 stand utility uses the builder's libc. C builds enable `-Wall -Wextra -Werror` and fatal linker warnings. Docker containers have no host mounts or TV access and are removed after building. Build logs are in `.build/`.

`make package` builds current sources and assembles the application under `.build/app/com.github.dikuchan.amneziawg`. It copies licenses from pinned dependencies and bundled assets automatically, then produces:

- `dist/webos-amneziawg_0.4.0_all.ipk`
- `dist/webos-amneziawg-0.4.0-armv7.tar.gz`
- `dist/SHA256SUMS`

The C builder requires Docker support for `linux/arm64`. The packager takes the application version from `VERSION`; `packaging/appinfo.json` is a template. To check UI extracted from an IPK, set `AWG_PREVIEW_DIR` to its application directory and run `make test-web`.

The application ID remains `com.github.dikuchan.amneziawg`; runtime state and migration behavior are unchanged. The static fonts and original Amnezia artwork are source assets and must not be regenerated.

From a clean committed checkout, `make source` also creates a complete source archive with every pinned dependency. `make release` runs checks, browser tests, packaging and source export. Distribute that archive alongside the binaries; a regular GitHub source archive does not include submodules. Source archives can build binaries directly without a Git checkout; exporting a new release archive requires Git.

## Linux network stand

```sh
make test-integration
```

This builds the binaries and uses disposable ARM64 Linux containers on an internal Docker bridge `172.31.222.0/24`, with synthetic credentials. Only `NET_ADMIN` and `/dev/net/tun` are required. The script cleans up its own containers, network and temporary files. `AWG_BUILDER_IMAGE` and `AWG_TEST_IMAGE` can select local images.

The stand exercises real encrypted packets, authenticated LAN editing, multiple HTTPS/local TXT sources, include/exclude switching, checksum and conditional HTTP validation, temporary download routes, cached startup, updater cancellation, LAN protection including `/0`, route collisions and idempotent Stop. A private test CA is validated normally; no TLS verification is disabled.

The older scripts can run separately on a prepared disposable client: `tests/network.sh` checks handshake, MTU and ordinary WG guards; `tests/failed-handshake.sh` checks rollback after the peer stops; `tests/autostart.sh` checks successful, failed and cancelled boot connections. Copy `packaging/{boot,dns}.sh` into the client's `/tmp/amneziawg-kit/scripts` and its native binaries into `/tmp/amneziawg-kit/bin`. `tests/lifecycle.sh` additionally needs a disposable environment that permits DNS bind mounts.

TV verification is still needed for HBC permissions, firmware compatibility, trust roots, resolver recovery, cold boot and native video applications. Local browser checks cannot reproduce the older webOS engine.
