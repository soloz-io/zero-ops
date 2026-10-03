import * as jose from "jose";
import { JwksFetchError } from "../types.js";
import { fetchJwksWithRetry, type JwksFetchFailure } from "./jwks/fetch.js";
import { resolveJwksOptions, type JwksCacheOptions, type ResolvedJwksOptions } from "./jwks/options.js";
import { fullJitterDelay } from "./jwks/retry-policy.js";

export type { JwksCacheOptions } from "./jwks/options.js";
export type { JwksFetchFailure } from "./jwks/fetch.js";
export { fullJitterDelay } from "./jwks/retry-policy.js";

/**
 * The issuer's signing keys, held in ONE place.
 *
 * There is one store -- jose's RemoteJWKSet -- and one way into the network,
 * installed as its `customFetch`. jose keeps the key set, de-duplicates
 * concurrent fetches and refetches on an unknown `kid`. The modules under
 * ./jwks/ decide the rest:
 *
 *   options.ts       configuration, validated
 *   retry-policy.ts  what is retried, and the full-jitter delay
 *   fetch.ts         one fetch: per-attempt timeout over headers AND body, retries
 *   usable-keys.ts   what counts as a key this service can verify with
 *
 * and this class owns the lifecycle and the status.
 *
 * LIFECYCLE (ADR-022, stable-but-not-ready). A service calls `start()` at boot
 * and reports `status().ready` as its readiness. `start()` fetches immediately,
 * retries with capped full-jitter backoff until it succeeds, then refreshes in
 * the background, so requests verify against keys already held instead of each
 * cold pod's first requests waiting on a download. It never throws and never
 * exits the process: an unreachable issuer leaves the pod alive and unready, and
 * it becomes ready on its own when the issuer returns.
 */
export interface JwksStatus {
  /**
   * The service can verify tokens: a key set holding at least one usable
   * signing key was fetched less than `maxStaleMs` ago. This is the dependency
   * half of the readiness probe, and the value behind `dependency_status`.
   */
  ready: boolean;
  /** Usable signing keys in the held set. */
  keys: number;
  lastSuccessAt?: number;
  consecutiveFailures: number;
  /** The last failure's reason. Internal detail: for logs, never a response body. */
  lastError?: string;
  /** `start()` is running the background refresh. */
  maintaining: boolean;
}

export class JwksCache {
  private readonly opts: ResolvedJwksOptions;
  private readonly store: jose.RemoteJWKSet;

  private usableKeys = 0;
  private lastSuccessAt?: number;
  private consecutiveFailures = 0;
  private lastError?: string;
  /** No request reaches the issuer before this time: it answered with a long Retry-After. */
  private retryAfterUntil = 0;
  /** The running `start()`, if any. */
  private run?: AbortController;
  private timer?: ReturnType<typeof setTimeout>;

  constructor(options: JwksCacheOptions) {
    this.opts = resolveJwksOptions(options);
    const o = this.opts;

    // Creating the store fetches nothing; jose fetches on first use.
    //
    // jose's own timeout spans headers AND body, and a body that stalls past it
    // is reported as "Failed to parse the JSON Web Key Set HTTP response as JSON"
    // -- what a Waypoint SDK pod returned for a fetch Zitadel had served in under
    // a second. So the fetch is ours, and jose's timeout is set to outlast every
    // attempt of it.
    //
    // cacheMaxAge is the STALENESS bound, not the refresh period: the background
    // refresh keeps the set younger than that, so a request does not block on a
    // download unless refreshing has been failing for longer than maxStaleMs.
    const budget = o.fetchAttempts * o.fetchTimeoutMs + (o.fetchAttempts - 1) * o.retryMaxMs + 1_000;
    this.store = jose.createRemoteJWKSet(new URL(o.jwksUrl), {
      timeoutDuration: budget,
      cooldownDuration: o.refreshIntervalMs,
      cacheMaxAge: o.maxStaleMs,
      [jose.customFetch]: (url, init) => this.fetch(url, init),
    });
  }

  /** The key resolver `jwtVerify` uses. The one store; there is no other. */
  async getSigningKey(): Promise<jose.JWTVerifyGetKey> {
    return this.store;
  }

  /** One fetch, awaited. Resolves when a usable set is held; rejects with JwksFetchError. Prefer `start()`. */
  async warm(): Promise<void> {
    await this.store.reload();
  }

