import type { TenantClaims } from "../jwt/claims.js";
import type {
  ResolveUserOptions,
  ResolvedUser,
  SqlExecutor,
  SqlTransactor,
} from "./types.js";

// The issuer this platform runs.
//
// It was "ory" until 2026-09-30 -- a default carried over from an identity
// provider that preceded Zitadel. Nothing failed for as long as it was wrong,
// because this value is only ever compared against itself: the library writes it,
// looks a person up by it, and matches. A wrong name stays consistent with itself
// and is invisible to every test of resolution. It was found by reading a row.
//
// `identities` is UNIQUE(provider, provider_user_id), so this value and the
// stored one must move together. If you ever change it, rewrite the stored rows
// in the same change -- otherwise every existing identity becomes unreachable,
// resolution judges each person new, and a SECOND user row is created for them.
// That insert succeeds, so nothing reports it.
const DEFAULT_PROVIDER = "zitadel";
const DEFAULT_SCHEMA = "public";

/** Reject anything that could not be a bare SQL identifier. */
function ident(name: string, what: string): string {
  if (!/^[a-z_][a-z0-9_]*$/i.test(name)) {
    throw new Error(`invalid ${what}: ${name}`);
  }
  return name;
}

/**
 * Resolve the tenant-local user for a validated set of OIDC claims, creating one
 * on first sight.
 *
 * WHY THIS IS A PLATFORM CONCERN
 *
 * The platform's tenant baseline ships `users` and `identities` in every tenant
 * database, with row-level security keyed on a tenant-local user id. Nothing
 * populated them: the identity provider knows a subject, the tenant's tables
 * expect a local uuid, and the mapping between the two is exactly what
 * `identities` exists to hold. Left to each tenant, this is the same fifty lines
 * written repeatedly and wrongly — most often by joining on email, which is the
 * bug described below.
 *
 * THE JOIN KEY IS THE SUBJECT, NEVER THE EMAIL
 *
 * Email is mutable in the identity provider and can be reassigned between people.
 * Joining on it silently merges two accounts the moment an address is changed or
 * reused, and the failure is a data-disclosure one rather than an error. The
 * subject is stable for the life of the identity, which is what
 * `identities.provider_user_id` is for.
 *
 * Email is still recorded and refreshed, because a tenant needs to display it —
 * but it is a property of the user, not a way to find them.
 *
 * CONCURRENCY
 *
 * Two requests from one person arriving together will both find nothing and both
 * insert. The unique constraint on (provider, provider_user_id) is what makes
 * that safe: the loser takes the `ON CONFLICT DO NOTHING` path and re-reads the
 * winner's row. This runs in one transaction so a failure cannot leave a `users`
 * row with no linked identity — an orphan that would be found by nothing and
 * re-created on every subsequent request.
 */
