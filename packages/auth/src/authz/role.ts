import type { MiddlewareHandler } from "hono";
import { getPrincipal } from "../middleware/hono.js";

/**
 * Hono middleware that checks if the authenticated principal has one of the
 * required roles.
 *
 * Returns 403 if the principal's roles do not include any of the listed roles.
 *
 * Usage:
 *   app.delete("/api/:id", requireRole("admin"), deleteHandler);
 *   app.post("/api/config", requireRole("platform_admin", "tenant_admin"), handler);
 */
export function requireRole(...requiredRoles: string[]): MiddlewareHandler {
  return async (c, next) => {
    const principal = getPrincipal(c);
    if (!principal) {
      return c.json({ error: "Unauthorized", code: "NOT_AUTHENTICATED" }, 401);
    }

    const hasRole = requiredRoles.some((role) => principal.roles.includes(role));

    if (!hasRole) {
      return c.json(
        {
          error: "Forbidden",
          code: "ROLE_DENIED",
          message: `Access denied: requires one of [${requiredRoles.join(", ")}]`,
        },
        403,
      );
    }

    await next();
  };
}
