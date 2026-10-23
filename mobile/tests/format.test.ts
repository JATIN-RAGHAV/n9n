import assert from 'node:assert/strict';
import test from 'node:test';
import { attemptSummary, formatDurationMs } from '../src/format';

test('durations read naturally at every scale', () => {
  assert.equal(formatDurationMs(0), '0 ms');
  assert.equal(formatDurationMs(999), '999 ms');
  assert.equal(formatDurationMs(1480), '1.5 s');
  assert.equal(formatDurationMs(12400), '12 s');
  assert.equal(formatDurationMs(90000), '1.5 min');
});

test('attempt summaries include the status code only when one was received', () => {
  assert.equal(attemptSummary({ attempt: 1, status: 'failed', http_status: 500, duration_ms: 210 }),
    'Attempt 1 · failed · HTTP 500 · 210 ms');
  assert.equal(attemptSummary({ attempt: 2, status: 'failed', error: 'HTTP request failed', duration_ms: 15000 }),
    'Attempt 2 · failed · 15 s');
  assert.equal(attemptSummary({ attempt: 3, status: 'succeeded', http_status: 200, duration_ms: 80 }),
    'Attempt 3 · succeeded · HTTP 200 · 80 ms');
});
