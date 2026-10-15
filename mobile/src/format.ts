import { StepAttempt } from './types';

/** Renders a millisecond duration at the scale a person would read it. */
export function formatDurationMs(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(ms < 10000 ? 1 : 0)} s`;
  return `${(ms / 60000).toFixed(1)} min`;
}

/** One line per HTTP attempt: number, outcome, status code and timing. */
export function attemptSummary(attempt: StepAttempt): string {
  const parts = [`Attempt ${attempt.attempt}`, attempt.status];
  if (attempt.http_status != null) parts.push(`HTTP ${attempt.http_status}`);
  parts.push(formatDurationMs(attempt.duration_ms));
  return parts.join(' · ');
}
