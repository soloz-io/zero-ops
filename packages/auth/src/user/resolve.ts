import type { TenantClaims } from "../jwt/claims.js";
import type {
  ResolveUserOptions,
  ResolvedUser,
  SqlExecutor,
  SqlTransactor,
} from "./types.js";

const DEFAULT_PROVIDER = "ory";
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
    // ONE CALL, INTO A SECURITY DEFINER FUNCTION (ADR-093).
    //
    // This used to issue the SELECT and the two INSERTs itself. It cannot any
    // more: the application no longer owns its tables, so row-level security
    // applies to it, and resolution runs BEFORE any user context exists. The
    // identities policy is `user_id = claims->>'user_id'`, so with no claims the
    // lookup matches nothing -- the caller would conclude the user is absent,
    // create a second one, and collide on (provider, provider_user_id).
    //
    // The function runs as the table owner, so it can see identities; the
    // application may only execute it. Where resolution happens is unchanged
    // (ADR-057: a library on the application's own connection, not a service) --
    // only the privilege the statement runs with.
    //
    // The race is the function's too, and is NOT solved by SECURITY DEFINER,
    // which only grants privilege. It is solved inside by a subtransaction: when
    // the identity insert hits the unique constraint, the user row inserted a
    // line earlier rolls back with it, so the loser adopts the winner rather than
    // leaving a user record no identity points at.
    //
    // The address comes back rather than being read here, because under RLS this
    // connection cannot read `users` until it has a context, and it has no
    // context until this returns.
    const rows = await tx.query<{
      user_id: string;
      email: string | null;
      is_new: boolean;
    }>(`SELECT user_id, email, is_new FROM ${schema}.resolve_user($1, $2, $3)`, [
      provider,
      claims.sub,
      email,
    ]);

    const row = rows[0];
    if (!row) {
      // A set-returning function that returns no row means the call did not
      // happen as expected -- surface it rather than returning a user id of
      // undefined that fails somewhere later.
      throw new Error(
        `resolve_user returned no row for subject ${claims.sub}`,
      );
    }

    return {
      userId: row.user_id,
      email: row.email ?? email ?? "",
      isNew: row.is_new,
    };
  });
}

/**
 * Run `fn` in a transaction whose row-level security context is set to this user.
 *
 * The platform baseline's RLS policies read
 * `current_setting('request.jwt.claims')::json->>'user_id'`. Nothing sets it, so
 * those policies currently match no rows and the protection they describe is
 * inert. This is what engages them.
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
