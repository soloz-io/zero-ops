export interface AuthenticatedPrincipal {
  subject: string;
  email: string;
  tenantId: string;
  tenantTier?: string;
  roles: string[];
  issuer?: string;
  audience?: string;
}

export interface TokenSet {
  accessToken: string;
  refreshToken?: string | null;
  expiresAt: number;
  scope?: string;
  tokenType?: string;
}

export interface AuthorizationDecision {
  allowed: boolean;
  reason?: string;
}

export interface Resource {
  tenantId: string;
  id?: string;
  type?: string;
}

export interface Operation {
  method: string;
  path: string;
  resource?: Resource;
}
