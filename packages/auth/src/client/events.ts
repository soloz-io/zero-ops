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
 * Names are Amplify's wherever the concept exists — `signedIn`, `signedOut` —
 * so that Amplify's documentation reads as documentation for this package. The
 * differences are deliberate, and every one of them follows from the same fact:
 * the gateway, not this client, performs OAuth and owns the session cookie.
 * Amplify's client holds tokens and runs the redirect flow itself, so it can
 * observe steps that are simply not visible from here.
 *
 *   tokenRefresh          not emitted. Amplify holds tokens in the client and
 *   tokenRefresh_failure  refreshes them there. Here the gateway owns the
 *                         session cookie and performs any refresh, so a refresh
 *                         is not an event this side can observe. `sessionExpired`
 *                         reports the only consequence that is observable, and
 *                         carries `tokenRefresh_failure`'s semantics.
 *
 *   signInWithRedirect    not emitted, and deliberately absent rather than
 *   signInWithRedirect_   declared-but-silent. Verified against agentgateway
 *   failure               (http/oidc/callback.rs, http/oidc/mod.rs): on success
 *                         the gateway redirects to the originally requested URI
 *                         with no `code` or `state` in the query, so a
 *                         successful return is indistinguishable from any other
 *                         authenticated load. On failure — `?error=` from the
 *                         provider, or a CSRF, nonce or exchange failure — the
 *                         gateway answers 400 or 500 AT the callback path, so
 *                         the application is never loaded and no listener of
 *                         ours can run. An event that can never fire is worse
 *                         than a missing one: it invites a sign-in error
 *                         handler that never runs, and the impression that
 *                         sign-in cannot fail.
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
