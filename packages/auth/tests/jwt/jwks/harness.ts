import { createServer, type Server, type ServerResponse } from "node:http";
import type { AddressInfo } from "node:net";
import * as jose from "jose";
import { JwtValidator } from "../../../src/jwt/validator.js";

/**
 * A real JWKS endpoint for the JWKS tests. Real because the failure being
 * guarded is a transport one: a Waypoint SDK pod received headers for the keys
 * and then a body that never finished, and jose reported it as "Failed to parse
 * the JSON Web Key Set HTTP response as JSON" -- for every request waiting on it.
 */

export const ISSUER = "https://id.example.test";

const pair = await jose.generateKeyPair("RS256", { extractable: true });
const jwks = JSON.stringify({ keys: [{ ...(await jose.exportJWK(pair.publicKey)), kid: "k", alg: "RS256", use: "sig" }] });
const symmetricOnly = JSON.stringify({ keys: [{ kty: "oct", k: "c2VjcmV0", kid: "s" }] });

/**
 * One step per request; after the plan runs out, `then` (default "ok"):
 *   ok | stall-body | not-json | no-usable-key | <status> | <status>:<retry-after seconds>
 */
export type Step = string;

export interface JwksServer {
  url: string;
  hits: () => number;
  close: () => Promise<void>;
}

const open = new Set<Server>();

/** Close every server a test opened. Call from afterEach. */
export async function closeServers(): Promise<void> {
  await Promise.all(
    [...open].map(
      (s) =>
        new Promise<void>((r) => {
          s.closeAllConnections();
          s.close(() => r());
        }),
    ),
  );
  open.clear();
}

export async function jwksServer(plan: Step[], then: Step = "ok"): Promise<JwksServer> {
  let hits = 0;
  const server = createServer((_req, res: ServerResponse) => {
    const step = plan[hits++] ?? then;
    const [code, after] = step.split(":");
    if (/^\d+$/.test(code)) {
      res.writeHead(Number(code), after ? { "retry-after": after } : {}).end("no");
    } else if (step === "not-json") {
      res.writeHead(200, { "content-type": "text/html" }).end("<html>gateway</html>");
    } else if (step === "no-usable-key") {
      res.writeHead(200, { "content-type": "application/json" }).end(symmetricOnly);
    } else if (step === "stall-body") {
      res.writeHead(200, { "content-type": "application/json" });
      res.write(jwks.slice(0, 20)); // and never the rest
    } else {
      res.writeHead(200, { "content-type": "application/json" }).end(jwks);
    }
  });
  open.add(server);
  await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
  const { port } = server.address() as AddressInfo;
  return {
    url: `http://127.0.0.1:${port}/oauth/v2/keys`,
    hits: () => hits,
    close: () =>
      new Promise<void>((r) => {
        open.delete(server);
        server.closeAllConnections();
        server.close(() => r());
      }),
  };
}

/** Short enough that a test of several attempts stays well under a second. */
export const FAST = { fetchTimeoutMs: 300, retryBaseMs: 10, retryMaxMs: 50 };

export function validator(jwksUrl: string, cache: Record<string, unknown> = {}): JwtValidator {
  return new JwtValidator({
    jwksUrl,
    issuer: ISSUER,
    audience: "aud",
    requireTenantId: false,
    jwksCache: { ...FAST, ...cache },
  });
}

export function token(): Promise<string> {
  return new jose.SignJWT({})
    .setProtectedHeader({ alg: "RS256", kid: "k" })
    .setIssuer(ISSUER)
    .setAudience("aud")
    .setSubject("svc")
    .setExpirationTime("1h")
    .sign(pair.privateKey);
}
