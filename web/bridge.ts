import type { UILanguage } from "./i18n";
import { decodeResponse, record } from "./protocol";
import type { Action, CommandResult } from "./state";

interface PalmBridge {
  onservicecallback: ((payload: string) => void) | null;
  call(uri: string, payload: string): void;
  cancel?(): void;
}

declare const PalmServiceBridge: { new (): PalmBridge } | undefined;

const applicationKit =
  "/media/developer/apps/usr/palm/applications/com.github.dikuchan.amneziawg/payload";

const commandTimeoutMs = 120000;

export function execute(
  action: Action,
  language: UILanguage,
): Promise<CommandResult> {
  if (typeof PalmServiceBridge === "undefined") {
    return Promise.resolve({
      kind: "service-error",
      message: "tvOnlyDetail",
      output: "",
    });
  }
  return new Promise((resolve) => {
    let bridge: PalmBridge | null = null;
    let completion: "waiting" | "finished" = "waiting";
    const timer = window.setTimeout(() => {
      finish({ kind: "service-error", message: "timeoutDetail", output: "" });
    }, commandTimeoutMs);
    function finish(result: CommandResult): void {
      if (completion === "finished") return;
      completion = "finished";
      window.clearTimeout(timer);
      if (bridge !== null) {
        bridge.onservicecallback = null;
        // Cancellation releases the LS2 request, not the background VPN.
        try {
          if (bridge.cancel) bridge.cancel();
          resolve(result);
        } catch {
          resolve({
            kind: "service-error",
            message: "releaseDetail",
            output: result.output,
          });
        }
      } else {
        resolve(result);
      }
    }
    try {
      bridge = new PalmServiceBridge();
      bridge.onservicecallback = (payload) => {
        try {
          finish(decodeResponse(payload));
        } catch {
          finish({
            kind: "service-error",
            message: "unreadableDetail",
            output: "",
          });
        }
      };
      const args =
        action === "autostart-enable"
          ? "autostart enable"
          : action === "autostart-disable"
            ? "autostart disable"
            : action === "upload-open"
              ? "lan open " + language
              : action === "upload-status"
                ? "lan status"
                : action === "upload-close"
                  ? "lan close"
                  : action === "routing-all"
                    ? "routing all"
                    : action === "routing-exclude"
                      ? "routing exclude"
                      : action === "routing-include"
                        ? "routing include"
                        : action === "routing-update"
                          ? "routing update"
                          : action;
      const command =
        "LD_PRELOAD= " +
        applicationKit +
        "/bin/amneziawgctl " +
        args +
        "; awg_result=$?; printf '\\nAWG_RC=%s\\n' \"$awg_result\"";
      bridge.call(
        "luna://org.webosbrew.hbchannel.service/exec",
        JSON.stringify({ command: command }),
      );
    } catch {
      finish({ kind: "service-error", message: "serviceDetail", output: "" });
    }
  });
}

export function readSystemLocale(): Promise<string | null> {
  if (typeof PalmServiceBridge === "undefined") return Promise.resolve(null);
  return new Promise((resolve) => {
    let bridge: PalmBridge | null = null;
    let completion: "waiting" | "finished" = "waiting";
    const timer = window.setTimeout(() => {
      finish(null);
    }, 3500);
    function finish(locale: string | null): void {
      if (completion === "finished") return;
      completion = "finished";
      window.clearTimeout(timer);
      if (bridge !== null) {
        bridge.onservicecallback = null;
        try {
          if (bridge.cancel) bridge.cancel();
        } catch {
          resolve(null);
          return;
        }
      }
      resolve(locale);
    }
    try {
      bridge = new PalmServiceBridge();
      bridge.onservicecallback = (payload) => {
        try {
          if (payload.length > 16384) {
            finish(null);
            return;
          }
          const response: unknown = JSON.parse(payload);
          if (!record(response) || response["returnValue"] !== true) {
            finish(null);
            return;
          }
          const settings = response["settings"];
          const info = record(settings) ? settings["localeInfo"] : null;
          const locales = record(info) ? info["locales"] : null;
          const locale = record(locales) ? locales["UI"] : null;
          finish(
            typeof locale === "string" && locale.length <= 64 ? locale : null,
          );
        } catch {
          finish(null);
        }
      };
      bridge.call(
        "luna://com.webos.settingsservice/getSystemSettings",
        JSON.stringify({ keys: ["localeInfo"], subscribe: false }),
      );
    } catch {
      finish(null);
    }
  });
}
