// ─── Types ─────────────────────────────────────────────────────────

export { requireRole } from "./authz/role.js";
export type { TenantBoundaryOptions } from "./authz/tenant-boundary.js";
// ─── Authorization ─────────────────────────────────────────────────
export { requireTenantBoundary } from "./authz/tenant-boundary.js";
export type {
  AuthenticatedPrincipal,
  AuthorizationDecision,
  Operation,
  Resource,
  TokenSet,
} from "./authz/types.js";
// ─── JWT Validation ────────────────────────────────────────────────
export type { TenantClaims } from "./jwt/claims.js";
export { claimsFromPayload } from "./jwt/claims.js";
export type { JwksCacheOptions, JwksFetchFailure, JwksStatus } from "./jwt/jwks-cache.js";
export { fullJitterDelay, JwksCache } from "./jwt/jwks-cache.js";
export type { JwtValidatorOptions } from "./jwt/validator.js";
export {
  type AllowedCaller,
  type CallerKind,
  JwtValidator,
  parseAllowedCallers,
} from "./jwt/validator.js";
export type { ActingUser, ActingUserOptions } from "./middleware/acting-user.js";
// The acting user and the identity endpoint, for an app's own server (ADR-057).
export {
  ACTING_USER_KEY,
  actingUserMiddleware,
  getActingUser,
  identityHandler,
  requireActingUser,
} from "./middleware/acting-user.js";
export type { AuthMiddlewareOptions } from "./middleware/hono.js";
// ─── Hono Middleware ───────────────────────────────────────────────
export { authMiddleware, getPrincipal, PRINCIPAL_KEY } from "./middleware/hono.js";
// ─── OAuth ─────────────────────────────────────────────────────────
export type { OidcClientOptions } from "./oauth/client.js";
export { OidcClient } from "./oauth/client.js";
export { generateOAuthState, hashState } from "./oauth/state.js";
export type { PendingOAuthFlow, RedisTokenStoreOptions, TokenStore } from "./oauth/token-store.js";
export { RedisTokenStore } from "./oauth/token-store.js";
export type { BffAuth, BffAuthOptions } from "./platform/bff-auth.js";
export { bffAuth } from "./platform/bff-auth.js";
export type { EnvSource } from "./platform/env.js";

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
  platformEnv,
  requirePlatformEnv,
} from "./platform/env.js";
export type { InternalChannelHeaders } from "./platform/internal-channel.js";
export {
  forwardedPrincipal,
  InternalTokenMissingError,
  internalCallHeaders,
  internalChannelHeaders,
  requireInternalCaller,
} from "./platform/internal-channel.js";
export type { ServiceTokenOptions, ServiceTokenSource } from "./platform/service-token.js";
// Calling another application as THIS APPLICATION, with no user in the request
// (ADR-097). The scopes an issuer needs for that are raw vocabulary; naming them
// here means a product team never writes them.
export {
  backendProjectIdKey,
  projectAudienceScope,
  RESOURCE_OWNER_SCOPE,
  serviceTokenSource,
} from "./platform/service-token.js";
export type { SurfaceValidatorOptions } from "./platform/validators.js";
export { browserSessionValidator, consumerApiValidator } from "./platform/validators.js";
export {
  AuthError,
  CallerNotAllowedError,
  IdTokenPresentedError,
  InsufficientScopeError,
  InvalidAudienceError,
  InvalidIssuerError,
  JwksFetchError,
  KeyNotFoundError,
  RoleDeniedError,
  TenantMismatchError,
  TokenExpiredError,
  TokenMissingError,
} from "./types.js";
export { resolveUser, withUserContext } from "./user/resolve.js";
// Tenant-local user resolution (platform baseline `users` / `identities`).
export type {
  ResolvedUser,
  ResolveUserOptions,
  SqlExecutor,
  SqlTransactor,
} from "./user/types.js";
