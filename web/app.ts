import { execute, readSystemLocale } from "./bridge";
import {
  englishMessages,
  formatTraffic,
  resolveUILanguage,
  uiMessages,
  type MessageKey,
  type UILanguage,
} from "./i18n";
import {
  errorMessage,
  readAutostart,
  readRouting,
  readUpload,
} from "./protocol";
import { remoteKeyCode } from "./remote";
import {
  primaryAction,
  readOnlyAction,
  readTunnel,
  stateTitle,
  uploadAction,
  type Action,
  type AppView,
  type AutoPreference,
  type CommandFailure,
  type Invocation,
  type Notice,
  type Routing,
  type Tunnel,
  type Upload,
} from "./state";

function requiredElement<T extends HTMLElement>(
  id: string,
  kind: { new (): T },
): T {
  const element = document.getElementById(id);
  if (!(element instanceof kind)) throw new Error("Missing UI element: " + id);
  return element;
}

const languageStorageKey = "amneziawg.ui.language";
type SavedLanguage =
  | { kind: "selected"; language: UILanguage }
  | { kind: "automatic" | "unavailable" };
function readLanguagePreference(): SavedLanguage {
  try {
    const value = localStorage.getItem(languageStorageKey);
    return value === "ru" || value === "en"
      ? { kind: "selected", language: value }
      : { kind: "automatic" };
  } catch {
    return { kind: "unavailable" };
  }
}
const savedLanguage = readLanguagePreference();
let languagePreference: UILanguage | null =
  savedLanguage.kind === "selected" ? savedLanguage.language : null;
let systemLocale = navigator.language;
let language = resolveUILanguage(languagePreference, systemLocale);
function text(key: MessageKey): string {
  return uiMessages[language][key];
}
function isMessageKey(value: string | undefined): value is MessageKey {
  return (
    value !== undefined &&
    Object.prototype.hasOwnProperty.call(englishMessages, value)
  );
}
function messageKey(value: string | undefined): MessageKey {
  if (!isMessageKey(value)) throw new Error("Invalid translation key");
  return value;
}

const home = requiredElement("home", HTMLElement);
const stateLabel = requiredElement("state", HTMLHeadingElement);
const primary = requiredElement("primary", HTMLButtonElement);
const noticeLabel = requiredElement("notice", HTMLParagraphElement);
const config = requiredElement("config", HTMLElement);
const logs = requiredElement("logs", HTMLElement);
const settings = requiredElement("settings", HTMLElement);
const importButton = requiredElement("import", HTMLButtonElement);
const importNote = requiredElement("import-note", HTMLParagraphElement);
const uploadDetails = requiredElement("upload-details", HTMLDivElement);
const uploadURL = requiredElement("upload-url", HTMLElement);
const uploadCode = requiredElement("upload-code", HTMLElement);
const uploadMessage = requiredElement("upload-message", HTMLParagraphElement);
const routingButton = requiredElement("routing", HTMLButtonElement);
const routingInfo = requiredElement("routing-info", HTMLDivElement);
const routingScreen = requiredElement("routing-screen", HTMLElement);
const editListsButton = requiredElement("edit-lists", HTMLButtonElement);
const routingSummary = requiredElement("routing-summary", HTMLSpanElement);
const routingRefresh = requiredElement("routing-refresh", HTMLButtonElement);
const logOutput = requiredElement("log-output", HTMLPreElement);
const autostartButton = requiredElement("autostart", HTMLButtonElement);
const settingsNotice = requiredElement("settings-notice", HTMLParagraphElement);
const configButton = requiredElement("open-config", HTMLButtonElement);
const logsButton = requiredElement("open-logs", HTMLButtonElement);
const settingsButton = requiredElement("open-settings", HTMLButtonElement);
const languageButton = requiredElement("language", HTMLButtonElement);
const languageFlag = requiredElement("language-flag", HTMLImageElement);
let tunnel: Tunnel = { kind: "unknown" };
let autostart: AutoPreference = "unknown";
let routing: Routing | null = null;
let upload: Upload = { kind: "idle" };
let uploadError: MessageKey | null = null;
let pending: Action | null = null;
let queued: { action: Action; invocation: Invocation } | null = null;
let currentView: AppView = "home";
let configReturn: "home" | "routing" = "home";
let routingMessage: MessageKey | null = null;
let notice: Notice =
  savedLanguage.kind === "unavailable"
    ? {
        kind: "error",
        message: "languageSaveFailed",
      }
    : null;
