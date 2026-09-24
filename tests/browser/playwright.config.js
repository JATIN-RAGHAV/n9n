import { defineConfig } from '@playwright/test';

const browserName = process.env.PLAYWRIGHT_BROWSER || 'chromium';
if (!['chromium', 'firefox', 'webkit'].includes(browserName)) {
  throw new Error(`Unsupported PLAYWRIGHT_BROWSER: ${browserName}`);
}
if (browserName !== 'chromium' && process.env.PLAYWRIGHT_CHANNEL) {
  throw new Error('PLAYWRIGHT_CHANNEL is only supported with Chromium');
}

export default defineConfig({
  testDir: '.',
  testMatch: '*.spec.js',
  timeout: 120_000,
  use: {
    baseURL: process.env.BASE_URL || 'http://localhost:8080',
    browserName,
    channel: browserName === 'chromium' ? process.env.PLAYWRIGHT_CHANNEL || undefined : undefined,
    headless: true,
    viewport: { width: 1440, height: 900 },
    actionTimeout: 10_000,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  expect: { timeout: 45_000 },
  retries: 0,
  reporter: [['list']],
});
