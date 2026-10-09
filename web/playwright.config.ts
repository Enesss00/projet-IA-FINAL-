import { defineConfig } from "@playwright/test";

// The e2e test runs against the Go server serving the built frontend
// (`make e2e` builds both). PITWALL_URL overrides the target.
const url = process.env.PITWALL_URL ?? "http://127.0.0.1:8099";

export default defineConfig({
  testDir: "e2e",
  timeout: 60_000,
  retries: 0,
  use: {
    baseURL: url,
    viewport: { width: 1440, height: 900 },
    launchOptions: process.env.PW_CHROMIUM ? { executablePath: process.env.PW_CHROMIUM } : {},
  },
  ...(process.env.PITWALL_URL
    ? {}
    : {
        webServer: {
          command: "cd ../server && go run ./cmd/pitwall serve -addr 127.0.0.1:8099",
          url: `${url}/healthz`,
          reuseExistingServer: true,
          timeout: 120_000,
        },
      }),
});
