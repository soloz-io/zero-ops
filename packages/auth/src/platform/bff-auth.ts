import type { Context, Handler, MiddlewareHandler } from "hono";
import type { AuthenticatedPrincipal } from "../authz/types.js";
import type { JwtValidator } from "../jwt/validator.js";
import { authMiddleware, PRINCIPAL_KEY } from "../middleware/hono.js";
import type { EnvSource } from "./env.js";
import { browserSessionValidator } from "./validators.js";

/**
 * A BFF's authentication, set up once: the validator and its key lifecycle,
 * readiness, the middleware, and the local-stack stand-in.
 *
 * Every BFF wrote this by hand, and each copy got a different part of it wrong:
 * one never started the validator, so its first request after every restart
 * waited on a key download; another started it but hooked SIGTERM to do so,
 * which stops Node exiting on the signal. Here the rules live in one place:
 *
 *   - ONE validator for the process, started at construction (ADR-022). Its
 *     refresh timers are unref'd, so nothing here holds the process open or
 *     needs a signal handler; pass `signal` only to stop it early.
 *   - READINESS is `readiness` (or `ready()`): 503 until the issuer's keys are
 *     held. Serve it on the path the readiness probe uses -- not the liveness
 *     path, which must never depend on the issuer.
 *   - LOCAL MODE is `<APPID>_ENV=local`, e.g. ORANGER_ENV=local. There is no
 *     platform sign-in on a local stack, so every request under the mount gets
 *     a stand-in principal and no token is checked. It is REFUSED, at
 *     construction, when NODE_ENV is production: a deployed BFF that believes it
 *     is local would admit everyone, so the process does not start.
 */
export interface BffAuthOptions {
  /** This application's id. Selects the local-mode switch, `<APPID>_ENV`. */
  appId: string;
  /** The validator to use. Defaults to `browserSessionValidator({ env })`, which is right for a BFF behind its gateway. */
  validator?: JwtValidator;
  /** Configuration source. Defaults to the process environment. */
  env?: EnvSource;
  /**
   * Paths under the mount that need no session, as prefixes matched on segment
   * boundaries: "/api/v1/auth" matches /api/v1/auth and /api/v1/auth/callback,
   * not /api/v1/authors. For routes that run before a session exists (a login
   * start, an identity probe). In local mode they get the stand-in like any other.
   */
  publicPaths?: string[];
  /**
   * The local stand-in principal. A fixed principal, or a function of the
   * request (for a stack that wants to impersonate several users). Defaults to
   * one fixed user: `local-user`, tenant from LOCAL_TENANT_ID, no roles.
   * Never used outside local mode.
   */
  localPrincipal?: AuthenticatedPrincipal | ((c: Context) => AuthenticatedPrincipal);
  /** Stops the validator's key refresh when aborted. Optional: nothing needs stopping for the process to exit. */
  signal?: AbortSignal;
  /** Where a JWKS failure's detail goes. Passed to authMiddleware. */
  onDependencyError?: (err: unknown) => void;
}

export interface BffAuth {
  /** True on a local stack: no token is checked, every request is the stand-in. */
  readonly local: boolean;
  /** The validator, outside local mode. */
  readonly validator?: JwtValidator;
  /** Mount on the API, e.g. `app.use("/api/*", auth.middleware)`. */
  readonly middleware: MiddlewareHandler;
  /** The readiness handler: 200 once the keys are held (always, locally), 503 before. */
  readonly readiness: Handler;
  /** The readiness fact, for an app that combines it with readiness of its own. */
  ready(): boolean;
  /** Stop the key refresh. Idempotent. */
  stop(): void;
}

export function bffAuth(opts: BffAuthOptions): BffAuth {
  const env = opts.env ?? process.env;
  if (!/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(opts.appId)) {
    throw new Error(`bffAuth: appId ${JSON.stringify(opts.appId)} is not an application id`);
  }
  const switchName = `${opts.appId.toUpperCase().replace(/-/g, "_")}_ENV`;
  const local = env[switchName] === "local";
  if (local && env.NODE_ENV === "production") {
    throw new Error(
      `bffAuth: ${switchName}=local with NODE_ENV=production. Local mode admits every request as a ` +
        `stand-in user, so it is refused in production; unset ${switchName} on a deployed BFF.`,
    );
  }

  const publicPaths = (opts.publicPaths ?? []).map((p) => p.replace(/\/+$/, ""));
  const isPublic = (path: string) => publicPaths.some((p) => path === p || path.startsWith(`${p}/`));

  if (local) {
    const fallback: AuthenticatedPrincipal = {
      subject: "local-user",
      email: `local-user@${opts.appId}.local`,
      tenantId: env.LOCAL_TENANT_ID ?? "",
      roles: [],
      issuer: "local",
    };
    const principalFor = (c: Context): AuthenticatedPrincipal => {
      const p = opts.localPrincipal ?? fallback;
      return typeof p === "function" ? p(c) : p;
    };
    console.warn(`${switchName}=local: every request is a local stand-in user; no session is validated`);
    return {
      local: true,
      validator: undefined,
      middleware: async (c, next) => {
        c.set(PRINCIPAL_KEY as never, principalFor(c) as never);
        await next();
      },
      readiness: (c) => c.json({ status: "ok", auth: "local" }),
      ready: () => true,
      stop: () => {},
    };
  }

  const validator = opts.validator ?? browserSessionValidator({ env });
  validator.start({ signal: opts.signal });
  const guard = authMiddleware({ validator, onDependencyError: opts.onDependencyError });

  return {
    local: false,
    validator,
    middleware: async (c, next) => (isPublic(c.req.path) ? next() : guard(c, next)),
    readiness: (c) =>
      validator.status().ready
        ? c.json({ status: "ok" })
        : c.json({ status: "auth-keys-unavailable" }, 503),
    ready: () => validator.status().ready,
    stop: () => validator.stop(),
  };
}