export async function resolveUser(
  db: SqlTransactor,
  claims: TenantClaims,
  options: ResolveUserOptions = {},
): Promise<ResolvedUser> {
  const provider = options.provider ?? DEFAULT_PROVIDER;
  const schema = ident(options.schema ?? DEFAULT_SCHEMA, "schema");

  if (!claims.sub) {
    throw new Error("cannot resolve a user from claims without a subject");
  }
  // claimsFromPayload defaults a missing email to "", not undefined, so an
  // emptiness check is required rather than a null check.
  const email = claims.email || null;

  return db.transaction(async (tx) => {
    // THE APPLICATION'S OWN TABLES, READ AND WRITTEN DIRECTLY.
    //
    // This briefly called a SECURITY DEFINER function the platform created in the
    // tenant's database, because ADR-093 had taken these tables away from the
    // application and row-level security then blocked a lookup that runs BEFORE
    // any user context exists. Both are gone: the platform provisions a database
    // and a role, and everything inside it is the application's, so there is no
    // privilege here the caller lacks and no function for the platform to own.
    //
    // What survives is the part that was never about privilege -- the race, and
    // the rule about email below.
    const found = await tx.query<{ user_id: string; email: string | null }>(
      `SELECT i.user_id, u.email
         FROM ${schema}.identities i
         JOIN ${schema}.users u ON u.id = i.user_id
        WHERE i.provider = $1 AND i.provider_user_id = $2`,
      [provider, claims.sub],
    );

    if (found[0]) {
      // Refresh a changed address so the application does not display a stale
      // one. Guarded on inequality: an unconditional UPDATE would write on every
      // authenticated request.
      if (email && email !== found[0].email) {
        await tx.query(`UPDATE ${schema}.users SET email = $1 WHERE id = $2`, [
          email,
          found[0].user_id,
        ]);
      }
      return { userId: found[0].user_id, email: email ?? found[0].email ?? "", isNew: false };
    }

    // FIRST SIGHTING.
    //
    // An address is required to create, because `users.email` is NOT NULL in the
    // shape this expects. Said here, naming the subject, rather than letting the
    // insert fail: a not-null violation on a column the caller never mentioned is
    // a confusing way to learn that the login produced no email claim.
    if (!email) {
      throw new Error(
        `cannot provision a user for subject ${claims.sub} without an email claim; ` +
          `the login must request the "email" scope`,
      );
    }

    // An address already registered to a DIFFERENT subject is refused rather than
    // adopted. `users.email` is UNIQUE, so the insert would fail anyway -- but the
    // reason matters: linking a second subject to an existing person is an
    // account-linking decision that needs proof the same human holds both, and
    // silently attaching one is how an address reassigned at the provider hands a
    // stranger someone else's records.
    const taken = await tx.query<{ id: string }>(
      `SELECT id FROM ${schema}.users WHERE email = $1`,
      [email],
    );
    if (taken[0]) {
      throw new Error(
        `address is already registered to a different identity; linking subject ` +
          `${claims.sub} to it requires an explicit verified-linking flow`,
      );
    }

    // Two requests from the same person can arrive together, both see nothing
    // above, and both try to create. The unique constraint on
    // (provider, provider_user_id) decides it, and `ON CONFLICT DO NOTHING` makes
    // the loser take a path that returns no row rather than raising.
    const created = await tx.query<{ id: string }>(
      `INSERT INTO ${schema}.users (email) VALUES ($1) RETURNING id`,
      [email],
    );
    const candidate = created[0]?.id;
    if (!candidate) {
      throw new Error(`could not create a user for subject ${claims.sub}`);
    }

    const linked = await tx.query<{ user_id: string }>(
      `INSERT INTO ${schema}.identities (user_id, provider, provider_user_id)
       VALUES ($1, $2, $3)
       ON CONFLICT (provider, provider_user_id) DO NOTHING
       RETURNING user_id`,
      [candidate, provider, claims.sub],
    );

    if (linked[0]) {
      return { userId: linked[0].user_id, email: email ?? "", isNew: true };
    }

    // The loser. Its `users` row is an orphan nothing points at -- exactly the
    // record ADR-057 names as invisible to every later lookup and re-created on
    // every request -- so it is removed before adopting the winner. Deleting it
    // is safe because it was inserted in THIS transaction and nothing else can
    // have referenced it yet.
    await tx.query(`DELETE FROM ${schema}.users WHERE id = $1`, [candidate]);

    const winner = await tx.query<{ user_id: string; email: string | null }>(
      `SELECT i.user_id, u.email
         FROM ${schema}.identities i
         JOIN ${schema}.users u ON u.id = i.user_id
        WHERE i.provider = $1 AND i.provider_user_id = $2`,
      [provider, claims.sub],
    );
    if (!winner[0]) {
      // The insert conflicted, so a row exists; not finding it means the unique
      // constraint is not the one this assumes. Surfaced rather than returning an
      // id of undefined that fails somewhere later.
      throw new Error(
        `identity for subject ${claims.sub} conflicted but could not be read back; ` +
          `check that ${schema}.identities has UNIQUE (provider, provider_user_id)`,
      );
    }
    return { userId: winner[0].user_id, email: email ?? winner[0].email ?? "", isNew: false };
  });
}

/**
 * Run `fn` in a transaction whose row-level security context is set to this user.
 *
 * FOR AN APPLICATION THAT CHOOSES ROW-LEVEL SECURITY. The platform ships no
 * policies -- it provisions a database and a role, and the schema is the
 * application's -- so nothing here is engaged unless the application wrote a
 * policy that reads
 * `current_setting('request.jwt.claims')::json->>'user_id'`.
 *
 * Worth knowing before adopting it: such a policy binds the application only
 * while the application sets the claim honestly. It catches a handler that forgot
 * a WHERE clause; it does not constrain code that sets a different user_id,
 * because the application holds the connection. That makes it a backstop against
 * developer error, not a security boundary -- which is a fine thing to want, as
 * long as it is not mistaken for the other.
 *
 * TRANSACTION-SCOPED, AND THAT IS NOT A DETAIL
 *
 * set_config's third argument is `is_local`, and it is `true` here. Tenants reach
 * PostgreSQL through PgBouncer in transaction pooling mode, where a server
 * connection is handed to a different client the moment a transaction ends. A
 * session-scoped setting would therefore outlive the request that set it and be
 * inherited by the next tenant user to borrow that connection — every RLS policy
 * would then evaluate against someone else's identity. Local scoping is what
 * makes pooling safe here.
 *
 * The claims are passed as a bound parameter rather than interpolated: SET does
 * not accept parameters, which is exactly why set_config exists, and building the
 * statement by string concatenation would put user-controlled text into SQL.
 */
export async function withUserContext<T>(
  db: SqlTransactor,
  context: { userId: string; tenantId?: string },
  fn: (tx: SqlExecutor) => Promise<T>,
): Promise<T> {
  const claims = JSON.stringify({
    user_id: context.userId,
    ...(context.tenantId ? { tenant_id: context.tenantId } : {}),
  });

  return db.transaction(async (tx) => {
    await tx.query(`SELECT set_config('request.jwt.claims', $1, true)`, [claims]);
    return fn(tx);
  });
}
