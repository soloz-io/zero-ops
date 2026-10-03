import { fullJitterDelay, isRetryableStatus, parseRetryAfter } from "./retry-policy.js";
import { countUsableKeys } from "./usable-keys.js";

/** One failed attempt, reported to `onFetchFailure`. */
export interface JwksFetchFailure {
  url: string;
  attempt: number;
  attempts: number;
  /** What happened: timeout, network error, HTTP status, malformed or unusable key set. */
  reason: string;
  status?: number;
  /** Whether another attempt follows within this fetch. */
  retrying: boolean;
}

export interface FetchPolicy {
  fetchTimeoutMs: number;
  fetchAttempts: number;
  retryBaseMs: number;
  retryMaxMs: number;
  algorithms: string[];
  report: (failure: JwksFetchFailure) => void;
}

export type FetchOutcome =
  | { ok: true; body: string; keys: number }
  /**
   * Stopped by the caller or by shutdown: not a dependency failure. Never
   * reported to `onFetchFailure` and never counted against the issuer.
   */
  | { ok: false; cancelled: true; reason: string; attempt: number }
  | {
      ok: false;
      cancelled?: false;
      reason: string;
      attempt: number;
      status?: number;
      /** Set when the issuer asked for longer than `retryMaxMs`: contact it no sooner. */
      retryAfterUntil?: number;
    };

/** What one attempt produced, before the retry decision. */
type Attempt =
  | { ok: true; body: string; keys: number }
  | { ok: false; cancelled: true; reason: string }
  | { ok: false; cancelled?: false; reason: string; status?: number; retryable: boolean; retryAfterMs?: number };

/**
 * One JWKS fetch, attempted up to `fetchAttempts` times.
 *
 * The body is read inside the attempt's timeout, so a stall mid-body is a timed
 * out attempt and not a parse error. A response counts only if it holds a usable
 * signing key. Never throws: the caller turns a failed outcome into an error.
 *
 * An abort of `init.signal` (the caller, or the cache's shutdown) ends it as
 * `cancelled`, and is not reported: a deployment stopping is not the issuer
 * being unavailable, and ADR-022's dependency metrics must not say it was.
 */
export async function fetchJwksWithRetry(url: string, init: RequestInit, policy: FetchPolicy): Promise<FetchOutcome> {
  const outer = init.signal ?? undefined;
  let attempt = 0;
  for (;;) {
    attempt++;
    const result = await attemptOnce(url, init, outer, policy);
    if (result.ok) return result;
    if (result.cancelled) return { ok: false, cancelled: true, reason: result.reason, attempt };

    const longWait = result.retryAfterMs !== undefined && result.retryAfterMs > policy.retryMaxMs;
    const retrying = result.retryable && !longWait && attempt < policy.fetchAttempts;
    policy.report({ url, attempt, attempts: policy.fetchAttempts, reason: result.reason, status: result.status, retrying });

    if (!retrying) {
      return {
        ok: false,
        reason: result.reason,
        attempt,
        status: result.status,
        retryAfterUntil: longWait ? Date.now() + result.retryAfterMs! : undefined,
      };
    }
    const wait =
      result.retryAfterMs !== undefined
        ? result.retryAfterMs + Math.random() * policy.retryBaseMs
        : fullJitterDelay(attempt, policy.retryBaseMs, policy.retryMaxMs);
    if (!(await sleep(wait, outer))) {
      return { ok: false, cancelled: true, reason: "cancelled while waiting to retry", attempt };
    }
  }
}

/** Wait `ms`, or less if `signal` aborts. Resolves false when aborted. The timer never outlives the abort. */
function sleep(ms: number, signal: AbortSignal | undefined): Promise<boolean> {
  if (signal?.aborted) return Promise.resolve(false);
  return new Promise((resolve) => {
    const onAbort = () => {
      clearTimeout(timer);
      resolve(false);
    };
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve(true);
    }, ms);
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

async function attemptOnce(
  url: string,
  init: RequestInit,
  outer: AbortSignal | undefined,
  policy: FetchPolicy,
): Promise<Attempt> {
  const timeout = AbortSignal.timeout(policy.fetchTimeoutMs);
  const signal = outer ? AbortSignal.any([outer, timeout]) : timeout;
  const started = Date.now();
  try {
    const res = await fetch(url, { ...init, signal });
    if (res.status !== 200) {
      await res.body?.cancel().catch(() => {});
      const retryAfterMs = isRetryableStatus(res.status) ? parseRetryAfter(res.headers.get("retry-after")) : undefined;
      const reason = `HTTP ${res.status}${retryAfterMs !== undefined ? `, Retry-After ${Math.ceil(retryAfterMs / 1000)}s` : ""}`;
      return { ok: false, reason, status: res.status, retryable: isRetryableStatus(res.status), retryAfterMs };
    }

    const body = await res.text();
    let json: unknown;
    try {
      json = JSON.parse(body);
    } catch {
      const type = res.headers.get("content-type") ?? "none";
      return { ok: false, reason: `response is not JSON (${body.length} bytes, content-type ${type})`, status: 200, retryable: true };
    }
    const keys = await countUsableKeys(json, policy.algorithms);
    if (keys === 0) {
      return {
        ok: false,
        reason: `key set has no usable signing key for ${policy.algorithms.join(", ")}`,
        status: 200,
        retryable: false,
      };
    }
    return { ok: true, body, keys };
  } catch (err) {
    const elapsed = Date.now() - started;
    if (outer?.aborted) return { ok: false, cancelled: true, reason: `cancelled after ${elapsed}ms` };
    if (timeout.aborted) {
      return {
        ok: false,
        reason: `timed out after ${elapsed}ms (limit ${policy.fetchTimeoutMs}ms, headers and body)`,
        retryable: true,
      };
    }
    const code = (err as { cause?: { code?: string } }).cause?.code ?? (err as Error).message;
    return { ok: false, reason: `network error: ${code}`, retryable: true };
  }
}
