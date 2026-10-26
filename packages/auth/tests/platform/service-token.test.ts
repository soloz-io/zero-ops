import { describe, it, expect, vi } from "vitest";
import {
  serviceTokenSource,
  backendProjectIdKey,
  projectAudienceScope,
  RESOURCE_OWNER_SCOPE,
} from "../../src/index.js";

// Client credentials is the flow that WORKS on the deployed issuer (ADR-097), so
// these cover the two scopes without which a structurally valid token is refused,
// and the caching that keeps the issuer out of the path of every call.

const ENV = {
  OIDC_ISSUER_URL: "https://id.dev.nutgraf.in",
  OIDC_SERVICE_CLIENT_ID: "oranger-service@nutgraf",
  OIDC_SERVICE_CLIENT_SECRET: "svc-secret",
  OIDC_BACKEND_PROJECT_ID_WAYPOINT: "392885920103137720",
};

function fakeFetch(body: unknown, ok = true, status = 200) {
  return vi.fn(async () => ({ ok, status, json: async () => body })) as unknown as typeof fetch;
}

describe("serviceTokenSource", () => {
  it("requests BOTH the target audience and the tenant, which are what make it acceptable", async () => {
    // The audience scope is what the target matches; the resource-owner scope is
    // what puts the tenant on the token. Missing either produces a refusal that
    // names neither.
    const f = fakeFetch({ access_token: "tok", expires_in: 3600 });
    const src = serviceTokenSource({ target: "waypoint", env: ENV, fetchImpl: f });
    expect(await src.token()).toBe("tok");

    const [, init] = (f as unknown as { mock: { calls: [string, RequestInit][] } }).mock.calls[0];
    const scope = new URLSearchParams(init.body as URLSearchParams).get("scope");
    expect(scope).toContain(projectAudienceScope(ENV.OIDC_BACKEND_PROJECT_ID_WAYPOINT));
    expect(scope).toContain(RESOURCE_OWNER_SCOPE);
    expect(new URLSearchParams(init.body as URLSearchParams).get("grant_type")).toBe(
      "client_credentials",
    );
  });

  it("authenticates with the secret in the header, never in the body", async () => {
    // A body is what a proxy or an access log keeps.
    const f = fakeFetch({ access_token: "tok", expires_in: 3600 });
    await serviceTokenSource({ target: "waypoint", env: ENV, fetchImpl: f }).token();

    const [, init] = (f as unknown as { mock: { calls: [string, RequestInit][] } }).mock.calls[0];
    const headers = init.headers as Record<string, string>;
    expect(headers.Authorization).toMatch(/^Basic /);
    expect(String(init.body)).not.toContain("svc-secret");
  });

  it("reuses a live token, so the issuer is not in the path of every call", async () => {
    const f = fakeFetch({ access_token: "tok", expires_in: 3600 });
    const src = serviceTokenSource({ target: "waypoint", env: ENV, fetchImpl: f });
    await src.token();
    await src.token();
    expect((f as unknown as { mock: { calls: unknown[] } }).mock.calls).toHaveLength(1);
  });

  it("treats a response with no expiry as expiring now, not as never expiring", async () => {
    // Reusing a token of unknown lifetime is how a caller starts sending expired
    // credentials and blames the receiver.
    const f = fakeFetch({ access_token: "tok" });
    const src = serviceTokenSource({ target: "waypoint", env: ENV, fetchImpl: f });
    await src.token();
    await src.token();
    expect((f as unknown as { mock: { calls: unknown[] } }).mock.calls).toHaveLength(2);
  });

  it("mints once under concurrency rather than once per caller", async () => {
    let resolve!: (v: unknown) => void;
    const gate = new Promise((r) => (resolve = r));
    const f = vi.fn(async () => {
      await gate;
      return { ok: true, status: 200, json: async () => ({ access_token: "tok", expires_in: 3600 }) };
    }) as unknown as typeof fetch;

    const src = serviceTokenSource({ target: "waypoint", env: ENV, fetchImpl: f });
    const all = Promise.all([src.token(), src.token(), src.token()]);
    resolve(null);
    expect(await all).toEqual(["tok", "tok", "tok"]);
    expect((f as unknown as { mock: { calls: unknown[] } }).mock.calls).toHaveLength(1);
  });

  it("keeps the issuer's own words, which name the scope it refused", async () => {
    const f = fakeFetch(
      { error: "invalid_scope", error_description: "scope not allowed" },
      false,
      400,
    );
    const src = serviceTokenSource({ target: "waypoint", env: ENV, fetchImpl: f });
    await expect(src.token()).rejects.toThrow(/invalid_scope: scope not allowed/);
  });

  it("refuses to build with no service identity, rather than calling without one", async () => {
    // A call made with no credential reaches the target, is refused, and reads as
    // an authorization problem at the far end.
    const { OIDC_SERVICE_CLIENT_ID: _id, ...noIdentity } = ENV;
    expect(() => serviceTokenSource({ target: "waypoint", env: noIdentity })).toThrow(
      /OIDC_SERVICE_CLIENT_ID/,
    );
  });

  it("refuses a target this application has not declared a dependency on", async () => {
    // Without the project id the token carries no audience and the target refuses
    // it -- so the missing DECLARATION is named here instead.
    expect(() => serviceTokenSource({ target: "atlas", env: ENV })).toThrow(
      /backendDependencies: \[atlas\]/,
    );
  });

  it("derives the dependency key the platform publishes under", () => {
    // The operator writes OIDC_BACKEND_PROJECT_ID_<APP> and the chart mounts it;
    // this derivation is the third copy and must match both.
    expect(backendProjectIdKey("waypoint")).toBe("OIDC_BACKEND_PROJECT_ID_WAYPOINT");
    expect(backendProjectIdKey("my-app")).toBe("OIDC_BACKEND_PROJECT_ID_MY_APP");
  });
});
