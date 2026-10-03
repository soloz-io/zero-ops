import { describe, it, expect, vi } from "vitest";
import { fullJitterDelay, isRetryableStatus, parseRetryAfter } from "../../../src/jwt/jwks/retry-policy.js";

describe("isRetryableStatus", () => {
  it.each([408, 429, 500, 502, 503, 504])("retries %i", (s) => expect(isRetryableStatus(s)).toBe(true));
  it.each([200, 302, 400, 401, 403, 404])("does not retry %i", (s) => expect(isRetryableStatus(s)).toBe(false));
});

describe("fullJitterDelay (ADR-022)", () => {
  it("is uniform in [0, min(cap, base * 2^(n-1))]", () => {
    const spy = vi.spyOn(Math, "random");
    spy.mockReturnValue(0.999999);
    expect(fullJitterDelay(1, 100, 1000)).toBeCloseTo(100, 0);
    expect(fullJitterDelay(3, 100, 1000)).toBeCloseTo(400, 0);
    expect(fullJitterDelay(10, 100, 1000)).toBeCloseTo(1000, 0);
    spy.mockReturnValue(0);
    expect(fullJitterDelay(10, 100, 1000)).toBe(0);
    spy.mockRestore();
  });
});

describe("parseRetryAfter", () => {
  it("reads seconds", () => expect(parseRetryAfter("120")).toBe(120_000));
  it("reads an HTTP date", () => {
    const ms = parseRetryAfter(new Date(Date.now() + 30_000).toUTCString())!;
    expect(ms).toBeGreaterThan(28_000);
    expect(ms).toBeLessThanOrEqual(30_000);
  });
  it("ignores absent or garbage", () => {
    expect(parseRetryAfter(null)).toBeUndefined();
    expect(parseRetryAfter("soon")).toBeUndefined();
  });
});
