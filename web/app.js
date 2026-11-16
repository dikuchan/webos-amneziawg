"use strict";
var OperationState;
(function (OperationState) {
    OperationState[OperationState["Idle"] = 0] = "Idle";
    OperationState[OperationState["Running"] = 1] = "Running";
})(OperationState || (OperationState = {}));
const applicationKit = "/media/developer/apps/usr/palm/applications/com.github.dikuchan.amneziawgtest/payload";
function requiredElement(id, kind) {
    const el = document.getElementById(id);
    if (!(el instanceof kind))
        throw new Error("Missing UI element: " + id);
    return el;
}
function record(value) {
    return typeof value === "object" && value !== null && !Array.isArray(value);
}
function decodeOutput(response) {
    if (!record(response))
        throw new Error("Некорректный ответ Homebrew Channel");
    if (response["returnValue"] === false)
        throw new Error("Homebrew Channel отклонил команду. Проверь root-доступ.");
    function field(text, encoded) {
        if (!record(response))
            return "";
        const raw = response[text];
        if (typeof raw === "string")
            return raw;
        const b64 = response[encoded];
        if (typeof b64 !== "string" || b64.length > 65536)
            return "";
        const decoded = atob(b64);
        const bytes = new Uint8Array(decoded.length);
        for (let i = 0; i < decoded.length; i++)
            bytes[i] = decoded.charCodeAt(i);
        return new TextDecoder("utf-8").decode(bytes);
    }
    return (field("stdoutString", "stdoutBytes") + "\n" + field("stderrString", "stderrBytes")).slice(0, 20000);
}
const output = requiredElement("output", HTMLPreElement);
const label = requiredElement("state", HTMLSpanElement);
const buttons = Array.from(document.querySelectorAll("button[data-action]"));
let operation = OperationState.Idle;
let activeBridge = null;
function show(text) {
    output.textContent = text;
    output.scrollTop = 0;
}
function run(action) {
    if (operation === OperationState.Running)
        return;
    if (typeof PalmServiceBridge === "undefined") {
        label.textContent = "Предпросмотр";
        show("На телевизоре команды выполняются через Homebrew Channel с root-доступом.");
        return;
    }
    operation = OperationState.Running;
    buttons.forEach(button => { button.disabled = true; });
    label.textContent = action === "start" ? "Подключение…" : "Выполнение…";
    show(action === "start" ? "Проверка сети и ожидание handshake. Это может занять до минуты." : "Ожидание ответа телевизора…");
    const bridge = new PalmServiceBridge();
    activeBridge = bridge;
    const timer = window.setTimeout(() => finish("Время ожидания истекло. Нажми «Статус» или «Отключить».", "Нет ответа"), 90000);
    function finish(text, state) {
        if (activeBridge !== bridge)
            return;
        activeBridge = null;
        window.clearTimeout(timer);
        bridge.onservicecallback = null;
        if (bridge.cancel)
            bridge.cancel();
        operation = OperationState.Idle;
        buttons.forEach(button => { button.disabled = false; });
        label.textContent = state;
        show(text);
        const selected = buttons.find(button => button.dataset["action"] === action);
        if (selected)
            selected.focus();
    }
    bridge.onservicecallback = function (payload) {
        try {
            const response = JSON.parse(payload);
            const text = decodeOutput(response);
            const match = /AWG_TEST_RC=(\d+)/.exec(text);
            finish(text.replace(/\n?AWG_TEST_RC=\d+\s*$/, "").trim(), match && match[1] === "0" ? "Готово" : "Ошибка");
        }
        catch (_a) {
            finish("Не удалось выполнить команду. Проверь Homebrew Channel и root-доступ.", "Ошибка");
        }
    };
    const args = action === "import" ? "import /tmp/redshield-awg31.conf" : action;
    const command = "LD_PRELOAD= " + applicationKit + "/bin/awg-test " + args +
        "; awg_test_result=$?; printf '\\nAWG_TEST_RC=%s\\n' \"$awg_test_result\"";
    try {
        bridge.call("luna://org.webosbrew.hbchannel.service/exec", JSON.stringify({ command: command }));
    }
    catch (_a) {
        finish("Сервис Homebrew Channel недоступен.", "Ошибка");
    }
}
buttons.forEach(button => {
    button.addEventListener("click", () => {
        const action = button.dataset["action"];
        if (action === "start" || action === "stop" || action === "status" || action === "import")
            run(action);
    });
});
document.addEventListener("keydown", (event) => {
    if (operation === OperationState.Running)
        return;
    const code = event.keyCode;
    if (code === 37 || code === 39) {
        event.preventDefault();
        const current = buttons.findIndex(button => button === document.activeElement);
        const delta = code === 37 ? -1 : 1;
        const next = buttons[(current + delta + buttons.length) % buttons.length];
        if (next)
            next.focus();
    }
    else if (code === 38 || code === 40) {
        event.preventDefault();
        output.scrollTop += code === 38 ? -220 : 220;
    }
});
run("status");
const first = buttons[0];
if (first)
    first.focus();
