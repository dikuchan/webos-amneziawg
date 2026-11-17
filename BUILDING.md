# Building

Prebuilt packages are distributed through [Releases](https://github.com/dikuchan/webos-amneziawg/releases). Follow this guide to develop the app or build a release from source.

## Prerequisites

- Go **1.26.3**, Bun **1.4.4**, Node.js, Git and Make.
- Docker with support for running **Linux ARM64** containers, natively or through emulation.
- For browser tests, Playwright's Chromium and WebKit builds.

Node.js runs the webOS packager. Bun and tool versions are pinned in `package.json`, `bun.lock`, the Makefile and CI configuration. Build tools are only needed on the development computer.

## Setup

```sh
git clone --recurse-submodules https://github.com/dikuchan/webos-amneziawg.git
cd webos-amneziawg
make setup
export PATH="$PWD/.build/tools:$PATH"
```

For an existing checkout, initialize dependencies with `git submodule update --init --recursive`. `make setup` installs frontend dependencies and local Go/shell tools, then builds the frontend. Dependencies stay at the commits recorded by Git.

A complete release source archive already includes the dependencies; extract it and run `make setup` without the submodule step.

## Build packages

```sh
make package
```

This builds the current sources and writes:

| File | Contents |
| --- | --- |
| `dist/webos-amneziawg_0.1.0_all.ipk` | TV app with ARMv7 native binaries |
| `dist/webos-amneziawg-0.1.0-armv7.tar.gz` | SSH-only installation: binaries, scripts and licenses |
| `dist/SHA256SUMS` | SHA-256 checksums |

`VERSION` is the single application version. It supplies the controller's version, generated app metadata and artifact filenames. `packaging/appinfo.json` is a template.

Intermediate files go into `.build/`; the frontend build also stages embedded LAN resources in `app/.build/lan`. Both directories are ignored by Git. C compiler output is saved in `.build/builder.log` and `.build/awg-build.log`.

## Source layout

- `app/`: Go controller and unit tests; embedded routing data in `app/presets/`.
- `web/`: TV interface and LAN editor sources.
- `tests/`: integration tests and stand utilities.
- `packaging/` and `scripts/`: TV scripts, build and packaging tools.

`go.mod` stays at the repository root. Run Go tests with `go test ./app ./tests/...` after `make ui`, which prepares the embedded LAN resources. `make check` handles this automatically.

## Target architecture

Release packages use Linux ARMv7 with VFPv3-D16. Both Go binaries have `CGO_ENABLED=0`; `awg` is statically linked with musl. They do not depend on the TV's glibc version. C builds enable `-Wall -Wextra -Werror` and fatal linker warnings.

One ARMv7 release target is the baseline for webOS TVs, including compatible devices with a 64-bit kernel. A kernel reporting `aarch64` does not by itself require an ARM64 application: webOS homebrew still uses a [32-bit ARM toolchain](https://github.com/webosbrew/native-toolchain), and the [64-bit bridge project](https://github.com/webosbrew/webos-bridge-64to32) documents the existing 32-bit userspace. Hardware and firmware compatibility still need testing on each model.

No firmware or CPU-model build matrix is used for this release. The scripts also build ARM64 binaries for the isolated Linux integration stand; these are not packaged as a second TV release. The Docker builder's architecture is a build-host requirement, separate from the TV target.

## Checks

```sh
make check
bun x playwright install --with-deps chromium webkit
make test-web
make test-integration
```

| Command | Checks |
| --- | --- |
| `make fmt` | Format application sources |
| `make check` | Formatting, Go lint on host and ARMv7, ShellCheck, strict TypeScript/ESLint, Go race tests and frontend unit tests |
| `make test-web` | TV navigation and state at 1080p/720p in Chromium; LAN editor in mobile WebKit |
| `make test-integration` | Real AWG peers, routing updates, ownership and recovery in disposable Linux containers |

The integration stand uses an internal Docker network and synthetic credentials. Containers need `NET_ADMIN` and `/dev/net/tun`; no TV access or real VPN profile is used. `AWG_BUILDER_IMAGE` and `AWG_TEST_IMAGE` override local image names.

To test an extracted IPK's UI, point `AWG_PREVIEW_DIR` at its application directory before running `make test-web`. Browser output and failure traces are under `.build/test-results`. Device tests are still needed for webOS permissions, trust roots, cold boot and DNS restoration.

## Release

Set `VERSION`, update documentation and commit the changes. From a clean committed checkout:

```sh
make release
```

This runs checks and browser tests, builds the packages and creates `dist/webos-amneziawg-0.1.0-source.tar.gz`, containing the repository and all pinned dependency sources. `make source` creates only the source archive; it requires Git and a clean checkout.

Upload the IPK, ARMv7 archive, complete source archive and `SHA256SUMS` to a GitHub release tagged `v0.1.0`. Include the complete source archive with binary releases: GitHub's automatically generated source archives omit submodules. Verify downloads with `sha256sum -c SHA256SUMS` or `shasum -a 256 -c SHA256SUMS`.
