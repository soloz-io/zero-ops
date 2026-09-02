import { describe, it, expect } from "vitest";
import { claimsFromPayload } from "../../src/jwt/claims.js";

// ADR-058 injects a `groups` claim, and the API server matches it for Kubernetes
// RBAC. This library dropped it, so no consumer could read the one claim that
// marks an identity as platform-scoped — which is how a platform admin, who
// legitimately has no tenant, became indistinguishable from a token that was
// simply missing its tenant_id.
describe("claimsFromPayload — groups (ADR-058)", () => {
  it("extracts a groups array", () => {
    const claims = claimsFromPayload({
      sub: "u1",
      email: "a@b.c",
      groups: ["platform_admins", "viewers"],
    });
    expect(claims.groups).toEqual(["platform_admins", "viewers"]);
  });

  it("accepts a single group emitted as a bare string", () => {
    // Same tolerance roles already has. A provider emitting one group as a
    // string must not silently produce an empty list, because an empty list
    // reads as "not a platform admin" and denies.
    const claims = claimsFromPayload({ sub: "u1", groups: "platform_admins" });
    expect(claims.groups).toEqual(["platform_admins"]);
  });

  it("defaults to an empty array when the claim is absent", () => {
    const claims = claimsFromPayload({ sub: "u1" });
    expect(claims.groups).toEqual([]);
  });

  it("does not confuse an empty tenant_id with a present one", () => {
    // The auth-proxy writes tenant_id: "" for an identity with no tenant, so
    // the claim is PRESENT but empty. Everything downstream keys on
    // falsiness, and this asserts the mapping preserves that rather than
    // turning "" into something truthy.
    const claims = claimsFromPayload({ sub: "u1", tenant_id: "" });
    expect(claims.tenant_id).toBe("");
    expect(Boolean(claims.tenant_id)).toBe(false);
  });
});
