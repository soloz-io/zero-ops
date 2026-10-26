import { describe, it, expect, beforeAll } from "vitest";
import * as jose from "jose";
import { JwtValidator } from "../../src/jwt/validator.js";
import { IdTokenPresentedError } from "../../src/types.js";

// ADR-095: APIs accept access tokens, never ID tokens.
//
// The discriminator is `at_hash`, not the audience. These tests encode WHY, by
// giving every token here the SAME issuer and the SAME audience -- which is what
// Zitadel actually does, signing one `session.Audience` slice into both tokens.
// If a future change reaches for the audience again, the access-token case and
// the ID-token case below are indistinguishable by it, and only the at_hash rule
// separates them.

const ISSUER = "https://id.example.test";
const CLIENT_ID = "389169228512494069";
const PROJECT_ID = "389169228512494070";
// Both tokens carry both, exactly as Zitadel's audienceFromProjectID builds it:
// append(<every client id in the project>, <project id>).
const AUDIENCE = [CLIENT_ID, PROJECT_ID];

let privateKey: jose.CryptoKey;
let publicKey: jose.CryptoKey;

beforeAll(async () => {
  const pair = await jose.generateKeyPair("RS256", { extractable: true });
  privateKey = pair.privateKey;
  publicKey = pair.publicKey;
});

async function sign(claims: Record<string, unknown>): Promise<string> {
  return new jose.SignJWT(claims)
    .setProtectedHeader({ alg: "RS256", kid: "test-key" })
    .setIssuer(ISSUER)
    .setAudience(AUDIENCE)
    .setSubject("user-1")
    .setIssuedAt()
    .setExpirationTime("5m")
    .sign(privateKey);
}

/** A validator whose JWKS lookup is the generated public key. */
function validator(opts: Partial<ConstructorParameters<typeof JwtValidator>[0]> = {}) {
  const v = new JwtValidator({
    jwksUrl: "https://id.example.test/keys",
    issuer: ISSUER,
    audience: CLIENT_ID,
    requireTenantId: false,
    ...opts,
  });
  // The network is not the subject of these tests.
  (v as unknown as { jwksCache: { getSigningKey: () => Promise<unknown> } }).jwksCache = {
    getSigningKey: async () => () => publicKey,
  };
  return v;
}

const accessToken = () =>
  sign({ client_id: CLIENT_ID, jti: "tok-1", "urn:zitadel:iam:org:project:roles": {} });

// at_hash is what createIDToken adds and nothing else does -- a ZITADEL
// invariant, not an OIDC guarantee (it is OPTIONAL in the code flow).
const idToken = () => sign({ azp: CLIENT_ID, auth_time: 1, at_hash: "s9Sm2PkgYFbLQDRcXKAZ3g" });

describe("ADR-095: an API refuses an ID token", () => {
  it("accepts an access token", async () => {
    const claims = await validator().validate(await accessToken());
    expect(claims.sub).toBe("user-1");
  });

  it("refuses an ID token, by at_hash", async () => {
    await expect(validator().validate(await idToken())).rejects.toThrow(IdTokenPresentedError);
  });

  it("names the failure ID_TOKEN_PRESENTED, not TOKEN_INVALID", async () => {
    // The token is well-formed, correctly signed, right issuer, right audience.
    // Reporting it as TOKEN_INVALID would send an operator looking for a signing
    // or clock fault that does not exist.
    await expect(validator().validate(await idToken())).rejects.toMatchObject({
      code: "ID_TOKEN_PRESENTED",
    });
  });

  it("proves the audience cannot make this distinction", async () => {
    // THE finding that rewrote ADR-095, asserted rather than described.
    //
    // at_hash is turned OFF here on purpose. With it on, the ID token is refused
    // and the test would pass without showing anything -- it could not tell an
    // audience rejection from an at_hash rejection. Off, the audience check is
    // the ONLY rule left, and both tokens pass it under either configuration.
    // That is the proof: no audience value separates them, so the first draft's
    // receiver contract could not have enforced its own invariant.
    for (const aud of [CLIENT_ID, PROJECT_ID]) {
      const permissive = () => validator({ audience: aud, rejectIdTokens: false });
      await expect(permissive().validate(await accessToken())).resolves.toBeDefined();
      await expect(permissive().validate(await idToken())).resolves.toBeDefined();
    }

    // And the corollary: tightening the audience to the project id -- which the
    // first draft made the endpoint of a three-step migration -- changes nothing
    // for either token.
    const tightened = validator({ audience: PROJECT_ID, rejectIdTokens: false });
    await expect(tightened.validate(await idToken())).resolves.toBeDefined();
  });

  it("refuses BEFORE deriving claims, so a rejected token resolves no identity", async () => {
    // requireTenantId is on here. A token with at_hash and no tenant must fail as
    // an ID token, not as a missing tenant: the order decides which of two rules
    // an operator is told about, and the kind of token is the cause.
    await expect(
      validator({ requireTenantId: true }).validate(await idToken()),
    ).rejects.toMatchObject({ code: "ID_TOKEN_PRESENTED" });
  });

  it("can be staged off, for a box whose gateway has not switched yet", async () => {
    const claims = await validator({ rejectIdTokens: false }).validate(await idToken());
    expect(claims.sub).toBe("user-1");
  });

  it("rejects on presence alone, whatever the hash value is", async () => {
    // The value hashes an access token this service was never given. Presence is
    // the test; nothing reads it.
    const weird = await sign({ at_hash: "" });
    await expect(validator().validate(weird)).rejects.toThrow(IdTokenPresentedError);
  });
});
