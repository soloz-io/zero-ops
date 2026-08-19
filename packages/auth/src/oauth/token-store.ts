import { randomBytes, createHash } from "node:crypto";
import type Redis from "ioredis";
import type { TokenSet } from "../authz/types.js";

export interface PendingOAuthFlow {
  subject: string;
  state: string;
  createdAt: number;
}

export interface TokenStore {
  get(subject: string): Promise<TokenSet | null>;
  put(subject: string, tokens: TokenSet): Promise<void>;
  delete(subject: string): Promise<void>;
  acquireRefreshLock(subject: string, ttlMs?: number): Promise<string | null>;
  releaseRefreshLock(subject: string, owner: string): Promise<boolean>;
  beginFlow(subject: string, flowTtlMs?: number): Promise<{ state: string; stateHash: string }>;
  resolveFlow(state: string, flowTtlMs?: number): Promise<PendingOAuthFlow | undefined>;
}

const DEFAULT_TOKEN_PREFIX = "auth:tokens:";
const DEFAULT_FLOW_PREFIX = "auth:flows:";
const DEFAULT_LOCK_PREFIX = "auth:lock:";
const DEFAULT_FLOW_TTL_MS = 5 * 60 * 1000;
const DEFAULT_LOCK_TTL_MS = 30 * 1000;
const TOKEN_TTL_SLACK_MS = 60 * 1000;
const REFRESH_LOCK_WAIT_MS = 10_000;

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export interface RedisTokenStoreOptions {
  /** Key prefix for token storage (default: "auth:tokens:") */
  tokenPrefix?: string;
  /** Key prefix for flow storage (default: "auth:flows:") */
  flowPrefix?: string;
  /** Key prefix for lock storage (default: "auth:lock:") */
  lockPrefix?: string;
  /** Default flow TTL in milliseconds (default: 300000 = 5 minutes) */
  defaultFlowTtlMs?: number;
  /** Default lock TTL in milliseconds (default: 30000) */
  defaultLockTtlMs?: number;
}

/**
 * Redis-backed token store with cross-replica mutex for safe refresh rotation.
 *
 * Uses SET NX PX for distributed locking so concurrent refresh across replicas
 * is serialized — a single refresh-token rotation must not invalidate a second,
 * concurrent refresh.
 */
export class RedisTokenStore implements TokenStore {
  private readonly redis: Redis;
  private readonly tokenPrefix: string;
  private readonly flowPrefix: string;
  private readonly lockPrefix: string;
  private readonly defaultFlowTtlMs: number;
  private readonly defaultLockTtlMs: number;

  constructor(redis: Redis, opts?: RedisTokenStoreOptions) {
    this.redis = redis;
    this.tokenPrefix = opts?.tokenPrefix ?? DEFAULT_TOKEN_PREFIX;
    this.flowPrefix = opts?.flowPrefix ?? DEFAULT_FLOW_PREFIX;
    this.lockPrefix = opts?.lockPrefix ?? DEFAULT_LOCK_PREFIX;
    this.defaultFlowTtlMs = opts?.defaultFlowTtlMs ?? DEFAULT_FLOW_TTL_MS;
    this.defaultLockTtlMs = opts?.defaultLockTtlMs ?? DEFAULT_LOCK_TTL_MS;
  }

  private tokenKey(subject: string): string {
    return `${this.tokenPrefix}${subject}`;
  }

  private flowKey(state: string): string {
    return `${this.flowPrefix}${state}`;
  }

  private lockKey(subject: string): string {
    return `${this.lockPrefix}${subject}`;
  }

  async get(subject: string): Promise<TokenSet | null> {
    const raw = await this.redis.get(this.tokenKey(subject));
    if (!raw) return null;
    try {
      return JSON.parse(raw) as TokenSet;
    } catch {
      return null;
    }
  }

  async put(subject: string, tokens: TokenSet): Promise<void> {
    const ttlMs = Math.max(tokens.expiresAt - Date.now() + TOKEN_TTL_SLACK_MS, TOKEN_TTL_SLACK_MS);
    await this.redis.set(this.tokenKey(subject), JSON.stringify(tokens), "PX", ttlMs);
  }

  async delete(subject: string): Promise<void> {
    await this.redis.del(this.tokenKey(subject));
  }

  async acquireRefreshLock(subject: string, ttlMs?: number): Promise<string | null> {
    const lockValue = randomBytes(8).toString("hex");
    const ttl = ttlMs ?? this.defaultLockTtlMs;
    const result = await this.redis.set(this.lockKey(subject), lockValue, "PX", ttl, "NX");
    return result === "OK" ? lockValue : null;
  }

  async releaseRefreshLock(subject: string, owner: string): Promise<boolean> {
    const current = await this.redis.get(this.lockKey(subject));
    if (current === owner) {
      await this.redis.del(this.lockKey(subject));
      return true;
    }
    return false;
  }

  /**
   * Refresh tokens with a cross-replica mutex.
   * The holder stores the rotated token set atomically before releasing the lock.
   * Waiters poll until the lock clears, then read the stored result.
   */
  async refreshToken(
    subject: string,
    refreshFn: () => Promise<TokenSet>,
  ): Promise<TokenSet> {
    const lockValue = await this.acquireRefreshLock(subject);

    if (lockValue) {
      try {
        const fresh = await refreshFn();
        await this.put(subject, fresh);
        return fresh;
      } finally {
        await this.releaseRefreshLock(subject, lockValue);
      }
    }

    // Another replica holds the lock — poll until it releases
    const deadline = Date.now() + REFRESH_LOCK_WAIT_MS;
    while (Date.now() < deadline) {
      await sleep(50);
      const locked = await this.redis.exists(this.lockKey(subject));
      if (!locked) {
        const stored = await this.get(subject);
        if (stored) return stored;
      }
    }

    throw new Error("Timed out waiting for concurrent refresh");
  }

  async beginFlow(
    subject: string,
    flowTtlMs?: number,
  ): Promise<{ state: string; stateHash: string }> {
    const state = randomBytes(32).toString("hex");
    const stateHash = createHash("sha256").update(state).digest("hex");
    const flow: PendingOAuthFlow = {
      subject,
      state,
      createdAt: Date.now(),
    };
    const ttl = Math.max(flowTtlMs ?? this.defaultFlowTtlMs, 1);
    await this.redis.set(this.flowKey(state), JSON.stringify(flow), "PX", ttl);
    return { state, stateHash };
  }

  async resolveFlow(
    state: string,
    flowTtlMs?: number,
  ): Promise<PendingOAuthFlow | undefined> {
    const raw = await this.redis.get(this.flowKey(state));
    await this.redis.del(this.flowKey(state));
    if (!raw) return undefined;

    let flow: PendingOAuthFlow;
    try {
      flow = JSON.parse(raw) as PendingOAuthFlow;
    } catch {
      return undefined;
    }

    const ttl = flowTtlMs ?? this.defaultFlowTtlMs;
    if (Date.now() - flow.createdAt > ttl) {
      return undefined;
    }

    return flow;
  }
}
