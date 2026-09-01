import type { UserIdentity } from "./types.js";

/**
 * Authentication events, as a closed set.
 *
 * A tenant application needs to *react* to authentication changing, not merely
 * read the current state: a session that expires mid-use must tear down the
 * session-scoped UI, cancel in-flight work and send the person to sign in
 * again. Polling `getState()` cannot express "this just happened", and every
 * application that reconstructs the transition from successive states arrives
 * at a slightly different answer.
 *
 * Names are Amplify's wherever the concept exists — `signedIn`, `signedOut`,
 * `signInWithRedirect`, `signInWithRedirect_failure`, underscore and all — so
 * that Amplify's documentation reads as documentation for this package. The
 * three differences are deliberate and each has a reason:
 *
 *   tokenRefresh          not emitted. Amplify holds tokens in the client and
 *   tokenRefresh_failure  refreshes them there. Here the gateway owns the
 *                         session cookie and performs any refresh, so a refresh
 *                         is not an event this side can observe. `sessionExpired`
 *                         reports the only consequence that is observable, and
 *                         carries `tokenRefresh_failure`'s semantics.
 *
 *   customOAuthState      not emitted. Amplify passes application state through
 *                         the `state` parameter and hands it back on return. The
 *                         gateway performs the exchange and does not surface
 *                         `state`, so there is nothing to hand back; an
 *                         application should keep pre-redirect state in session
 *                         storage instead.
 *
 *   checkFailed           has no Amplify equivalent, because Amplify's client
 *                         reads a local token and cannot fail to reach it. Here
 *                         determining the session is a network call to the
 *                         gateway, which introduces a third answer — "unknown" —
 *                         that must not be collapsed into "signed out".
 *
 * The payload shape follows Amplify too: a single `data` field, so a listener
 * written against one reads against the other.
 *
 * The set is closed for the same reason `AuthState` is: an open string channel
 * invites each application to invent its own events, which is exactly the
 * per-tenant authentication logic this package exists to remove.
 *
 * The distinction that matters, and the one applications get wrong:
 *
 *   signedOut       the person asked to leave. Expected, no warning, no return
 *                   path to preserve.
 *
 *   sessionExpired  the person did NOT ask. They were authenticated a moment
 *                   ago and are not now. The application should say so, keep
 *                   whatever it can, and offer a way back — signing someone out
 *                   silently mid-task is indistinguishable from a bug.
 *
 *   checkFailed     the client does not KNOW. A network failure or a gateway
 *                   error is not a signed-out signal, and treating it as one
 *                   signs people out because a request timed out. Applications
 *                   should generally do nothing here except perhaps retry.
 *
 * Modelled on the AWS Amplify Hub auth channel, which draws the same lines
 * (`signedIn`, `signedOut`, `tokenRefresh_failure`). The naming follows this
 * platform's conventions rather than Amplify's underscore style.
 */
export type AuthEvent =
  /** Dispatched when the user is signed-in. Amplify: `signedIn`. */
  | { readonly event: "signedIn"; readonly data: UserIdentity }
  /** Dispatched after the user signs out. Amplify: `signedOut`. */
  | { readonly event: "signedOut" }
  /**
   * Dispatched when a redirect sign-in completes. Amplify: `signInWithRedirect`.
   *
   * Detected from the callback landing, because the gateway — not this client —
   * performs the OAuth exchange. See `detectRedirectResult`.
   */
  | { readonly event: "signInWithRedirect" }
  /**
   * Dispatched when the redirect flow fails. Amplify:
   * `signInWithRedirect_failure`, underscore and all, so the name matches the
   * reference exactly.
   *
   * Distinct from `checkFailed`: the provider explicitly refused — consent
   * denied, a bad client, an expired authorization code. An application should
   * show a sign-in error rather than retry, which is why the two cannot share an
   * event.
   */
  | { readonly event: "signInWithRedirect_failure"; readonly data: { readonly error: Error } }
  /**
   * An established session is no longer valid, without the person asking.
   *
   * Amplify's nearest equivalent is `tokenRefresh_failure`, and the name is
   * deliberately NOT reused: that event describes a token this client never
   * holds. The gateway owns the session cookie and performs any refresh, so the
   * only thing observable here is that a session which worked no longer does.
   * The semantics are Amplify's — a definitive authentication failure, never a
   * transient one — while the name describes what actually happened.
   *
   * This is the event an application logs out on. It fires only on a transition
   * FROM authenticated, so an anonymous first load is never mistaken for an
   * expiry.
   */
  | { readonly event: "sessionExpired" }
  /**
   * The session could not be determined. Not a signed-out signal.
   *
   * Mirrors the rule Amplify states in TokenOrchestrator.handleErrors: clear the
   * session only for definitive authentication failures, never for transient
   * ones such as service issues or rate limits.
   */
  | { readonly event: "checkFailed"; readonly data: { readonly error: Error } };

/**
 * What an OAuth redirect landing says about the attempt.
 *
 * The gateway performs the code exchange, so this client never sees a token. It
 * can still read the provider's answer from the URL it was returned to, which is
 * the only place a redirect failure is visible: without this, a refused consent
 * is indistinguishable from any other unauthenticated load.
 *
 * Exported for testing and because a native client landing on a deep link needs
 * the same interpretation.
 */
export function detectRedirectResult(
  search: string,
): { kind: "success" } | { kind: "failure"; error: Error } | null {
  let params: URLSearchParams;
  try {
    params = new URLSearchParams(search);
  } catch {
    return null;
  }
  const error = params.get("error");
  if (error) {
    const description = params.get("error_description");
    const err = new Error(description ? `${error}: ${description}` : error);
    // `name` carries the provider's code, matching Amplify's AuthError shape
    // ({ name, message }) so a listener can branch on it.
    err.name = error;
    return { kind: "failure", error: err };
  }
  // `code` with `state` is the successful authorization-code landing.
  if (params.get("code") && params.get("state")) return { kind: "success" };
  return null;
}

export type AuthEventListener = (event: AuthEvent) => void;

/**
 * Fans an event out to listeners without letting one break the others.
 *
 * A listener that throws must not prevent the remaining listeners from running:
 * these fire on session expiry, when the listeners that matter most are the ones
 * tearing down access to data. Errors are reported and swallowed.
 */
export function createEventEmitter() {
  const listeners = new Set<AuthEventListener>();

  return {
    listen(listener: AuthEventListener): () => void {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    emit(event: AuthEvent): void {
      for (const listener of [...listeners]) {
        try {
          listener(event);
        } catch (cause) {
          // Reported, not rethrown. One application's faulty handler must not
          // stop another's from revoking access.
          // eslint-disable-next-line no-console
          console.error("[zero-ops-auth] auth event listener threw", cause);
        }
      }
    },
    get size() {
      return listeners.size;
    },
  };
}
