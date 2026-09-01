import { createEventEmitter, type AuthEventListener } from "./events.js";
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
  /**
   * Observe authentication EVENTS. Returns an unsubscribe function.
   *
   * Use this to react to a session ending — tear down session-scoped UI, cancel
   * in-flight work, send the person to sign in. `subscribe` reports what the
   * state is; this reports what just happened, which is what an application
   * needs in order to log someone out.
   */
  listen(listener: AuthEventListener): () => void;
  /**
   * End the session locally and announce it.
   *
   * Emits `signedOut`, never `sessionExpired`: the person asked. Clearing the
   * gateway's cookie is the gateway's business — this is the client-side half,
   * so an application has one call that means "the session is over" regardless
   * of which side ended it.
   */
  signOut(): void;
  /**
   * Re-check the session periodically so expiry is noticed without user action.
   *
   * Without this a session expires silently and the person discovers it when
   * their next action fails, which surfaces as a broken feature rather than as
   * being signed out. Returns a function that stops the polling.
   */
  startSessionWatch(intervalMs?: number): () => void;
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
  const events = createEventEmitter();

  /**
   * Applies a new state and derives the event the transition represents.
   *
   * Events are derived here, in one place, rather than at each call site. The
   * transition — not the destination — carries the meaning: arriving at
   * `unauthenticated` is an expiry if the previous state was `authenticated`,
   * and an ordinary unauthenticated visitor otherwise. Every application that
   * reconstructs that from successive states gets it subtly wrong, usually by
   * signing people out on first load.
   */
  function set(next: AuthState): AuthState {
    const previous = state;
    state = next;
    for (const listener of listeners) listener(state);

    if (next.status === "authenticated" && previous.status !== "authenticated") {
      events.emit({ event: "signedIn", data: next.user as UserIdentity });
    } else if (
      next.status === "unauthenticated" &&
      previous.status === "authenticated"
    ) {
      // The only transition that means a live session ended on its own.
      events.emit({ event: "sessionExpired" });
    } else if (next.status === "error" && next.error) {
      events.emit({ event: "checkFailed", data: { error: next.error } });
    }
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
        // Do not follow the gateway's redirect to the identity provider.
        //
        // An unauthenticated request is answered with a 302 to the provider,
        // which is right for a document navigation and unusable for this one:
        // the browser follows it cross-origin, the provider sends no CORS
        // headers, and the application sees a network error naming the identity
        // provider instead of learning that its session ended.
        //
        // With redirect: "manual" that same 302 arrives as an opaque response —
        // type "opaqueredirect", status 0 — which is a reliable, same-origin
        // signal meaning exactly "the gateway wants us to authenticate".
        redirect: "manual",
      });

      // Treated identically to 401. The gateway expresses "not authenticated"
      // as a redirect; the API expresses it as a status. Both mean signed out,
      // and an application should not have to know which layer answered.
      if (res.type === "opaqueredirect" || res.status === 0) {
        return set({ status: "unauthenticated", user: null, error: null });
      }
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
    listen: events.listen,
    signOut() {
      const wasAuthenticated = state.status === "authenticated";
      state = { status: "unauthenticated", user: null, error: null };
      for (const listener of listeners) listener(state);
      // Deliberately not routed through set(): that would derive
      // `sessionExpired` from the same transition. The person asked to leave,
      // and telling the application their session expired would have it show a
      // warning and try to recover a session nobody wants.
      if (wasAuthenticated) events.emit({ event: "signedOut" });
    },
    startSessionWatch(intervalMs = 60_000) {
      // Only meaningful while signed in, but the check is cheap and the state
      // may change under it, so it simply re-checks and lets set() decide
      // whether anything happened.
      const id = setInterval(() => {
        void this.refresh();
      }, intervalMs);
      return () => clearInterval(id);
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
