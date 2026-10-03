import type { JwksFetchFailure } from "./fetch.js";

/** What a JwksCache can be configured with. Every field but `jwksUrl` has a default. */
export interface JwksCacheOptions {
  /** JWKS endpoint URL. `https:`; `http:` only for a loopback host or with `allowInsecureHttp`. */
  jwksUrl: string;
  /**
   * Permit `http:` to a non-loopback host. For a development stack that has no
   * TLS in front of its issuer; never for a deployed environment, where the key
   * set is the root of every token's trust and must not be fetched in the clear.
   */
  allowInsecureHttp?: boolean;
  /** Background refresh period once `start()` has been called, ms (default 300000 = 5 min). */
  keyTtlMs?: number;
  /**
   * Oldest a key set may be and still be used, ms (default 900000 = 15 min).
   * Past it, a request refetches before verifying and `status().ready` is false.
   * Bounds how long a key the issuer has withdrawn can still verify a token.
   */
  maxStaleMs?: number;
  /** Minimum interval between refetches caused by a token naming an unknown `kid`, ms (default 10000). */
  refreshIntervalMs?: number;
  /** Timeout for ONE fetch attempt, headers and body together, ms (default 10000). */
  fetchTimeoutMs?: number;
  /** Attempts within one fetch, i.e. while a request waits (default 2 -- one retry). */
  fetchAttempts?: number;
  /** Full-jitter backoff base, ms (default 250). */
  retryBaseMs?: number;
  /** Cap on the pause between attempts while a request waits, ms (default 2000). */
  retryMaxMs?: number;
  /** Cap on the background backoff while the issuer is unavailable, ms (default and maximum 300000 -- ADR-022). */
  backoffMaxMs?: number;
  /** Algorithms a key must support to count as usable (default: RS256, ES256). */
  algorithms?: string[];
  /**
   * Called on every failed attempt, with the full detail. Where an application
   * increments `dependency_unavailable_total` (ADR-022). Must not throw; if it
   * does, the error is ignored.
   */
  onFetchFailure?: (failure: JwksFetchFailure) => void;
}

/** The options after validation: every value present and within bounds. */
export interface ResolvedJwksOptions {
  jwksUrl: string;
  keyTtlMs: number;
  maxStaleMs: number;
  refreshIntervalMs: number;
  fetchTimeoutMs: number;
  fetchAttempts: number;
  retryBaseMs: number;
  retryMaxMs: number;
  backoffMaxMs: number;
  algorithms: string[];
  onFetchFailure?: (failure: JwksFetchFailure) => void;
}

/** ADR-022: backoff is capped at five minutes. */
export const ADR_022_BACKOFF_CAP_MS = 5 * 60_000;

const DEFAULTS = {
  keyTtlMs: 5 * 60_000,
  maxStaleMs: 15 * 60_000,
  refreshIntervalMs: 10_000,
  fetchTimeoutMs: 10_000,
  fetchAttempts: 2,
  retryBaseMs: 250,
  retryMaxMs: 2_000,
  backoffMaxMs: ADR_022_BACKOFF_CAP_MS,
  algorithms: ["RS256", "ES256"],
};

/**
 * Validate, never normalise: a wrong value is a configuration error, and
 * silently rounding it would run the service on a policy nobody chose.
 * Throws RangeError for a value out of bounds, TypeError for an unparseable URL.
 */
export function resolveJwksOptions(opts: JwksCacheOptions): ResolvedJwksOptions {
  const jwksUrl = checkedUrl(opts.jwksUrl, opts.allowInsecureHttp ?? false);
  const fetchTimeoutMs = int("fetchTimeoutMs", opts.fetchTimeoutMs, DEFAULTS.fetchTimeoutMs, 100, 60_000);
  const fetchAttempts = int("fetchAttempts", opts.fetchAttempts, DEFAULTS.fetchAttempts, 1, 5);
  const retryBaseMs = int("retryBaseMs", opts.retryBaseMs, DEFAULTS.retryBaseMs, 0, 10_000);
  const retryMaxMs = int("retryMaxMs", opts.retryMaxMs, DEFAULTS.retryMaxMs, retryBaseMs, 30_000);
  const backoffMaxMs = int("backoffMaxMs", opts.backoffMaxMs, DEFAULTS.backoffMaxMs, retryBaseMs, ADR_022_BACKOFF_CAP_MS);
  const keyTtlMs = int("keyTtlMs", opts.keyTtlMs, DEFAULTS.keyTtlMs, 1_000, 24 * 3_600_000);
  const maxStaleMs = int("maxStaleMs", opts.maxStaleMs, Math.max(DEFAULTS.maxStaleMs, keyTtlMs), keyTtlMs, 7 * 24 * 3_600_000);
  const refreshIntervalMs = int(
    "refreshIntervalMs",
    opts.refreshIntervalMs,
    Math.min(DEFAULTS.refreshIntervalMs, keyTtlMs),
    0,
    keyTtlMs,
  );
  const algorithms = opts.algorithms ?? DEFAULTS.algorithms;
  if (algorithms.length === 0) {
    throw new RangeError("JwksCacheOptions.algorithms must name at least one algorithm");
  }
  return {
    jwksUrl,
    keyTtlMs,
    maxStaleMs,
    refreshIntervalMs,
    fetchTimeoutMs,
    fetchAttempts,
    retryBaseMs,
    retryMaxMs,
    backoffMaxMs,
    algorithms,
    onFetchFailure: opts.onFetchFailure,
  };
}

const LOOPBACK = new Set(["localhost", "127.0.0.1", "[::1]"]);

/** A JWKS URL this runtime may fetch from. TypeError when unparseable, RangeError when not permitted. */
function checkedUrl(raw: string, allowInsecureHttp: boolean): string {
  const url = new URL(raw);
  if (url.protocol === "https:") return url.href;
  if (url.protocol === "http:" && (allowInsecureHttp || LOOPBACK.has(url.hostname))) return url.href;
  throw new RangeError(
    `JwksCacheOptions.jwksUrl must be https:` +
      (url.protocol === "http:" ? ` (http: is permitted only for a loopback host, or with allowInsecureHttp)` : "") +
      `, got ${url.protocol}//${url.host}`,
  );
}

function int(name: string, value: number | undefined, fallback: number, min: number, max: number): number {
  const v = value ?? fallback;
  if (!Number.isInteger(v) || v < min || v > max) {
    throw new RangeError(`JwksCacheOptions.${name} must be an integer in [${min}, ${max}], got ${String(value)}`);
  }
  return v;
}
