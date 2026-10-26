import { describe, it, expect, beforeAll } from "vitest";
import * as jose from "jose";
import { countUsableKeys } from "../../../src/jwt/jwks/usable-keys.js";

// "Ready" is defined on this count: a set the service cannot verify with is no set.

let rsa: jose.JWK;
let rsaPrivate: jose.JWK;

beforeAll(async () => {
  const pair = await jose.generateKeyPair("RS256", { extractable: true });
  rsa = await jose.exportJWK(pair.publicKey);
  rsaPrivate = await jose.exportJWK(pair.privateKey);
});

const algs = ["RS256", "ES256"];

describe("countUsableKeys", () => {
  it("counts a public signing key, with or without alg", async () => {
    expect(await countUsableKeys({ keys: [{ ...rsa, alg: "RS256", use: "sig" }, rsa] }, algs)).toBe(2);
  });

  it.each([
    ["an encryption key", () => ({ ...rsa, use: "enc" })],
    ["an algorithm not accepted", () => ({ ...rsa, alg: "PS512" })],
    ["a private key", () => rsaPrivate],
    ["a symmetric key", () => ({ kty: "oct", k: "c2VjcmV0" })],
    ["a malformed key", () => ({ kty: "RSA", n: "x" })],
    // jose's selection, which the verifier applies: readiness must agree with it.
    ['key_ops without "verify"', () => ({ ...rsa, use: "sig", key_ops: ["sign"] })],
    ["key_ops with a duplicate (jose 6.2.12)", () => ({ ...rsa, key_ops: ["verify", "verify"] })],
    ["a non-boolean ext (jose 6.2.12)", () => ({ ...rsa, ext: "yes" })],
    ["an EC key on the wrong curve", () => ({ kty: "EC", crv: "P-384", x: "AA", y: "AA" })],
  ])("does not count %s", async (_name, key) => {
    expect(await countUsableKeys({ keys: [key()] }, algs)).toBe(0);
  });

  it('counts a key whose key_ops include "verify"', async () => {
    expect(await countUsableKeys({ keys: [{ ...rsa, key_ops: ["verify"] }] }, algs)).toBe(1);
  });

  it("is zero for a document that is not a key set", async () => {
    expect(await countUsableKeys({}, algs)).toBe(0);
    expect(await countUsableKeys(null, algs)).toBe(0);
  });
});
