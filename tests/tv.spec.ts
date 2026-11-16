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

test("saves autoconnect and language and switches include/exclude with the remote", async ({
  page,
}, info) => {
  await page.goto("/?scenario=connected&locale=en_US");
  await expect(page.locator("#state")).toHaveText("Connected");

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
  await page.locator("#language").click();
  await expect(page.locator("html")).toHaveAttribute("lang", "ru");
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
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("lang", "ru");
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
