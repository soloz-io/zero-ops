# Changelog

## 0.20.0

### Added

- `bffAuth({ appId, validator?, publicPaths?, localPrincipal?, signal? })`: a BFF's
  authentication in one call, replacing the ~40 lines each BFF wrote by hand.
  - Creates one validator (default `browserSessionValidator()`) and starts it at
    construction (ADR-022). No process signal handler is installed: the refresh
    timers are unref'd, and a SIGTERM handler would stop Node exiting on the
    signal. Pass `signal` to stop the refresh early.
  - `auth.readiness`: 503 `auth-keys-unavailable` until the keys are held, 200 after.
    Serve it on the READINESS probe path, never the liveness path.
    `auth.ready()` gives the same fact to an app with readiness of its own.
  - `auth.middleware`: `authMiddleware` for the mount, skipping `publicPaths`
    (prefixes matched on segment boundaries).
  - Local mode is `<APPID>_ENV=local` (e.g. `ORANGER_ENV=local`): every request
    gets a stand-in principal (default `local-user`, tenant from
    `LOCAL_TENANT_ID`, or `localPrincipal`, fixed or per request) and readiness is
    always 200. **Refused at construction when `NODE_ENV=production`**, so a
    deployed BFF that believes it is local does not start.

## 0.19.0

Issuer key (JWKS) handling, after a Waypoint SDK pod refused three requests of
one page load with 401 `Failed to parse the JSON Web Key Set HTTP response as
JSON`: its first key download stalled mid-body, and every request waiting on it
failed together.

### Added

- `JwtValidator.start({ signal? })` / `stop()` / `status()`: the ADR-022
  lifecycle. `start()` fetches the keys at boot, retries with full-jitter backoff
  (capped at 5 min) until it succeeds, then refreshes in the background. It never
  throws. `status().ready` is the readiness signal.
- `JwtValidator.warm()`: one awaited fetch.
- `jwksCache` options: `fetchAttempts`, `retryBaseMs`, `retryMaxMs`,
  `backoffMaxMs`, `maxStaleMs`, `algorithms`, `onFetchFailure`,
  `allowInsecureHttp`.
- `authMiddleware({ onDependencyError })`.
- Exports: `fullJitterDelay`, types `JwksStatus`, `JwksFetchFailure`.

### Changed

- A key download has a timeout per attempt covering headers **and** body, and is
  retried on timeout, network error, 408, 429 (honouring `Retry-After`), 5xx and
  a non-JSON body. 400/401/403/404 and redirects are not retried.
- A key set with no usable public signing key is a failed fetch: it never
  replaces held keys and never makes the service ready. "Usable" is decided by
  jose's own key selection (`use`, `key_ops` including `verify`, `alg`, `crv`,
  `ext`) plus the import, so readiness cannot count a key the verifier rejects.
- A fetch cancelled by `stop()`, an aborted `start({ signal })` or the caller is
  not a dependency failure: it is not reported to `onFetchFailure` and does not
  change `consecutiveFailures` or `lastError`.
- `jose` is pinned to `6.2.12` (was `^6.0.11`). The readiness check mirrors that
  version's key selection, which tightened `key_ops` and added `ext` over 6.2.9.
- `authMiddleware` answers a JWKS failure **503** `JWKS_FETCH_ERROR` with a
  generic message, instead of 401 with the internal detail.
- `JwksFetchError` messages name the cause and attempts, and carry `status`.
- jose's `cacheMaxAge` is now `maxStaleMs` (15 min), not the 5-minute refresh
  period. With `start()` the keys still refresh every 5 minutes; **without it**
  they are refetched only once older than 15 minutes or on an unknown `kid`.
- `jwksCache` options are validated at construction and throw `RangeError`
  rather than being normalised; the JWKS URL must be `https:` (`http:` only for
  loopback or with `allowInsecureHttp`).

### Removed (breaking)

- `JwksCache.getKey(kid)` and its private Map cache. It was a second, unused key
  path with none of the retry policy; nothing in zero-ops, waypoint or oranger
  called it. Verification always went through jose's store, and still does.
