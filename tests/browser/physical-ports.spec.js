import { test, expect } from '@playwright/test';
import { PNG } from 'pngjs';

const triggerX = 180;
const actionX = 650;
const nodeY = 130;
const betweenPorts = actionX - (triggerX + 216);

function greenPortCenters(buffer) {
  const image = PNG.sync.read(buffer);
  const { width, height, data } = image;
  const marked = new Uint8Array(width * height);
  const green = (x, y) => {
    const i = (y * width + x) * 4;
    return Math.abs(data[i] - 37) <= 6 && Math.abs(data[i + 1] - 92) <= 6 && Math.abs(data[i + 2] - 81) <= 6 && data[i + 3] > 240;
  };
  const circles = [];
  for (let y = 200; y < Math.min(height - 80, 800); y++) {
    for (let x = 250; x < Math.min(width - 300, 1120); x++) {
      const start = y * width + x;
      if (marked[start] || !green(x, y)) continue;
      const queue = [[x, y]];
      marked[start] = 1;
      let left = x, right = x, top = y, bottom = y;
      for (let index = 0; index < queue.length; index++) {
        const [px, py] = queue[index];
        left = Math.min(left, px); right = Math.max(right, px);
        top = Math.min(top, py); bottom = Math.max(bottom, py);
        for (const [nx, ny] of [[px - 1, py], [px + 1, py], [px, py - 1], [px, py + 1]]) {
          if (nx < 250 || nx >= width - 300 || ny < 200 || ny >= height - 80) continue;
          const next = ny * width + nx;
          if (!marked[next] && green(nx, ny)) { marked[next] = 1; queue.push([nx, ny]); }
        }
      }
      const diameterX = right - left + 1;
      const diameterY = bottom - top + 1;
      if (queue.length >= 18 && queue.length <= 150 && diameterX >= 6 && diameterX <= 15 && diameterY >= 6 && diameterY <= 15) {
        circles.push({ x: (left + right) / 2, y: (top + bottom) / 2, size: queue.length });
      }
    }
  }
  return circles;
}

function findConnectedPorts(circles) {
  for (const output of circles) {
    for (const input of circles) {
      if (Math.abs(input.x - output.x - betweenPorts) <= 3 && Math.abs(input.y - output.y) <= 3) {
        return { output, input };
      }
    }
  }
  return null;
}

test('outer halves of green ports connect with accessibility semantics disabled', async ({ page }, testInfo) => {
  const email = `physical-${Date.now()}-${Math.random().toString(36).slice(2, 8)}@example.test`;
  const registration = await page.request.post('/api/auth/register', { data: { email, password: 'physical-ports-password' } });
  expect(registration.ok()).toBeTruthy();
  const draft = {
    nodes: [
      { id: 'trigger', type: 'manual_trigger', position: { x: triggerX, y: nodeY }, config: {} },
      { id: 'action', type: 'set_fields', position: { x: actionX, y: nodeY }, config: { fields: {} } },
    ],
    edges: [],
  };
  const created = await page.request.post('/api/workflows', { data: { name: 'Physical port regression', draft } });
  expect(created.ok()).toBeTruthy();
  const workflowId = (await created.json()).workflow.id;
  const loaded = page.waitForResponse((response) => response.url().endsWith(`/api/workflows/${workflowId}`) && response.ok());
  await page.goto(`/workflows/${workflowId}`);
  await loaded;
  await page.waitForTimeout(1000);
  expect(await page.locator('flt-semantics-placeholder').count(), 'semantics must remain disabled during pointer clicks').toBeGreaterThan(0);

  const before = await page.screenshot();
  const ports = findConnectedPorts(greenPortCenters(before));
  if (!ports) {
    await testInfo.attach('before-port-clicks', { body: before, contentType: 'image/png' });
    throw new Error('Could not locate the green output/input port pair in the rendered Flutter canvas');
  }
  await page.mouse.click(ports.output.x + 4, ports.output.y);
  await page.mouse.click(ports.input.x - 4, ports.input.y);
  await testInfo.attach('after-port-clicks', { body: await page.screenshot(), contentType: 'image/png' });

  // Accessibility is enabled only after the physical pointer interaction, to save the draft.
  await page.locator('flt-semantics-placeholder').evaluate((element) => element.click());
  await page.getByRole('button', { name: 'Save draft' }).click();
  await expect(page.getByText('Draft saved').last()).toBeVisible();
  const saved = await page.request.get(`/api/workflows/${workflowId}`);
  expect(saved.ok()).toBeTruthy();
  expect((await saved.json()).workflow.draft.edges).toEqual([
    expect.objectContaining({ source: 'trigger', target: 'action', source_port: 'out' }),
  ]);
});
