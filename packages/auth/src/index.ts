// ─── Types ─────────────────────────────────────────────────────────
export type { AuthenticatedPrincipal, TokenSet, Resource, Operation, AuthorizationDecision } from "./authz/types.js";

export {
  AuthError,
  TokenExpiredError,
  InvalidIssuerError,
  InvalidAudienceError,
  InsufficientScopeError,
  TenantMismatchError,
  RoleDeniedError,
  TokenMissingError,
  IdTokenPresentedError,
  CallerNotAllowedError,
  JwksFetchError,
  KeyNotFoundError,
} from "./types.js";

// ─── JWT Validation ────────────────────────────────────────────────
export type { TenantClaims } from "./jwt/claims.js";
export { claimsFromPayload } from "./jwt/claims.js";

export type { JwksCacheOptions, JwksFetchFailure, JwksStatus } from "./jwt/jwks-cache.js";
export { JwksCache, fullJitterDelay } from "./jwt/jwks-cache.js";

export type { JwtValidatorOptions } from "./jwt/validator.js";
export { JwtValidator, parseAllowedCallers } from "./jwt/validator.js";

// ─── Hono Middleware ───────────────────────────────────────────────
export { authMiddleware, getPrincipal, PRINCIPAL_KEY } from "./middleware/hono.js";
export type { AuthMiddlewareOptions } from "./middleware/hono.js";

// The acting user and the identity endpoint, for an app's own server (ADR-057).
export {
  actingUserMiddleware,
  getActingUser,
  requireActingUser,
  identityHandler,
  ACTING_USER_KEY,
} from "./middleware/acting-user.js";
export type { ActingUser, ActingUserOptions } from "./middleware/acting-user.js";

// ─── OAuth ─────────────────────────────────────────────────────────
export type { OidcClientOptions } from "./oauth/client.js";
export { OidcClient } from "./oauth/client.js";

export { generateOAuthState, hashState } from "./oauth/state.js";

export type { TokenStore, PendingOAuthFlow, RedisTokenStoreOptions } from "./oauth/token-store.js";
export { RedisTokenStore } from "./oauth/token-store.js";

// ─── Authorization ─────────────────────────────────────────────────
export { requireTenantBoundary } from "./authz/tenant-boundary.js";
export type { TenantBoundaryOptions } from "./authz/tenant-boundary.js";

export { requireRole } from "./authz/role.js";

// Tenant-local user resolution (platform baseline `users` / `identities`).
export type {
  SqlExecutor,
  SqlTransactor,
  ResolvedUser,
  ResolveUserOptions,
} from "./user/types.js";
export { resolveUser, withUserContext } from "./user/resolve.js";

// ─── Platform surfaces ─────────────────────────────────────────────
//
// What an application should reach for FIRST. The exports above are the
// primitives this platform is built from; these are the shapes it actually has,
// with the security-relevant switches already decided. An application that
// configures a JwtValidator by hand has taken on four decisions whose wrong
// settings fail by accepting a token rather than by erroring.
export {
  PLATFORM_ENV,
  PLATFORM_LOGIN_SCOPES,
  PlatformConfigError,
  requirePlatformEnv,
  platformEnv,
} from "./platform/env.js";
export type { EnvSource } from "./platform/env.js";

export { browserSessionValidator, consumerApiValidator } from "./platform/validators.js";
export type { SurfaceValidatorOptions } from "./platform/validators.js";

export {
  internalChannelHeaders,
  internalCallHeaders,
  requireInternalCaller,
  forwardedPrincipal,
  InternalTokenMissingError,
} from "./platform/internal-channel.js";
export type { InternalChannelHeaders } from "./platform/internal-channel.js";

// Calling another application as THIS APPLICATION, with no user in the request
// (ADR-097). The scopes an issuer needs for that are raw vocabulary; naming them
// here means a product team never writes them.
export {
  serviceTokenSource,
  backendProjectIdKey,
  projectAudienceScope,
  RESOURCE_OWNER_SCOPE,
} from "./platform/service-token.js";
export type { ServiceTokenOptions, ServiceTokenSource } from "./platform/service-token.js";
