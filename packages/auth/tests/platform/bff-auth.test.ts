import { describe, it, expect, afterEach, vi } from "vitest";
import { Hono } from "hono";
import { bffAuth, type BffAuth } from "../../src/platform/bff-auth.js";
import { getPrincipal } from "../../src/middleware/hono.js";
import { closeServers, jwksServer, token, validator as fastValidator } from "../jwt/jwks/harness.js";

// One BFF's authentication: validator lifecycle, readiness, middleware, and the
// local stand-in -- each rule that every app's hand-written copy had to restate.

const started: BffAuth[] = [];
afterEach(async () => {
  started.splice(0).forEach((a) => a.stop());
  vi.restoreAllMocks();
  await closeServers();
});

function appWith(auth: BffAuth) {
  started.push(auth);
  const app = new Hono();
  app.get("/health/ready", auth.readiness);
  app.use("/api/*", auth.middleware);
  app.get("/api/*", (c) => c.json({ principal: getPrincipal(c) ?? null }));
  return app;
}

const until = (cond: () => boolean) => vi.waitFor(() => expect(cond()).toBe(true), { timeout: 3000, interval: 10 });

describe("local mode: <APPID>_ENV=local", () => {
  it("gives every request the default stand-in and checks no token", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    const auth = bffAuth({ appId: "shop", env: { SHOP_ENV: "local", LOCAL_TENANT_ID: "t-1" } });
    expect(auth.local).toBe(true);
    expect(auth.validator).toBeUndefined();
    const res = await appWith(auth).request("/api/v1/things");
    expect(res.status).toBe(200);
    expect((await res.json()).principal).toEqual({
      subject: "local-user",
      email: "local-user@shop.local",
      tenantId: "t-1",
      roles: [],
      issuer: "local",
    });
  });

  it("accepts a stand-in computed from the request", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    const auth = bffAuth({
      appId: "shop",
      env: { SHOP_ENV: "local" },
      localPrincipal: (c) => ({ subject: c.req.header("x-dev-user") ?? "anon", email: "", tenantId: "t", roles: [] }),
    });
    const res = await appWith(auth).request("/api/x", { headers: { "x-dev-user": "alice" } });
    expect((await res.json()).principal.subject).toBe("alice");
  });

  it("is ready without keys: there is no issuer locally", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    const auth = bffAuth({ appId: "shop", env: { SHOP_ENV: "local" } });
    expect(auth.ready()).toBe(true);
    expect((await appWith(auth).request("/health/ready")).status).toBe(200);
  });

  it("is refused in production, at construction", () => {
    expect(() => bffAuth({ appId: "shop", env: { SHOP_ENV: "local", NODE_ENV: "production" } })).toThrow(
      /SHOP_ENV=local with NODE_ENV=production/,
    );
  });

  it("derives the switch from the app id: my-shop -> MY_SHOP_ENV", () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    expect(bffAuth({ appId: "my-shop", env: { MY_SHOP_ENV: "local" } }).local).toBe(true);
    expect(() => bffAuth({ appId: "My Shop", env: {} })).toThrow(/not an application id/);
  });
});

describe("deployed: validator, readiness, middleware", () => {
  it("starts the validator; readiness is 503 until the keys are held, then 200", async () => {
    const s = await jwksServer(["503", "503"]);
    const auth = bffAuth({ appId: "shop", env: {}, validator: fastValidator(s.url, { fetchAttempts: 1, backoffMaxMs: 50 }) });
    const app = appWith(auth);
    expect(auth.local).toBe(false);
    expect(auth.validator?.status().maintaining).toBe(true); // started at construction
    expect((await app.request("/health/ready")).status).toBe(503);
    await until(() => auth.ready());
    const res = await app.request("/health/ready");
    expect(res.status).toBe(200);
  });

  it("guards the mount: no token is 401, a valid token sets the principal", async () => {
    const s = await jwksServer([]);
    const auth = bffAuth({ appId: "shop", env: {}, validator: fastValidator(s.url) });
    const app = appWith(auth);
    expect((await app.request("/api/v1/things")).status).toBe(401);
    const ok = await app.request("/api/v1/things", { headers: { authorization: `Bearer ${await token()}` } });
    expect(ok.status).toBe(200);
    expect((await ok.json()).principal.subject).toBe("svc");
  });

  it("publicPaths skip the guard on segment boundaries only", async () => {
    const s = await jwksServer([]);
    const auth = bffAuth({ appId: "shop", env: {}, validator: fastValidator(s.url), publicPaths: ["/api/v1/auth/"] });
    const app = appWith(auth);
    expect((await app.request("/api/v1/auth")).status).toBe(200);
    expect((await app.request("/api/v1/auth/callback")).status).toBe(200);
    expect((await app.request("/api/v1/authors")).status).toBe(401);
    expect((await app.request("/api/v1/things")).status).toBe(401);
  });

  it("a shutdown signal stops the key refresh", async () => {
    const s = await jwksServer([]);
    const shutdown = new AbortController();
    const auth = bffAuth({ appId: "shop", env: {}, validator: fastValidator(s.url), signal: shutdown.signal });
    started.push(auth);
    shutdown.abort();
    expect(auth.validator?.status().maintaining).toBe(false);
  });

  it("installs no process signal handler (Node must still exit on SIGTERM)", async () => {
    const s = await jwksServer([]);
    const before = process.listenerCount("SIGTERM") + process.listenerCount("SIGINT");
    started.push(bffAuth({ appId: "shop", env: {}, validator: fastValidator(s.url) }));
    expect(process.listenerCount("SIGTERM") + process.listenerCount("SIGINT")).toBe(before);
  });

  it("defaults to the platform's browser-session validator, from the environment", () => {
    const env = {
      OIDC_ISSUER_URL: "https://id.example.test",
      OIDC_JWKS_URL: "https://id.example.test/oauth/v2/keys",
      OIDC_CLIENT_ID: "shop-public",
      OIDC_PROJECT_ID: "1",
      OIDC_EXCHANGE_CLIENT_ID: "shop-exchange",
      OIDC_ORG_ID: "2",
    };
    const auth = bffAuth({ appId: "shop", env });
    started.push(auth);
    expect(auth.validator).toBeDefined();
    expect(auth.ready()).toBe(false); // keys are fetched in the background, not at construction
  });
});
