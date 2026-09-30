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
 * ACCEPTS AN ID TOKEN, because that is what the gateway forwards. The caller
 * allowlist below is what makes that safe.
 *
 * This surface briefly refused them, on the belief that the gateway could mint an
 * access token instead. It cannot on the deployed issuer, and that was found by
 * RUNNING it rather than reading it. Zitadel v4.15.3 validates exchange scopes
 * with
 *
 *     !contains(subjectScopes, scope) || !contains(actorScopes, scope)
 *
 * -- the scope must be on BOTH input tokens -- and an id-token subject carries
 * none. So every requested scope is refused:
 *
 *     {"error":"invalid_scope","error_description":"scope
 *      \"urn:zitadel:iam:user:resourceowner\" not found in subject or actor token"}
 *
 * and requesting no scopes yields a token with no tenant claim, which a receiver
 * refuses as tenant-less. No RELEASED Zitadel validates it otherwise: the union
 * check that would is in no tag, and there is no published v5 image.
 *
 * `azp` must be this application's own gateway client, so a sibling
 * application's token is refused even though it carries this project's audience
 * by construction -- Zitadel issues any project's audience to any client that
 * asks, so the audience has never been the control.
 *
 * SERVICE-TO-SERVICE IS UNAFFECTED. Client credentials has no subject token, so
 * there is no union check to fail; that path is `consumerApiValidator` and it is
 * not waiting on anything (ADR-097).
 */
export function browserSessionValidator(opts: SurfaceValidatorOptions = {}): JwtValidator {
  const env = opts.env ?? process.env;
  const cfg = requirePlatformEnv(
    ["issuerUrl", "jwksUrl", "clientId", "orgId"],
    "a BFF cannot validate the session its gateway forwards",
    env,
  );

  return new JwtValidator({
    jwksUrl: cfg.jwksUrl,
    issuer: cfg.issuerUrl,
    // This application's own client: the audience the gateway requested at login,
    // and the `azp` on the id token it forwards.
    audience: cfg.clientId,
    allowedAzp: [cfg.clientId],
    // See the note above. Set by the platform WITH its reason, so it is not a
    // line an application flips to make an error go away.
    rejectIdTokens: false,
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