let lastError: CommandFailure | null = null;
let logText: string | null = null;
let settingsMessage: MessageKey | null = null;

function renderLogs(): void {
  if (currentView !== "logs") return;
  const atEnd =
    logOutput.scrollTop + logOutput.clientHeight >= logOutput.scrollHeight - 4;
  const report =
    logText === null ? text("loading") : logText || text("logsEmpty");
  const error =
    lastError === null
      ? ""
      : lastError.kind === "service-error"
        ? text(lastError.message) +
          (lastError.output === "" ? "" : "\n" + lastError.output)
        : lastError.output;
  logOutput.textContent =
    error !== "" && error !== logText
      ? report + "\n\n" + text("lastError") + "\n" + error
      : report;
  if (atEnd) logOutput.scrollTop = logOutput.scrollHeight;
}

function render(): void {
  document.documentElement.lang = language;
  for (const element of Array.from(
    document.querySelectorAll<HTMLElement>("[data-i18n]"),
  ))
    element.textContent = text(messageKey(element.dataset["i18n"]));
  for (const element of Array.from(
    document.querySelectorAll<HTMLElement>("[data-i18n-aria]"),
  ))
    element.setAttribute(
      "aria-label",
      text(messageKey(element.dataset["i18nAria"])),
    );
  const languageName = language === "ru" ? "Русский" : "English";
  requiredElement("language-value", HTMLSpanElement).textContent = languageName;
  const flag = "assets/flags/" + (language === "ru" ? "ru" : "gb") + ".svg";
  if (languageFlag.getAttribute("src") !== flag) languageFlag.src = flag;
  languageButton.setAttribute(
    "aria-label",
    text("language") + ": " + languageName,
  );
  home.dataset["state"] = tunnel.kind;
  stateLabel.textContent = text(
    pending === "start"
      ? "connecting"
      : pending === "stop"
        ? "disconnecting"
        : pending === "status" && tunnel.kind === "unknown"
          ? "checking"
          : stateTitle(tunnel),
  );
  const action = primaryAction(tunnel);
  primary.setAttribute(
    "aria-label",
    text(
      action === "start"
        ? "connect"
        : action === "status"
          ? "checkVPN"
          : tunnel.kind === "cleanup-required"
            ? "recoverNetwork"
            : "disconnect",
    ),
  );
  primary.setAttribute(
    "aria-disabled",
    String(pending !== null && pending !== "status" && pending !== "logs"),
  );
  primary.setAttribute(
    "aria-busy",
    String(pending === "start" || pending === "stop"),
  );
  noticeLabel.textContent = notice === null ? "" : text(notice.message);
  noticeLabel.dataset["kind"] = notice === null ? "" : notice.kind;
  const uploadPending = pending === "upload-open" || pending === "upload-close";
  importButton.hidden = upload.kind === "waiting" || uploadPending;
  requiredElement("upload-open-tool", HTMLDivElement).hidden =
    importButton.hidden;
  importButton.setAttribute(
    "aria-disabled",
    String(pending !== null && !readOnlyAction(pending)),
  );
  uploadDetails.hidden = upload.kind !== "waiting";
  uploadURL.textContent = upload.kind === "waiting" ? upload.url : "";
  uploadCode.textContent =
    upload.kind === "waiting"
      ? upload.code.slice(0, 4) + " " + upload.code.slice(4)
      : "";
  importNote.textContent = text("uploadAddress");
  const uploadMessageKey =
    uploadError !== null
      ? uploadError
      : uploadPending
        ? "loading"
        : upload.kind === "waiting"
          ? null
          : upload.kind === "locked"
            ? "uploadLocked"
            : upload.kind === "closed" || upload.kind === "expired"
              ? "uploadEnded"
              : upload.kind === "failed"
                ? "uploadFailed"
                : null;
  uploadMessage.textContent =
    uploadMessageKey === null ? "" : text(uploadMessageKey);
  uploadMessage.dataset["kind"] =
    uploadError !== null || upload.kind === "failed" || upload.kind === "locked"
      ? "error"
      : "";
  const modeKey: MessageKey =
    routing === null
      ? "unknown"
      : routing.mode === "all"
        ? "routeAll"
        : routing.mode === "exclude"
          ? "routeExclude"
          : "routeInclude";
  requiredElement("routing-value", HTMLSpanElement).textContent = text(modeKey);
  routingButton.setAttribute(
    "aria-disabled",
    String(pending !== null && !readOnlyAction(pending)),
  );
  for (const mode of ["all", "exclude", "include"] as const) {
    const button = requiredElement("mode-" + mode, HTMLButtonElement);
    button.setAttribute(
      "aria-checked",
      String(routing !== null && routing.mode === mode),
    );
    button.setAttribute(
      "aria-disabled",
      String(
        routing === null || (pending !== null && !readOnlyAction(pending)),
      ),
    );
  }
  routingInfo.hidden = routing === null;
  routingRefresh.setAttribute(
    "aria-disabled",
    String(pending !== null && !readOnlyAction(pending)),
  );
  routingRefresh.setAttribute(
    "aria-busy",
    String(pending === "routing-update"),
  );
  if (routing !== null) {
    routingSummary.textContent =
      pending === "routing-update"
        ? text("routingUpdating")
        : text("sourceCount") +
          ": " +
          String(routing.sources) +
          " · " +
          String(routing.prefixes) +
          " " +
          text("routingSubnets") +
          (routing.update === "failed" ? " · " + text("routingCached") : "");
  }
  requiredElement("routing-notice", HTMLParagraphElement).textContent =
    routingMessage === null ? "" : text(routingMessage);
  autostartButton.setAttribute("aria-checked", String(autostart === "enabled"));
  autostartButton.setAttribute(
    "aria-disabled",
    String(
      autostart === "unknown" ||
        (pending !== null && pending !== "status" && pending !== "logs"),
    ),
  );
  settingsNotice.textContent =
    settingsMessage === null ? "" : text(settingsMessage);
  const statistics =
    tunnel.kind === "connected" || tunnel.kind === "connecting"
      ? tunnel.statistics
      : null;
  requiredElement("received", HTMLSpanElement).textContent = formatTraffic(
    statistics === null ? null : statistics.received,
    language,
  );
  requiredElement("sent", HTMLSpanElement).textContent = formatTraffic(
    statistics === null ? null : statistics.sent,
    language,
  );
  renderLogs();
}

