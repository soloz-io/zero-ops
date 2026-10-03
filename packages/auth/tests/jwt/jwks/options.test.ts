import { describe, it, expect } from "vitest";
import { resolveJwksOptions } from "../../../src/jwt/jwks/options.js";

// Validated, not normalised: a wrong value is a configuration error.

const url = "https://id.example.test/keys";

describe("resolveJwksOptions", () => {
  it("fills every default", () => {
    expect(resolveJwksOptions({ jwksUrl: url })).toMatchObject({
      keyTtlMs: 300_000,
      maxStaleMs: 900_000,
      refreshIntervalMs: 10_000,
      fetchTimeoutMs: 10_000,
      fetchAttempts: 2,
      retryBaseMs: 250,
      retryMaxMs: 2_000,
      backoffMaxMs: 300_000,
      algorithms: ["RS256", "ES256"],
    });
  });

  it.each([
    [{ fetchAttempts: 1.5 }, /fetchAttempts must be an integer in \[1, 5\], got 1.5/],
    [{ fetchAttempts: 0 }, /fetchAttempts/],
    [{ retryBaseMs: -100 }, /retryBaseMs/],
    [{ fetchTimeoutMs: -1 }, /fetchTimeoutMs/],
    [{ backoffMaxMs: 300_001 }, /backoffMaxMs must be an integer in \[250, 300000\]/],
    [{ keyTtlMs: 600_000, maxStaleMs: 60_000 }, /maxStaleMs/],
    [{ algorithms: [] }, /algorithms/],
  ])("refuses %o", (opts, message) => {
    expect(() => resolveJwksOptions({ jwksUrl: url, ...opts })).toThrow(message);
  });

  it.each([["https://id.example.test/keys"], ["http://127.0.0.1:8080/keys"], ["http://localhost/keys"], ["http://[::1]/keys"]])(
    "accepts %s",
    (jwksUrl) => expect(() => resolveJwksOptions({ jwksUrl })).not.toThrow(),
  );

  it("refuses http: to a non-loopback host", () => {
    expect(() => resolveJwksOptions({ jwksUrl: "http://id.dev.nutgraf.in/oauth/v2/keys" })).toThrow(
      /must be https: \(http: is permitted only for a loopback host, or with allowInsecureHttp\)/,
    );
  });

  it("accepts http: to a non-loopback host only with allowInsecureHttp", () => {
    expect(() => resolveJwksOptions({ jwksUrl: "http://zitadel.local/keys", allowInsecureHttp: true })).not.toThrow();
  });

  it.each([["file:///etc/keys.json"], ["ftp://id.example.test/keys"]])("refuses %s", (jwksUrl) => {
    expect(() => resolveJwksOptions({ jwksUrl })).toThrow(/must be https:/);
  });

  it("refuses an unparseable URL", () => {
    expect(() => resolveJwksOptions({ jwksUrl: "not a url" })).toThrow(TypeError);
  });
});
