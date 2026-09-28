import * as jose from "jose";
import { JwksCache } from "./jwks-cache.js";
import { type TenantClaims, claimsFromPayload } from "./claims.js";
import {
  TokenExpiredError,
  InvalidIssuerError,
  InvalidAudienceError,
  TokenMissingError,
  IdTokenPresentedError,
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
   * `at_hash` binds an ID token to the access token issued beside it, so only an
   * ID token has one -- an access token has nothing to bind. Refusing it is how
   * this validator enforces ADR-095's invariant that APIs accept access tokens
   * and never ID tokens.
   *
   * It is a claim check rather than an audience check because in Zitadel the two
   * tokens share an audience: `createIDToken` and `createJWT` are both handed
   * `session.Audience`, so no audience distinguishes them. `at_hash` is set in
   * exactly one place in that codebase, inside `createIDToken`.
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
}

export class JwtValidator {
  private readonly jwksCache: JwksCache;
  private readonly issuer?: string;
  private readonly audience?: string;
  private readonly algorithms: string[];
  private readonly requireTenantId: boolean;
  private readonly rejectIdTokens: boolean;

  constructor(opts: JwtValidatorOptions) {
    this.issuer = opts.issuer;
    this.audience = opts.audience;
    this.algorithms = opts.algorithms ?? ["RS256", "ES256"];
    this.requireTenantId = opts.requireTenantId ?? true;
    this.rejectIdTokens = opts.rejectIdTokens ?? true;

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