function openScreen(next: AppView): void {
  currentView = next;
  home.setAttribute("aria-hidden", String(next !== "home"));
  config.hidden = next !== "config";
  logs.hidden = next !== "logs";
  settings.hidden = next !== "settings";
  routingScreen.hidden = next !== "routing";
  let target: HTMLButtonElement;
  switch (next) {
    case "home":
      target = primary;
      break;
    case "routing":
      target = requiredElement(
        "mode-" + (routing === null ? "all" : routing.mode),
        HTMLButtonElement,
      );
      break;
    case "config":
      target = requiredElement("config-back", HTMLButtonElement);
      break;
    case "logs":
      target = requiredElement("logs-back", HTMLButtonElement);
      break;
    case "settings":
      target =
        autostartButton.getAttribute("aria-disabled") !== "true"
          ? autostartButton
          : requiredElement("settings-back", HTMLButtonElement);
      break;
  }
  target.focus();
  if (next === "logs") {
    renderLogs();
    logOutput.scrollTop = logOutput.scrollHeight;
  }
}

function closeScreen(): void {
  const previous = currentView;
  if (previous === "routing") {
    openScreen("settings");
    routingButton.focus();
    return;
  }
  if (previous === "config") {
    upload = { kind: "idle" };
    uploadError = null;
    openScreen(configReturn);
    if (configReturn === "routing") editListsButton.focus();
    else configButton.focus();
    void run("upload-close", "refresh");
    return;
  }
  openScreen("home");
  (previous === "logs" ? logsButton : settingsButton).focus();
}
function openWebSettings(from: "home" | "routing"): void {
  configReturn = from;
  upload = { kind: "idle" };
  uploadError = null;
  openScreen("config");
  render();
  void run("upload-open", "user");
}
async function run(action: Action, invocation: Invocation): Promise<void> {
  if (pending !== null) {
    // Closing the dialog always takes priority over a queued poll.
    if (
      action === "upload-close" ||
      (queued === null && (readOnlyAction(pending) || action === "logs"))
    )
      queued = { action: action, invocation: invocation };
    return;
  }
  pending = action;
  if (invocation === "user" && action !== "logs") {
    notice = null;
    lastError = null;
  }
  const autoAction =
    action === "autostart-enable" || action === "autostart-disable";
  const routingAction =
    action === "routing-all" ||
    action === "routing-exclude" ||
    action === "routing-include" ||
    action === "routing-update";
  if (autoAction) settingsMessage = null;
  if (routingAction) routingMessage = null;
  if (action === "upload-open") uploadError = null;
  render();
  let result = await execute(action, language);
  pending = null;
  if (action === "start" || action === "stop" || action === "status") {
    const reported = readTunnel(result.output);
    if (result.kind === "success" && reported === null)
      result = {
        kind: "service-error",
        message: "missingStateDetail",
        output: result.output,
      };
    tunnel =
      result.kind === "success" && reported !== null
        ? reported
        : { kind: "unknown" };
  }
  if (action === "status" || autoAction) {
    const reported = readAutostart(result.output);
    if (autoAction && result.kind === "success" && reported === "unknown")
      result = {
        kind: "service-error",
        message: "autostartReadDetail",
        output: "",
      };
    autostart = result.kind === "success" ? reported : "unknown";
  }
  if (action === "status" || action === "start" || routingAction) {
    const reported = readRouting(result.output);
    if (routingAction && result.kind === "success" && reported === null)
      result = {
        kind: "service-error",
        message: "routingReadDetail",
        output: "",
      };
    if (reported !== null) routing = reported;
    else if (action === "status") routing = null;
  }
  if (
    (action === "upload-open" || action === "upload-status") &&
    result.kind === "success" &&
    currentView === "config"
  ) {
    const reported = readUpload(result.output);
    if (reported === null)
      result = {
        kind: "service-error",
        message: "incompleteDetail",
        output: "",
      };
    else {
      upload = reported;
      if (reported.kind === "imported") {
        notice = { kind: "success", message: "imported" };
        closeScreen();
      }
    }
  }
  if (result.kind !== "success") {
    // A failed upload response must never place the on-screen PIN in logs.
    lastError = uploadAction(action) ? { ...result, output: "" } : result;
    if (autoAction) settingsMessage = errorMessage(action, result);
    else if (routingAction) routingMessage = errorMessage(action, result);
    else if (uploadAction(action)) {
      if (currentView === "config") uploadError = errorMessage(action, result);
      else if (action === "upload-close")
        notice = { kind: "error", message: "uploadCloseFailed" };
    } else notice = { kind: "error", message: errorMessage(action, result) };
  }
  if (action === "logs") logText = result.output;
  render();
  if (queued !== null) {
    const next = queued;
    queued = null;
    void run(next.action, next.invocation);
  } else if (!readOnlyAction(action) && !uploadAction(action)) {
    void run("status", "refresh");
  }
}

