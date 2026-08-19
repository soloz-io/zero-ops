import * as jose from "jose";
import { JwksFetchError, KeyNotFoundError } from "../types.js";

export interface JwksCacheOptions {
  /** JWKS endpoint URL */
  jwksUrl: string;
  /** Per-key TTL in milliseconds (default: 300000 = 5 minutes) */
  keyTtlMs?: number;
  /** Minimum interval between JWKS refreshes in milliseconds (default: 10000 = 10 seconds) */
  refreshIntervalMs?: number;
  /** HTTP fetch timeout in milliseconds (default: 10000) */
  fetchTimeoutMs?: number;
}

interface CachedKey {
  key: jose.CryptoKey | jose.KeyObject;
  expiresAt: number;
}

export class JwksCache {
  private readonly jwksUrl: string;
  private readonly keyTtlMs: number;
  private readonly refreshIntervalMs: number;
  private readonly fetchTimeoutMs: number;

  private cache = new Map<string, CachedKey>();
  private lastRefresh = 0;
  private remoteStore: jose.RemoteJWKSet | null = null;

  constructor(opts: JwksCacheOptions) {
    this.jwksUrl = opts.jwksUrl;
    this.keyTtlMs = opts.keyTtlMs ?? 5 * 60 * 1000;
    this.refreshIntervalMs = opts.refreshIntervalMs ?? 10 * 1000;
    this.fetchTimeoutMs = opts.fetchTimeoutMs ?? 10 * 1000;
  }

  /**
   * Get a signing key for JWT verification.
   * Uses jose's built-in remote JWKS store which handles caching and refresh.
   */
  async getSigningKey(): Promise<jose.JWTVerifyGetKey> {
    if (!this.remoteStore) {
      this.remoteStore = jose.createRemoteJWKSet(new URL(this.jwksUrl));
    }
    return this.remoteStore;
  }

  /**
   * Get a specific key by kid (for manual verification).
   * Falls back to remote JWKS store on cache miss.
   */
  async getKey(kid: string): Promise<jose.CryptoKey | jose.KeyObject> {
    const cached = this.cache.get(kid);
    if (cached && Date.now() < cached.expiresAt) {
      return cached.key;
    }

    await this.refresh();
    const afterRefresh = this.cache.get(kid);
    if (afterRefresh && Date.now() < afterRefresh.expiresAt) {
      return afterRefresh.key;
    }

    throw new KeyNotFoundError(kid);
  }

  private async refresh(): Promise<void> {
    if (Date.now() - this.lastRefresh < this.refreshIntervalMs) {
      return;
    }

    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), this.fetchTimeoutMs);

    try {
      const res = await fetch(this.jwksUrl, { signal: controller.signal });
      if (!res.ok) {
        throw new JwksFetchError(this.jwksUrl, new Error(`HTTP ${res.status}`));
      }

      const body = (await res.json()) as { keys?: Array<Record<string, unknown>> };
      if (!body.keys || !Array.isArray(body.keys)) {
        throw new JwksFetchError(this.jwksUrl, new Error("Invalid JWKS response: missing keys array"));
      }

      const newCache = new Map<string, CachedKey>();
      const now = Date.now();

      for (const jwk of body.keys) {
        try {
          const kid = String(jwk.kid ?? "");
          if (!kid) continue;
          const key = await jose.importJWK(jwk as jose.JWK);
          if (key instanceof Uint8Array) continue; // Skip symmetric keys
          newCache.set(kid, { key, expiresAt: now + this.keyTtlMs });
        } catch {
          // Skip keys that fail to import
        }
      }

      this.cache = newCache;
      this.lastRefresh = now;
    } catch (err) {
      if (err instanceof JwksFetchError) throw err;
      throw new JwksFetchError(this.jwksUrl, err);
    } finally {
      clearTimeout(timeout);
    }
  }
}
