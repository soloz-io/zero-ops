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
  /** A session was established. Carries the person, so listeners need not re-read state. */
  | { readonly event: "signedIn"; readonly user: UserIdentity }
  /** The person deliberately ended the session. */
  | { readonly event: "signedOut" }
  /**
   * An established session is no longer valid, without the person asking.
   *
   * This is the event an application logs out on. It fires only on a transition
   * FROM authenticated — never on first load for someone who was never signed
   * in, which is an ordinary unauthenticated visitor and not an expiry.
   */
  | { readonly event: "sessionExpired" }
  /** The session could not be determined. Not a signed-out signal. */
  | { readonly event: "checkFailed"; readonly error: Error };

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
