import { describe, it, expect, beforeAll } from "vitest";
import * as jose from "jose";
import { JwtValidator } from "../../src/jwt/validator.js";
import { CallerNotAllowedError } from "../../src/types.js";

// ADR-094 invariant 2: a receiver admits only applications on its allowlist.
//
// This is THE cross-application control, and the tests say why an audience
// cannot do it: every token below carries a perfectly valid audience and they
// are still not interchangeable.

const ISSUER = "https://id.example.test";
const AUD = ["own-client", "own-project"];

let priv: jose.CryptoKey;
let pub: jose.CryptoKey;

beforeAll(async () => {
  const pair = await jose.generateKeyPair("RS256", { extractable: true });
  priv = pair.privateKey;
  pub = pair.publicKey;
});

async function token(extra: Record<string, unknown>): Promise<string> {
  return new jose.SignJWT({ "urn:zitadel:iam:user:resourceowner:id": "org-1", ...extra })
    .setProtectedHeader({ alg: "RS256", kid: "k" })
    .setIssuer(ISSUER)
    .setAudience(AUD)
    .setSubject("user-1")
    .setIssuedAt()
    .setExpirationTime("5m")
    .sign(priv);
}

function validator(allowedAzp?: string[]) {
  const v = new JwtValidator({
    jwksUrl: "https://id.example.test/keys",
    issuer: ISSUER,
    audience: "own-client",
    allowedAzp,
  });
  (v as unknown as { jwksCache: { getSigningKey: () => Promise<unknown> } }).jwksCache = {
    getSigningKey: async () => () => pub,
  };
  return v;
}

describe("ADR-094 invariant 2: the caller allowlist", () => {
  it("admits a permitted caller", async () => {
    const claims = await validator(["own-bff"]).validate(await token({ azp: "own-bff" }));
    expect(claims.azp).toBe("own-bff");
  });

  it("refuses a sibling application, despite a valid audience", async () => {
    // The whole point. oranger's token for waypoint carries waypoint's audience
    // by construction -- Zitadel issues any project's audience to any client
    // that asks -- so `aud` admits it and only azp does not.
    await expect(
      validator(["own-bff"]).validate(await token({ azp: "oranger-bff" })),
    ).rejects.toThrow(CallerNotAllowedError);
  });

  it("refuses a token with NO caller identity", async () => {
    // A client_credentials token carries no azp. "Cannot tell who is calling"
    // must not read as "anyone may call".
    await expect(validator(["own-bff"]).validate(await token({}))).rejects.toMatchObject({
      code: "CALLER_NOT_ALLOWED",
    });
  });

  it("reads client_id when azp is absent", async () => {
    // Zitadel sets azp on ID tokens and client_id on access tokens for the same
    // fact. A receiver must not have to know which kind it is holding.
    const claims = await validator(["own-bff"]).validate(await token({ client_id: "own-bff" }));
    expect(claims.azp).toBe("own-bff");
  });

  it("prefers azp over client_id when both are present", async () => {
    const claims = await validator(["a"]).validate(await token({ azp: "a", client_id: "b" }));
    expect(claims.azp).toBe("a");
  });

  it("skips the check when no allowlist is configured", async () => {
    // Backwards compatible on purpose: a receiver unreachable by any other
    // application does not need the check, and turning it on by default would
    // refuse every existing deployment on upgrade.
    const claims = await validator().validate(await token({ azp: "anyone" }));
    expect(claims.azp).toBe("anyone");
  });

  it("refuses the caller BEFORE complaining about anything else", async () => {
    // A disallowed caller whose token is also tenant-less must be reported as a
    // disallowed caller. Otherwise an operator chases scopes when the answer is
    // that this application should not be calling at all.
    const v = new JwtValidator({
      jwksUrl: "https://id.example.test/keys",
      issuer: ISSUER,
      allowedAzp: ["own-bff"],
    });
    (v as unknown as { jwksCache: { getSigningKey: () => Promise<unknown> } }).jwksCache = {
      getSigningKey: async () => () => pub,
    };
    const tenantless = await new jose.SignJWT({ azp: "stranger" })
      .setProtectedHeader({ alg: "RS256", kid: "k" })
      .setIssuer(ISSUER)
      .setSubject("u")
      .setIssuedAt()
      .setExpirationTime("5m")
      .sign(priv);
    await expect(v.validate(tenantless)).rejects.toMatchObject({ code: "CALLER_NOT_ALLOWED" });
  });
});
