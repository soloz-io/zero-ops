import * as jose from "jose";

/**
 * Keys in a JWKS document that can verify a token here. This count is what
 * "ready" is defined on, so it must never count a key the verifier would reject.
 *
 * So the verifier decides. Each key goes through jose's own local selection, the
 * filter `jwtVerify` applies to the remote set, followed by the import. In jose
 * 6.2.12 (`isUsableJWK`) that is: `kty` for the algorithm; `alg`, when present,
 * equal to it; `use`, when present, `sig`; `key_ops`, when present, unique
 * strings including `verify`; `ext`, when present, a boolean; `crv` for EC.
 * Re-implementing it here would be a second copy free to drift from the first --
 * 6.2.12 already tightened `key_ops` and added `ext` over 6.2.9.
 *
 * A private or symmetric key is refused on top: it is not a public signing key,
 * whatever jose would make of it.
 */
export async function countUsableKeys(json: unknown, algorithms: string[]): Promise<number> {
  const keys = (json as { keys?: unknown })?.keys;
  if (!Array.isArray(keys)) return 0;
  let usable = 0;
  for (const jwk of keys as jose.JWK[]) {
    if (await isUsable(jwk, algorithms)) usable++;
  }
  return usable;
}

async function isUsable(jwk: jose.JWK, algorithms: string[]): Promise<boolean> {
  if (typeof jwk !== "object" || jwk === null) return false;
  if ("d" in jwk || jwk.kty === "oct") return false; // private or symmetric
  let select: ReturnType<typeof jose.createLocalJWKSet>;
  try {
    // One key per set, so a document of several keys never answers "multiple
    // matching keys" for a header that names no kid.
    select = jose.createLocalJWKSet({ keys: [jwk] });
  } catch {
    return false;
  }
  for (const alg of algorithms) {
    try {
      await select({ alg });
      return true;
    } catch {
      // Not selected, or not importable, for this algorithm; try the next.
    }
  }
  return false;
}
