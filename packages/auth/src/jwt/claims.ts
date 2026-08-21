export interface TenantClaims {
  /** Subject (user ID) */
  sub: string;
  /** User email */
  email: string;
  /** Tenant ID — required for tenant-scoped authorization */
  tenant_id: string;
  /** Tenant tier (e.g., "free", "pro", "enterprise") */
  tenant_tier?: string;
  /** User roles */
  roles: string[];
  /** JWT issuer */
  iss?: string;
  /** JWT audience */
  aud?: string | string[];
  /** Expiration time (unix seconds) */
  exp?: number;
  /** Issued at (unix seconds) */
  iat?: number;
  /** Not before (unix seconds) */
  nbf?: number;
  /** JWT ID */
  jti?: string;
  /** Token scope (space-delimited) */
  scope?: string;
}

export function claimsFromPayload(payload: Record<string, unknown>): TenantClaims {
  return {
    sub: String(payload.sub ?? ""),
    email: String(payload.email ?? ""),
    tenant_id: String(payload.tenant_id ?? ""),
    tenant_tier: payload.tenant_tier ? String(payload.tenant_tier) : undefined,
    // The platform's auth-proxy injects a SINGULAR `role` claim
    // (internal/auth-proxy/validate.go). Without this fallback `roles` was always
    // empty and every requireRole() check denied.
    roles: Array.isArray(payload.roles)
      ? (payload.roles as unknown[]).map(String)
      : typeof payload.roles === "string"
        ? [payload.roles]
        : Array.isArray(payload.role)
          ? (payload.role as unknown[]).map(String)
          : typeof payload.role === "string"
            ? [payload.role]
        : [],
    iss: payload.iss ? String(payload.iss) : undefined,
    aud: payload.aud as TenantClaims["aud"],
    exp: typeof payload.exp === "number" ? payload.exp : undefined,
    iat: typeof payload.iat === "number" ? payload.iat : undefined,
    nbf: typeof payload.nbf === "number" ? payload.nbf : undefined,
    jti: payload.jti ? String(payload.jti) : undefined,
    scope: payload.scope ? String(payload.scope) : undefined,
  };
}