  /**
   * Keep the keys fetched until `stop()`, or until `signal` aborts. Idempotent
   * while running; never throws.
   *
   * Until the first success it retries with full-jitter backoff capped at
   * `backoffMaxMs`, honouring a Retry-After. After it, it refreshes every
   * `keyTtlMs` with ±10% jitter, so a fleet started together does not refresh
   * together. Timers are unref'd and do not hold the process open.
   *
   * Each run owns an AbortController. Stopping aborts it: the pending timer is
   * cleared, a retry pause in progress ends, an in-flight fetch is cancelled, and
   * the loop cannot reschedule itself -- including when `start()` is called again
   * before the old run's fetch has settled, which then cannot leave two loops.
   * jose shares one download between everything waiting on it, so a stop also
   * fails that download for its other waiters; a run started right after a stop
   * sees one failure and retries it after a jittered pause.
   */
  start(options: { signal?: AbortSignal } = {}): void {
    if (this.run) return;
    const run = new AbortController();
    this.run = run;
    if (options.signal) {
      if (options.signal.aborted) return this.stop();
      options.signal.addEventListener("abort", () => this.stopRun(run), { once: true, signal: run.signal });
    }
    let failures = 0;
    const tick = async () => {
      let next: number;
      try {
        await this.store.reload();
        failures = 0;
        next = this.opts.keyTtlMs * (0.9 + Math.random() * 0.2);
      } catch {
        failures++;
        const backoff = fullJitterDelay(failures, Math.max(this.opts.retryBaseMs, 1), this.opts.backoffMaxMs);
        next = Math.max(backoff, this.retryAfterUntil - Date.now());
      }
      if (run.signal.aborted) return;
      this.timer = setTimeout(() => void tick(), next);
      this.timer.unref?.();
    };
    void tick();
  }

  /** End the background refresh. The held keys stay usable until stale. Idempotent. */
  stop(): void {
    if (this.run) this.stopRun(this.run);
  }

  private stopRun(run: AbortController): void {
    if (this.run !== run) return;
    run.abort();
    this.run = undefined;
    if (this.timer) clearTimeout(this.timer);
    this.timer = undefined;
  }

  status(): JwksStatus {
    const fresh = this.lastSuccessAt !== undefined && Date.now() - this.lastSuccessAt < this.opts.maxStaleMs;
    return {
      ready: fresh && this.usableKeys > 0,
      keys: this.usableKeys,
      lastSuccessAt: this.lastSuccessAt,
      consecutiveFailures: this.consecutiveFailures,
      lastError: this.lastError,
      maintaining: this.run !== undefined,
    };
  }

  /**
   * jose's `customFetch`: the only network path. Records the outcome, and hands
   * jose either a buffered body or an error -- never a set without a usable key,
   * so jose never replaces held keys with one.
   */
  private async fetch(url: string, init: RequestInit): Promise<Response> {
    const holdoff = this.retryAfterUntil - Date.now();
    if (holdoff > 0) {
      const reason = `issuer asked to retry after ${Math.ceil(holdoff / 1000)}s; not contacted`;
      this.recordFailure(reason);
      throw this.error(reason, 0);
    }

    // A stop cancels a fetch in flight as well as the loop around it.
    const signal = this.run && init.signal ? AbortSignal.any([init.signal, this.run.signal]) : init.signal;
    const outcome = await fetchJwksWithRetry(url, { ...init, signal }, { ...this.opts, report: (f) => this.report(f) });
    if (outcome.ok) {
      this.usableKeys = outcome.keys;
      this.lastSuccessAt = Date.now();
      this.consecutiveFailures = 0;
      this.lastError = undefined;
      return new Response(outcome.body, { status: 200, headers: { "content-type": "application/json" } });
    }
    // Cancelled is not a failure: status keeps describing the issuer.
    if (outcome.cancelled) throw this.error(outcome.reason, outcome.attempt);
    if (outcome.retryAfterUntil) this.retryAfterUntil = outcome.retryAfterUntil;
    this.recordFailure(outcome.reason);
    throw this.error(outcome.reason, outcome.attempt, outcome.status);
  }

  private recordFailure(reason: string): void {
    this.consecutiveFailures++;
    this.lastError = reason;
  }

  private error(reason: string, attempt: number, status?: number): JwksFetchError {
    const detail = `${reason}; ${attempt} of ${this.opts.fetchAttempts} attempt(s)`;
    return new JwksFetchError(this.opts.jwksUrl, undefined, detail, status);
  }

  private report(failure: JwksFetchFailure): void {
    try {
      this.opts.onFetchFailure?.(failure);
    } catch {
      // A metrics hook must not change the outcome of a fetch.
    }
  }
}
