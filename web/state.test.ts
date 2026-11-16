import { expect, test } from "bun:test";

import { primaryAction, readTunnel, stateTitle } from "./state";

test("an existing tunnel is shown as connected and offers disconnect", () => {
  const report =
    "state=connected\ninterface=awg0\nreceived_bytes=1024\nsent_bytes=512\nhandshake_age_seconds=3\nmtu=1280";

  const tunnel = readTunnel(report);

  expect(tunnel).toEqual({
    kind: "connected",
    statistics: { received: 1024, sent: 512, handshakeAge: 3, mtu: 1280 },
  });
  if (tunnel === null) throw new Error("Expected the reported tunnel");
  expect(primaryAction(tunnel)).toBe("stop");
  expect(stateTitle(tunnel)).toBe("connected");
});

test("unfinished network recovery offers Stop even when the daemon is absent", () => {
  const report = "state=cleanup-required\nDaemon is absent";

  const tunnel = readTunnel(report);

  expect(tunnel).toEqual({ kind: "cleanup-required" });
  if (tunnel === null) throw new Error("Expected recovery state");
  expect(primaryAction(tunnel)).toBe("stop");
  expect(stateTitle(tunnel)).toBe("recoveryNeeded");
});

test("a different interface or incomplete report cannot claim an AWG connection", () => {
  expect(readTunnel("state=connected\ninterface=wg0")).toBeNull();
  expect(readTunnel("interface=awg0")).toBeNull();
  expect(primaryAction({ kind: "unknown" })).toBe("status");
});
