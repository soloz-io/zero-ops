import { describe, expect, it } from "vitest";
import { Hono } from "hono";
import { authMiddleware } from "../../src/middleware/hono.js";
import {
  actingUserMiddleware,
  identityHandler,
  requireActingUser,
} from "../../src/middleware/acting-user.js";
import type { JwtValidator } from "../../src/jwt/validator.js";
import type { SqlExecutor, SqlTransactor } from "../../src/user/types.js";

const validatorReturning = (claims: Record<string, unknown>) =>
  ({ validate: async () => claims }) as unknown as JwtValidator;

const rejectingValidator = {
  validate: async () => {
    throw new Error("bad token");
  },
} as unknown as JwtValidator;

/** A transactor that answers resolve_user with a fixed row and records the call. */
function fakeDb(row: { user_id: string; email: string | null; is_new: boolean } | Error) {
  const calls: Array<{ sql: string; params: readonly unknown[] }> = [];
  const tx: SqlExecutor = {
    async query<T>(sql: string, params: readonly unknown[] = []) {
      calls.push({ sql, params });
      if (row instanceof Error) throw row;
      return [row] as unknown as T[];
    },
  };
  const db: SqlTransactor = { transaction: (fn) => fn(tx) };
  return { db, calls };
}

const claims = {
  sub: "subject-1",
  email: "a@example.com",
  tenant_id: "org-1",
  roles: ["member"],
};

function app(validator: JwtValidator, db: SqlTransactor, onError?: (e: unknown) => void) {
  const a = new Hono();
  a.use("/api/*", authMiddleware({ validator }));
  a.use("/api/*", actingUserMiddleware({ db, onError }));
  a.get("/api/v1/auth/me", identityHandler());
  a.post("/api/things", (c) => c.json({ owner: requireActingUser(c).userId }));
  return a;
}

const me = (a: Hono) =>
  a.request("/api/v1/auth/me", { headers: { Authorization: "Bearer t" } });

describe("identityHandler", () => {
  it("answers with the tenant-local user id, not the subject", async () => {
    const { db } = fakeDb({ user_id: "local-uuid", email: "a@example.com", is_new: false });
    const res = await me(app(validatorReturning(claims), db));
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({
      user: {
        userId: "local-uuid",
        subject: "subject-1",
        email: "a@example.com",
        role: "member",
        tenantId: "org-1",
      },
    });
  });

  it("resolves by subject through resolve_user, passing the address as data only", async () => {
    const { db, calls } = fakeDb({ user_id: "u", email: null, is_new: true });
    await me(app(validatorReturning(claims), db));
    expect(calls).toHaveLength(1);
    expect(calls[0].sql).toContain("resolve_user(");
    expect(calls[0].params).toEqual(["ory", "subject-1", "a@example.com"]);
  });

  it("is signed in with ownership unknown when resolution fails -- not an error", async () => {
    const { db } = fakeDb(new Error("database down"));
    const errors: unknown[] = [];
    const res = await me(app(validatorReturning(claims), db, (e) => errors.push(e)));
    expect(res.status).toBe(200);
    const body = (await res.json()) as { user: { userId: unknown; subject: string } };
    expect(body.user.userId).toBeNull();
    expect(body.user.subject).toBe("subject-1");
    expect(errors).toHaveLength(1);
  });

  it("is 401 for an invalid token, and never touches the database", async () => {
    const { db, calls } = fakeDb({ user_id: "u", email: null, is_new: false });
    const res = await me(app(rejectingValidator, db));
    expect(res.status).toBe(401);
    expect(calls).toHaveLength(0);
  });

  it("is 401 with no token at all", async () => {
    const { db } = fakeDb({ user_id: "u", email: null, is_new: false });
    const res = await app(validatorReturning(claims), db).request("/api/v1/auth/me");
    expect(res.status).toBe(401);
  });
});

describe("requireActingUser", () => {
  it("fails the operation that needs an owner, where the requirement is", async () => {
    const { db } = fakeDb(new Error("database down"));
    const res = await app(validatorReturning(claims), db, () => {}).request("/api/things", {
      method: "POST",
      headers: { Authorization: "Bearer t" },
    });
    expect(res.status).toBe(500);
  });

  it("supplies the owner when one was resolved", async () => {
    const { db } = fakeDb({ user_id: "local-uuid", email: null, is_new: false });
    const res = await app(validatorReturning(claims), db).request("/api/things", {
      method: "POST",
      headers: { Authorization: "Bearer t" },
    });
    expect(await res.json()).toEqual({ owner: "local-uuid" });
  });
});
