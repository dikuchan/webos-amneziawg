import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./tests",
  outputDir: ".build/test-results",
  fullyParallel: false,
  workers: 1,
  forbidOnly: Boolean(process.env["CI"]),
  retries: 0,
  timeout: 20_000,
  expect: { timeout: 5_000 },
  use: { trace: "retain-on-failure", screenshot: "only-on-failure" },
  reporter: [["list"]],
  webServer: [
    {
      command: "bun run preview",
      url: "http://127.0.0.1:8091",
      reuseExistingServer: false,
    },
    {
      command: "sh scripts/lan-browser.sh",
      url: "http://127.0.0.1:8090",
      timeout: 45_000,
      reuseExistingServer: false,
    },
  ],
  projects: [
    {
      name: "tv-1080",
      testMatch: "tv.spec.ts",
      use: {
        browserName: "chromium",
        baseURL: "http://127.0.0.1:8091",
        viewport: { width: 1920, height: 1080 },
      },
    },
    {
      name: "tv-720",
      testMatch: "tv.spec.ts",
      use: {
        browserName: "chromium",
        baseURL: "http://127.0.0.1:8091",
        viewport: { width: 1280, height: 720 },
      },
    },
    {
      name: "lan-mobile",
      testMatch: "lan.spec.ts",
      use: { ...devices["iPhone 13"], baseURL: "http://127.0.0.1:8090" },
    },
  ],
});
