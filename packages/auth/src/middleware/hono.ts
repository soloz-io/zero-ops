import type { Context, MiddlewareHandler } from "hono";
import type { JwtValidator } from "../jwt/validator.js";
import type { AuthenticatedPrincipal } from "../authz/types.js";
import { AuthError, TokenMissingError, InsufficientScopeError } from "../types.js";

const PRINCIPAL_KEY = "auth:principal" as const;

export interface AuthMiddlewareOptions {
  /** JwtValidator instance */
  validator: JwtValidator;
  /** Required scopes — if provided, token must contain all listed scopes */
  requiredScopes?: string[];
  /** Custom header to extract token from (default: "Authorization") */
  headerName?: string;
  /**
   * Where a JWKS failure's detail goes (URL, cause, attempts). Defaults to
   * console.warn. The response carries only the code: that detail describes the
   * service's own infrastructure and is not the caller's to read.
   */
  onDependencyError?: (err: unknown) => void;
}

/**
 * Hono middleware that extracts a Bearer token, validates it via JwtValidator,
 * and sets the authenticated principal on the context.
 *
 * Usage:
 *   app.use("/api/*", authMiddleware({ validator }));
 *   // c.get("principal") → AuthenticatedPrincipal
 */
export function authMiddleware(opts: AuthMiddlewareOptions): MiddlewareHandler {
  const reportDependency =
    opts.onDependencyError ??
    ((err: unknown) => {
      console.warn("[zero-ops-auth] JWKS unavailable:", err instanceof Error ? err.message : "unknown error");
    });

  return async (c, next) => {
    const headerName = opts.headerName ?? "Authorization";
    const auth = c.req.header(headerName);

    if (!auth || !auth.startsWith("Bearer ")) {
      return c.json({ error: "Unauthorized", code: "TOKEN_MISSING" }, 401);
    }

    const token = auth.slice(7);

    try {
      const claims = await opts.validator.validate(token);

      // A token with NO scope claim previously skipped this check entirely and was
      // admitted. Absence of scope must be treated as "no scopes granted", not as
      // "check not applicable".
      if (opts.requiredScopes?.length) {
        const tokenScopes = (claims.scope ?? "").split(/\s+/).filter(Boolean);
        const missing = opts.requiredScopes.filter((s) => !tokenScopes.includes(s));
        if (missing.length > 0) {
          return c.json(
            {
              error: "Forbidden",
              code: "INSUFFICIENT_SCOPE",
              required: opts.requiredScopes,
              missing,
            },
            403,
          );
        }
      }

      const principal: AuthenticatedPrincipal = {
        subject: claims.sub,
        email: claims.email,
        tenantId: claims.tenant_id,
        tenantTier: claims.tenant_tier,
        roles: claims.roles,
        issuer: claims.iss,
        audience: typeof claims.aud === "string" ? claims.aud : Array.isArray(claims.aud) ? claims.aud[0] : undefined,
      };

      c.set(PRINCIPAL_KEY, principal);
      await next();
    } catch (err) {
      if (err instanceof AuthError) {
        // The keys could not be fetched: the token was never examined, so the
        // caller is not at fault and must not be told its credential is bad.
        if (err.code === "JWKS_FETCH_ERROR") {
          reportDependency(err);
          return c.json(
            { error: "Service Unavailable", code: err.code, message: "Credentials cannot be verified right now" },
            503,
          );
        }
        return c.json({ error: "Unauthorized", code: err.code, message: err.message }, 401);
      }
      return c.json({ error: "Unauthorized", code: "TOKEN_INVALID" }, 401);
    }
  };
}

/**
 * Extract the authenticated principal from the Hono context.
 * Must be called after authMiddleware has run.
 */
export function getPrincipal(c: Context): AuthenticatedPrincipal | undefined {
  return c.get(PRINCIPAL_KEY) as AuthenticatedPrincipal | undefined;
}

export { PRINCIPAL_KEY };
