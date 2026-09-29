import { describe, it, expect, beforeAll } from "vitest";
import * as jose from "jose";
import { JwtValidator } from "../../src/jwt/validator.js";
import { IdTokenPresentedError } from "../../src/types.js";

// ADR-095 RELEASE GATE: the Zitadel compatibility invariant, against a real issuer.
//
//   every ID token carries at_hash; no JWT access token does
//
// The unit tests beside this one assert the RULE against fixtures. They cannot
// fail when Zitadel changes, because they mint their own tokens -- so on their
// own they would keep passing while the platform silently started accepting ID
// tokens again. This one asks the issuer.
//
// SKIPPED unless pointed at an instance, so `vitest run` stays hermetic:
//
//   ZITADEL_ISSUER=https://id.example        the issuer
//   ZITADEL_EXCHANGE_CLIENT_ID=...           the gateway's confidential client
//   ZITADEL_EXCHANGE_CLIENT_SECRET=...       its secret
//   ZITADEL_SUBJECT_ID_TOKEN=...             an ID token from a real login
//   ZITADEL_PROJECT_ID=...                   the audience to exchange for
//
// The ID token is obtained by logging in and reading the session cookie the
// gateway set, or from the token endpoint directly. It expires quickly; this is
// a gate to run at release, not a watch.

const env = (k: string) => process.env[k] ?? "";
const ISSUER = env("ZITADEL_ISSUER");
const CLIENT_ID = env("ZITADEL_EXCHANGE_CLIENT_ID");
const CLIENT_SECRET = env("ZITADEL_EXCHANGE_CLIENT_SECRET");
const SUBJECT = env("ZITADEL_SUBJECT_ID_TOKEN");
const PROJECT_ID = env("ZITADEL_PROJECT_ID");

const configured = Boolean(ISSUER && CLIENT_ID && CLIENT_SECRET && SUBJECT && PROJECT_ID);

// The same request the gateway's backendAuth policy makes. Kept in step with
// manifests/tenants/charts/universal-tenant/templates/agentgateway.yaml: if the
// two drift, this gate stops testing what actually runs.
async function exchange(): Promise<string> {
  const body = new URLSearchParams({
    grant_type: "urn:ietf:params:oauth:grant-type:token-exchange",
    subject_token: SUBJECT,
    subject_token_type: "urn:ietf:params:oauth:token-type:id_token",
    // Without this Zitadel returns an OPAQUE token, regardless of the app's
    // accessTokenType=JWT. Asserting the result parses as a JWT is what catches
    // its removal.
    requested_token_type: "urn:ietf:params:oauth:token-type:jwt",
    audience: PROJECT_ID,
    // No `resource`: Zitadel rejects it outright.
  });

  const res = await fetch(new URL("/oauth/v2/token", ISSUER), {
    method: "POST",
    headers: {
      "content-type": "application/x-www-form-urlencoded",
      authorization: `Basic ${Buffer.from(`${encodeURIComponent(CLIENT_ID)}:${encodeURIComponent(CLIENT_SECRET)}`).toString("base64")}`,
    },
    body,
  });
  const json = (await res.json()) as Record<string, unknown>;
  if (!res.ok) {
    throw new Error(`token exchange failed (${res.status}): ${JSON.stringify(json)}`);
  }
  return json.access_token as string;
}

describe.skipIf(!configured)("ADR-095 gate: the Zitadel at_hash invariant, live", () => {
  let exchanged: Record<string, unknown>;
  let subjectClaims: Record<string, unknown>;

  beforeAll(async () => {
    exchanged = jose.decodeJwt(await exchange()) as Record<string, unknown>;
    subjectClaims = jose.decodeJwt(SUBJECT) as Record<string, unknown>;
  }, 30_000);

  it("the subject really is an ID token: it carries at_hash", () => {
    // If this fails the gate proves nothing -- it would be comparing two access
    // tokens and finding them alike.
    expect(subjectClaims).toHaveProperty("at_hash");
  });

  it("the exchanged token is a JWT and carries NO at_hash", () => {
    // The invariant itself.
    expect(exchanged).not.toHaveProperty("at_hash");
  });

  it("is audienced to the project it was exchanged for", () => {
    const aud = exchanged.aud;
    expect(Array.isArray(aud) ? aud : [aud]).toContain(PROJECT_ID);
  });

  it("carries azp and the project roles", () => {
    // ADR-094 requires azp. Roles must survive the exchange -- createExchangeJWT
    // asserts them, and an exchange that dropped them would authenticate every
    // user as having none, which reads as an authorisation bug, not an identity one.
    expect(exchanged.azp ?? exchanged.client_id).toBeTruthy();
    expect(exchanged).toHaveProperty("urn:zitadel:iam:org:project:roles");
  });

  it("the receiver accepts the exchanged token and refuses the subject", async () => {
    // End to end, through the real validator against the real JWKS: the whole
    // point of ADR-095 in two assertions.
    const validator = new JwtValidator({
      jwksUrl: new URL("/oauth/v2/keys", ISSUER).toString(),
      issuer: ISSUER,
      requireTenantId: false,
    });

    await expect(validator.validate(await exchange())).resolves.toBeDefined();
    await expect(validator.validate(SUBJECT)).rejects.toThrow(IdTokenPresentedError);
  }, 30_000);
});
