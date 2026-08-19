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
  return async (c, next) => {
    const headerName = opts.headerName ?? "Authorization";
    const auth = c.req.header(headerName);

    if (!auth || !auth.startsWith("Bearer ")) {
      return c.json({ error: "Unauthorized", code: "TOKEN_MISSING" }, 401);
    }

    const token = auth.slice(7);

    try {
      const claims = await opts.validator.validate(token);

      if (opts.requiredScopes?.length && claims.scope) {
        const tokenScopes = claims.scope.split(/\s+/);
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
        const status = err.code === "TOKEN_EXPIRED" ? 401 : 401;
        return c.json({ error: "Unauthorized", code: err.code, message: err.message }, status);
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
