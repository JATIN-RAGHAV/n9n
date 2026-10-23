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

async function replaceFlutterText(page, locator, value) {
  await locator.click();
  await page.waitForTimeout(300);
  await page.keyboard.press(process.platform === 'darwin' ? 'Meta+A' : 'Control+A');
  await page.keyboard.press('Backspace');
  await page.keyboard.type(value, { delay: 30 });
}

function currentRoute(page) {
  const url = new URL(page.url());
  return url.hash.startsWith('#/') ? url.hash.slice(1) : url.pathname;
}

async function runStatus(page, runId) {
  return (await (await page.request.get(`/api/runs/${runId}`)).json()).run?.status;
}

test('a failed run is replayed from its failing step with a corrected input', async ({ page }) => {
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));

  await page.goto('/');
  await enableFlutterSemantics(page);
  await expect(page.getByText('Welcome back')).toBeVisible();
  await page.getByText('New here? Create an account').click();
  await expect(page.getByText('Create your workspace')).toBeVisible();
  const email = `replay-${Date.now()}-${Math.random().toString(36).slice(2, 8)}@example.test`;
  await typeFlutter(page, page.getByRole('textbox').first(), email);
  await typeFlutter(page, page.getByRole('textbox').nth(1), 'browser-smoke-password-123');
  await page.getByRole('button', { name: 'Create account' }).click();
  await expect(page.getByRole('button', { name: 'New workflow' })).toBeVisible();

  // A three-node chain whose last node needs input.absent.value.
  const created = await page.request.post('/api/workflows', { data: {
    name: 'Browser replay flow',
    draft: {
      nodes: [
        { id: 'trigger', type: 'manual_trigger', position: { x: 40, y: 40 }, config: {} },
        { id: 'stamp', type: 'set_fields', position: { x: 320, y: 40 }, config: { fields: { stamped: 'yes' } } },
        { id: 'needs', type: 'set_fields', position: { x: 600, y: 40 }, config: { fields: { copy: '{{input.absent.value}}' } } },
      ],
      edges: [
        { id: 'e1', source: 'trigger', target: 'stamp', source_port: 'out' },
        { id: 'e2', source: 'stamp', target: 'needs', source_port: 'out' },
      ],
    },
  } });
  expect(created.ok()).toBeTruthy();
  const workflowId = (await created.json()).workflow.id;
  const queued = await page.request.post(`/api/workflows/${workflowId}/test`, { data: { input: {} } });
  expect(queued.ok()).toBeTruthy();
  const parentId = (await queued.json()).run.id;
  await expect.poll(() => runStatus(page, parentId)).toBe('failed');

  await page.goto(`/runs/${parentId}`);
  await enableFlutterSemantics(page);
  await expect(page.getByText('TIMELINE')).toBeVisible();
  await expect(page.getByText('AT THIS MOMENT')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Replay against current draft' })).toBeVisible();

  // The failing step is selected by default, so the moment panel's button
  // replays from it with the input edited to satisfy the mapping.
  await page.getByRole('button', { name: 'Re-run from here' }).first().click();
  await expect(page.getByText(/Re-run from Set fields · needs/)).toBeVisible();
  const dialog = page.getByRole('alertdialog');
  await replaceFlutterText(page, dialog.getByRole('textbox'), '{"stamped":"yes","absent":{"value":7}}');
  await dialog.getByRole('button', { name: 'Start replay' }).click();
  // The parent's URL already matches /runs/<id>, so wait until it changes.
  await expect(page).not.toHaveURL(new RegExp(`/runs/${parentId}$`));
  await expect(page).toHaveURL(/\/runs\/[0-9a-f]+$/);
  const replayId = currentRoute(page).split('/').at(-1);
  expect(replayId).not.toBe(parentId);
  await expect.poll(() => runStatus(page, replayId)).toBe('succeeded');

  const replay = await (await page.request.get(`/api/runs/${replayId}`)).json();
  expect(replay.run.kind).toBe('replay');
  expect(replay.run.parent_run_id).toBe(parentId);
  expect(replay.run.resume_from_node).toBe('needs');
  const steps = Object.fromEntries(replay.steps.map((step) => [step.node_id, step]));
  expect(steps.stamp.output.absent.value).toBe(7);
  expect(steps.needs.output.copy).toBe(7);
  await expect(page.getByText(/REPLAY FROM needs/)).toBeVisible();
  await expect(page.getByRole('button', { name: new RegExp(`Replay of ${parentId.slice(0, 8)}`) })).toBeVisible();

  const parent = await (await page.request.get(`/api/runs/${parentId}`)).json();
  expect(parent.children.map((child) => child.id)).toContain(replayId);
  expect((await (await page.request.get(`/api/workflows/${workflowId}`)).json()).workflow.published_version).toBeNull();

  expect(errors, `uncaught browser errors: ${errors.join(' | ')}`).toEqual([]);
});
