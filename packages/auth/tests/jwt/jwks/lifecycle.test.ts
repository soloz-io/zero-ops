import { describe, it, expect, afterEach, vi } from "vitest";
import { Hono } from "hono";
import { JwksCache } from "../../../src/jwt/jwks-cache.js";
import { authMiddleware } from "../../../src/middleware/hono.js";
import { JwksFetchError } from "../../../src/types.js";
import { closeServers, FAST, jwksServer, token, validator } from "./harness.js";

// The ADR-022 lifecycle: alive while the issuer is unavailable, not ready, ready
// on its own when it returns; held keys survive a failed refresh until stale;
// stopping leaves nothing running; retries are jittered and obey Retry-After.

afterEach(async () => {
  vi.restoreAllMocks();
  await closeServers();
});

/** Delays the background loop schedules: setTimeout calls made from jwks-cache.ts only. */
function backgroundDelays(): number[] {
  const delays: number[] = [];
  const real = globalThis.setTimeout;
  vi.spyOn(globalThis, "setTimeout").mockImplementation(((fn: () => void, ms?: number) => {
    if (new Error().stack?.includes("jwks-cache.ts")) delays.push(ms ?? 0);
    return real(fn, ms);
  }) as typeof setTimeout);
  return delays;
}

const until = (cond: () => boolean) =>
  vi.waitFor(() => expect(cond()).toBe(true), { timeout: 3000, interval: 10 });

describe("start: alive and not ready, then ready without a restart", () => {
  it("does not throw while the issuer is unavailable, and reports not ready", async () => {
    const s = await jwksServer([], "503");
    const v = validator(s.url, { fetchAttempts: 1 });
    expect(() => v.start()).not.toThrow();
    await until(() => v.status().consecutiveFailures >= 1); // the failure recorded, not merely sent
    expect(v.status()).toMatchObject({ ready: false, maintaining: true });
    v.stop();
  });

  it("keeps retrying, and becomes ready when the issuer returns", async () => {
    const s = await jwksServer(["503", "503", "503"]);
    const v = validator(s.url, { fetchAttempts: 1, backoffMaxMs: 50 });
    v.start();
    v.start(); // idempotent while running
    await until(() => v.status().ready);
    expect(v.status()).toMatchObject({ ready: true, keys: 1, consecutiveFailures: 0 });
    expect(s.hits()).toBe(4);
    await v.validate(await token()); // held: the request does not fetch
    expect(s.hits()).toBe(4);
    v.stop();
  });

  it("warm() is one awaited fetch; the first request then needs none", async () => {
    const s = await jwksServer([]);
    const v = validator(s.url);
    await v.warm();
    expect(v.status().ready).toBe(true);
    await v.validate(await token());
    expect(s.hits()).toBe(1);
  });
});

describe("a failed refresh keeps the held keys until they are stale", () => {
  it("keeps verifying, and stays ready, within maxStaleMs", async () => {
    const s = await jwksServer(["ok"], "503");
    const v = validator(s.url);
    await v.warm();
    await expect(v.warm()).rejects.toBeInstanceOf(JwksFetchError); // the refresh fails
    expect(v.status()).toMatchObject({ ready: true, keys: 1, consecutiveFailures: 1 });
    await expect(v.validate(await token())).resolves.toMatchObject({ sub: "svc" });
  });

  it("past maxStaleMs: not ready, and a request fails as a dependency failure (503)", async () => {
    const s = await jwksServer(["ok"], "503");
    const v = validator(s.url);
    await v.warm();
    const t = await token();
    const now = Date.now();
    vi.spyOn(Date, "now").mockReturnValue(now + 15 * 60_000 + 1); // default maxStaleMs
    expect(v.status().ready).toBe(false);

    const app = new Hono();
    app.use("*", authMiddleware({ validator: v, onDependencyError: () => {} }));
    app.get("/", (c) => c.text("ok"));
    const res = await app.request("/", { headers: { authorization: `Bearer ${t}` } });
    expect(res.status).toBe(503);
    expect((await res.json()).code).toBe("JWKS_FETCH_ERROR");
  });
});

