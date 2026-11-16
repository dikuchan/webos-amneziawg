import type { MessageKey } from "./i18n";

export type Action =
  | "start"
  | "stop"
  | "status"
  | "logs"
  | "autostart-enable"
  | "autostart-disable"
  | "upload-open"
  | "upload-status"
  | "upload-close"
  | "routing-all"
  | "routing-exclude"
  | "routing-include"
  | "routing-update";

export type RoutingMode = "all" | "exclude" | "include";

export interface Routing {
  mode: RoutingMode;
  sources: number;
  prefixes: number;
  updated: number;
  update: "ready" | "failed" | "missing";
}

export type Upload =
  | { kind: "idle" | "imported" | "closed" | "expired" | "locked" | "failed" }
  | { kind: "waiting"; url: string; code: string; expires: number };

export type AutoPreference = "enabled" | "disabled" | "unknown";

export type AppView = "home" | "config" | "logs" | "settings" | "routing";

export type Invocation = "user" | "refresh";

export type CommandFailure =
  | { kind: "command-error"; output: string }
  | { kind: "service-error"; message: MessageKey; output: string };

export type CommandResult =
  | { kind: "success"; output: string }
  | CommandFailure;

export interface Statistics {
  handshakeAge: number | null;
  mtu: number | null;
  received: number | null;
  sent: number | null;
}

export type Tunnel =
  | { kind: "unknown" | "stopped" | "cleanup-required" }
  | { kind: "connecting" | "connected"; statistics: Statistics };

export type Notice = { kind: "error" | "success"; message: MessageKey } | null;

export function readTunnel(output: string): Tunnel | null {
  const fields = new Map<string, string>();
  for (const line of output.split("\n")) {
    const match = /^([a-z_]+)=([^\r\n]*)\r?$/.exec(line);
    if (match && match[1] && match[2] !== undefined)
      fields.set(match[1], match[2]);
  }
  function number(key: string): number | null {
    const raw = fields.get(key);
    if (raw === undefined || !/^\d{1,16}$/.test(raw)) return null;
    const value = Number(raw);
    return Number.isSafeInteger(value) ? value : null;
  }
  const kind = fields.get("state");
  if (kind === "stopped" || kind === "cleanup-required") return { kind: kind };
  if (kind === "connecting" || kind === "connected") {
    if (fields.get("interface") !== "awg0") return null;
    return {
      kind: kind,
      statistics: {
        handshakeAge: number("handshake_age_seconds"),
        mtu: number("mtu"),
        received: number("received_bytes"),
        sent: number("sent_bytes"),
      },
    };
  }
  return null;
}

export function stateTitle(state: Tunnel): MessageKey {
  switch (state.kind) {
    case "unknown":
      return "unknown";
    case "stopped":
      return "disconnected";
    case "connecting":
      return "connecting";
    case "connected":
      return "connected";
    case "cleanup-required":
      return "recoveryNeeded";
  }
}

export function primaryAction(state: Tunnel): Action {
  switch (state.kind) {
    case "unknown":
      return "status";
    case "stopped":
      return "start";
    case "connecting":
    case "connected":
    case "cleanup-required":
      return "stop";
  }
}

export function readOnlyAction(action: Action): boolean {
  return action === "status" || action === "logs" || action === "upload-status";
}

export function uploadAction(action: Action): boolean {
  return (
    action === "upload-open" ||
    action === "upload-status" ||
    action === "upload-close"
  );
}
