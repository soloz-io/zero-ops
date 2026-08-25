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
export function selfServiceBrowserUrl(flowType: string, returnTo?: string): string {
  const query = returnTo ? `?return_to=${encodeURIComponent(returnTo)}` : '';
  return `${kratosBaseUrl}/self-service/${flowType}/browser${query}`;
}
