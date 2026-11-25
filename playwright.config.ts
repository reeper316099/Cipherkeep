import { defineConfig, devices } from '@playwright/test';

// End-to-end tests of the app: the Go API server (go/cmd/ck-server) serving the UI bundle the desktop app embeds,
// driven in WebKit (the engine of the macOS and Linux web views) and Chromium (close to Windows' WebView2).
// The desktop shell around them is covered by scripts/smoke-app.mjs. CK_E2E_BROWSERS=chromium limits the engines.
const engines = (process.env.CK_E2E_BROWSERS ?? 'webkit,chromium').split(',');

export default defineConfig({
  testDir: 'e2e',
  globalSetup: './e2e/global-setup.ts',
  timeout: 180_000,
  expect: { timeout: 15_000 },
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: [['list']],
  use: { trace: 'retain-on-failure' },
  projects: [
    { name: 'webkit', use: { ...devices['Desktop Safari'] } },
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
  ].filter((p) => engines.includes(p.name)),
});
