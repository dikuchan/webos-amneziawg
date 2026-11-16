import { expect, test, type Page } from "@playwright/test";

async function login(page: Page): Promise<void> {
  await page.goto("/?reset=1&lang=ru");
  await page.getByLabel("Код с экрана ТВ").fill("1234 5678");
  await page.getByRole("button", { name: "Открыть", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Профиль VPN" }),
  ).toBeVisible();
}

test("imports a local IPv4 list, preserves it and updates its routing mode", async ({
  page,
}, info) => {
  await login(page);
  await page.getByRole("link", { name: "Маршрутизация", exact: true }).click();
  await page.getByRole("button", { name: "Добавить источник" }).click();
  await page
    .getByRole("combobox", { name: "Источник", exact: true })
    .selectOption("file");
  await page.getByLabel("TXT-файл с IPv4").setInputFiles({
    name: "direct.txt",
    mimeType: "text/plain",
    buffer: Buffer.from("203.0.113.1\n"),
  });
  await page.locator('input[name="mode"][value="exclude"]').check();
  await page.getByRole("button", { name: "Сохранить", exact: true }).click();

  await expect(page.getByRole("status")).toHaveText(
    "Настройки маршрутизации сохранены.",
  );
  await expect(page.locator(".source")).toHaveCount(1);
  await expect(page.locator(".source .meta")).toContainText(/1\s+подсет/);
  await page.locator('input[name="mode"][value="include"]').check();
  await page.getByRole("button", { name: "Сохранить", exact: true }).click();
  await expect(page.getByRole("status")).toHaveText(
    "Настройки маршрутизации сохранены.",
  );
  await expect(
    page.locator('input[name="mode"][value="include"]'),
  ).toBeChecked();
  await expect(page.locator(".source .meta")).toContainText(/1\s+подсет/);
  await page.screenshot({
    path: info.outputPath("lan-routing.png"),
    fullPage: true,
  });
});

test("rejects a malformed profile and accepts a corrected file in the same session", async ({
  page,
}) => {
  await login(page);
  const file = page.getByLabel("Конфигурация (.conf)", { exact: true });
  await file.setInputFiles({
    name: "invalid.conf",
    mimeType: "text/plain",
    buffer: Buffer.from("invalid"),
  });
  await page
    .getByRole("button", { name: "Импортировать", exact: true })
    .click();
  await expect(page.getByRole("status")).toContainText("Проверь файл");

  const key = Buffer.from("0123456789abcdef0123456789abcdef").toString(
    "base64",
  );
  const profile = `[Interface]\nPrivateKey = ${key}\nAddress = 10.203.7.2/32\n[Peer]\nPublicKey = ${key}\nAllowedIPs = 0.0.0.0/0\nEndpoint = vpn.example.test:31820\n`;
  await file.setInputFiles({
    name: "synthetic.conf",
    mimeType: "text/plain",
    buffer: Buffer.from(profile),
  });
  await page
    .getByRole("button", { name: "Импортировать", exact: true })
    .click();

  await expect(page.getByRole("status")).toHaveText(
    "Конфигурация загружена. Можно подключаться на ТВ.",
  );
  await expect(
    page.getByRole("button", { name: "Импортировать", exact: true }),
  ).toHaveCount(0);
});

test("language links keep the routing editor open", async ({ page }) => {
  await login(page);
  await page.getByRole("link", { name: "Маршрутизация", exact: true }).click();

  await page.getByRole("link", { name: "English", exact: true }).click();

  await expect(page).toHaveURL("http://127.0.0.1:8090/routing?lang=en");
  await expect(
    page.getByRole("heading", { name: "Routing", exact: true }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Русский", exact: true }).click();
  await expect(page).toHaveURL("http://127.0.0.1:8090/routing?lang=ru");
  await expect(
    page.getByRole("heading", { name: "Маршрутизация", exact: true }),
  ).toBeVisible();
});
