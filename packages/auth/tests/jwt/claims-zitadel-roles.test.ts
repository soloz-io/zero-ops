import { describe, expect, it } from "vitest";
import { claimsFromPayload } from "../../src/jwt/claims";

const ORG = "389091373187334477";
const OTHER_ORG = "389090776237277520";

/** Shape copied from a real Zitadel id_token captured on 2026-09-03. */
const base = {
  sub: "389091433551757645",
  email: "arun4infra@gmail.com",
  "urn:zitadel:iam:user:resourceowner:id": ORG,
  "urn:zitadel:iam:user:resourceowner:name": "waypoint",
};

describe("Zitadel project roles", () => {
  it("reads roles granted in the token's own organisation", () => {
    const c = claimsFromPayload({
      ...base,
      "urn:zitadel:iam:org:project:roles": {
        admin: { [ORG]: "waypoint.id.dev.nutgraf.in" },
      },
    });
    expect(c.roles).toEqual(["admin"]);
    expect(c.tenant_id).toBe(ORG);
  });

  // The whole reason the nested org id is not discarded. A role granted in a
  // DIFFERENT organisation must not read as a role here, or a user with access
  // to two tenants carries the union of both into each.
  it("ignores a role granted in another organisation", () => {
    const c = claimsFromPayload({
      ...base,
      "urn:zitadel:iam:org:project:roles": {
        admin: { [OTHER_ORG]: "other.id.dev.nutgraf.in" },
      },
    });
    expect(c.roles).toEqual([]);
  });

  it("keeps only the local half of a cross-tenant grant", () => {
    const c = claimsFromPayload({
      ...base,
      "urn:zitadel:iam:org:project:roles": {
        admin: { [ORG]: "waypoint.id.dev.nutgraf.in" },
        owner: { [OTHER_ORG]: "other.id.dev.nutgraf.in" },
      },
    });
    expect(c.roles).toEqual(["admin"]);
  });

  it("treats an absent claim as no roles, never as all roles", () => {
    expect(claimsFromPayload(base).roles).toEqual([]);
  });

  // Hydra must be untouched: this is additive, not a migration.
  it("prefers an explicit roles claim when the issuer emits one", () => {
    const c = claimsFromPayload({
      ...base,
      roles: ["from-hydra"],
      "urn:zitadel:iam:org:project:roles": {
        admin: { [ORG]: "waypoint.id.dev.nutgraf.in" },
      },
    });
    expect(c.roles).toEqual(["from-hydra"]);
  });
});
