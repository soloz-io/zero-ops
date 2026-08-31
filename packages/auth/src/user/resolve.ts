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
    const existing = await tx.query<{ user_id: string; email: string }>(
      `SELECT i.user_id, u.email
         FROM ${schema}.identities i
         JOIN ${schema}.users u ON u.id = i.user_id
        WHERE i.provider = $1 AND i.provider_user_id = $2`,
      [provider, claims.sub],
    );

    if (existing.length > 0) {
      const row = existing[0]!;
      // Refresh a changed address so the tenant does not display a stale one.
      // Guarded on inequality: an unconditional UPDATE would write on every
      // request and produce needless WAL and row versions.
      if (email && email !== row.email) {
        await tx.query(`UPDATE ${schema}.users SET email = $1 WHERE id = $2`, [
          email,
          row.user_id,
        ]);
      }
      return { userId: row.user_id, email: email ?? row.email, isNew: false };
    }

    if (!email) {
      // users.email is NOT NULL in the baseline, so a first sighting without one
      // cannot be provisioned. Fail loudly rather than inventing a placeholder
      // that would later collide on the unique index.
      throw new Error(
        `cannot provision a user for subject ${claims.sub}: no email claim present`,
      );
    }

    const created = await tx.query<{ id: string }>(
      `INSERT INTO ${schema}.users (email, email_verified)
            VALUES ($1, $2)
       ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
         RETURNING id`,
      [email, claims.email_verified === true],
    );
    const userId = created[0]!.id;

    const linked = await tx.query<{ user_id: string }>(
      `INSERT INTO ${schema}.identities (user_id, provider, provider_user_id)
            VALUES ($1, $2, $3)
       ON CONFLICT (provider, provider_user_id) DO NOTHING
         RETURNING user_id`,
      [userId, provider, claims.sub],
    );

    if (linked.length === 0) {
      // Another request won the race. Its row is authoritative — returning ours
      // would hand out a user id that no identity points at.
      const raced = await tx.query<{ user_id: string }>(
        `SELECT user_id FROM ${schema}.identities
          WHERE provider = $1 AND provider_user_id = $2`,
        [provider, claims.sub],
      );
      if (raced.length === 0) {
        throw new Error(
          `identity for subject ${claims.sub} could neither be created nor found`,
        );
      }
      return { userId: raced[0]!.user_id, email, isNew: false };
    }

    return { userId, email, isNew: true };
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
