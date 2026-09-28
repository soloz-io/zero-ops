import { describe, expect, it } from "vitest";
import { resolveUser, withUserContext } from "../../src/user/resolve.js";
import type { SqlExecutor, SqlTransactor } from "../../src/user/types.js";
import type { TenantClaims } from "../../src/jwt/claims.js";

const claims = (over: Partial<TenantClaims> = {}): TenantClaims =>
  ({
    sub: "kratos-subject-1",
    email: "person@example.com",
    email_verified: true,
    tenant_id: "acme",
    roles: [],
    ...over,
  }) as TenantClaims;

/** Records every statement so tests can assert on what reached the database. */
function fakeDb(
  responses: unknown[][],
): SqlTransactor & { sql: string[]; params: unknown[][] } {
  const sql: string[] = [];
  const params: unknown[][] = [];
  let call = 0;
  const exec: SqlExecutor = {
    async query(statement, p) {
      sql.push(statement.replace(/\s+/g, " ").trim());
      params.push([...(p ?? [])]);
      return (responses[call++] ?? []) as never;
    },
  };
  return {
    sql,
    params,
    async transaction(fn) {
      return fn(exec);
    },
  };
}

describe("resolveUser", () => {
  // The library no longer issues the SELECT and INSERTs itself (ADR-093). It
  // cannot: the application does not own its tables any more, so row-level
  // security applies to it, and resolution runs before any user context exists --
  // the identities policy would hide the row it is looking for. One call into a
  // SECURITY DEFINER function replaces all of it.
  //
  // So these assert the CONTRACT with that function: which arguments go in, what
  // comes back, and that nothing else is issued. The behaviours that moved into
  // the database -- the race, the email refresh, provisioning on first sight --
  // are asserted against a real PostgreSQL in the migration, because asserting
  // them against a fake here would only restate this file's own mock.

  it("resolves through the function, keyed on the subject and not the email", async () => {
    const db = fakeDb([
      [{ user_id: "local-uuid", email: "person@example.com", is_new: false }],
    ]);
    const user = await resolveUser(db, claims());

    expect(user).toEqual({
      userId: "local-uuid",
      email: "person@example.com",
      isNew: false,
    });
    // Subject, never address. Joining on email silently merges two people the
    // moment an address is reassigned.
    expect(db.params[0]).toEqual([
      "ory",
      "kratos-subject-1",
      "person@example.com",
    ]);
    expect(db.sql[0]).toContain("resolve_user($1, $2, $3)");
    expect(db.sql[0]).not.toContain("u.email = ");
  });

  it("issues exactly one statement", async () => {
    // The point of the function is that resolution is one round trip whose
    // privilege is the owner's. A second statement here would mean something was
    // left behind that RLS will block at runtime rather than in this test.
    const db = fakeDb([
      [{ user_id: "u", email: "person@example.com", is_new: false }],
    ]);
    await resolveUser(db, claims());
    expect(db.sql).toHaveLength(1);
  });

  it("reports a first sighting as new", async () => {
    const db = fakeDb([
      [{ user_id: "new-uuid", email: "person@example.com", is_new: true }],
    ]);
    const user = await resolveUser(db, claims());
    expect(user).toEqual({
      userId: "new-uuid",
      email: "person@example.com",
      isNew: true,
    });
  });

  it("takes the address the function returns, not the one in the claims", async () => {
    // The caller cannot read public.users to check: under RLS it has no user
    // context until this call returns. The stored address is therefore whatever
    // the function says it is.
    const db = fakeDb([
      [{ user_id: "u", email: "stored@example.com", is_new: false }],
    ]);
    const user = await resolveUser(db, claims({ email: "claimed@example.com" }));
    expect(user.email).toBe("stored@example.com");
  });

  it("fails loudly when the function returns no row", async () => {
    // A set-returning function yielding nothing means the call did not happen as
    // expected. Returning an undefined user id here would fail somewhere later,
    // with nothing pointing back at resolution.
    const db = fakeDb([[]]);
    await expect(resolveUser(db, claims())).rejects.toThrow(
      /returned no row/,
    );
  });

  it("rejects a schema name that is not a bare identifier", async () => {
    const db = fakeDb([[]]);
    await expect(
      resolveUser(db, claims(), { schema: 'public"; DROP TABLE users; --' }),
    ).rejects.toThrow(/invalid schema/);
  });

  it("keeps providers distinct so one subject cannot span two of them", async () => {
    const db = fakeDb([
      [{ user_id: "u", email: "person@example.com", is_new: false }],
    ]);
    await resolveUser(db, claims(), { provider: "github" });
    expect(db.params[0]?.[0]).toBe("github");
  });
});

describe("withUserContext", () => {
  it("sets the RLS claims transaction-locally and binds them as a parameter", async () => {
    const db = fakeDb([[]]);
    const seen = await withUserContext(
      db,
      { userId: "u-1", tenantId: "acme" },
      async () => "ran",
    );

    expect(seen).toBe("ran");
    // is_local=true. A session-scoped setting would survive the transaction and
    // be inherited by the next client to borrow the pooled connection.
    expect(db.sql[0]).toContain("set_config('request.jwt.claims', $1, true)");
    expect(JSON.parse(String(db.params[0]?.[0]))).toEqual({
      user_id: "u-1",
      tenant_id: "acme",
    });
  });

  it("omits the tenant when none is supplied rather than emitting a null", async () => {
    const db = fakeDb([[]]);
    await withUserContext(db, { userId: "u-1" }, async () => undefined);
    expect(JSON.parse(String(db.params[0]?.[0]))).toEqual({ user_id: "u-1" });
  });
});
