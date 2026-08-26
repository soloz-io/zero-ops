/**
 * The base URL every Kratos call in this app goes through.
 *
 * There is exactly one of these on purpose. The fallback used to be written out at
 * each call site — the Elements config, the FrontendApi client, and both branches
 * of the expired-flow redirect — and they drifted: the deployed bundle dialled
 * http://localhost:4433 from an HTTPS page because one copy was never given a
 * value. Four copies of a default is four chances to miss one.
 *
 * Same origin is the right default rather than a build argument. Vite inlines
 * import.meta.env at BUILD time, so anything baked here cannot be corrected by a
 * manifest and makes the image environment-specific. The console serves this app at
 * '/' and the gateway routes '/self-service' to Kratos on the same host, so reading
 * the origin at runtime is correct in every environment, needs no build argument,
 * and keeps the requests same-origin so no CORS grant is involved.
 *
 * VITE_KRATOS_URL still wins when set, which is what local development uses to point
 * at a Kratos running on localhost.
 */
export const kratosBaseUrl: string =
  import.meta.env.VITE_KRATOS_URL || window.location.origin;

/**
 * Absolute URL for a browser-initiated self-service flow.
 *
 * Used when a flow has expired (Kratos answers 410) and the browser has to restart
 * it. Centralised with the base URL because it has the same failure mode: a
 * hardcoded host here sends an expired session to a machine that is not serving the
 * app, and expiry is common enough that it would look intermittent.
 */
export function selfServiceBrowserUrl(
  flowType: string,
  opts: { returnTo?: string; loginChallenge?: string } = {},
): string {
  const params = new URLSearchParams();
  if (opts.returnTo) params.set('return_to', opts.returnTo);

  // login_challenge is what binds this flow to a pending OAuth2 request.
  //
  // Dropping it does not fail: Kratos happily creates a plain login flow, the user
  // authenticates, a session is issued — and nothing hands control back to Hydra,
  // because as far as Kratos is concerned no authorization request was ever
  // involved. The browser then sits on the console instead of returning to the app
  // that started the login, which reads as "signed in but nothing happened" rather
  // than as a lost parameter.
  if (opts.loginChallenge) params.set('login_challenge', opts.loginChallenge);

  const query = params.toString();
  return `${kratosBaseUrl}/self-service/${flowType}/browser${query ? `?${query}` : ''}`;
}
