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
  // The library issues its own SQL again, against tables the APPLICATION owns.
  // The platform provisions a database and a role and nothing inside it, so there
  // is no function to call and no privilege the caller lacks.
  //
  // Every case below is one of the two failures this code exists to prevent: a
  // person resolved by the wrong key, or a person created twice.

  it("finds a person by SUBJECT, never by email address", async () => {
    // The whole reason this is a library rather than fifty lines in each app. An
    // address is mutable at the provider and reassignable between people, so a
    // match on it merges two accounts the moment one changes hands -- and it
    // fails silently, as one person reading another's records.
    const db = fakeDb([[{ user_id: "local-uuid", email: "person@example.com" }]]);
    const user = await resolveUser(db, claims());

    expect(user).toEqual({ userId: "local-uuid", email: "person@example.com", isNew: false });
    expect(db.params[0]).toEqual(["zitadel", "kratos-subject-1"]);
    expect(db.sql[0]).toContain("i.provider = $1 AND i.provider_user_id = $2");
    expect(db.sql[0]).not.toContain("u.email = ");
  });

  it("names the provider Zitadel, which is the issuer this platform runs", async () => {
    // Pinned by name, because this value is only ever compared against itself:
    // written, then looked up by, the same constant. A wrong one is
    // self-consistent and fails nothing -- "ory" survived here for months.
    const db = fakeDb([[{ user_id: "u", email: "person@example.com" }]]);
    await resolveUser(db, claims());
    expect(db.params[0][0]).toBe("zitadel");
  });

  it("still lets a genuine second provider be named", async () => {
    const db = fakeDb([[{ user_id: "u", email: "person@example.com" }]]);
    await resolveUser(db, claims(), { provider: "okta" });
    expect(db.params[0][0]).toBe("okta");
  });

  it("creates the user and the identity together on a first sighting", async () => {
    const db = fakeDb([
      [],                                  // no identity yet
      [],                                  // address not taken
      [{ id: "new-uuid" }],                // insert user
      [{ user_id: "new-uuid" }],           // insert identity, won
    ]);
    const user = await resolveUser(db, claims());
    expect(user).toEqual({ userId: "new-uuid", email: "person@example.com", isNew: true });
    expect(db.sql[3]).toContain("ON CONFLICT (provider, provider_user_id) DO NOTHING");
  });

  it("adopts the winner when two requests race, and leaves no orphan behind", async () => {
    // Both requests see no identity and both insert. The unique constraint
    // decides it; the loser must NOT return its own user id, because no identity
    // points at that row -- it would be invisible to every later lookup and
    // re-created on every request.
    const db = fakeDb([
      [],                                              // no identity yet
      [],                                              // address not taken
      [{ id: "loser-uuid" }],                          // this request's user row
      [],                                              // identity insert conflicted
      [],                                              // delete the orphan
      [{ user_id: "winner-uuid", email: "person@example.com" }],
    ]);
    const user = await resolveUser(db, claims());

    expect(user.userId).toBe("winner-uuid");
    expect(user.isNew).toBe(false);
    const deleted = db.sql.findIndex((q) => q.startsWith("DELETE FROM public.users"));
    expect(deleted).toBeGreaterThan(-1);
    expect(db.params[deleted]).toEqual(["loser-uuid"]);
  });

  it("says so when a conflict cannot be read back, rather than returning nothing", async () => {
    // The insert conflicted, so a row exists. Not finding it means the unique
    // constraint is not the one this assumes -- which is a schema problem the
    // application must hear about, not an undefined id that fails later.
    const db = fakeDb([[], [], [{ id: "u" }], [], [], []]);
    await expect(resolveUser(db, claims())).rejects.toThrow(/UNIQUE \(provider, provider_user_id\)/);
  });

  it("refreshes a changed address, and only when it changed", async () => {
    const changed = fakeDb([[{ user_id: "u", email: "old@example.com" }], []]);
    await resolveUser(changed, claims({ email: "new@example.com" }));
    expect(changed.sql[1]).toContain("UPDATE public.users SET email");

    const same = fakeDb([[{ user_id: "u", email: "person@example.com" }]]);
    await resolveUser(same, claims());
    // An unconditional UPDATE would write on every authenticated request.
    expect(same.sql).toHaveLength(1);
  });

  it("names the missing email claim rather than failing on a not-null column", async () => {
    // `users.email` is NOT NULL. Letting the insert fail teaches the caller about
    // a column it never mentioned, instead of about a login that requested no
    // email scope.
    const db = fakeDb([[], []]);
    await expect(resolveUser(db, claims({ email: "" }))).rejects.toThrow(/without an email claim/);
  });

  it("refuses to attach a new subject to an address someone else already holds", async () => {
    // Adopting it silently is how an address reassigned at the provider hands a
    // stranger someone else's records. Linking two subjects to one person is a
    // decision that needs proof the same human holds both.
    const db = fakeDb([[], [{ id: "someone-else" }]]);
    await expect(resolveUser(db, claims())).rejects.toThrow(/verified-linking flow/);
  });

  it("refuses claims with no subject", async () => {
    const db = fakeDb([]);
    await expect(resolveUser(db, claims({ sub: undefined }))).rejects.toThrow(/subject/);
    expect(db.sql).toHaveLength(0);
  });

  it("runs every statement in ONE transaction", async () => {
    // A failure between the two inserts would leave a user row no identity points
    // at. The transaction is what makes that impossible.
    const db = fakeDb([[], [], [{ id: "n" }], [{ user_id: "n" }]]);
    await resolveUser(db, claims());
    expect(db.sql.length).toBeGreaterThan(1);
  });

  it("rejects a schema name that is not a bare identifier", async () => {
    // The schema is interpolated, not bound -- it cannot be a parameter.
    const db = fakeDb([]);
    await expect(resolveUser(db, claims(), { schema: 'pub"lic' })).rejects.toThrow(/invalid schema/);
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
