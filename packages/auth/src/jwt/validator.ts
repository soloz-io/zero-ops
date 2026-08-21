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
}

export class JwtValidator {
  private readonly jwksCache: JwksCache;
  private readonly issuer?: string;
  private readonly audience?: string;
  private readonly algorithms: string[];

  constructor(opts: JwtValidatorOptions) {
    this.issuer = opts.issuer;
    this.audience = opts.audience;
    this.algorithms = opts.algorithms ?? ["RS256", "ES256"];

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

      if (!claims.tenant_id) {
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
        if (claim === "iss") {
          throw new InvalidIssuerError(this.issuer ?? "", claim);
        }
        if (claim === "aud") {
          throw new InvalidAudienceError(this.audience ?? "", claim);
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
