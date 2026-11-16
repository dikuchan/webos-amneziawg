# Amnezia icon

The unmodified source image in `web/amnezia-icon.png` is from AmneziaVPN:

- Repository: https://github.com/amnezia-vpn/amnezia-client
- Commit: `5aff2ce5809514ef7c292ac5e7ed3758d7d0b333`
- Asset: `client/images/icon.png`
- Source: https://raw.githubusercontent.com/amnezia-vpn/amnezia-client/5aff2ce5809514ef7c292ac5e7ed3758d7d0b333/client/images/icon.png
- Repository license: GNU GPL v3, included as `amnezia-client-GPL-3.0.txt`.

`largeIcon.png` is the original 256px image. `icon.png` is resized to 130px for the webOS launcher. No artwork has been redrawn. Amnezia trademarks belong to their respective owners. This webOS client is an independent project.

## Interface icons

Lucide Icons, ISC license, source files retained in `web/icons/`.
Pinned commit: `333574bdecb43e7cb4a4bb1cd21d1367cfbbee2c`.
Source: https://github.com/lucide-icons/lucide/tree/333574bdecb43e7cb4a4bb1cd21d1367cfbbee2c/icons
License: `lucide-ISC.txt`. SVGs are embedded inline to inherit the interface color.

## Palette

Mauve Dark + Iris Dark from Radix Colors: https://www.radix-ui.com/colors
`#121113` background, `#1a191b` surfaces, `#eeeef0` text, `#b5b2bc` secondary text, `#5b5bd6` accent, `#b1a9ff` focus.

## Fonts

Fonts are bundled locally as static WOFF2 files with Latin and Cyrillic glyphs. No CDN requests are made at runtime.

- Inter Regular/Medium: https://github.com/rsms/inter/tree/353b61b9f4430d5f420d56605a6e7993e0941470/docs/font-files — SIL OFL 1.1, `Inter-OFL.txt`.
- JetBrains Mono Regular: https://github.com/JetBrains/JetBrainsMono/tree/19371302b95d218af43299bce79ddbddd0bc364d/fonts/webfonts — SIL OFL 1.1, `JetBrainsMono-OFL.txt`.

Font files are unmodified. Their combined size is 317996 bytes.

## Routing data

RU Direct Standard IPv4 list: https://github.com/kyoresuas/ru-direct
Bundled release commit: `7e14508232595bac5881aa25d31202843a3f6021`.
The list and its manifest are embedded in the controller. MIT license: `ru-direct-MIT.txt`.
Automatic updates use the project's release branch via GitHub or jsDelivr.
