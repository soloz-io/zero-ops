import { JwtValidator, parseAllowedCallers } from "../jwt/validator.js";
import { PLATFORM_ENV, requirePlatformEnv, platformEnv, type EnvSource } from "./env.js";

/**
 * Validators configured for the surfaces this platform actually has.
 *
 * WHY FACTORIES AND NOT DOCUMENTATION. `JwtValidator` has four security-relevant
 * switches -- `audience`, `allowedAzp`/`allowedCallers`, `rejectIdTokens`,
 * `requireTenantId` -- and the correct setting of each depends on facts about
 * Zitadel and about this platform's gateway that live nowhere near an
 * application's repository. Asking every product team to get four switches right
 * from an ADR means every team re-derives the same reasoning, and one of them
 * gets it wrong quietly: the failure mode of a wrong switch is a token accepted,
 * not an error.
 *
 * It has already happened once. The caller allowlist was rendered with the
 * PUBLIC browser client id, which would have admitted any token from the calling
 * application's own login. Nothing failed, because admitting too much never
 * fails.
 *
 * So the platform names the surfaces and owns the switches. A product team picks
 * the surface it is building and gets a validator that is right for it.
 */

export interface SurfaceValidatorOptions {
  /** Configuration source. Defaults to the process environment. */
  env?: EnvSource;
  /** JWKS cache tuning, when an application has a reason to differ. */
  jwksCache?: Partial<import("../jwt/jwks-cache.js").JwksCacheOptions>;
}

/**
 * For a BFF sitting behind this tenant's gateway, validating the session
 * credential the gateway forwards.
 *
 * REFUSES AN ID TOKEN, like every other surface. There is no browser exception
 * and there is no switch to turn one on.
 *
 * There was one until 2026-09-30, on the belief that Zitadel's exchange could not
 * serve a browser session. It can: an ID-token subject is validated by
 * `validateImpersonationTokenExchangeScopes`, which checks subject-data scopes
 * against the client's allowlist rather than against the subject token, so the
 * minted token carries the tenant claim. Every tenant gateway now mints an access
 * token and forwards that, so a BFF receiving an ID token is not a supported
 * arrangement -- it is a gateway that is not doing its job, and accepting it
 * would hide that.
 *
 * Two controls, not one, and both are fixed here. `azp` must be this
 * application's own gateway exchange client, so a sibling application's token is
 * refused even though it carries this project's audience by construction --
 * Zitadel issues any project's audience to any client that asks, so the audience
 * has never been the control.
 */
export function browserSessionValidator(opts: SurfaceValidatorOptions = {}): JwtValidator {
  const env = opts.env ?? process.env;
  const cfg = requirePlatformEnv(
    ["issuerUrl", "jwksUrl", "projectId", "exchangeClientId", "orgId"],
    "a BFF cannot validate the token its gateway mints",
    env,
  );

  return new JwtValidator({
    jwksUrl: cfg.jwksUrl,
    issuer: cfg.issuerUrl,
    // The PROJECT, not this application's client id.
    //
    // A minted token is audienced to projects -- this application's own, plus
    // every application it calls -- because that is what an RFC 8693 `audience`
    // parameter names. Checking a client id here refuses every request, and the
    // error reads as an audience mismatch against a value that looks plausible.
    audience: cfg.projectId,
    // The EXCHANGE client, not the browser client.
    //
    // `azp` names the client that AUTHENTICATED the exchange, which is the
    // confidential exchange client -- never the public PKCE client the browser
    // logged in with. The two are different identities, and admitting the public
    // one would admit any token obtained through this application's own login.
    allowedAzp: [cfg.exchangeClientId],
    // Default, stated. An ID token reaching here means the gateway forwarded a
    // session credential instead of minting one, and that must fail loudly.
    rejectIdTokens: true,
    requireTenantId: true,
    // COMPARED, not merely required. A sibling tenant's user carries a valid
    // tenant claim -- a different one -- and presence alone accepts it.
    expectedTenantId: cfg.orgId,
    jwksCache: opts.jwksCache,
  });
}

/**
 * For a surface another APPLICATION calls (waypoint ADR-042).
 *
 * ACCESS TOKENS ONLY. The browser-session exception above is scoped to that hop
 * and must not travel: two meanings for "authenticated principal" on one box
 * means a receiver has to know which kind of caller it faces before it knows
 * which rules apply, and that is the confusion ADR-094 invariant 1 exists to
 * remove.
 *
 * Uses the caller MAP, so the receiver can name its caller. A list would admit
 * without naming, and a receiver that partitions records per consuming
 * application would then have to take the name from the request -- exactly what
 * invariant 2b forbids. `claims.consumer_app_id` is set from the map, never from
 * the token.
 *
 * Refuses to build when the platform has published no callers. An empty
 * allowlist on this surface is not "admit nobody", it is "check nothing", and a
 * cross-application surface with no caller check is open to every application on
 * the box.
 */
export function consumerApiValidator(opts: SurfaceValidatorOptions = {}): JwtValidator {
  const env = opts.env ?? process.env;
  const cfg = requirePlatformEnv(
    ["issuerUrl", "jwksUrl", "projectId", "orgId"],
    "a cross-application surface cannot validate its callers",
    env,
  );

  const raw = platformEnv("allowedCallers", env);
  const allowedCallers = parseAllowedCallers(raw);
  if (Object.keys(allowedCallers).length === 0) {
    throw new Error(
      `${PLATFORM_ENV.allowedCallers} is empty, so this cross-application surface would ` +
        `admit every application on this box.\n\n` +
        `The platform writes it when another application declares ` +
        `backendDependencies naming this one. Until one does, this surface has no ` +
        `callers and should not be mounted.`,
    );
  }

  return new JwtValidator({
    jwksUrl: cfg.jwksUrl,
    issuer: cfg.issuerUrl,
    // The PROJECT, not the client: an exchanged token is audienced to the target
    // application's project, which is what the caller requested a scope for.
    audience: cfg.projectId,
    allowedCallers,
    rejectIdTokens: true,
    requireTenantId: true,
    // COMPARED, not merely required. A sibling tenant's user carries a valid
    // tenant claim -- a different one -- and presence alone accepts it.
    expectedTenantId: cfg.orgId,
    jwksCache: opts.jwksCache,
  });
}
