import { requirePlatformEnv, type EnvSource } from "./env.js";

/**
 * A token for calling ANOTHER APPLICATION, with no user in the request (ADR-097).
 *
 * WHY THE PLATFORM OWNS THIS AND NOT EACH APPLICATION
 *
 * The flow is standard OAuth 2.0 client credentials, but the two scopes it must
 * request are raw issuer vocabulary:
 *
 *     urn:zitadel:iam:org:project:id:<targetProjectId>:aud
 *     urn:zitadel:iam:user:resourceowner
 *
 * The first is what puts the target's audience on the token; without it the
 * receiver refuses a token that is otherwise valid. The second is what puts the
 * TENANT on it; without it the receiver refuses it as tenant-less, correctly. An
 * application writing these by hand gets an `invalid_target` or a tenant-less
 * rejection with nothing naming the missing scope, and every team re-derives the
 * same two strings.
 *
 * So the platform names them once, from the target application's id, and a
 * product team asks for "a token to call waypoint".
 *
 * WHY THE USER'S IDENTITY IS NOT HERE
 *
 * Because it cannot be, on the deployed issuer. Propagating a user across an
 * application boundary needs an RFC 8693 exchange, and Zitadel v4.15.3 requires
 * every requested scope to be present on BOTH the subject and actor tokens --
 * an id-token subject carries none, so every scope is refused. A token from here
 * carries the SERVICE's identity and nothing may map its subject to a person
 * (ADR-097 invariant 8). When a call genuinely needs the acting user, the honest
 * answer is that the platform cannot carry it yet.
 */

/** What the issuer returns, and the only fields this reads. */
interface TokenResponse {
  access_token?: string;
  expires_in?: number;
  token_type?: string;
  error?: string;
  error_description?: string;
}

export interface ServiceTokenOptions {
  /**
   * The application being called, as it is named in `backendDependencies` --
   * "waypoint", not a URL and not a project id. The project id is looked up from
   * the key the platform publishes for that dependency.
   */
  target: string;
  /** Configuration source. Defaults to the process environment. */
  env?: EnvSource;
  /**
   * Seconds before expiry at which a cached token is replaced. A token that
   * expires in flight fails the call it was fetched for, so this is not tuning:
   * it is the margin between "still valid here" and "still valid when it
   * arrives".
   */
  refreshMarginSeconds?: number;
  /** Injection point for tests. Defaults to global fetch. */
  fetchImpl?: typeof fetch;
}

/** The per-dependency key the platform publishes the target's project id under. */
export function backendProjectIdKey(target: string): string {
  return `OIDC_BACKEND_PROJECT_ID_${target.toUpperCase().replace(/-/g, "_")}`;
}

/** The audience scope that makes a token acceptable to the target. */
export function projectAudienceScope(projectId: string): string {
  return `urn:zitadel:iam:org:project:id:${projectId}:aud`;
}

/** The scope that puts the tenant on the token. */
export const RESOURCE_OWNER_SCOPE = "urn:zitadel:iam:user:resourceowner";

export interface ServiceTokenSource {
  /** A valid access token for the target, minting or reusing one as needed. */
  token(): Promise<string>;
}

/**
 * Build a token source for one target application.
 *
 * REFUSES TO BUILD when the platform has published no service identity. That
 * happens for an application that declares no `backendDependencies`, and the
 * failure it prevents is the worst-shaped one available: a call made with no
 * credential reaches the target, is refused, and reads as an authorization
 * problem at the far end rather than a missing declaration at this one.
 */
export function serviceTokenSource(opts: ServiceTokenOptions): ServiceTokenSource {
  const env = opts.env ?? process.env;
  const target = opts.target;
  if (!target) {
    throw new Error("serviceTokenSource: a target application id is required");
  }

  const cfg = requirePlatformEnv(
    ["issuerUrl", "serviceClientId", "serviceClientSecret"],
    `this application cannot call ${target} as itself`,
    env,
  );

  // Read directly: this key's name is DERIVED from the target, so it is not one
  // of PLATFORM_ENV's fixed entries. The derivation is the contract, and it is
  // the same one the operator publishes under and the chart mounts from.
  const projectKey = backendProjectIdKey(target);
  const targetProjectId: string | undefined = env[projectKey];
  if (!targetProjectId) {
    throw new Error(
      `${projectKey} is not set, so a token for ${target} would carry no audience ` +
        `and ${target} would refuse it.\n\n` +
        `The platform writes it when this application declares ` +
        `identity.backendDependencies: [${target}]. Until it does, this ` +
        `application has no dependency on ${target} and should not call it.`,
    );
  }

  // Re-bound after the guard: `mint` is a closure, and TypeScript does not carry
  // a narrowing across one.
  const projectId: string = targetProjectId;

  const margin = opts.refreshMarginSeconds ?? 30;
  const doFetch = opts.fetchImpl ?? fetch;
  const tokenUrl = `${cfg.issuerUrl.replace(/\/$/, "")}/oauth/v2/token`;

  // Cached across calls: a token endpoint round trip per outbound request would
  // put the issuer in the path of every cross-application call, which is the
  // cost this design exists to avoid.
  let cached: { token: string; expiresAt: number } | null = null;
  let inFlight: Promise<string> | null = null;

  async function mint(): Promise<string> {
    const body = new URLSearchParams({
      grant_type: "client_credentials",
      scope: `${projectAudienceScope(projectId)} ${RESOURCE_OWNER_SCOPE}`,
    });

    const res = await doFetch(tokenUrl, {
      method: "POST",
      headers: {
        "Content-Type": "application/x-www-form-urlencoded",
        // Client secret basic: the credential never appears in a body that a
        // proxy or an access log might keep.
        Authorization:
          "Basic " +
          Buffer.from(
            `${encodeURIComponent(cfg.serviceClientId)}:${encodeURIComponent(cfg.serviceClientSecret)}`,
          ).toString("base64"),
      },
      body,
    });

    const json = (await res.json().catch(() => ({}))) as TokenResponse;
    if (!res.ok || !json.access_token) {
      // The issuer's own words, kept. `invalid_scope` here names the scope it
      // refused, which is the only thing that distinguishes a missing audience
      // from a missing tenant claim.
      const detail = json.error
        ? `${json.error}${json.error_description ? `: ${json.error_description}` : ""}`
        : `HTTP ${res.status}`;
      throw new Error(`service token for ${target} refused by the issuer (${detail})`);
    }

    const ttl = typeof json.expires_in === "number" ? json.expires_in : 0;
    cached = {
      token: json.access_token,
      // A response without expires_in is treated as expiring immediately rather
      // than as never expiring: reusing a token of unknown lifetime is how a
      // caller starts sending expired credentials and blames the receiver.
      expiresAt: ttl > 0 ? Date.now() + (ttl - margin) * 1000 : 0,
    };
    return json.access_token;
  }

  return {
    async token(): Promise<string> {
      if (cached && Date.now() < cached.expiresAt) {
        return cached.token;
      }
      // One mint at a time. Without this, a burst of concurrent calls on a cold
      // cache each open their own token request, and the issuer sees a
      // thundering herd from every replica at once.
      if (!inFlight) {
        inFlight = mint().finally(() => {
          inFlight = null;
        });
      }
      return inFlight;
    },
  };
}
