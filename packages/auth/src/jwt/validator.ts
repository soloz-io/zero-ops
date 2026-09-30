import * as jose from "jose";
import { JwksCache } from "./jwks-cache.js";
import { type TenantClaims, claimsFromPayload } from "./claims.js";
import {
  TokenExpiredError,
  InvalidIssuerError,
  InvalidAudienceError,
  TokenMissingError,
  IdTokenPresentedError,
  CallerNotAllowedError,
  AuthError,
} from "../types.js";

export interface JwtValidatorOptions {
  /** JWKS endpoint URL */
  jwksUrl: string;
  /** Expected issuer — if provided, validation fails when issuer mismatches */
  issuer?: string;
  /** Expected audience — if provided, validation fails when audience mismatches */
  audience?: string;
  /** Allowed signing algorithms (default: ["RS256", "ES256"]) */
  algorithms?: string[];
  /** JWKS cache options */
  jwksCache?: Partial<import("./jwks-cache.js").JwksCacheOptions>;
  /**
   * Require a tenant claim. Default true.
   *
   * Set false only for a validator guarding a platform-scoped surface where no
   * identity is expected to carry a tenant. A tenant-scoped service must leave
   * this on: without it, a token minted for no tenant is accepted wherever
   * tenant isolation is the boundary.
   */
  requireTenantId?: boolean;
  /**
   * Refuse a token that carries `at_hash`. Default true.
   *
   * A ZITADEL COMPATIBILITY INVARIANT, not a general OIDC rule. OIDC Core
   * requires `at_hash` only where an access token is returned from the
   * authorization endpoint (implicit and hybrid); in the authorization-code flow
   * this platform uses it is OPTIONAL, so a conforming issuer may omit it. What
   * holds is narrower and version-scoped:
   *
   *   for the pinned Zitadel, every ID token carries `at_hash` and no JWT
   *   access token does -- `createIDToken` is the only place that sets it, and
   *   it is called with a non-empty access token on every token-endpoint path.
   *
   * Re-establish it before trusting a different issuer or a Zitadel upgrade; an
   * issuer that stops emitting it does not fail this check, it silently passes.
   *
   * It is a claim check rather than an audience check because in Zitadel the two
   * tokens share an audience: `createIDToken` and `createJWT` are both handed
   * `session.Audience`, so no audience distinguishes them.
   *
   * Sound against a hostile caller regardless: `at_hash` is signed, so it cannot
   * be stripped from a genuine ID token without invalidating the signature.
   *
   * ORDERING. A deployment whose gateway still forwards the ID token will have
   * EVERY request refused by this. The gateway must be forwarding an exchanged
   * access token first (ADR-095 step 1); adopting the SDK version that carries
   * this default is step 2, and the two are separate deploys on purpose.
   *
   * Set false only to stage that ordering on a box that cannot do both at once.
   * It is not a setting to leave off: off, the confusion this exists to catch is
   * a configuration slip away.
   */
  rejectIdTokens?: boolean;
  /**
   * The applications permitted to present a token here, by `azp`/`client_id`
   * (ADR-094 invariant 2).
   *
   * THE cross-application control. An audience says a token may be accepted
   * here; it does not say who asked for it, and Zitadel issues any project's
   * audience to any client that requests it -- so a receiver checking only
   * `aud` accepts a token any application on the box could mint for it. This is
   * the check that distinguishes them.
   *
   * Absence of `azp` is a REFUSAL, not a pass. A token with no caller identity
   * cannot be matched against an allowlist, and treating "cannot tell" as
   * "allowed" is the failure this exists to prevent.
   *
   * Omitted (or empty) means no caller check, which is correct only where a
   * receiver is unreachable by any other application. Say so where you omit it.
   */
  allowedAzp?: string[];
  /**
   * The callers this receiver admits, AND what each one is called:
   * `{ "<azp>": "<applicationId>" }`.
   *
   * A MAP where `allowedAzp` is a list, and the difference is not cosmetic. A
   * list says a caller may enter; it does not say who entered. A receiver that
   * owns records per consuming application — waypoint ADR-042 partitions chat
   * sessions and runs by `consumer_app_id` — would then have to take the name
   * from the request, which is precisely what ADR-094 invariant 2b forbids.
   *
   * Only the platform can supply this. It knows both halves when it renders the
   * allowlist, and neither end can be trusted to assert it: the caller would be
   * naming itself, and the receiver has nothing to derive it from.
   *
   * Set BOTH gate and name: a token whose `azp` is absent here is refused
   * exactly as `allowedAzp` refuses, and one that passes arrives with
   * `consumer_app_id` set on its claims. Supply this or `allowedAzp`, not both;
   * if both are given this one decides, because it is the stricter statement.
   *
   * Build it from the platform's `OIDC_ALLOWED_AZP` with `parseAllowedCallers`.
   */
  allowedCallers?: Record<string, string>;
}

