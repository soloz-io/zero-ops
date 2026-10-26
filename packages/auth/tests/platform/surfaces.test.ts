import { describe, it, expect } from "vitest";
import {
  browserSessionValidator,
  consumerApiValidator,
  internalCallHeaders,
  internalChannelHeaders,
  InternalTokenMissingError,
  PlatformConfigError,
  parseAllowedCallers,
} from "../../src/index.js";

// These factories exist so a product team does not decide four security switches
// from an ADR. That only holds if the switches stay decided — and a wrong switch
// here fails by ACCEPTING a token, which no integration test would notice. So
// each one is pinned by name.

const BASE = {
  OIDC_ISSUER_URL: "https://id.dev.nutgraf.in",
  OIDC_JWKS_URL: "https://id.dev.nutgraf.in/oauth/v2/keys",
  OIDC_CLIENT_ID: "oranger-public@nutgraf",
  OIDC_PROJECT_ID: "392885920103137720",
  OIDC_EXCHANGE_CLIENT_ID: "oranger-gateway-exchange@nutgraf",
  OIDC_ORG_ID: "331885920103137720",
};

/** The switches live on the instance; read them without widening the public API. */
const sw = (v: unknown) => v as unknown as Record<string, unknown>;

describe("browserSessionValidator", () => {
  it("accepts an ID token, because that is what the gateway forwards", () => {
    // Not a preference. Zitadel v4.15.3 requires an exchange scope to be on BOTH
    // the subject and actor tokens, and an id-token subject carries none -- so
    // minting an access token here is refused by the issuer, and no released
    // Zitadel validates it otherwise.
    expect(sw(browserSessionValidator({ env: BASE })).rejectIdTokens).toBe(false);
  });

  it("still admits exactly one caller, which is what keeps a sibling out", () => {
    // THE pairing that must never come apart: ID tokens accepted AND no caller
    // check would leave this surface open to every application on the box.
    const v = sw(browserSessionValidator({ env: BASE }));
    expect(v.allowedAzp).toEqual([BASE.OIDC_CLIENT_ID]);
  });

  it("audiences on this application's own client, which the id token carries", () => {
    expect(sw(browserSessionValidator({ env: BASE })).audience).toBe(BASE.OIDC_CLIENT_ID);
  });

  it("COMPARES the tenant, not merely requires it (ADR-094 invariant 3)", () => {
    // Presence alone is not isolation: a sibling tenant's user carries a valid
    // tenant claim, just a different one.
    const v = sw(browserSessionValidator({ env: BASE }));
    expect(v.requireTenantId).toBe(true);
    expect(v.expectedTenantId).toBe(BASE.OIDC_ORG_ID);
  });

  it("names every missing platform value at once, not the first", () => {
    try {
      browserSessionValidator({ env: { OIDC_ISSUER_URL: BASE.OIDC_ISSUER_URL } });
      throw new Error("expected a PlatformConfigError");
    } catch (e) {
      expect(e).toBeInstanceOf(PlatformConfigError);
      expect((e as PlatformConfigError).missing).toEqual([
        "OIDC_JWKS_URL",
        "OIDC_CLIENT_ID",
        "OIDC_ORG_ID",
      ]);
    }
  });
});

describe("consumerApiValidator", () => {
  const env = { ...BASE, OIDC_ALLOWED_AZP: "oranger-gateway-exchange@nutgraf=oranger" };

  it("refuses ID tokens: the browser exception does not cross an application boundary", () => {
    expect(sw(consumerApiValidator({ env })).rejectIdTokens).toBe(true);
  });

  it("audiences on the project, which is what an exchanged token carries", () => {
    expect(sw(consumerApiValidator({ env })).audience).toBe(BASE.OIDC_PROJECT_ID);
  });

  it("compares the tenant too", () => {
    expect(sw(consumerApiValidator({ env })).expectedTenantId).toBe(BASE.OIDC_ORG_ID);
  });

  it("uses the caller MAP, so the receiver can name its caller", () => {
    expect(sw(consumerApiValidator({ env })).allowedCallers).toEqual({
      "oranger-gateway-exchange@nutgraf": "oranger",
    });
  });

  it("refuses to build with no callers, rather than checking nothing", () => {
    // An empty allowlist is not "admit nobody" — JwtValidator reads it as "no
    // caller check". On a cross-application surface that is open to everyone.
    expect(() => consumerApiValidator({ env: { ...BASE, OIDC_ALLOWED_AZP: "" } })).toThrow(
      /would\s+admit every application/,
    );
    expect(() => consumerApiValidator({ env: BASE })).toThrow(/OIDC_ALLOWED_AZP/);
  });
});

describe("parseAllowedCallers", () => {
  it("keeps an unnamed entry rather than dropping it", () => {
    // Dropping it would REVOKE a caller during the upgrade that introduces names.
    expect(parseAllowedCallers("old-client new-client=newapp")).toEqual({
      "old-client": "",
      "new-client": "newapp",
    });
  });

  it("reads both separators the platform has used", () => {
    expect(parseAllowedCallers("a=1, b=2")).toEqual({ a: "1", b: "2" });
  });

  it("is empty for an absent value", () => {
    expect(parseAllowedCallers(undefined)).toEqual({});
  });
});

describe("internal channel", () => {
  it("derives the names both applications already spell by hand", () => {
    expect(internalChannelHeaders("oranger")).toEqual({
      token: "x-oranger-internal-token",
      subject: "x-oranger-user-subject",
      email: "x-oranger-user-email",
      tenant: "x-oranger-user-tenant",
      roles: "x-oranger-user-roles",
    });
    expect(internalChannelHeaders("waypoint").token).toBe("x-waypoint-internal-token");
  });

  it("throws on a missing token instead of sending the call without one", () => {
    // The defect this replaces: `process.env.X ? {...} : {}` in eight places, so
    // an absent token produced a call with no header and a bare 401 at the far end.
    expect(() => internalCallHeaders("waypoint", undefined)).toThrow(InternalTokenMissingError);
    expect(() => internalCallHeaders("waypoint", "")).toThrow(/has not received its secret/);
  });

  it("carries the acting user, roles included", () => {
    expect(
      internalCallHeaders("oranger", "tok", {
        subject: "sub-1",
        email: "a@b.c",
        tenantId: "nutgraf",
        roles: ["admin", "editor"],
      }),
    ).toEqual({
      "x-oranger-internal-token": "tok",
      "x-oranger-user-subject": "sub-1",
      "x-oranger-user-email": "a@b.c",
      "x-oranger-user-tenant": "nutgraf",
      "x-oranger-user-roles": "admin,editor",
    });
  });

  it("permits an internal call with no acting user, and sends no identity then", () => {
    expect(internalCallHeaders("oranger", "tok")).toEqual({ "x-oranger-internal-token": "tok" });
  });
});
