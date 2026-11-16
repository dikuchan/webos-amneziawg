import { expect, test } from "bun:test";

import { decodeResponse, readRouting, readUpload } from "./protocol";

test("a successful service call still reports a failed controller command", () => {
  const payload = JSON.stringify({
    returnValue: true,
    stdoutString: "state=stopped\nAWG_RC=1\n",
    stderrString: "No handshake",
  });

  const result = decodeResponse(payload);

  expect(result).toEqual({
    kind: "command-error",
    output: "state=stopped\nNo handshake",
  });
});

test("a truncated service response cannot be interpreted as command success", () => {
  const payload = JSON.stringify({
    returnValue: true,
    stdoutString: "state=connected",
  });

  const result = decodeResponse(payload);

  expect(result).toEqual({
    kind: "service-error",
    message: "incompleteDetail",
    output: "",
  });
});

test("base64 output preserves UTF-8 log messages", () => {
  const output = "Подключено\nAWG_RC=0\n";
  const bytes = new TextEncoder().encode(output);
  const payload = JSON.stringify({
    returnValue: true,
    stdoutBytes: btoa(String.fromCharCode(...bytes)),
  });

  const result = decodeResponse(payload);

  expect(result).toEqual({ kind: "success", output: "Подключено" });
});

test("service response limits apply before command output is rendered", () => {
  const payload = JSON.stringify({
    returnValue: true,
    stdoutString: "a".repeat(32769),
  });

  expect(() => decodeResponse(payload)).toThrow("Oversized command output");
});

test("web settings only accept a PIN with a private IPv4 TV address", () => {
  const report =
    "upload=waiting\nupload_code=12345678\nupload_expires=1794870000\nupload_url=";

  const local = readUpload(report + "http://192.168.0.64:8089");
  const foreign = readUpload(report + "http://example.org:8089");

  expect(local).toEqual({
    kind: "waiting",
    code: "12345678",
    expires: 1794870000,
    url: "http://192.168.0.64:8089",
  });
  expect(foreign).toBeNull();
});

test("include mode survives status decoding while invalid source counts are rejected", () => {
  const report =
    "routing=include\nrouting_prefixes=100\nrouting_updated=1794870000\nrouting_update=ready\nrouting_sources=";

  const valid = readRouting(report + "2");
  const oversized = readRouting(report + "9");

  expect(valid).toEqual({
    mode: "include",
    sources: 2,
    prefixes: 100,
    updated: 1794870000,
    update: "ready",
  });
  expect(oversized).toBeNull();
});
