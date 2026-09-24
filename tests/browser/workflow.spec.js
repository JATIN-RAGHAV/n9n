import { test, expect } from '@playwright/test';

async function enableFlutterSemantics(page) {
  const placeholder = page.locator('flt-semantics-placeholder');
  await placeholder.waitFor({ state: 'attached', timeout: 20_000 }).catch(() => {});
  if (await placeholder.count()) await placeholder.evaluate((element) => element.click());
}

async function typeFlutter(page, locator, value) {
  await locator.click();
  await page.waitForTimeout(300);
  await page.keyboard.type(value, { delay: 50 });
}

function currentRoute(page) {
  const url = new URL(page.url());
  return url.hash.startsWith('#/') ? url.hash.slice(1) : url.pathname;
}

test('Flutter Wasm UI builds and runs a workflow across a deep link and mobile menu', async ({ page }) => {
  const errors = [];
  const assets = [];
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('response', (response) => {
    if (/\.(?:wasm|mjs)(?:\?|$)/.test(response.url())) {
      assets.push({ url: response.url(), status: response.status(), type: response.headers()['content-type'] || '' });
    }
  });

  await page.goto('/');
  await enableFlutterSemantics(page);
  await expect(page.getByText('Welcome back')).toBeVisible();
  await page.getByText('New here? Create an account').click();
  await expect(page.getByText('Create your workspace')).toBeVisible();
  const email = `browser-${Date.now()}-${Math.random().toString(36).slice(2, 8)}@example.test`;
  await typeFlutter(page, page.getByRole('textbox').first(), email);
  await typeFlutter(page, page.getByRole('textbox').nth(1), 'browser-smoke-password-123');
  await page.getByRole('button', { name: 'Create account' }).click();
  await expect(page.getByRole('button', { name: 'New workflow' })).toBeVisible();

  await page.getByRole('button', { name: 'New workflow' }).click();
  const createDialog = page.getByRole('alertdialog');
  await expect(createDialog).toBeVisible();
  await typeFlutter(page, createDialog.getByRole('textbox'), 'Browser smoke flow');
  await createDialog.getByRole('button', { name: 'Create' }).click();
  await expect(page).toHaveURL(/\/workflows\/[0-9a-f]+/);
  const workflowPath = currentRoute(page);
  await expect(page.getByText('NODE LIBRARY')).toBeVisible();
  await page.getByText(/^Manual trigger$/i).first().click();
  await page.getByText(/^Set fields$/i).first().click();
  await expect(page.getByText('Unsaved changes')).toBeVisible();

  await page.getByRole('button', { name: 'Connect out output' }).first().click();
  await expect(page.getByText('Now choose an input port')).toBeVisible();
  await page.getByRole('button', { name: 'Connect input' }).click();
  await page.getByRole('button', { name: 'Save draft' }).click();
  await expect(page.getByText('Draft saved').last()).toBeVisible();
  await page.getByRole('button', { name: 'Publish' }).click();
  await expect(page.getByText(/Version 1 published/).last()).toBeVisible();

  await page.getByRole('button', { name: 'Run now' }).click();
  await expect(page.getByText('Run workflow')).toBeVisible();
  await page.getByRole('button', { name: 'Start run' }).click();
  await expect(page).toHaveURL(/\/runs\/[0-9a-f]+/);
  const runPath = currentRoute(page);
  const runId = runPath.split('/').at(-1);
  await expect.poll(async () => {
    const response = await page.request.get(`/api/runs/${runId}`);
    return (await response.json()).run?.status;
  }).toBe('succeeded');
  await expect(page.getByText('STEP BY STEP')).toBeVisible();
  await expect(page.getByText(/Manual trigger ·/)).toBeVisible();
  await expect(page.getByText(/Set fields ·/)).toBeVisible();

  await page.reload();
  await enableFlutterSemantics(page);
  await expect(page).toHaveURL(new RegExp(`/runs/${runId}$`));
  await expect(page.getByText('STEP BY STEP')).toBeVisible();
  await expect(page.getByRole('button', { name: /Set fields .* succeeded/ })).toBeVisible();

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(workflowPath);
  await enableFlutterSemantics(page);
  await expect(page.getByRole('button', { name: 'Open navigation menu' })).toBeVisible();
  await page.getByRole('button', { name: 'Open navigation menu' }).click();
  await page.getByRole('menuitem', { name: 'Credentials' }).click();
  await expect(page).toHaveURL(/\/credentials$/);
  await expect(page.getByRole('button', { name: 'Connect Gmail' }).first()).toBeVisible();

  expect(errors, `uncaught browser errors: ${errors.join(' | ')}`).toEqual([]);
  const wasm = assets.filter((asset) => /\.wasm(?:\?|$)/.test(asset.url));
  const modules = assets.filter((asset) => /\.mjs(?:\?|$)/.test(asset.url));
  expect(wasm.length, 'Flutter Wasm was loaded').toBeGreaterThan(0);
  expect(modules.length, 'Flutter module was loaded').toBeGreaterThan(0);
  for (const asset of wasm) {
    expect(asset.status, asset.url).toBe(200);
    expect(asset.type, asset.url).toContain('application/wasm');
  }
  for (const asset of modules) {
    expect(asset.status, asset.url).toBe(200);
    expect(asset.type, asset.url).toMatch(/javascript/);
  }
});
