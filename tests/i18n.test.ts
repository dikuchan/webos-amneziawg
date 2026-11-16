// Pure presentation contracts: locale precedence and localized byte counters.
function expectEqual(actual: string, expected: string, scenario: string): void {
    if (actual !== expected) throw new Error(scenario + ": expected " + expected + ", got " + actual);
}

// Arrange / Act / Assert: explicit selection wins, otherwise use system locale.
for (const [saved, system, expected] of [
    ["ru", "en-US", "ru"],
    ["en", "ru-RU", "en"],
    [null, "ru_RU", "ru"],
    [null, "en-GB", "en"],
    ["obsolete", "RU-ru", "ru"],
    [null, "de-DE", "en"],
    [null, "ru<script>", "en"]
] as const) expectEqual(resolveUILanguage(saved, system), expected, "locale selection");

// Arrange / Act / Assert: units and decimals follow the UI language, with a
// bounded number of digits even for large counters and an honest missing value.
for (const [bytes, language, expected] of [
    [null, "ru", "—"], [0, "en", "0 B"], [1024, "en", "1.0 KB"],
    [1536, "ru", "1,5 КБ"], [1048576, "ru", "1,0 МБ"],
    [1099511627776, "en", "1.0 TB"]
] as const) expectEqual(formatTraffic(bytes, language), expected, "traffic format");
console.log("PASS: language precedence, unsupported locale fallback, localized traffic units");
