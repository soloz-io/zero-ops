import { describe, it, expect } from "vitest";
import { generateOAuthState, hashState } from "../../src/oauth/state.js";

describe("generateOAuthState", () => {
  it("generates a 64-character hex string", () => {
    const state = generateOAuthState();
    expect(state).toMatch(/^[a-f0-9]{64}$/);
  });

  it("generates unique values", () => {
    const state1 = generateOAuthState();
    const state2 = generateOAuthState();
    expect(state1).not.toBe(state2);
  });
});

describe("hashState", () => {
  it("returns a 64-character hex SHA-256 hash", () => {
    const hash = hashState("test-state");
    expect(hash).toMatch(/^[a-f0-9]{64}$/);
  });

  it("is deterministic", () => {
    const hash1 = hashState("test-state");
    const hash2 = hashState("test-state");
    expect(hash1).toBe(hash2);
  });

  it("produces different hashes for different inputs", () => {
    const hash1 = hashState("state-1");
    const hash2 = hashState("state-2");
    expect(hash1).not.toBe(hash2);
  });
});
