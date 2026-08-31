/**
 * The signed-in person, as a client application sees them.
 *
 * Shared by every client the platform serves — browser applications and native
 * ones alike. The shape is deliberately identical across them: the distinction
 * between the two identifiers below is the thing clients get wrong, and it must
 * not be re-decided per platform.
 */
export interface UserIdentity {
  /**
   * This tenant's own identifier for the person (ADR-057).
   *
   * Use this anywhere the client references ownership — whose record this is,
   * what to filter by, what to send when creating something. It is the value the
   * database's isolation policies key on.
   *
   * Null when the identity provider has authenticated the person but the
   * tenant-local user could not be resolved. That is a real state, not an error:
   * the session is valid and the client should treat it as "signed in, ownership
   * unknown" rather than signed out.
   */
  userId: string | null;
  /**
   * The identity provider's subject.
   *
   * Stable, but meaningful only to the provider. Never use it to reference
   * tenant data: doing so ties the tenant's records to an external system, and
   * it is a different value from userId for the same person.
   */
  subject: string;
  email: string | null;
  role: string | null;
  tenantId: string | null;
}

/**
 * Authentication state as a closed set.
 *
 * Modelled as a union rather than independent flags because the combinations
 * flags permit are mostly meaningless — "loading and authenticated", "errored
 * with a user" — and each meaningless combination is a branch some client will
 * eventually render. A union makes the impossible states unrepresentable.
 *
 * "unknown" is the initial state and is distinct from "unauthenticated": before
 * the first check completes, a client knows nothing, and rendering a signed-out
 * view during that window produces a visible flash of the wrong screen.
 */
export type AuthState =
  | { status: "unknown"; user: null; error: null }
  | { status: "authenticated"; user: UserIdentity; error: null }
  | { status: "unauthenticated"; user: null; error: null }
  | { status: "error"; user: null; error: Error };

/**
 * How a client reaches the identity endpoint.
 *
 * Abstracted because platforms differ in how a session travels. A browser sends
 * a host-only cookie the gateway set and needs credentials included; a native
 * application has no cookie jar the gateway can write to and must present a
 * token it holds itself. Both are the same request otherwise, so the difference
 * is expressed here rather than by duplicating the client.
 */
export interface AuthTransport {
  /**
   * Base URL for the identity endpoint. Empty for a browser served from the
   * same origin; an absolute URL for a native application.
   */
  baseUrl?: string;
  /** Supplied so tests and native runtimes can provide their own. */
  fetch?: typeof globalThis.fetch;
  /**
   * Headers carrying the session, for platforms that cannot rely on cookies.
   * Called per request so a native client can return a freshly refreshed token
   * rather than one captured at construction.
   */
  authHeaders?: () => Promise<Record<string, string>> | Record<string, string>;
  /**
   * Whether to send credentials. Defaults to "include", which is what a browser
   * needs and what a native runtime ignores.
   *
   * Spelled as a union rather than referencing the DOM type: this package is
   * compiled for a server runtime, and pulling in the DOM library to name one
   * string union would put every browser global in scope for code that must not
   * use them.
   */
  credentials?: "omit" | "same-origin" | "include";
}
