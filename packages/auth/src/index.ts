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
  JwksFetchError,
  KeyNotFoundError,
} from "./types.js";

// ─── JWT Validation ────────────────────────────────────────────────
export type { TenantClaims } from "./jwt/claims.js";
export { claimsFromPayload } from "./jwt/claims.js";

export type { JwksCacheOptions } from "./jwt/jwks-cache.js";
export { JwksCache } from "./jwt/jwks-cache.js";

export type { JwtValidatorOptions } from "./jwt/validator.js";
export { JwtValidator } from "./jwt/validator.js";

// ─── Hono Middleware ───────────────────────────────────────────────
export { authMiddleware, getPrincipal, PRINCIPAL_KEY } from "./middleware/hono.js";
export type { AuthMiddlewareOptions } from "./middleware/hono.js";

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
