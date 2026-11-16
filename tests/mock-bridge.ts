// Browser preview only. Never packaged in the TV application.
enum PreviewState { Stopped, Imported, Connected, Cleanup }
let previewRouting: "all" | "ru-direct" = "all";
let previewRoutingUpdate: "bundled" | "current" = "bundled";
let previewUpload: "closed" | "waiting" | "imported" = "closed";
let previewUploadStarted = 0;
let previewAutostart: "on" | "off" = "off";
let previewJournal = "2026-11-16 18:00:00Z  Boot autoconnect: connected.\n" + Array.from({length:24},(_,i) => "2026-11-16 18:"+String(i).padStart(2,"0")+":00Z  start: completed.").join("\n");
const scenario = new URLSearchParams(window.location.search).get("scenario");
let previewState = scenario === "connected" || scenario === "base64" ? PreviewState.Connected :
    scenario === "cleanup" ? PreviewState.Cleanup : scenario === "missing-config" ? PreviewState.Stopped : PreviewState.Imported;

function previewRoutingStatus(): string {
    return "routing="+previewRouting+"\nrouting_prefixes=635\nrouting_updated=1794825135\nrouting_update="+previewRoutingUpdate;
}
function previewUploadStatus(): string {
    if (scenario === "upload-success" && previewUpload === "waiting" && Date.now()-previewUploadStarted > 7000) { previewUpload = "imported"; previewState = PreviewState.Imported; }
    return "upload="+previewUpload+(previewUpload === "waiting" ? "\nupload_url=http://192.168.0.64:8089\nupload_code=12345678\nupload_expires=1794870000" : "");
}
function previewStatus(): string {
    switch (previewState) {
        case PreviewState.Stopped: case PreviewState.Imported: return previewRoutingStatus()+"\nautostart="+previewAutostart+"\nstate=stopped";
        case PreviewState.Cleanup: return previewRoutingStatus()+"\nautostart="+previewAutostart+"\nstate=cleanup-required\nDaemon is absent; run Stop to restore network state.";
        case PreviewState.Connected: return previewRoutingStatus()+"\nautostart="+previewAutostart+"\nstate=connected\ninterface=awgtest0\npid=14998\nhandshake_age_seconds=3\nmtu=1280\nreceived_bytes=1048576\nsent_bytes=131072\nphysical_ipv6=suspended-on-eth0";
    }
}

class PalmServiceBridge {
    onservicecallback: ((payload: string) => void) | null = null;
    private timer: number | null = null;
    call(uri: string, payload: string): void {
        const request: unknown = JSON.parse(payload);
        if (uri === "luna://com.webos.settingsservice/getSystemSettings") {
            const parameters = new URLSearchParams(window.location.search);
            const locale = parameters.get("locale") || "en_US";
            this.timer = window.setTimeout(() => {
                if (this.onservicecallback) this.onservicecallback(JSON.stringify({returnValue:true,settings:{localeInfo:{locales:{UI:locale}}}}));
            }, scenario === "locale-delayed" ? 1500 : 50);
            return;
        }
        if (typeof request !== "object" || request === null || !("command" in request) || typeof request.command !== "string") throw new Error("Bad preview request");
        const command = request.command;
        let code = 0;
        let stdout = "";
        let stderr = "";
        let delayMs = 100;
        if (command.includes("awg-test upload open")) {
            previewUpload = "waiting"; previewUploadStarted = Date.now(); stdout = previewUploadStatus();
            if (scenario === "upload-error") { stdout = ""; stderr = "ERROR: private LAN IPv4 address is unavailable"; code = 1; }
        } else if (command.includes("awg-test upload status")) {
            stdout = previewUploadStatus();
        } else if (command.includes("awg-test upload close")) {
            if (previewUpload !== "imported") previewUpload = "closed";
        } else if (command.includes("awg-test routing ru-direct")) {
            previewRouting = "ru-direct"; stdout = previewRoutingStatus();
        } else if (command.includes("awg-test routing all")) {
            previewRouting = "all"; stdout = previewRoutingStatus();
        } else if (command.includes("awg-test routing update")) {
            delayMs = 1200; previewRoutingUpdate = "current"; stdout = previewRoutingStatus();
        } else if (command.includes("awg-test import ")) {
            previewState = PreviewState.Imported;
            stdout = "config=imported\nmtu=1280\ndns_servers=1\nfeatures=header-protection,RandomTrailers,DisableCookies";
        } else if (command.includes("awg-test start")) {
            delayMs = 1200;
            if (scenario === "wg-active") {
                stderr = "ERROR: ordinary wg0 is active; stop WG manually before testing AWG"; code = 1;
            } else if (previewState === PreviewState.Stopped) {
                stderr = "ERROR: cannot open config file"; code = 1;
            } else if (scenario === "handshake-failure") {
                stdout = "state=connecting\nStart failed; rolling back the test session.\nstate=stopped\nOriginal DNS and IPv6 state restored; ordinary WG was not changed.";
                stderr = "ERROR: no handshake in 20 seconds; check server/config/connectivity"; code = 1;
            } else {
                previewState = PreviewState.Connected;
                stdout = previewStatus();
            }
        } else if (command.includes("awg-test stop")) {
            previewState = PreviewState.Imported;
            stdout = "state=stopped\nOriginal DNS and IPv6 state restored; ordinary WG was not changed.";
        } else if (command.includes("awg-test logs")) {
            stdout = previewJournal;
        } else if (command.includes("awg-test autostart enable")) {
            if (scenario === "autostart-conflict") {
                stderr = "ERROR: ordinary WireGuard autostart is enabled; disable it in the WG app first"; code = 1;
            } else { previewAutostart = "on"; stdout = "autostart=on"; }
        } else if (command.includes("awg-test autostart disable")) {
            previewAutostart = "off"; stdout = "autostart=off";
        } else if (command.includes("awg-test status")) {
            stdout = previewStatus();
        } else {
            throw new Error("Unexpected preview command");
        }
        if (!command.includes("awg-test status") && !command.includes("awg-test logs")) previewJournal += "\n2026-11-16 19:00:00Z  "+(code === 0 ? "Operation completed." : stderr);
        if (scenario === "partial-error") {
            previewState = PreviewState.Connected;
            stdout = previewStatus();
            stderr = "ERROR: cannot read tunnel transfer counters";
            code = 1;
        }
        if (scenario !== "no-marker") stdout += "\nAWG_TEST_RC=" + code + "\n";
        const result = scenario === "service-error" ? {returnValue: false, errorText: "Not permitted"} :
            scenario === "base64" ? {returnValue: true, stdoutBytes: btoa(stdout), stderrBytes: btoa(stderr)} :
            {returnValue: true, stdoutString: stdout, stderrString: stderr};
        this.timer = window.setTimeout(() => {
            if (this.onservicecallback) this.onservicecallback(JSON.stringify(result));
        }, delayMs);
    }
    cancel(): void {
        if (this.timer !== null) window.clearTimeout(this.timer);
    }
}
