import { describe, it, expect, afterEach } from "vitest";
import { Hono } from "hono";
import type { JwksFetchFailure } from "../../src/jwt/jwks-cache.js";
import { authMiddleware } from "../../src/middleware/hono.js";
import { JwksFetchError } from "../../src/types.js";
import { closeServers, jwksServer, token, validator } from "./jwks/harness.js";

// JwksCache on the request path, against a real HTTP server (./jwks/harness.ts):
// fetch.ts and the middleware together. The pure policy is tested beside its
// module under ./jwks/, and the background lifecycle in ./jwks/lifecycle.test.ts.

afterEach(closeServers);

describe("JWKS fetch: one store, retried", () => {
  it("retries a body that stalls, and the token validates", async () => {
    const s = await jwksServer(["stall-body"]);
    const claims = await validator(s.url).validate(await token());
    expect(claims.sub).toBe("svc");
    expect(s.hits()).toBe(2);
  });

  it("concurrent requests share one fetch and all succeed after a retry", async () => {
    // The incident's shape: three requests of one page load on a cold pod.
    const s = await jwksServer(["stall-body"]);
    const v = validator(s.url);
    const t = await token();
    const results = await Promise.all([v.validate(t), v.validate(t), v.validate(t)]);
    expect(results.map((r) => r.sub)).toEqual(["svc", "svc", "svc"]);
    expect(s.hits()).toBe(2);
  });

  it("says it timed out when every attempt stalls, not that the JSON was bad", async () => {
    const s = await jwksServer(["stall-body", "stall-body"]);
    const err = await validator(s.url).validate(await token()).catch((e) => e);
    expect(err).toBeInstanceOf(JwksFetchError);
    expect(err.code).toBe("JWKS_FETCH_ERROR");
    expect(err.message).toMatch(/timed out .*headers and body.*2 of 2 attempt/);
  });

  it.each([["not-json"], ["500"], ["503"], ["408"], ["429"]])("retries %s", async (step) => {
    const s = await jwksServer([step]);
    await expect(validator(s.url).validate(await token())).resolves.toMatchObject({ sub: "svc" });
    expect(s.hits()).toBe(2);
  });

  it.each([["400"], ["401"], ["403"], ["404"], ["302"]])("does not retry %s: repeating it only delays the error", async (step) => {
    const s = await jwksServer([step]);
    const err = await validator(s.url).validate(await token()).catch((e) => e);
    expect(err).toBeInstanceOf(JwksFetchError);
    expect(err.status).toBe(Number(step));
    expect(s.hits()).toBe(1);
  });

  it("waits a short Retry-After, then retries", async () => {
    const s = await jwksServer(["429:0"]);
    await expect(validator(s.url).validate(await token())).resolves.toMatchObject({ sub: "svc" });
    expect(s.hits()).toBe(2);
  });

  it("a long Retry-After stops retries, and the issuer is not contacted until it passes", async () => {
    const s = await jwksServer(["429:120"]);
    const v = validator(s.url);
    const first = await v.validate(await token()).catch((e) => e);
    expect(first.message).toMatch(/HTTP 429, Retry-After 120s/);
    const second = await v.validate(await token()).catch((e) => e);
    expect(second).toBeInstanceOf(JwksFetchError);
    expect(second.message).toMatch(/not contacted/);
    expect(s.hits()).toBe(1);
  });

  it("a key set with no usable signing key is a failure, never held and never ready", async () => {
    const s = await jwksServer(["no-usable-key"]);
    const v = validator(s.url);
    const err = await v.warm().catch((e) => e);
    expect(err).toBeInstanceOf(JwksFetchError);
    expect(err.message).toMatch(/no usable signing key for RS256, ES256/);
    expect(v.status()).toMatchObject({ ready: false, keys: 0, consecutiveFailures: 1 });
    expect(s.hits()).toBe(1);
  });

  it("reports every failed attempt, with detail, to onFetchFailure", async () => {
    const s = await jwksServer(["503", "503"]);
    const seen: JwksFetchFailure[] = [];
    await validator(s.url, { onFetchFailure: (f: JwksFetchFailure) => seen.push(f) }).warm().catch(() => {});
    expect(seen.map((f) => [f.attempt, f.status, f.retrying])).toEqual([
      [1, 503, true],
      [2, 503, false],
    ]);
  });
});

describe("middleware: 503, and no infrastructure detail in the body", () => {
  it("answers an unobtainable JWKS 503 with only the code, and reports the detail", async () => {
    const s = await jwksServer(["404"]);
    const reported: unknown[] = [];
    const app = new Hono();
    app.use("*", authMiddleware({ validator: validator(s.url), onDependencyError: (e) => reported.push(e) }));
    app.get("/", (c) => c.text("ok"));
    const res = await app.request("/", { headers: { authorization: `Bearer ${await token()}` } });
    expect(res.status).toBe(503);
    const body = await res.json();
    expect(body.code).toBe("JWKS_FETCH_ERROR");
    expect(JSON.stringify(body)).not.toContain("127.0.0.1");
    expect(JSON.stringify(body)).not.toContain("404");
    expect((reported[0] as Error).message).toMatch(/127\.0\.0\.1.*HTTP 404/);
  });
});
