// React binding for the client surface (ADR-057).
//
// Every browser application on the platform needs the same provider: hold render
// until identity is known, react to a session ENDING rather than to a state,
// log out through the client so the right event fires, and read the tenant from
// the identity and nowhere else. waypoint wrote it first; a second application
// writing its own is the divergence ADR-057 exists to prevent, so it lives here.
//
// Depends on react and on ../client, nothing else. Written without JSX so the
// package needs no JSX configuration to build it.
import {
  createContext,
  createElement,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { createAuthClient, hasUserContext } from "../client/auth-client.js";
import type { AuthState, AuthTransport, UserIdentity } from "../client/types.js";

export type { AuthState, UserIdentity };

export interface AuthContextValue {
  /** The signed-in person, or null when not authenticated. */
  user: UserIdentity | null;
  /** The full state. Prefer this over `user` when the distinction matters. */
  state: AuthState;
  /**
   * True until the first identity check completes. Distinct from being signed
   * out -- rendering the signed-out view in this window is the flash ADR-057
   * names.
   */
  loading: boolean;
  /**
   * Whether the tenant-local user id is known. Being authenticated is not
   * sufficient: anything keyed on ownership waits for this rather than sending
   * null.
   */
  ready: boolean;
  refresh: () => Promise<void>;
  login: () => void;
  logout: () => void;
}

export interface AuthProviderProps {
  children?: ReactNode;
  /** How the identity endpoint is reached. Same-origin cookie by default. */
  transport?: AuthTransport;
  /**
   * A fixed identity for LOCAL DEVELOPMENT, used instead of the network.
   *
   * The application decides when to pass it, behind its own build-time gate --
   * in a Vite app, `import.meta.env.DEV` AND an explicit flag AND a local
   * hostname, so a production bundle cannot carry the path. A library cannot
   * read a bundler's build flags, which is why this is a prop and not a check.
   */
  devIdentity?: UserIdentity;
  /**
   * Where to send the browser to sign in again, and after signing out. The
   * gateway's OIDC policy redirects any unauthenticated request to the identity
   * provider, so the application root is enough: the client neither knows nor
   * duplicates the login flow. Defaults to "/".
   */
  signInPath?: string;
  /** How the browser is sent there. Defaults to assigning location. */
  navigate?: (path: string) => void;
}

const AuthContext = createContext<AuthContextValue | null>(null);

function defaultNavigate(path: string): void {
  // Typed loosely: this package is compiled without the DOM library, and a
  // browser is the only runtime that reaches this line.
  (globalThis as unknown as { location: { href: string } }).location.href = path;
}

export function AuthProvider({
  children,
  transport,
  devIdentity,
  signInPath = "/",
  navigate = defaultNavigate,
}: AuthProviderProps) {
  // Created once. The transport is configuration; a new client per render would
  // drop in-flight checks and listeners.
  // biome-ignore lint/correctness/useExhaustiveDependencies: see above
  const client = useMemo(() => createAuthClient(transport), []);
  const [state, setState] = useState<AuthState>(() =>
    devIdentity
      ? { status: "authenticated", user: devIdentity, error: null }
      : client.getState(),
  );

  const refresh = useCallback(async () => {
    if (devIdentity) return;
    setState(await client.refresh());
  }, [client, devIdentity]);

  useEffect(() => {
    if (devIdentity) return;
    // Subscribe before refreshing so a response that arrives first is not missed.
    const unsubscribe = client.subscribe(setState);

    // React to the session ENDING, which state alone cannot express. The client
    // derives the event from the transition, so an anonymous first load is never
    // mistaken for an expiry.
    const unlisten = client.listen((event) => {
      if (event.event === "sessionExpired") {
        // The person did not ask to leave. Back through the gateway, which
        // returns them here signed in.
        navigate(signInPath);
      }
      // signedOut is initiated by logout() below. checkFailed is deliberately
      // ignored: the client does not know the session ended, and signing someone
      // out because a gateway blipped is the failure that distinction prevents.
    });

    void client.refresh();
    // Notice expiry without waiting for the person's next action to fail.
    const stopWatch = client.startSessionWatch();

    return () => {
      unsubscribe();
      unlisten();
      stopWatch();
    };
  }, [client, devIdentity, navigate, signInPath]);

  const login = useCallback(() => navigate(signInPath), [navigate, signInPath]);

  const logout = useCallback(() => {
    // Through the client, so it emits signedOut rather than sessionExpired.
    //
    // Client-side only. Ending the identity provider's session needs an
    // end-session call the gateway does not yet expose, so the next load of the
    // application may sign the person straight back in.
    client.signOut();
    navigate(signInPath);
  }, [client, navigate, signInPath]);

  const value = useMemo<AuthContextValue>(
    () => ({
      user: state.status === "authenticated" ? state.user : null,
      state,
      loading: state.status === "unknown",
      ready: hasUserContext(state),
      refresh,
      login,
      logout,
    }),
    [state, refresh, login, logout],
  );

  return createElement(AuthContext.Provider, { value }, children);
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within an AuthProvider");
  return ctx;
}

/**
 * The tenant this session acts within.
 *
 * The validated identity is the only source (ADR-057). No query parameter, no
 * stored value, no fallback: no identity means no tenant, and a second source
 * for a single-authority value is how an interface comes to present one tenant
 * while acting in another.
 */
export function useTenantId(): string {
  const { user } = useAuth();
  return user?.tenantId ?? "";
}
