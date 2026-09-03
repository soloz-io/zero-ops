import * as jose from "jose";
import { JwksCache } from "./jwks-cache.js";
import { type TenantClaims, claimsFromPayload } from "./claims.js";
import {
  TokenExpiredError,
  InvalidIssuerError,
  InvalidAudienceError,
  TokenMissingError,
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
}

export class JwtValidator {
  private readonly jwksCache: JwksCache;
  private readonly issuer?: string;
  private readonly audience?: string;
  private readonly algorithms: string[];
  private readonly requireTenantId: boolean;

  constructor(opts: JwtValidatorOptions) {
    this.issuer = opts.issuer;
    this.audience = opts.audience;
    this.algorithms = opts.algorithms ?? ["RS256", "ES256"];
    this.requireTenantId = opts.requireTenantId ?? true;

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
      const claims = claimsFromPayload(payload as Record<string, unknown>);

      // No platform-scoped exemption. It existed because Ory could mint an
      // identity belonging to no tenant, so a platform admin had to be excused
      // from a rule everyone else obeyed — an exemption is a hole, and this one
      // was widened by a group string any token could claim to hold.
      //
      // The provider closed it. In Zitadel an Organization OWNS the user rather
      // than describing it, so a tenant-less identity is not expressible and a
      // platform admin is simply a member of the platform's own organisation,
      // carrying that organisation as its tenant. There is no longer a state for
      // the exemption to model, so the rule applies to everyone without one.
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
