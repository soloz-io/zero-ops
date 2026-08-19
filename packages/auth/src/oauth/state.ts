import { createHash, randomBytes } from "node:crypto";

/**
 * Generate a cryptographically random OAuth state parameter.
 * Returns a 32-byte hex string.
 */
export function generateOAuthState(): string {
  return randomBytes(32).toString("hex");
}

/**
 * SHA-256 hash of the state parameter.
 * Used for storing the hash while sending the raw state to the client.
 */
export function hashState(state: string): string {
  return createHash("sha256").update(state).digest("hex");
}
