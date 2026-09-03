export interface TenantClaims {
  /** Subject (user ID) */
  sub: string;
  /** User email */
  email: string;
  /**
   * Standard OIDC claim. Optional because not every provider or flow emits it,
   * and an absent claim must not be read as "verified".
   */
  email_verified?: boolean;
  /** Tenant ID — required for tenant-scoped authorization */
  tenant_id: string;
  /**
   * Human-readable tenant name, when the provider supplies one.
   *
   * Never load-bearing: tenant_id is the identifier and the only thing an
   * authorization decision may use. This is for display and logs, where an
   * issuer-allocated identifier is unreadable — they are commonly opaque
   * numbers, while the tenant has a name a human recognises.
   */
  tenant_name?: string;
  /** Tenant tier (e.g., "free", "pro", "enterprise") */
  tenant_tier?: string;
  /** User roles */
  roles: string[];
  /**
   * Platform-assigned groups (ADR-058).
   *
   * The auth-proxy injects this claim, and the API server matches it for
   * Kubernetes RBAC — but this library dropped it, so no consumer could read
   * the one claim that identifies a platform-scoped identity. That is why a
   * platform admin, who legitimately has no tenant, could not be told apart
   * from a token that was simply missing its tenant.
   */
  groups: string[];
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

/**
 * Claim names the platform's own contract does not define.
 *
 * The platform's contract is `tenant_id` and `roles`. An issuer that does not
 * emit those spells them its own way, and reading it means naming its claims
 * somewhere — there is no abstraction that removes the literal, only one that
 * decides where it lives.
 *
 * They live HERE, in one table, so the rest of this library is written against
 * the contract rather than against an issuer. Supporting another issuer is an
 * entry in this table, not an edit to the logic below (ADR-059). The reasoning
 * for each name, and which product uses it, belongs in that ADR rather than in
 * this file.
 *
 * Order is preference order: the platform's own claim first, so a token that
 * already speaks the contract is never reinterpreted.
 */
const TENANT_ID_CLAIMS = [
  "tenant_id",
  "urn:zitadel:iam:user:resourceowner:id",
  "urn:zitadel:iam:org:id",
] as const;

const TENANT_NAME_CLAIMS = [
  "tenant_name",
  "urn:zitadel:iam:user:resourceowner:name",
  "urn:zitadel:iam:org:name",
] as const;

/** Nested as `{ roleKey: { grantingTenantId: domain } }`. */
const SCOPED_ROLES_CLAIM = "urn:zitadel:iam:org:project:roles";

function firstString(
  payload: Record<string, unknown>,
  names: readonly string[],
): string {
  for (const n of names) {
    const v = payload[n];
    if (typeof v === "string" && v) return v;
  }
  return "";
}

/**
 * Roles granted to this user WITHIN THE GIVEN TENANT.
 *
 * The claim is nested because one token can carry the same role granted in
 * several tenants. An issuer emits it only when asked for the scope that mints
 * it, so absent means no roles — never "all roles".
 *
 * The tenant filter is the security-relevant part, not a detail. Flattening to
 * `Object.keys()` would return a role granted in a DIFFERENT tenant as if it had
 * been granted here — a cross-tenant privilege leak that reads as a correct role
 * list, and one that stays invisible until a user is granted access to a second
 * tenant. Roles are therefore kept only where the nested tenant id equals the
 * tenant this token is scoped to.
 */
function rolesGrantedInTenant(
  payload: Record<string, unknown>,
  tenantId: string,
): string[] {
  const raw = payload[SCOPED_ROLES_CLAIM];
  if (!raw || typeof raw !== "object" || !tenantId) return [];
  const out: string[] = [];
  for (const [roleKey, grantedIn] of Object.entries(
    raw as Record<string, unknown>,
  )) {
    if (
      grantedIn &&
      typeof grantedIn === "object" &&
      Object.prototype.hasOwnProperty.call(grantedIn, tenantId)
    ) {
      out.push(roleKey);
    }
  }
  return out;
}

export function claimsFromPayload(payload: Record<string, unknown>): TenantClaims {
  const tenantId = firstString(payload, TENANT_ID_CLAIMS);

  return {
    sub: String(payload.sub ?? ""),
    email: String(payload.email ?? ""),
    email_verified: payload.email_verified === true,
    // Read through the alias table, so one library serves any issuer and a
    // provider swap is a configuration change rather than a fork.
    //
    // An issuer that carries tenancy structurally — where the tenant OWNS the
    // user rather than being an attribute written onto it — mints these claims
    // only for the scope that asks for them. Without that scope the claim is
    // absent and the token is correctly rejected as tenant-less, so the
    // requirement in the validator must stay a check and never a default.
    tenant_id: tenantId,
    tenant_name: firstString(payload, TENANT_NAME_CLAIMS) || undefined,
    tenant_tier: payload.tenant_tier ? String(payload.tenant_tier) : undefined,
    // The platform's auth-proxy injects a SINGULAR `role` claim
    // (internal/auth-proxy/validate.go). Without this fallback `roles` was always
    // empty and every requireRole() check denied.
    // Same singular/plural tolerance as roles: the source of this claim is
    // metadata_public.groups, which is an array, but a provider emitting a
    // single string must not silently produce an empty list.
    groups: Array.isArray(payload.groups)
      ? (payload.groups as unknown[]).map(String)
      : typeof payload.groups === "string"
        ? [payload.groups]
        : [],
    // The contract's own claim wins when present; the issuer's scoped roles
    // otherwise.
    //
    // Ordered this way so a token that already speaks the contract behaves
    // exactly as before and this stays additive. Where it falls through,
    // authorisation comes from the issuer's own model rather than from a claim
    // the platform injected — which is what ADR-059 means by not running a
    // second model beside the provider's.
    roles: Array.isArray(payload.roles)
      ? (payload.roles as unknown[]).map(String)
      : typeof payload.roles === "string"
        ? [payload.roles]
        : Array.isArray(payload.role)
          ? (payload.role as unknown[]).map(String)
          : typeof payload.role === "string"
            ? [payload.role]
            : rolesGrantedInTenant(payload, tenantId),
    iss: payload.iss ? String(payload.iss) : undefined,
    aud: payload.aud as TenantClaims["aud"],
    exp: typeof payload.exp === "number" ? payload.exp : undefined,
    iat: typeof payload.iat === "number" ? payload.iat : undefined,
    nbf: typeof payload.nbf === "number" ? payload.nbf : undefined,
    jti: payload.jti ? String(payload.jti) : undefined,
    scope: payload.scope ? String(payload.scope) : undefined,
  };
}
