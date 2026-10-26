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
//
// The second block is the ADR-094 cross-project role EXPERIMENT. It is an open
// question, not a settled invariant, and it has its own env vars so it can be
// run without the first block's.

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
async function exchange(extraScopes: string[] = [], extraAudiences: string[] = []): Promise<string> {
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
  for (const a of extraAudiences) body.append("audience", a);
  if (extraScopes.length > 0) body.set("scope", extraScopes.join(" "));

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

// ── ADR-094 EXPERIMENT: can one exchanged token carry BOTH projects' roles? ──
//
// The cross-app gap: roles are resolved against the EXCHANGE CLIENT'S project
// (`getUserInfo(subjectToken.userID, client.client.ProjectID, ...)`), so a token
// oranger mints for waypoint carries ORANGER's roles. waypoint can then answer
// "is this token for me?" and "which app called?" but not "what may this user do
// here?".
//
// The candidate fix is Zitadel's plural roles scope. Read from source before
// writing this, because two details differ from how it is usually described and
// both change what must be sent:
//
//   1. `urn:zitadel:iam:org:projects:roles` is driven by the `:aud` SCOPES in
//      the request, not by the token's audience. prepareRoles builds its
//      roleAudience with AddAudScopeToAudience(ctx, roleAudience, scope), which
//      parses `urn:zitadel:iam:org:project:id:<id>:aud` out of the SCOPE list.
//      The RFC 8693 `audience` parameter is a different input entirely, so both
//      are sent below.
//
//   2. The plural scope is NOT subject to the cross-project role filter.
//      isScopeAllowed returns true for it before reaching
//      `slices.Contains(allowedScopes, scope)`, and allowedScopes is built from
//      the AUTHENTICATING client's ProjectRoleKeys. That check is what filters a
//      SPECIFIC role scope (`...:project:role:<key>`) by the caller's own
//      project -- the defect surface in the report against 4.17.2. So this
//      deliberately requests NO specific role scopes.
//
// Also: `ProjectRoleAssertion` cannot be varied per client. It is a column on
// the PROJECT (internal/query/project.go), shared by every app in it. What IS
// per-app is AccessTokenRoleAssertion, and it must stay true -- assertRoles
// early-returns on it, so false yields no roles at all rather than unfiltered
// ones.
//
//   ZITADEL_PEER_PROJECT_ID=...   the OTHER project (e.g. waypoint's)
//   ZITADEL_PEER_ROLE=...         a role the test user holds in THAT project
//
// A failure here is a FINDING, not a broken build: it decides between the
// audience-carried roles below and receiver-side exchange. Read the assertion
// messages rather than only the pass/fail.

const PEER_PROJECT = env("ZITADEL_PEER_PROJECT_ID");
const PEER_ROLE = env("ZITADEL_PEER_ROLE");
const crossConfigured = configured && Boolean(PEER_PROJECT && PEER_ROLE);

describe.skipIf(!crossConfigured)("ADR-094 experiment: cross-project roles in one exchanged token", () => {
  let claims: Record<string, unknown>;

  beforeAll(async () => {
    const token = await exchange(
      [
        "openid",
        // The plural scope. Singular `...:project:role:<key>` is deliberately
        // absent -- that is the filtered path.
        "urn:zitadel:iam:org:projects:roles",
        // What actually drives roleAudience.
        `urn:zitadel:iam:org:project:id:${PROJECT_ID}:aud`,
        `urn:zitadel:iam:org:project:id:${PEER_PROJECT}:aud`,
      ],
      [PEER_PROJECT],
    );
    claims = jose.decodeJwt(token) as Record<string, unknown>;
  }, 30_000);

  it("is audienced to BOTH projects", () => {
    const aud = claims.aud;
    const list = Array.isArray(aud) ? aud : [aud];
    expect(list).toContain(PROJECT_ID);
    expect(list).toContain(PEER_PROJECT);
  });

  it("carries the OWN project's roles, project-qualified", () => {
    // setUserInfoRoleClaims emits urn:zitadel:iam:org:project:<id>:roles for
    // every project whose grants were fetched, plus an unqualified claim for the
    // requesting project only.
    expect(claims).toHaveProperty(`urn:zitadel:iam:org:project:${PROJECT_ID}:roles`);
  });

  it("carries the PEER project's roles — this is the whole experiment", () => {
    const key = `urn:zitadel:iam:org:project:${PEER_PROJECT}:roles`;
    expect(
      claims,
      `No ${key} in the exchanged token. If this fails, the audience cannot carry ` +
        `cross-project authorization and the choice is receiver-side exchange, not ` +
        `a second authorization store. Claims present: ${Object.keys(claims).join(", ")}`,
    ).toHaveProperty(key);
    expect(Object.keys(claims[key] as object)).toContain(PEER_ROLE);
  });

  it("does not collapse the peer's roles into the requesting project's claim", () => {
    // The failure mode worth naming: roles arriving under the WRONG project key
    // would let waypoint authorise on oranger's roles while looking correct.
    const own = (claims[`urn:zitadel:iam:org:project:${PROJECT_ID}:roles`] ?? {}) as object;
    expect(Object.keys(own)).not.toContain(PEER_ROLE);
  });
});
