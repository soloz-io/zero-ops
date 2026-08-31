// Browser and native client surface.
//
// Kept separate from the package root deliberately: the root pulls a Redis
// client, a JOSE implementation and server middleware, none of which can be
// bundled for a browser or a native runtime. Importing the root from a client
// application would fail at bundle time, or worse, succeed and ship them.
//
// Nothing in this subpath has a dependency.
export type { UserIdentity, AuthState, AuthTransport } from "./types.js";
export { createAuthClient, hasUserContext } from "./auth-client.js";
export type { AuthClient } from "./auth-client.js";
