export interface AuthErrorOptions {
  code: string;
  message: string;
  cause?: unknown;
}

export class AuthError extends Error {
  readonly code: string;

  constructor(opts: AuthErrorOptions) {
    super(opts.message, { cause: opts.cause });
    this.name = "AuthError";
    this.code = opts.code;
  }
}

export class TokenExpiredError extends AuthError {
  constructor(message = "Token has expired", cause?: unknown) {
    super({ code: "TOKEN_EXPIRED", message, cause });
    this.name = "TokenExpiredError";
  }
}

export class InvalidIssuerError extends AuthError {
  constructor(expected: string, actual: string) {
    super({ code: "INVALID_ISSUER", message: `Invalid issuer: expected "${expected}", got "${actual}"` });
    this.name = "InvalidIssuerError";
  }
}

export class InvalidAudienceError extends AuthError {
  constructor(expected: string, actual: string | string[]) {
    super({ code: "INVALID_AUDIENCE", message: `Invalid audience: expected "${expected}", got "${JSON.stringify(actual)}"` });
    this.name = "InvalidAudienceError";
  }
}

export class InsufficientScopeError extends AuthError {
  constructor(required: string[], actual: string[]) {
    super({
      code: "INSUFFICIENT_SCOPE",
      message: `Insufficient scope: required [${required.join(", ")}], got [${actual.join(", ")}]`,
    });
    this.name = "InsufficientScopeError";
  }
}

export class TenantMismatchError extends AuthError {
  constructor(expected: string, actual: string) {
    super({ code: "TENANT_MISMATCH", message: `Tenant mismatch: expected "${expected}", got "${actual}"` });
    this.name = "TenantMismatchError";
  }
}

export class RoleDeniedError extends AuthError {
  constructor(required: string[]) {
    super({ code: "ROLE_DENIED", message: `Role denied: required one of [${required.join(", ")}]` });
    this.name = "RoleDeniedError";
  }
}

export class TokenMissingError extends AuthError {
  constructor(message = "No token provided") {
    super({ code: "TOKEN_MISSING", message });
    this.name = "TokenMissingError";
  }
}

export class JwksFetchError extends AuthError {
  constructor(url: string, cause?: unknown) {
    super({ code: "JWKS_FETCH_ERROR", message: `Failed to fetch JWKS from ${url}`, cause });
    this.name = "JwksFetchError";
  }
}

export class KeyNotFoundError extends AuthError {
  constructor(kid: string) {
    super({ code: "KEY_NOT_FOUND", message: `Key not found in JWKS: ${kid}` });
    this.name = "KeyNotFoundError";
  }
}
