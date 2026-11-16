import type { MessageKey } from "./i18n";
import type {
  Action,
  AutoPreference,
  CommandFailure,
  CommandResult,
  Routing,
  Upload,
} from "./state";

export function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

// The service response is the only untyped boundary. Never render its HTML.
export function decodeResponse(payload: string): CommandResult {
  if (payload.length > 131072) throw new Error("Oversized service response");
  const response: unknown = JSON.parse(payload);
  if (!record(response) || response["returnValue"] !== true) {
    return { kind: "service-error", message: "serviceDetail", output: "" };
  }
  function field(text: string, encoded: string): string {
    if (!record(response)) throw new Error("Invalid service response");
    const raw = response[text];
    if (typeof raw === "string") {
      if (raw.length > 32768) throw new Error("Oversized command output");
      return raw;
    }
    const base64 = response[encoded];
    if (base64 === undefined) return "";
    if (typeof base64 !== "string" || base64.length > 65536)
      throw new Error("Invalid command output");
    const decoded = atob(base64);
    const bytes = new Uint8Array(decoded.length);
    for (let i = 0; i < decoded.length; i++) bytes[i] = decoded.charCodeAt(i);
    return new TextDecoder("utf-8").decode(bytes);
  }
  const stdout = field("stdoutString", "stdoutBytes");
  const stderr = field("stderrString", "stderrBytes");
  const marker = /\nAWG_RC=(\d+)\s*$/.exec(stdout);
  if (marker === null)
    return { kind: "service-error", message: "incompleteDetail", output: "" };
  return {
    kind: marker[1] === "0" ? "success" : "command-error",
    output: (
      stdout.slice(0, marker.index).trim() +
      "\n" +
      stderr.trim()
    ).trim(),
  };
}

export function readAutostart(output: string): AutoPreference {
  const match = /^autostart=(on|off|conflict)$/m.exec(output);
  return match && match[1] === "on"
    ? "enabled"
    : match && match[1] === "off"
      ? "disabled"
      : "unknown";
}

export function errorMessage(
  action: Action,
  failure: CommandFailure,
): MessageKey {
  const output = failure.output;
  if (output.includes("stop the previous AmneziaWG app")) return "legacyActive";
  if (output.includes("import or update routing lists first"))
    return "listsMissing";
  if (output.includes("ordinary wg0 is active")) return "wgActive";
  if (output.includes("ordinary WireGuard autostart is enabled"))
    return "wgAutostart";
  if (output.includes("cannot open config file")) return "configurationMissing";
  if (output.includes("no handshake in 20 seconds")) return "serverNoResponse";
  if (
    failure.kind === "service-error" &&
    (failure.message === "serviceDetail" || failure.message === "tvOnlyDetail")
  )
    return "serviceUnavailable";
  switch (action) {
    case "upload-open":
      return "uploadFailed";
    case "upload-status":
      return "uploadCheckFailed";
    case "upload-close":
      return "uploadCloseFailed";
    case "routing-all":
    case "routing-exclude":
    case "routing-include":
      return "routingFailed";
    case "routing-update":
      return "routingUpdateFailed";
    case "start":
      return "connectFailed";
    case "stop":
      return "disconnectFailed";
    case "status":
      return "statusFailed";
    case "logs":
      return "logsFailed";
    case "autostart-enable":
    case "autostart-disable":
      return "autostartFailed";
  }
}

export function commandFields(output: string): Map<string, string> {
  const fields = new Map<string, string>();
  for (const line of output.split("\n")) {
    const match = /^([a-z_]+)=([^\r\n]*)\r?$/.exec(line);
    if (match && match[1] && match[2] !== undefined)
      fields.set(match[1], match[2]);
  }
  return fields;
}

export function readRouting(output: string): Routing | null {
  const fields = commandFields(output);
  const mode = fields.get("routing"),
    update = fields.get("routing_update");
  const sources = Number(fields.get("routing_sources")),
    prefixes = Number(fields.get("routing_prefixes")),
    updated = Number(fields.get("routing_updated"));
  if (
    (mode !== "all" && mode !== "exclude" && mode !== "include") ||
    (update !== "ready" && update !== "failed" && update !== "missing") ||
    !Number.isInteger(sources) ||
    sources < 0 ||
    sources > 8 ||
    !Number.isInteger(prefixes) ||
    prefixes < 0 ||
    prefixes > 16384 ||
    !Number.isSafeInteger(updated) ||
    updated < 0 ||
    updated > 4102444800
  )
    return null;
  return {
    mode: mode,
    update: update,
    sources: sources,
    prefixes: prefixes,
    updated: updated,
  };
}

export function readUpload(output: string): Upload | null {
  const fields = commandFields(output);
  const kind = fields.get("upload");
  if (
    kind === "closed" ||
    kind === "expired" ||
    kind === "locked" ||
    kind === "failed" ||
    kind === "imported"
  )
    return { kind: kind };
  if (kind !== "waiting") return null;
  const url = fields.get("upload_url"),
    code = fields.get("upload_code"),
    expires = Number(fields.get("upload_expires"));
  if (
    url === undefined ||
    code === undefined ||
    !/^\d{8}$/.test(code) ||
    !Number.isSafeInteger(expires)
  )
    return null;
  const match =
    /^http:\/\/(10\.\d{1,3}\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3}):8089$/.exec(
      url,
    );
  if (
    match === null ||
    match[1] === undefined ||
    match[1].split(".").some((value) => Number(value) > 255)
  )
    return null;
  return { kind: "waiting", url: url, code: code, expires: expires };
}