primary.addEventListener("click", () => {
  void run(primaryAction(tunnel), "user");
});
configButton.addEventListener("click", () => {
  openWebSettings("home");
});
editListsButton.addEventListener("click", () => {
  openWebSettings("routing");
});
logsButton.addEventListener("click", () => {
  openScreen("logs");
  void run("logs", "user");
});
settingsButton.addEventListener("click", () => {
  openScreen("settings");
  void run("status", "refresh");
});
importButton.addEventListener("click", () => {
  if (importButton.getAttribute("aria-disabled") !== "true")
    void run("upload-open", "user");
});
autostartButton.addEventListener("click", () => {
  if (autostartButton.getAttribute("aria-disabled") !== "true")
    void run(
      autostart === "enabled" ? "autostart-disable" : "autostart-enable",
      "user",
    );
});
routingButton.addEventListener("click", () => {
  if (routingButton.getAttribute("aria-disabled") !== "true")
    openScreen("routing");
});
for (const mode of ["all", "exclude", "include"] as const)
  requiredElement("mode-" + mode, HTMLButtonElement).addEventListener(
    "click",
    () => {
      if (pending === null || readOnlyAction(pending))
        void run(
          mode === "all"
            ? "routing-all"
            : mode === "exclude"
              ? "routing-exclude"
              : "routing-include",
          "user",
        );
    },
  );