describe("stop: nothing left running", () => {
  it("stopping during a retry pause ends the pause, cancels the loop and clears its timer", async () => {
    // A long in-request pause, so the stop lands inside it.
    const s = await jwksServer([], "503");
    const reported: unknown[] = [];
    const cache = new JwksCache({
      jwksUrl: s.url,
      ...FAST,
      fetchAttempts: 2,
      retryBaseMs: 10_000,
      retryMaxMs: 10_000,
      onFetchFailure: (f) => reported.push(f),
    });
    cache.start();
    await until(() => reported.length === 1); // the real 503, reported; now pausing
    cache.stop();
    await new Promise((r) => setTimeout(r, 100));
    expect(cache.status().maintaining).toBe(false);
    expect((cache as unknown as { timer?: unknown }).timer).toBeUndefined();
    expect(s.hits()).toBe(1); // the second attempt never ran
    expect(reported).toHaveLength(1); // the cancellation was not reported
  });

  it("a shutdown is not a dependency failure: not reported, not counted, lastError untouched", async () => {
    const s = await jwksServer(["ok"], "stall-body");
    const reported: unknown[] = [];
    const cache = new JwksCache({ jwksUrl: s.url, ...FAST, fetchTimeoutMs: 10_000, onFetchFailure: (f) => reported.push(f) });
    await cache.warm(); // keys held
    cache.start(); // its first refresh stalls mid-body
    await until(() => s.hits() === 2);
    cache.stop(); // a rolling deployment terminating
    await new Promise((r) => setTimeout(r, 100));
    expect(reported).toHaveLength(0);
    expect(cache.status()).toMatchObject({ ready: true, consecutiveFailures: 0, lastError: undefined, maintaining: false });
  });

  it("an aborted signal stops it the same way", async () => {
    const s = await jwksServer([], "503");
    const cache = new JwksCache({ jwksUrl: s.url, ...FAST, fetchAttempts: 1, backoffMaxMs: 50 });
    const shutdown = new AbortController();
    cache.start({ signal: shutdown.signal });
    await until(() => s.hits() >= 2);
    shutdown.abort();
    expect(cache.status().maintaining).toBe(false);
    const hits = s.hits();
    await new Promise((r) => setTimeout(r, 200));
    expect(s.hits()).toBeLessThanOrEqual(hits + 1); // at most the fetch already in flight
    expect((cache as unknown as { timer?: unknown }).timer).toBeUndefined();
  });

  it("start() after stop() runs one loop, not two", async () => {
    const s = await jwksServer([], "ok");
    const cache = new JwksCache({ jwksUrl: s.url, ...FAST, keyTtlMs: 1_000 });
    const delays = backgroundDelays();
    cache.start();
    cache.stop(); // before the first fetch settles
    cache.start();
    await until(() => cache.status().ready);
    await new Promise((r) => setTimeout(r, 50));
    // The stopped run cancelled the download both runs shared, so the live run
    // may back off once (a short delay) before succeeding. What must hold is one
    // loop: exactly one periodic refresh is scheduled.
    expect(delays.filter((d) => d >= 900)).toHaveLength(1);
    cache.stop();
  });
});

describe("retry schedule", () => {
  it("the background loop waits out a long Retry-After, without contacting the issuer meanwhile", async () => {
    const s = await jwksServer(["429:120"]);
    const cache = new JwksCache({ jwksUrl: s.url, ...FAST });
    const delays = backgroundDelays();
    cache.start();
    await until(() => delays.length >= 1);
    expect(delays[0]).toBeGreaterThan(119_000);
    expect(delays[0]).toBeLessThanOrEqual(120_000);
    await expect(cache.warm()).rejects.toThrow(/not contacted/); // a request does not reach it either
    expect(s.hits()).toBe(1);
    cache.stop();
  });

  it("a fleet failing together does not retry together (full jitter)", async () => {
    const s = await jwksServer([], "503");
    // backoffMaxMs caps the background retry: at base, so every pause this
    // records -- first attempt or a later one racing in before a slow first
    // fetch settles -- is within [0, base].
    const fleet = Array.from(
      { length: 20 },
      () =>
        new JwksCache({
          jwksUrl: s.url,
          ...FAST,
          fetchAttempts: 1,
          retryBaseMs: 1_000,
          retryMaxMs: 1_000,
          backoffMaxMs: 1_000,
        }),
    );
    const delays = backgroundDelays();
    fleet.forEach((c) => c.start());
    await until(() => delays.length >= 20);
    fleet.forEach((c) => c.stop());
    const first = delays.slice(0, 20);
    for (const d of first) {
      expect(d).toBeGreaterThanOrEqual(0);
      expect(d).toBeLessThanOrEqual(1_000); // capped at base: [0, base]
    }
    expect(new Set(first.map((d) => Math.round(d))).size).toBeGreaterThan(15);
    expect(Math.max(...first) - Math.min(...first)).toBeGreaterThan(300);
  });
});
