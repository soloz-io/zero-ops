import type { Context, MiddlewareHandler } from "hono";
import { TenantMismatchError } from "../types.js";
import { getPrincipal } from "../middleware/hono.js";

export interface TenantBoundaryOptions {
  /**
   * Function to extract the resource tenant ID from the request.
   * Default: reads from route param "tenantId" or query param "tenant_id".
   */
  getTenantId?: (c: Context) => string | undefined;
}

/**
 * Hono middleware that enforces tenant boundary:
 * principal.tenantId == resource.tenantId.
 *
 * Returns 403 if the authenticated principal's tenant does not match the
 * resource's tenant.
 *
 * Usage:
 *   app.get("/api/v1/spokes/:tenantId", requireTenantBoundary(), handler);
 */
export function requireTenantBoundary(opts?: TenantBoundaryOptions): MiddlewareHandler {
  return async (c, next) => {
    const principal = getPrincipal(c);
    if (!principal) {
      return c.json({ error: "Unauthorized", code: "NOT_AUTHENTICATED" }, 401);
    }

    const getTenantId = opts?.getTenantId ?? defaultGetTenantId;
    const resourceTenantId = getTenantId(c);

    if (!resourceTenantId) {
      return c.json(
        { error: "Bad Request", code: "TENANT_ID_REQUIRED", message: "Tenant ID is required for this operation" },
        400,
      );
    }

    if (principal.tenantId !== resourceTenantId) {
      return c.json(
        {
          error: "Forbidden",
          code: "TENANT_MISMATCH",
          message: `Access denied: your tenant (${principal.tenantId}) does not have access to tenant ${resourceTenantId}`,
        },
        403,
      );
    }

    await next();
  };
}

function defaultGetTenantId(c: Context): string | undefined {
  // Try route params first
  const routeTenantId = c.req.param("tenantId") ?? c.req.param("tenant_id");
  if (routeTenantId) return routeTenantId;

  // Try query params
  const queryTenantId = c.req.query("tenant_id");
  if (queryTenantId) return queryTenantId;

  return undefined;
}
