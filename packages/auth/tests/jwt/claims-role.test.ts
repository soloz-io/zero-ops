import { describe, expect, it } from "vitest";
import { claimsFromPayload } from "../../src/jwt/claims.js";

// The platform's auth-proxy injects a SINGULAR `role` claim. Without a fallback,
// `roles` was always [] and every requireRole() check denied — silently.
describe("claimsFromPayload — role claim compatibility", () => {
  const base = { sub: "u1", email: "u@t.io", tenant_id: "t1" };

  it("accepts the singular `role` claim auth-proxy emits", () => {
    expect(claimsFromPayload({ ...base, role: "platform_admin" }).roles).toEqual([
      "platform_admin",
    ]);
  });

  it("accepts a singular `role` supplied as an array", () => {
    expect(claimsFromPayload({ ...base, role: ["admin", "viewer"] }).roles).toEqual([
      "admin",
      "viewer",
    ]);
  });

  it("still prefers plural `roles` when both are present", () => {
    const c = claimsFromPayload({ ...base, roles: ["a"], role: "b" });
    expect(c.roles).toEqual(["a"]);
  });

  it("yields an empty list when neither is present", () => {
    expect(claimsFromPayload(base).roles).toEqual([]);
  });
});
