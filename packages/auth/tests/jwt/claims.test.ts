import { describe, it, expect } from "vitest";
import { claimsFromPayload, type TenantClaims } from "../../src/jwt/claims.js";

describe("claimsFromPayload", () => {
  it("extracts all fields from a valid payload", () => {
    const payload = {
      sub: "user-123",
      email: "test@example.com",
      tenant_id: "tenant-456",
      tenant_tier: "pro",
      roles: ["admin", "viewer"],
      iss: "https://auth.nutgraf.in",
      aud: "waypoint-public-client",
      exp: 1700000000,
      iat: 1699999000,
      nbf: 1699999000,
      jti: "token-789",
      scope: "openid offline_access",
    };

    const claims = claimsFromPayload(payload);

    expect(claims.sub).toBe("user-123");
    expect(claims.email).toBe("test@example.com");
    expect(claims.tenant_id).toBe("tenant-456");
    expect(claims.tenant_tier).toBe("pro");
    expect(claims.roles).toEqual(["admin", "viewer"]);
    expect(claims.iss).toBe("https://auth.nutgraf.in");
    expect(claims.aud).toBe("waypoint-public-client");
    expect(claims.exp).toBe(1700000000);
    expect(claims.iat).toBe(1699999000);
    expect(claims.nbf).toBe(1699999000);
    expect(claims.jti).toBe("token-789");
    expect(claims.scope).toBe("openid offline_access");
  });

  it("handles missing optional fields", () => {
    const payload = {
      sub: "user-123",
      email: "test@example.com",
      tenant_id: "tenant-456",
    };

    const claims = claimsFromPayload(payload);

    expect(claims.sub).toBe("user-123");
    expect(claims.email).toBe("test@example.com");
    expect(claims.tenant_id).toBe("tenant-456");
    expect(claims.tenant_tier).toBeUndefined();
    expect(claims.roles).toEqual([]);
    expect(claims.iss).toBeUndefined();
    expect(claims.exp).toBeUndefined();
  });

  it("handles roles as a single string", () => {
    const payload = {
      sub: "user-123",
      email: "test@example.com",
      tenant_id: "tenant-456",
      roles: "admin",
    };

    const claims = claimsFromPayload(payload);
    expect(claims.roles).toEqual(["admin"]);
  });

  it("handles missing roles", () => {
    const payload = {
      sub: "user-123",
      email: "test@example.com",
      tenant_id: "tenant-456",
    };

    const claims = claimsFromPayload(payload);
    expect(claims.roles).toEqual([]);
  });
});
