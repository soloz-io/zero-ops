import type { AuthState, AuthTransport, UserIdentity } from "./types.js";

/** Where the aggregation boundary serves the current identity. */
const IDENTITY_PATH = "/api/v1/auth/me";

export interface AuthClient {
  /** Current state. Synchronous; "unknown" until the first check completes. */
  getState(): AuthState;
  /** Re-check the session. Safe to call concurrently. */
  refresh(): Promise<AuthState>;
  /** Observe state changes. Returns an unsubscribe function. */
  subscribe(listener: (state: AuthState) => void): () => void;
}

/**
 * A platform-agnostic authentication client.
 *
 * Exists because the identity contract — which endpoint, what the response
 * means, which failures are "signed out" and which are "something broke" — is
 * platform behaviour, and it is about to have more than one consumer. A browser
 * application and a native one that each implement it will diverge, and the way
 * they diverge is by disagreeing on the cases below, none of which is obvious.
 *
 * WHAT AN UNSUCCESSFUL RESPONSE MEANS
 *
 * A 401 means the session is absent or invalid: the person is signed out, which
 * is an ordinary state and not an error. Anything else — a network failure, a
 * gateway error, a malformed body — means the client does not know, and saying
 * "signed out" would be a claim it cannot support. Clients routinely conflate
 * the two and sign people out because a request timed out.
 *
 * CONCURRENCY
 *
 * Overlapping refreshes share one in-flight request. Without that, mounting
 * several components that each check on mount produces a burst of identical
 * requests, and their responses can resolve out of order and leave the last
 * writer's stale answer in place.
 */
export function createAuthClient(transport: AuthTransport = {}): AuthClient {
  const doFetch = transport.fetch ?? globalThis.fetch;
  const base = transport.baseUrl ?? "";
  const credentials = transport.credentials ?? "include";

  let state: AuthState = { status: "unknown", user: null, error: null };
  let inFlight: Promise<AuthState> | null = null;
  const listeners = new Set<(s: AuthState) => void>();

  function set(next: AuthState): AuthState {
    state = next;
    for (const listener of listeners) listener(state);
    return state;
  }

  async function check(): Promise<AuthState> {
    if (!doFetch) {
      return set({
        status: "error",
        user: null,
        error: new Error("no fetch implementation available"),
      });
    }

    try {
      const headers = transport.authHeaders
        ? await transport.authHeaders()
        : undefined;

      const res = await doFetch(`${base}${IDENTITY_PATH}`, {
        credentials,
        headers,
      });

      if (res.status === 401) {
        return set({ status: "unauthenticated", user: null, error: null });
      }
      if (!res.ok) {
        // Not a signed-out signal. Reporting one here would sign a person out
        // because a gateway returned 502.
        return set({
          status: "error",
          user: null,
          error: new Error(`identity check failed with status ${res.status}`),
        });
      }

      const body = (await res.json()) as { user?: UserIdentity };
      if (!body.user?.subject) {
        return set({
          status: "error",
          user: null,
          error: new Error("identity response contained no subject"),
        });
      }

      return set({ status: "authenticated", user: body.user, error: null });
    } catch (cause) {
      return set({
        status: "error",
        user: null,
        error: cause instanceof Error ? cause : new Error("identity check failed"),
      });
    }
  }

  return {
    getState: () => state,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    refresh() {
      if (inFlight) return inFlight;
      inFlight = check().finally(() => {
        inFlight = null;
      });
      return inFlight;
    },
  };
}

/**
 * Whether the client may act on behalf of a specific person yet.
 *
 * Authenticated is not sufficient: the tenant-local identifier can be absent
 * while the session is perfectly valid, and anything keyed on ownership must
 * wait for it rather than sending null. Provided here so each client does not
 * decide separately what "ready" means.
 */
export function hasUserContext(
  state: AuthState,
): state is { status: "authenticated"; user: UserIdentity & { userId: string }; error: null } {
  return state.status === "authenticated" && state.user.userId !== null;
}
