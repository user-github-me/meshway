import { defineConfig } from '@playwright/test';
export default defineConfig({
  testDir: './e2e', timeout: 45_000, workers: 1,
  use: { baseURL: 'http://127.0.0.1:5173', headless: true, screenshot: 'only-on-failure', launchOptions: { executablePath: process.env.CHROMIUM_EXECUTABLE } },
  webServer: { command: 'npm run dev -- --port 5173 --strictPort', url: 'http://127.0.0.1:5173', reuseExistingServer: !process.env.CI, timeout: 30_000 },
});