/**
 * Read the platform's `OIDC_ALLOWED_AZP` into a caller map.
 *
 * The value is space- or comma-separated `<clientId>=<applicationId>` entries.
 * An entry with no `=` is a caller admitted before the platform rendered names,
 * and maps to `""` — admitted, unnamed. A receiver that needs the name must
 * treat `""` as unusable rather than as a name, which is why this returns the
 * empty string rather than dropping the entry: dropping it would silently
 * REVOKE a caller during the upgrade that introduces names.
 */
export function parseAllowedCallers(value: string | undefined): Record<string, string> {
  const out: Record<string, string> = {};
  for (const entry of (value ?? "").replace(/,/g, " ").split(/\s+/)) {
    if (!entry) continue;
    const eq = entry.indexOf("=");
    if (eq === -1) {
      out[entry] = "";
      continue;
    }
    out[entry.slice(0, eq)] = entry.slice(eq + 1);
  }
  return out;
}

export class JwtValidator {
  private readonly jwksCache: JwksCache;
  private readonly issuer?: string;
  private readonly audience?: string;
  private readonly algorithms: string[];
  private readonly requireTenantId: boolean;
  private readonly rejectIdTokens: boolean;
  private readonly allowedAzp: string[];
  private readonly allowedCallers?: Record<string, string>;

  constructor(opts: JwtValidatorOptions) {
    this.issuer = opts.issuer;
    this.audience = opts.audience;
    this.algorithms = opts.algorithms ?? ["RS256", "ES256"];
    this.requireTenantId = opts.requireTenantId ?? true;
    this.rejectIdTokens = opts.rejectIdTokens ?? true;
    this.allowedAzp = opts.allowedAzp ?? [];
    this.allowedCallers = opts.allowedCallers;

    this.jwksCache = new JwksCache({
      jwksUrl: opts.jwksUrl,
      ...opts.jwksCache,
    });
  }

