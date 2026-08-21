import { randomBytes, createHash } from "node:crypto";
import type { TokenSet } from "../authz/types.js";

export interface OidcClientOptions {
  /** OIDC issuer URL (e.g., "https://auth.dev.nutgraf.in") */
  issuerUrl: string;
  /** OAuth client ID */
  clientId: string;
  /** OAuth client secret (required for confidential clients) */
  clientSecret?: string;
  /** Redirect URI after authorization */
  redirectUri: string;
  /** Scopes to request */
  scopes: string[];
  /** Expected audience for the access token */
  audience?: string;
  /** HTTP fetch timeout in milliseconds (default: 10000) */
  fetchTimeoutMs?: number;
}

interface ProviderDiscovery {
  authorization_endpoint: string;
  token_endpoint: string;
  revocation_endpoint?: string;
}

interface TokenResponse {
  access_token: string;
  refresh_token?: string;
  expires_in?: number;
  scope?: string;
  token_type?: string;
  error?: string;
  error_description?: string;
}

export class OidcClient {
  private readonly opts: OidcClientOptions;
  private discoveryCache: ProviderDiscovery | null = null;
  private discoveryPromise: Promise<ProviderDiscovery> | null = null;

  constructor(opts: OidcClientOptions) {
    this.opts = opts;
  }

  private async discover(): Promise<ProviderDiscovery> {
    if (this.discoveryCache) return this.discoveryCache;
    if (this.discoveryPromise) return this.discoveryPromise;

    this.discoveryPromise = this.doDiscover();
    try {
      const result = await this.discoveryPromise;
      return result;
    } finally {
      this.discoveryPromise = null;
    }
  }

  private async doDiscover(): Promise<ProviderDiscovery> {
    const url = `${this.opts.issuerUrl}/.well-known/openid-configuration`;
    const res = await fetch(url, {
      signal: AbortSignal.timeout(this.opts.fetchTimeoutMs ?? 10_000),
    });
    if (!res.ok) {
      throw new Error(`OIDC discovery failed: HTTP ${res.status}`);
    }
    const doc = (await res.json()) as ProviderDiscovery;
    this.discoveryCache = doc;
    return doc;
  }

  private authHeaders(): Headers {
    const headers = new Headers();
    headers.set("content-type", "application/x-www-form-urlencoded");

    if (this.opts.clientSecret) {
      const credentials = Buffer.from(
        `${encodeURIComponent(this.opts.clientId)}:${encodeURIComponent(this.opts.clientSecret)}`,
      ).toString("base64");
      headers.set("authorization", `Basic ${credentials}`);
    }

    return headers;
  }

  private async exchangeForTokens(body: Record<string, string>): Promise<TokenSet> {
    const provider = await this.discover();
    const form = new URLSearchParams();
    for (const [key, value] of Object.entries(body)) {
      form.set(key, value);
    }

    const res = await fetch(provider.token_endpoint, {
      method: "POST",
      headers: this.authHeaders(),
      body: form,
      signal: AbortSignal.timeout(this.opts.fetchTimeoutMs ?? 10_000),
    });

    const data = (await res.json().catch(() => ({}))) as TokenResponse;

    if (!res.ok || !data.access_token) {
      throw new Error(
        `Token exchange failed: HTTP ${res.status} ${data.error ?? ""} ${data.error_description ?? ""}`.trim(),
      );
    }

    return {
      accessToken: data.access_token,
      refreshToken: data.refresh_token ?? null,
      expiresAt: Date.now() + (data.expires_in ?? 3600) * 1000,
      scope: data.scope,
      tokenType: data.token_type,
    };
  }

  /**
   * Build an authorization URL with optional PKCE support.
   * Returns the URL, state, and code verifier (if PKCE was used).
   */
  async buildAuthorizationUrl(
    opts?: {
      state?: string;
      codeChallenge?: string;
      codeChallengeMethod?: "S256";
      /**
       * The verifier matching `codeChallenge`. Passed through to the return value so
       * a caller can hand both to exchangeCode() without threading it separately —
       * the previous signature promised a codeVerifier it could never produce.
       */
      codeVerifier?: string;
      additionalParams?: Record<string, string>;
    },
  ): Promise<{ url: string; state: string; codeVerifier?: string }> {
    const provider = await this.discover();
    const state = opts?.state ?? randomBytes(32).toString("hex");

    const params = new URLSearchParams({
      client_id: this.opts.clientId,
      response_type: "code",
      redirect_uri: this.opts.redirectUri,
      scope: this.opts.scopes.join(" "),
      state,
    });

    if (this.opts.audience) {
      params.set("audience", this.opts.audience);
    }

    if (opts?.codeChallenge) {
      params.set("code_challenge", opts.codeChallenge);
      params.set("code_challenge_method", opts.codeChallengeMethod ?? "S256");
    }

    if (opts?.additionalParams) {
      for (const [key, value] of Object.entries(opts.additionalParams)) {
        params.set(key, value);
      }
    }

    return {
      url: `${provider.authorization_endpoint}?${params.toString()}`,
      state,
      // Was a dead ternary that always yielded undefined, so callers had to retain
      // the verifier from generatePkce() themselves or silently break PKCE.
      codeVerifier: opts?.codeVerifier,
    };
  }

  /**
   * Generate PKCE code verifier and challenge.
   */
  static generatePkce(): { codeVerifier: string; codeChallenge: string } {
    const codeVerifier = randomBytes(32)
      .toString("base64url")
      .replace(/[^a-zA-Z0-9]/g, "")
      .slice(0, 128);
    const codeChallenge = createHash("sha256").update(codeVerifier).digest("base64url");
    return { codeVerifier, codeChallenge };
  }

  /**
   * Exchange an authorization code for tokens.
   */
  async exchangeCode(opts: {
    code: string;
    codeVerifier?: string;
  }): Promise<TokenSet> {
    const body: Record<string, string> = {
      grant_type: "authorization_code",
      code: opts.code,
      redirect_uri: this.opts.redirectUri,
    };

    if (opts.codeVerifier) {
      body.code_verifier = opts.codeVerifier;
    }

    return this.exchangeForTokens(body);
  }

  /**
   * Refresh an access token using a refresh token.
   */
  async refreshTokens(refreshToken: string): Promise<TokenSet> {
    return this.exchangeForTokens({
      grant_type: "refresh_token",
      refresh_token: refreshToken,
    });
  }

  /**
   * Revoke a token (access or refresh).
   */
  async revokeToken(token: string): Promise<void> {
    const provider = await this.discover();
    const endpoint = provider.revocation_endpoint;

    if (!endpoint) {
      throw new Error("OIDC provider does not support token revocation");
    }

    const res = await fetch(endpoint, {
      method: "POST",
      headers: this.authHeaders(),
      body: new URLSearchParams({
        token,
        client_id: this.opts.clientId,
      }),
      signal: AbortSignal.timeout(this.opts.fetchTimeoutMs ?? 10_000),
    });

    if (!res.ok && res.status !== 200) {
      throw new Error(`Token revocation failed: HTTP ${res.status}`);
    }
  }
}
