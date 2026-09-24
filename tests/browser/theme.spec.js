import { test, expect } from '@playwright/test';
import { PNG } from 'pngjs';

async function semantics(page) {
  const placeholder = page.locator('flt-semantics-placeholder');
  await placeholder.waitFor({ state: 'attached' });
  await placeholder.evaluate(element => element.click());
}

function brightness(buffer) {
  const { data } = PNG.sync.read(buffer);
  let sum = 0;
  for (let i = 0; i < data.length; i += 4) sum += (data[i] + data[i + 1] + data[i + 2]) / 3;
  return sum / (data.length / 4);
}

test('theme changes visible surfaces and persists across refresh on mobile', async ({ page }) => {
  await page.goto('/');
  await semantics(page);
  const light = page.getByRole('button', { name: 'Switch to light theme' });
  await expect(light).toBeVisible();
  const darkBrightness = brightness(await page.screenshot());
  await light.click();
  await expect(page.getByRole('button', { name: 'Switch to dark theme' })).toBeVisible();
  await expect.poll(() => page.evaluate(() => localStorage.getItem('n9n.theme'))).toBe('light');
  await page.waitForTimeout(350);
  expect(brightness(await page.screenshot())).toBeGreaterThan(darkBrightness + 25);

  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  await semantics(page);
  await page.getByRole('button', { name: 'Switch to dark theme' }).click();
  await expect.poll(() => page.evaluate(() => localStorage.getItem('n9n.theme'))).toBe('dark');
  await page.reload();
  await semantics(page);
  await expect(page.getByRole('button', { name: 'Switch to light theme' })).toBeVisible();
});
