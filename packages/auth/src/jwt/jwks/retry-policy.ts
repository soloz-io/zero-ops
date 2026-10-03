/**
 * How a failed JWKS fetch is retried. Pure functions: no I/O, no state.
 *
 *   network error, timeout          retry
 *   200, not JSON                   retry
 *   408, 429, 5xx                   retry; Retry-After honoured (see fetch.ts)
 *   200, no usable signing key      terminal: it will not change in a second
 *   any other status (400/401/403/  terminal: a wrong URL or a refusal, and
 *   404, 3xx -- redirects are not   repeating it only delays the error
 *   followed for a key set)
 */

/** Whether an HTTP status from the JWKS endpoint is worth another attempt. */
export function isRetryableStatus(status: number): boolean {
  return status === 408 || status === 429 || status >= 500;
}

/**
 * Delay before attempt `n + 1`, with full jitter (ADR-022): uniformly random in
 * [0, min(cap, base * 2^(n-1))], so a fleet failing together does not retry
 * together.
 */
export function fullJitterDelay(n: number, baseMs: number, capMs: number): number {
  return Math.random() * Math.min(capMs, baseMs * 2 ** Math.max(0, n - 1));
}

/** A Retry-After header (seconds or an HTTP date) as milliseconds from now. */
export function parseRetryAfter(header: string | null): number | undefined {
  if (!header) return undefined;
  const seconds = Number(header);
  if (Number.isFinite(seconds) && seconds >= 0) return seconds * 1000;
  const date = Date.parse(header);
  return Number.isNaN(date) ? undefined : Math.max(0, date - Date.now());
}
