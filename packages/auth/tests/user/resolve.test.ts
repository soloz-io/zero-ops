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
  it("finds an existing user by subject, not by email", async () => {
    const db = fakeDb([[{ user_id: "local-uuid", email: "person@example.com" }]]);
    const user = await resolveUser(db, claims());

    expect(user).toEqual({
      userId: "local-uuid",
      email: "person@example.com",
      isNew: false,
    });
    // Keyed on the subject. Joining on email silently merges two people the
    // moment an address is reassigned.
    expect(db.params[0]).toEqual(["ory", "kratos-subject-1"]);
    expect(db.sql[0]).toContain("i.provider_user_id = $2");
    expect(db.sql[0]).not.toContain("u.email = ");
  });

  it("provisions a user and links the identity on first sight", async () => {
    const db = fakeDb([[], [{ id: "new-uuid" }], [{ user_id: "new-uuid" }]]);
    const user = await resolveUser(db, claims());

    expect(user).toEqual({
      userId: "new-uuid",
      email: "person@example.com",
      isNew: true,
    });
    expect(db.sql[1]).toContain("INSERT INTO public.users");
    expect(db.sql[2]).toContain(
      "ON CONFLICT (provider, provider_user_id) DO NOTHING",
    );
  });

  it("yields to the winner when two requests race the same subject", async () => {
    const db = fakeDb([[], [{ id: "loser-uuid" }], [], [{ user_id: "winner-uuid" }]]);
    const user = await resolveUser(db, claims());

    // Returning our own id would hand out a user no identity points at.
    expect(user.userId).toBe("winner-uuid");
    expect(user.isNew).toBe(false);
  });

  it("fails rather than inventing an address when none is present", async () => {
    const db = fakeDb([[]]);
    await expect(resolveUser(db, claims({ email: "" }))).rejects.toThrow(
      /no email claim/,
    );
  });

  it("refreshes a changed email but leaves an unchanged one alone", async () => {
    const changed = fakeDb([[{ user_id: "u", email: "old@example.com" }]]);
    await resolveUser(changed, claims({ email: "new@example.com" }));
    expect(
      changed.sql.some((s) => s.startsWith("UPDATE public.users SET email")),
    ).toBe(true);

    const same = fakeDb([[{ user_id: "u", email: "person@example.com" }]]);
    await resolveUser(same, claims());
    expect(same.sql.some((s) => s.startsWith("UPDATE"))).toBe(false);
  });

  it("rejects a schema name that is not a bare identifier", async () => {
    const db = fakeDb([[]]);
    await expect(
      resolveUser(db, claims(), { schema: 'public"; DROP TABLE users; --' }),
    ).rejects.toThrow(/invalid schema/);
  });

  it("keeps providers distinct so one subject cannot span two of them", async () => {
    const db = fakeDb([[{ user_id: "u", email: "person@example.com" }]]);
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