  /**
   * Validate a JWT and return typed tenant claims.
   * Throws on expired, invalid issuer, invalid audience, or missing required claims.
   */
  async validate(token: string): Promise<TenantClaims> {
    if (!token) {
      throw new TokenMissingError();
    }

    try {
      const key = await this.jwksCache.getSigningKey();

      const verifyOptions: jose.JWTVerifyOptions = {
        algorithms: this.algorithms as jose.JWSAlgorithm[],
      };
      if (this.issuer) {
        verifyOptions.issuer = this.issuer;
      }
      if (this.audience) {
        verifyOptions.audience = this.audience;
      }

      const { payload } = await jose.jwtVerify(token, key, verifyOptions);

      // Checked on the VERIFIED payload, and before anything is derived from it.
      //
      // On the verified payload because an unverified one would let a forged
      // claim decide, and the point is to reject a genuine token of the wrong
      // kind -- not to guess at an untrusted one. Before deriving claims because
      // a refused token must not reach tenant or role resolution at all: those
      // read an identity out of it, and an ID token has a perfectly good one.
      //
      // Presence is the whole test. The value is a hash of an access token this
      // service was never given and cannot check, so nothing is gained by
      // reading it -- an ID token is disqualified by HAVING one.
      if (this.rejectIdTokens && "at_hash" in (payload as Record<string, unknown>)) {
        throw new IdTokenPresentedError();
      }

      const claims = claimsFromPayload(payload as Record<string, unknown>);

      // Checked BEFORE the tenant requirement below, deliberately. A token from
      // an application this receiver does not admit is refused for that reason
      // and not for whatever else happens to be wrong with it -- an operator
      // told MISSING_TENANT_ID would go looking at scopes when the answer is
      // that the caller should not be here at all.
      // The map decides where one is given: it is the stricter statement, and a
      // receiver that supplied both meant the one that also names the caller.
      if (this.allowedCallers !== undefined) {
        const permitted = Object.keys(this.allowedCallers);
        if (claims.azp === undefined || !(claims.azp in this.allowedCallers)) {
          throw new CallerNotAllowedError(claims.azp, permitted);
        }
        // Set from the ALLOWLIST, never from the token. The token says which
        // client asked for it; only the platform's map says which application
        // that client belongs to, and a token cannot be allowed to assert its
        // own application (ADR-094 invariant 2b).
        //
        // Empty when the entry predates names. Left undefined rather than "" so
        // a receiver that partitions by it fails a lookup instead of writing
        // records under an empty owner that every later caller would match.
        const appId = this.allowedCallers[claims.azp];
        claims.consumer_app_id = appId === "" ? undefined : appId;
      } else if (this.allowedAzp.length > 0 && (claims.azp === undefined || !this.allowedAzp.includes(claims.azp))) {
        throw new CallerNotAllowedError(claims.azp, this.allowedAzp);
      }

      // No platform-scoped exemption, deliberately.
      //
      // One existed while the tenant was an ATTRIBUTE written onto an identity,
      // because an identity could then be created with none — so a platform
      // admin had to be excused from a rule everyone else obeyed, and the excuse
      // was a group string, which is a hole widened by anything that can claim
      // to hold it.
      //
      // Where the tenant OWNS the identity instead, a tenant-less identity is
      // not expressible: a platform admin belongs to the platform's own tenant
      // and carries it like anyone else. There is no longer a state for the
      // exemption to model, so the rule applies to everyone without one
      // (ADR-059).
      if (this.requireTenantId && !claims.tenant_id) {
        throw new AuthError({
          code: "MISSING_TENANT_ID",
          message: "Token missing required tenant_id claim",
        });
      }

      return claims;
    } catch (err) {
      if (err instanceof AuthError) throw err;

      const joseErr = err as { code?: string; message?: string };

      if (joseErr.code === "ERR_JWT_EXPIRED") {
        throw new TokenExpiredError(undefined, err);
      }
      // jose v6 reports both issuer and audience mismatches as
      // ERR_JWT_CLAIM_VALIDATION_FAILED and names the offending claim on `claim`.
      // The previous code tested "ERR_JWT_INVALIDIssuer" and
      // "ERR_JWT_INVALID_AUDIENCE" — neither is emitted — so InvalidIssuerError and
      // InvalidAudienceError were unreachable and a P0-1 audience violation looked
      // identical to any other malformed token.
      const claim = (err as { claim?: string }).claim;
      if (joseErr.code === "ERR_JWT_CLAIM_VALIDATION_FAILED") {
        // Report the claim's VALUE, not its name. Both errors take (expected,
        // actual) and were being handed `claim`, which is the string "iss" or
        // "aud" — so an audience mismatch read `expected "<...>", got ""aud""`,
        // naming the field instead of what the token actually carried. The one
        // fact needed to diagnose the mismatch was the one thing omitted.
        //
        // Decoded without verification, and only to build the message: the token
        // has already failed validation and nothing here is trusted or returned.
        const actual = decodeClaimForDiagnostics(token, claim);
        if (claim === "iss") {
          throw new InvalidIssuerError(this.issuer ?? "", describeClaim(actual));
        }
        if (claim === "aud") {
          throw new InvalidAudienceError(this.audience ?? "", describeClaim(actual));
        }
      }

      throw new AuthError({
        code: "TOKEN_INVALID",
        message: `Token validation failed: ${joseErr.message ?? String(err)}`,
        cause: err,
      });
    }
  }
}


/**
 * Reads one claim out of an unverified token, for error messages only.
 *
 * The token reaching here has already failed verification, so the value is
 * untrusted by construction and is used solely to say what was seen. Returns
 * undefined rather than throwing: a malformed token must not turn a claim
 * mismatch into a different error on the way out.
 */
function describeClaim(value: unknown): string {
  if (value === undefined) return "<absent>";
  if (typeof value === "string") return value;
  return JSON.stringify(value);
}

function decodeClaimForDiagnostics(token: string, claim: string | undefined): unknown {
  if (!claim) return undefined;
  try {
    return (jose.decodeJwt(token) as Record<string, unknown>)[claim];
  } catch {
    return undefined;
  }
}