routingRefresh.addEventListener("click", () => {
  if (routingRefresh.getAttribute("aria-disabled") !== "true")
    void run("routing-update", "user");
});
languageButton.addEventListener("click", () => {
  language = language === "ru" ? "en" : "ru";
  languagePreference = language;
  try {
    localStorage.setItem(languageStorageKey, language);
    if (notice?.message === "languageSaveFailed") notice = null;
  } catch {
    notice = {
      kind: "error",
      message: "languageSaveFailed",
    };
  }
  render();
});
requiredElement("refresh-logs", HTMLButtonElement).addEventListener(
  "click",
  () => {
    void run("logs", "user");
  },
);
for (const id of ["config-back", "logs-back", "settings-back", "routing-back"])
  requiredElement(id, HTMLButtonElement).addEventListener("click", closeScreen);

function focusable(): HTMLButtonElement[] {
  const container =
    currentView === "home"
      ? home
      : currentView === "config"
        ? config
        : currentView === "logs"
          ? logs
          : currentView === "routing"
            ? routingScreen
            : settings;
  return Array.from(
    container.querySelectorAll<HTMLButtonElement>("button"),
  ).filter(
    (button) =>
      !button.hidden &&
      button.getAttribute("aria-disabled") !== "true" &&
      button.getClientRects().length > 0,
  );
}

document.addEventListener("keydown", (event: KeyboardEvent) => {
  const code = remoteKeyCode(event);
  if (code === 461 || code === 27 || code === 8) {
    event.preventDefault();
    if (currentView !== "home") closeScreen();
    else window.close();
    return;
  }
  if (code === 13 && event.repeat) {
    event.preventDefault();
    return;
  }
  if (code !== 9 && (code < 37 || code > 40)) return;
  event.preventDefault();
  const buttons = focusable();
  if (buttons.length === 0) return;
  const current = buttons.findIndex(
    (button) => button === document.activeElement,
  );
  if (code === 9) {
    const next =
      buttons[
        (current + (event.shiftKey ? -1 : 1) + buttons.length) % buttons.length
      ];
    if (next) next.focus();
  } else if (currentView === "home") {
    if (code === 38) {
      if (
        document.activeElement === primary ||
        document.activeElement === languageButton ||
        primary.getAttribute("aria-disabled") === "true"
      )
        languageButton.focus();
      else primary.focus();
    } else if (code === 40) {
      if (document.activeElement === languageButton)
        (primary.getAttribute("aria-disabled") !== "true"
          ? primary
          : configButton
        ).focus();
      else if (document.activeElement === primary) configButton.focus();
    } else if (code === 37 || code === 39) {
      const tools = [configButton, logsButton, settingsButton];
      const index = tools.findIndex(
        (button) => button === document.activeElement,
      );
      if (index >= 0) {
        const next =
          tools[
            Math.max(
              0,
              Math.min(tools.length - 1, index + (code === 37 ? -1 : 1)),
            )
          ];
        if (next) next.focus();
      }
    }
  } else if (
    (currentView === "settings" || currentView === "routing") &&
    (code === 38 || code === 40)
  ) {
    const next =
      buttons[
        Math.max(
          0,
          Math.min(buttons.length - 1, current + (code === 38 ? -1 : 1)),
        )
      ];
    if (next) next.focus();
  } else if (code === 38 || code === 40) {
    if (currentView === "logs") logOutput.scrollTop += code === 38 ? -180 : 180;
  } else {
    const next =
      buttons[
        (current + (code === 37 ? -1 : 1) + buttons.length) % buttons.length
      ];
    if (next) next.focus();
  }
});

document.addEventListener("visibilitychange", () => {
  if (document.hidden && currentView === "config") closeScreen();
  else if (!document.hidden) void run("status", "refresh");
});
window.setInterval(() => {
  if (
    !document.hidden &&
    currentView === "config" &&
    upload.kind === "waiting" &&
    pending === null
  )
    void run("upload-status", "refresh");
}, 2000);
window.setInterval(() => {
  if (!document.hidden && pending === null)
    void run(currentView === "logs" ? "logs" : "status", "refresh");
}, 15000);

render();
primary.focus();
void run("status", "refresh");

void readSystemLocale().then((locale) => {
  if (locale === null) return; // Navigator is the fallback when the TV API is unavailable.
  systemLocale = locale;
  language = resolveUILanguage(languagePreference, systemLocale);
  render();
});
