import type { Context, Handler, MiddlewareHandler } from "hono";
import type { TenantClaims } from "../jwt/claims.js";
import { resolveUser } from "../user/resolve.js";
import type { ResolveUserOptions, SqlTransactor } from "../user/types.js";
import { getPrincipal } from "./hono.js";

const ACTING_USER_KEY = "auth:actingUser" as const;

/** The acting user, resolved to this app's own `users` row (ADR-057). */
export interface ActingUser {
  /**
   * Tenant-local user id -- the uuid every ownership column and row-level
   * security policy keys on. Distinct from `subject`.
   */
  userId: string;
  /** The identity provider's subject. Stable, but meaningful only to it. */
  subject: string;
  email: string;
  tenantId: string;
  /** True when this request provisioned the user (first sighting). */
  isNew: boolean;
}

export interface ActingUserOptions {
  /** The app's own database, as the RUNTIME role (ADR-093). */
  db: SqlTransactor;
  resolve?: ResolveUserOptions;
  /** Where resolution failures are reported. Defaults to console.warn. */
  onError?: (err: unknown) => void;
}

/**
 * Resolve the validated principal to a tenant-local user, once per request.
 *
 * Mount AFTER `authMiddleware`: this trusts only the principal that middleware
 * established from a validated token, never a header. For an app whose
 * authenticating boundary also holds the database connection -- the common
 * case, which ADR-057 permits -- the two run in one process and nothing is
 * forwarded between them.
 *
 * A failure does not fail the request. ADR-057: absent identity is permitted,
 * unattributed writes are not. The handler that needs an owner calls
 * `requireActingUser()` and fails there, where the requirement is visible.
 */
export function actingUserMiddleware(opts: ActingUserOptions): MiddlewareHandler {
  const report =
    opts.onError ??
    ((err: unknown) => {
      console.warn(
        "acting user resolution failed:",
        err instanceof Error ? err.message : "unknown error",
      );
    });

  return async (c, next) => {
    const principal = getPrincipal(c);
    if (principal?.subject) {
      try {
        // resolveUser reads only sub and email. Built explicitly rather than cast
        // from the principal, so a claim this library does not intend to pass
        // cannot reach the resolution function.
        const claims: TenantClaims = {
          sub: principal.subject,
          email: principal.email,
          tenant_id: principal.tenantId,
          roles: principal.roles,
          groups: [],
        };
        const user = await resolveUser(opts.db, claims, opts.resolve);
        const acting: ActingUser = {
          userId: user.userId,
          subject: principal.subject,
          email: user.email,
          tenantId: principal.tenantId,
          isNew: user.isNew,
        };
        c.set(ACTING_USER_KEY, acting);
      } catch (err) {
        report(err);
      }
    }
    await next();
  };
}

/** The acting user, or undefined when none was resolved. */
export function getActingUser(c: Context): ActingUser | undefined {
  return c.get(ACTING_USER_KEY) as ActingUser | undefined;
}

/**
 * The acting user, or a thrown error.
 *
 * For handlers whose work is meaningless without an owner -- anything that
 * creates, mutates or lists a user's own records. Pair with `withUserContext`
 * so the row-level security policies see the same user.
 */
export function requireActingUser(c: Context): ActingUser {
  const user = getActingUser(c);
  if (!user) {
    throw new Error("this operation requires an acting user, and none was resolved");
  }
  return user;
}

/**
 * The identity endpoint `zero-ops-auth/client` reads (`GET /api/v1/auth/me`).
 *
 * Mount after `authMiddleware` and `actingUserMiddleware`. The response shape is
 * the client's `UserIdentity`, so every app answers the question the same way:
 * 401 means signed out, and an authenticated person whose tenant-local user
 * could not be resolved is `userId: null` -- signed in, ownership unknown --
 * rather than an error.
 */
export function identityHandler(): Handler {
  return (c) => {
    const principal = getPrincipal(c);
    if (!principal?.subject) {
      return c.json({ error: "Unauthenticated" }, 401);
    }
    const acting = getActingUser(c);
    return c.json({
      user: {
        userId: acting?.userId ?? null,
        subject: principal.subject,
        email: acting?.email || principal.email || null,
        role: principal.roles[0] ?? null,
        tenantId: principal.tenantId || null,
      },
    });
  };
}

export { ACTING_USER_KEY };
