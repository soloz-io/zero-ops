import { describe, expect, it } from "vitest";
import { Hono } from "hono";
import { authMiddleware } from "../../src/middleware/hono.js";
import type { JwtValidator } from "../../src/jwt/validator.js";

/** Stand-in validator returning fixed claims, so we exercise the scope gate alone. */
const validatorReturning = (claims: Record<string, unknown>) =>
  ({ validate: async () => claims }) as unknown as JwtValidator;

const call = async (app: Hono) =>
  app.request("/x", { headers: { Authorization: "Bearer t" } });

describe("authMiddleware — scope enforcement fails closed", () => {
  const claims = { sub: "u1", email: "u@t.io", tenant_id: "t1", roles: [] };

  it("REJECTS a token with no scope claim when scopes are required", async () => {
    const app = new Hono();
    app.use(
      "*",
      authMiddleware({
        validator: validatorReturning(claims),
        requiredScopes: ["tenant:read"],
      }),
    );
    app.get("/x", (c) => c.text("ok"));
    // Previously admitted: the check was skipped entirely when `scope` was absent.
    expect((await call(app)).status).toBe(403);
  });

  it("rejects a token whose scopes omit a required one", async () => {
    const app = new Hono();
    app.use(
      "*",
      authMiddleware({
        validator: validatorReturning({ ...claims, scope: "openid" }),
        requiredScopes: ["tenant:read"],
      }),
    );
    app.get("/x", (c) => c.text("ok"));
    expect((await call(app)).status).toBe(403);
  });

  it("admits a token carrying every required scope", async () => {
    const app = new Hono();
    app.use(
      "*",
      authMiddleware({
        validator: validatorReturning({ ...claims, scope: "openid tenant:read" }),
        requiredScopes: ["tenant:read"],
      }),
    );
    app.get("/x", (c) => c.text("ok"));
    expect((await call(app)).status).toBe(200);
  });

  it("admits when no scopes are required", async () => {
    const app = new Hono();
    app.use("*", authMiddleware({ validator: validatorReturning(claims) }));
    app.get("/x", (c) => c.text("ok"));
    expect((await call(app)).status).toBe(200);
  });
});
