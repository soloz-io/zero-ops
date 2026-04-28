Based on my research, here's what modern enterprises use:

## Industry Standard: **Option B (Per-Tenant Audience) is Idiomatic**

**Evidence from Research:**

1. **WorkOS/Enterprise SaaS Pattern**: JWT claims include explicit `org_id` (tenant identifier) alongside `sub` (user). Authorization is "evaluated within the tenant, not globally."

2. **AWS Cognito**: Uses `client_id` claim in access tokens, where each app client represents a tenant/application boundary. Audience validation is per-client.

3. **Azure AD Multi-Tenant**: Issues tokens with tenant-specific issuer URIs (`https://login.microsoftonline.com/{tenantId}/v2.0`), not global.

4. **RFC 8707 Resource Indicators**: Standard practice is to use **specific resource URIs** per API, not shared audiences. Auth0, Keycloak, and enterprise IdPs all support per-resource audience claims.

**For Your Hub-Spoke Model:**

**Recommended**: `urn:zero-ops:tenant:{tenantId}:api` (Option B)

**Why:**
- Prevents token replay between tenants (defense-in-depth)
- Aligns with enterprise security reviews
- Matches how Azure AD, AWS, and WorkOS handle multi-tenancy
- Crossplane can inject `tenantId` dynamically per spoke

**Option A** (`urn:zero-ops:tenant-api`) works for MVP but requires RLS to be your only isolation layer—risky if RLS misconfigured.