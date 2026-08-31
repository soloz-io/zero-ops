/**
 * Minimal SQL surface the user-resolution helpers need.
 *
 * Deliberately not tied to a driver. Tenants use postgres.js, node-postgres and
 * drizzle in different combinations, and a shared auth package that forces one of
 * them becomes a dependency conflict rather than an abstraction. Callers adapt
 * whatever client they already have; the adapter is a few lines and lives with
 * the code that owns the connection.
 */
export interface SqlExecutor {
  /**
   * Execute a parameterised statement and return the rows.
   *
   * Parameters are $1, $2, … — every value below is passed this way rather than
   * interpolated, including the JSON handed to set_config.
   */
  query<T = Record<string, unknown>>(
    sql: string,
    params?: readonly unknown[],
  ): Promise<T[]>;
}

/**
 * A client able to run a set of statements inside one transaction.
 *
 * Required, not optional, for {@link withUserContext}: the row-level security
 * settings it applies are transaction-scoped by design, so there is no correct
 * way to apply them without one.
 */
export interface SqlTransactor {
  transaction<T>(fn: (tx: SqlExecutor) => Promise<T>): Promise<T>;
}

/** A tenant-local user, as the tenant's own database understands them. */
export interface ResolvedUser {
  /**
   * The tenant-local user id — a UUID in the tenant's `users` table.
   *
   * This is NOT the OIDC subject. They are different UUIDs for the same person,
   * and conflating them is the mistake this module exists to prevent: row-level
   * security, foreign keys and every ownership column key on this value, while
   * the subject is only meaningful to the identity provider.
   */
  userId: string;
  email: string;
  /** True when this call created the user, rather than finding one. */
  isNew: boolean;
}

export interface ResolveUserOptions {
  /**
   * Identity provider name recorded in `identities.provider`.
   *
   * Defaults to "ory", the platform's provider. Overriding it is only correct
   * when a tenant genuinely federates a second provider — the same subject from
   * two providers must not collapse to one user.
   */
  provider?: string;
  /** Schema holding the platform baseline tables. Defaults to "public". */
  schema?: string;
}
