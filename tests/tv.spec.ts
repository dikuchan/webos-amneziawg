import { expect, test } from "@playwright/test";

test("shows the running tunnel and supports remote navigation through logs", async ({
  page,
}, info) => {
  await page.goto("/?scenario=connected&locale=en_US");

  await expect(page.locator("#state")).toHaveText("Connected");
  await expect(page.locator("#primary")).toHaveAttribute(
    "aria-label",
    "Disconnect",
  );
  await expect(page.locator("#received")).toHaveText("1.0 MB");
  await expect(page.locator("#sent")).toHaveText("128.0 KB");
  await page.locator("#primary").focus();
  await page.keyboard.press("ArrowDown");
  await expect(page.locator("#open-config")).toBeFocused();
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("Enter");
  await expect(page.locator("#logs")).toBeVisible();
  await expect(page.locator("#log-output")).toContainText(
    "Boot autoconnect: connected.",
  );
  await page.keyboard.press("Escape");
  await expect(page.locator("#open-logs")).toBeFocused();
  await page.screenshot({ path: info.outputPath("connected.png") });
});

test("changes language from Home with the remote and remembers the selection", async ({
  page,
}) => {
  await page.goto("/?scenario=connected&locale=en_US");
  await expect(page.locator("#state")).toHaveText("Connected");
  const home = page.locator("#home");
  const language = home.getByRole("button", { name: "Language: English" });
  await expect(language).toBeVisible();
  await expect(language.locator("img")).toBeVisible();

  await page.keyboard.press("ArrowUp");
  await expect(language).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(
    home.getByRole("button", { name: "Язык: Русский" }),
  ).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await expect(page.locator("#primary")).toBeFocused();
  await page.reload();

  await expect(page.locator("html")).toHaveAttribute("lang", "ru");
  await expect(page.locator("#state")).toHaveText("Подключено");
  await expect(
    home.getByRole("button", { name: "Язык: Русский" }),
  ).toBeVisible();
});

for (const [locale, language, label] of [
  ["ru_RU", "ru", "Язык: Русский"],
  ["de_DE", "en", "Language: English"],
] as const)
  test(`uses the TV locale with an English fallback: ${locale}`, async ({
    page,
  }) => {
    await page.goto("/?scenario=connected&locale=" + locale);

    await expect(page.locator("html")).toHaveAttribute("lang", language);
    await expect(
      page.locator("#home").getByRole("button", { name: label }),
    ).toBeVisible();
  });

test("shows language persistence errors on Home", async ({ page }) => {
  await page.addInitScript(() => {
    Storage.prototype.setItem = () => {
      throw new Error("Storage unavailable");
    };
  });
  await page.goto("/?scenario=connected&locale=en_US");
  await expect(page.locator("#state")).toHaveText("Connected");

  await page
    .locator("#home")
    .getByRole("button", { name: "Language: English" })
    .click();

  await expect(page.locator("html")).toHaveAttribute("lang", "ru");
  await expect(page.locator("#notice")).toHaveText(
    "Не удалось сохранить выбранный язык",
  );
});

test("keeps focus inside Settings when the TV service is unavailable", async ({
  page,
}) => {
  await page.goto("/?scenario=service-error&locale=en_US");
  await expect(page.locator("#state")).toHaveText("Status unavailable");

  await page.locator("#open-settings").click();

  await expect(page.locator("#settings button:focus")).toBeVisible();
});

test("saves autoconnect and switches include/exclude with the remote", async ({
  page,
}, info) => {
  await page.goto("/?scenario=connected&locale=ru_RU");
  await expect(page.locator("#state")).toHaveText("Подключено");

  await page.locator("#open-settings").click();
  await expect(page.locator("#autostart")).toHaveAttribute(
    "aria-disabled",
    "false",
  );
  await page.keyboard.press("Enter");
  await expect(page.locator("#autostart")).toHaveAttribute(
    "aria-checked",
    "true",
  );
  await expect(page.locator("#autostart")).toContainText(
    "Подключаться при запуске устройства",
  );
  await page.locator("#routing").click();
  await expect(page.locator("#mode-all")).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");
  await expect(page.locator("#mode-exclude")).toHaveAttribute(
    "aria-checked",
    "true",
  );
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");
  await expect(page.locator("#mode-include")).toHaveAttribute(
    "aria-checked",
    "true",
  );
  await page.screenshot({ path: info.outputPath("routing-ru.png") });
  await page.keyboard.press("Escape");
  await expect(page.locator("#routing")).toBeFocused();
});

test("automatically returns from web settings when a profile is imported", async ({
  page,
}) => {
  await page.goto("/?scenario=upload-success&locale=en_US");
  await expect(page.locator("#state")).toHaveText("Disconnected");
  await page.locator("#open-config").click();

  await expect(page.locator("#upload-url")).toHaveText(
    "http://192.168.0.64:8089",
  );
  await expect(page.locator("#upload-code")).toHaveText("1234 5678");
  await expect(page.locator("#config")).toBeHidden({ timeout: 12_000 });
  await expect(page.locator("#notice")).toHaveText("Configuration imported");
  await expect(page.locator("#open-config")).toBeFocused();
});
