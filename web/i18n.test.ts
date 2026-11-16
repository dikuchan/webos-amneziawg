import { expect, test } from "bun:test";

import { formatTraffic, resolveUILanguage } from "./i18n";

// Pure presentation contracts: locale precedence and localized byte counters.

// Arrange / Act / Assert: explicit selection wins, otherwise use system locale.
for (const [saved, system, expected] of [
  ["ru", "en-US", "ru"],
  ["en", "ru-RU", "en"],
  [null, "ru_RU", "ru"],
  [null, "en-GB", "en"],
  ["obsolete", "RU-ru", "ru"],
  [null, "de-DE", "en"],
  [null, "ru<script>", "en"],
] as const)
  test(`locale ${String(saved)} / ${system}`, () => {
    expect(resolveUILanguage(saved, system)).toBe(expected);
  });

// Arrange / Act / Assert: units and decimals follow the UI language, with a
// bounded number of digits even for large counters and an honest missing value.
for (const [bytes, language, expected] of [
  [null, "ru", "—"],
  [0, "en", "0 B"],
  [1024, "en", "1.0 KB"],
  [1536, "ru", "1,5 КБ"],
  [1048576, "ru", "1,0 МБ"],
  [1099511627776, "en", "1.0 TB"],
] as const)
  test(`traffic ${String(bytes)} / ${language}`, () => {
    expect(formatTraffic(bytes, language)).toBe(expected);
  });
