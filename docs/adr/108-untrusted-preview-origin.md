# ADR-108: Session previews are served from a separate registrable domain, admitted by a platform handoff

**Date:** 2026-10-20
**Status:** Proposed. The contract below is agreed with the product team (2026-10-20)
and implemented (see "Implementation"). The decision is taken on 2026-11-06, after the
browser proof in "Acceptance criteria", which needs the preview domain to exist.
**Relates to:** ADR-050 (tenant authentication at the gateway), ADR-052 (sandbox
compute and S3 workspaces), ADR-053 (platform-provisioned OAuth clients), ADR-088
(per-application gateway), ADR-106 (sandbox containment baseline), ADR-107 (sandbox
isolation runtime)

## Context

An application can show a live preview of code running in a session's sandbox,
embedded in its own UI. That code is untrusted. Serving it under the application's
registrable domain would let it read or set the application's cookies and act
same-site against the application.

So previews need their own registrable domain and one host per preview. They also
need access control per preview, not just per tenant.

Running the tenant gateway's OIDC login on the preview host doesn't work. Verified
at the deployed gateway version:

- **No `Partitioned` attribute.** The gateway's session cookie can't carry it, so
  inside a cross-site iframe it is an unpartitioned third-party cookie.
- **One fixed redirect URI per login policy.** Hosts per preview would mean a login
  policy and an issuer registration per preview, and the gateway can't hand a
  completed login to another host.
- **No `prompt` control.** Silent completion can't be guaranteed, and an issuer
  login page inside a frame may refuse to render.

The gateway does support external authorisation per request, and that is what this
ADR builds on.

## Decision

### 1. Hosts

Each preview is served at `<previewHost>.<preview-domain>`. The preview domain is a
registrable domain that holds nothing but previews. TLS uses one wildcard
certificate per environment, obtained by DNS-01. The platform assigns the host;
product doesn't choose it.

### 2. The preview record

Product declares a preview as a namespaced resource in its tenant namespace:

```yaml
apiVersion: compute.nutgraf.in/v1alpha1
kind: PreviewAccess
metadata:
  name: <previewId>            # product's id; unique per namespace
  ownerReferences: [...]       # optional, see "Lifetime" below
spec:
  jobRef: <EphemeralJob name>  # the sandbox served; MUTABLE (re-point keeps the host)
  port: <app server port>      # HTTP and WebSocket
  ownerSubject: <issuer sub>   # issuer subject IDs, not emails or usernames
  sharedWith: [<issuer sub>, ...]
  expiresAt: <RFC 3339>        # required when there is no ownerReference
status:
  host: <previewHost>.<preview-domain>   # fixed for the record's whole life
  conditions: [Ready]
```

- **Lifetime.** The host belongs to the record, not to the job. Product picks one
  of two forms:
  - an ownerReference to the session's EphemeralJob, so deleting that job deletes
    the record and its host;
  - no ownerReference: product deletes the record when the session ends, and
    `expiresAt` is required. Admission refuses the record without it, so a
    forgotten record can't outlive its bound.

  To keep a preview's host when the sandbox is recreated as a new EphemeralJob,
  use the second form and patch `jobRef`. Cookies stay valid across a re-point,
  because they are bound to the preview and host, not to the job. While `jobRef`
  names a job that isn't Running, requests are refused with `unavailable` (§6).
- **Cardinality.** At most one record per (`jobRef`, `port`). A create, or a
  `jobRef`/`port` patch, that would make a second one is refused at admission with
  `409 Conflict`, reason `PreviewTargetInUse`, naming the existing record.
  Admission can race, so the controller also enforces this: of two records on one
  target, the newer never becomes Ready (`Ready=False`, reason
  `PreviewTargetInUse`) and serves nothing.

- **Identity.** The application's SDK creates, patches and deletes the record with
  its own service account. The universal-tenant chart grants create, get, patch
  and delete on PreviewAccess in that namespace only.
- **Tenant binding.** The tenant is the record's namespace. Admission refuses a
  `jobRef` that isn't in the same namespace.
- **Revocation** is a patch to `sharedWith` or a delete. With the
  ownerReference form, deleting the EphemeralJob deletes the record.

### 3. Getting a code

The application's UI calls an endpoint on its own host. The endpoint sits behind
the existing gateway login (ADR-050), so product implements no auth:

```
POST https://<app host>/_platform/preview-access/v1/codes
Content-Type: application/json
{"previewId": "<previewId>"}

200 {"handoffUrl": "https://<host>/_handoff?code=<code>",
     "codeExpiresInSeconds": 10,
     "cookieMaxAgeSeconds": 28800}
403 {"previewAccess": "denied"}     caller is not the owner or in sharedWith
404 {"previewAccess": "not-found"}
```

- The preview-access service reads the caller's `sub` and organisation from the
  session credential the gateway forwards. It validates that credential against
  the same issuer and keys as the application's backend.
- It requires `Content-Type: application/json` and a same-origin `Origin`.
- The code is 256 bits of randomness, single-use, expires 10 seconds after issue,
  and is bound to one host, one subject and one preview.

### 3a. Reading a preview's state

The preview host is the untrusted app's own origin, so a message posted from it is
a hint and never an instruction. The canvas acts on this endpoint, which is behind
the same gateway login as §3:

```
GET https://<app host>/_platform/preview-access/v1/previews/<previewId>

200 {"previewId": "...", "state": "active|expired|denied|revoked|ended|unavailable",
     "host": "<host>", "cookieExpiresAt": "<RFC 3339>" | null}
```

- `state` is for the caller's subject:
  - `active`: the caller is admitted and their cookie is valid;
  - `expired`: the caller is admitted but their cookie has lapsed or was never
    set, so get a new code;
  - `denied`: the caller isn't the owner and isn't in `sharedWith`;
  - `revoked`: they were removed from `sharedWith` after redeeming;
  - `ended`: the record is gone;
  - `unavailable`: the record is valid but `jobRef` isn't Running.
- `cookieExpiresAt` is the expiry of the newest cookie redeemed for this subject
  and preview.
- A record that doesn't exist in the caller's tenant is `ended`. The endpoint
  never says whether it exists in another tenant.

### 4. Redemption and the cookie

The UI sets the iframe `src` to `handoffUrl`. The preview host routes `/_handoff` to
the preview-access service, which:

- redeems the code;
- sets `__Host-zo-preview=<signed value>; Secure; HttpOnly; SameSite=None;
  Partitioned; Path=/; Max-Age=28800`. The signed value holds preview, host,
  subject and expiry. Max-Age never exceeds `expiresAt`;
- responds with `303` to `/`, so the code leaves the URL.

**The service sets the cookie.** The gateway sets no cookie on the preview host.

### 5. Every request is checked

The platform's public gateway routes every preview host to the preview-access
service, and only there. The service checks each request and then proxies it to
the session's sandbox. Checking and proxying happen in the same process, so there
is no route to a preview that skips the check. That covers document loads, assets
and the dev-server WebSocket upgrade, which is an HTTP request. The service:

- verifies the cookie's signature, host and expiry;
- checks the subject against the record's current owner and `sharedWith`;
- denies when the record is absent.

The service holds every record in memory, fed by a watch. A decision is a local
lookup, and nothing is cached in the gateway or in the decision. Revocation takes
effect when the watch delivers the change; the bound is measured in the acceptance
criteria and must be under 2 seconds. If the check can't be made (service
unreachable or timed out), the request is denied.

Before the request reaches the sandbox, the service strips the `__Host-zo-preview`
cookie. The preview's own code never sees it. On the way back, the service removes
any `X-Preview-Access` header and any `Set-Cookie` for that name the sandbox sends.

This replaced the first draft's "the gateway calls the service per request". The
gateway in front of preview hosts is the shared :443 Cilium Gateway, which has no
external-authorisation hook, and each tenant's agentgateway can't route to a
sandbox chosen per request. Product's interface is unchanged.

### 6. Refusals

Every refusal carries `X-Preview-Access: <state>`, where state is one of `expired`,
`denied`, `revoked`, `ended` or `unavailable`. `unavailable` is answered with `503`. The sandbox's app can't set that header: the gateway
removes it from upstream responses. Using a header makes a refusal distinguishable
from an app error.

| Request | Response |
|---------|----------|
| Document (`Sec-Fetch-Dest: iframe` or `document`) | `401` (`expired`) or `403` (others), a platform page that posts `{type: "zero-ops.preview-access", state, previewId}` to the parent, targeted at the application's origin only |
| Asset or fetch | same status, body `{"previewAccess": "<state>"}` |
| WebSocket upgrade | same status. The browser exposes only a failed connection, so the canvas learns the state when the dev server's client reloads the document |

- **Trust.** The posted message is a hint only. The preview's own code can post an
  identical message from the same origin. On any message, or on a failed load, the
  canvas reads §3a and acts on that answer alone.
- **Getting a new code.** Only when §3a says `expired` does the canvas request a
  new code and reload the iframe. On `denied`, `revoked` or `ended` it does not.
  On `unavailable` it waits for the session and reads §3a again.
- **Proactive refresh.** `cookieMaxAgeSeconds` in the code response lets the canvas
  refresh before expiry.

### 7. A browser that refuses the embedded cookie

If the browser proof shows a supported browser refusing the partitioned cookie in
the embed, then **in that browser the preview opens outside the canvas**, as a
top-level window (`window.open(handoffUrl)`). There its cookie is first-party.

A cookie set during that top-level visit is still third-party when the same host is
embedded later, so the top-level window does not restore embedding. A canvas mode that
embeds the live app is not available in that browser. Nothing else changes: the
record, the code, the state endpoint, the checks and the refusals are the same.

Restoring the embed through the Storage Access API is evaluated in the browser
proof, not promised. That would mean a top-level visit, then a user gesture in the
iframe, then `requestStorageAccess()`, then a redemption that sets an unpartitioned
cookie. It becomes part of this ADR, per browser, only where the proof shows it
working end to end.

### 8. Audit

The preview-access service logs one line per issued code, per redemption and per
denied request, and one sampled line per allowed decision:

- time;
- tenant and namespace;
- preview id;
- subject;
- host;
- request kind;
- decision and state.

The logs go to the platform's log pipeline.

### 9. Network

The preview path reaches only `spec.port` on the record's sandbox. For each Ready
record, the service renders `<record>-preview`, a CiliumNetworkPolicy in the tenant
namespace that admits the preview-access pods to that sandbox pod on that port.
Nothing else gets through ADR-106's sandbox ingress deny. The policy is owned by
the record, so deleting the record deletes it.

## Acceptance criteria

1. Embedded in the application's UI, in current Chrome, Safari and Firefox, and in
   Chrome with third-party cookies blocked: the first navigation, asset requests
   and the dev-server WebSocket upgrade succeed for the owner.
2. A shared subject is admitted. Any other subject, in the same tenant or another,
   is refused with `denied`.
3. A replayed code, a code older than 10 seconds, and a code redeemed on another
   host are each refused.
4. Removing a subject from `sharedWith`, or deleting the session, refuses that
   subject's next request. The measured bound is under 2 seconds.
5. An expired cookie produces `401` with `X-Preview-Access: expired` on a document,
   on an asset, and on a WebSocket upgrade.
6. The sandbox never receives `__Host-zo-preview`. A response from the sandbox can't
   carry `X-Preview-Access`.
7. If criterion 1 fails in a supported browser, the proof shows the top-level
   window working in that browser. It also records whether the Storage Access API
   path restores the embed there (§7).
8. A message posted by the preview's own code, shaped like a refusal message, causes
   no code fetch or reload. Only §3a's answer does.
9. Re-pointing `jobRef` keeps `status.host`, and an existing cookie keeps working.
   While the target isn't Running, requests get `503 unavailable`.
10. A second record on the same (`jobRef`, `port`) is refused with
    `409 PreviewTargetInUse`. In a race, only one of the two records becomes Ready.

## Implementation

| Part | Where |
|------|-------|
| `PreviewAccess` API (compute.nutgraf.in/v1alpha1) | `operators/ephemeral-job-operator/api/v1alpha1/previewaccess_types.go`; CRD in `config/crd/bases` |
| Service (one binary, same image as the operator) | `cmd/preview-access`, `internal/previewaccess`: `server.go` (§3, §3a, §4–§6), `cookie.go` (§4), `jwt.go` (§3 identity), `state.go`, `reconciler.go` (§1, §2 status and cardinality, §9), `webhook.go` (§2 admission) |
| Deployment, RBAC, admission, its own ingress policy: a separate component from the operator (`config/default`), because it needs cert-manager and Cilium | `config/preview-access/` |
| Public listener `*.<previewDomain>`, wildcard certificate, route | `manifests/tenants/charts/platform-spoke-gateway` (`preview.domain`) |
| The app-side route, app origin, gateway label and egress | universal-tenant `previewAccess.enabled` |
| Per-environment issuer and preview domain | environment-manager `previewDomain`; `spoke-catalog/templated-fields.yaml` |

**How the caller is identified.** The service reads **which application** is calling
from the source address of the tenant gateway pod that made the call. Those pods are
labelled by the chart and are the only callers Cilium admits on that port. The
namespace never comes from a header or a path. **Who** is calling comes from the
session credential the gateway forwards: RS256, verified against the platform
issuer's keys.

**The signing key** is generated by the service on first start into the Secret
`platform-ops/preview-access-signing-key`. It is encrypted at rest (ADR-105) and
never leaves the cluster. Deleting the Secret rotates it, which sends every cookie
down the `expired` path.

**One addition to the code response:** a record that isn't Ready (another record
already holds its target) gets `503 {"previewAccess":"unavailable"}`.

**No preview domain is a hard failure, not a state.** Without `previewDomain`:

- the service refuses to start, naming the variable;
- PreviewAccess creates are refused at admission (`failurePolicy: Fail`);
- the preview listener is not rendered;
- `108-untrusted-preview-origin.sh` P2 fails.

No preview is ever served from a domain other than the declared one.

### Enabling previews in an environment

1. Buy the preview registrable domain and delegate it to the DNS service the ACME
   dns01 solver writes to. Add it to that solver's `dnsZones` in
   `spoke-catalog/infra/acme-cluster-issuer.yaml`, for both issuers.
2. Set `previewDomain` for the environment manager (the box's bootstrap values).
   The chart refuses a domain that equals, contains or sits inside the platform's
   own domain.
3. For each app: set `previewAccess.enabled: true`, and grant its SDK
   `previewaccesses` (create, get, list, watch, patch, update, delete) in
   `workloadRbac`.

## Ownership

| Part | Owner |
|------|-------|
| Preview domain, wildcard certificate, preview gateway, preview-access service, PreviewAccess CRD and its RBAC, audit | Platform |
| Creating, updating and deleting PreviewAccess records; calling the code endpoint; reacting to refusals | Product |

## Consequences

- Product implements no auth on the preview path and holds no signing keys.
- The cookie-signing key lives in the platform secret store, per environment.
  Rotating it ends every preview cookie, which then follows the `expired` path.
- The preview-access service is on the request path of every preview. It fails
  closed: if it is down, previews are down; nothing else is affected.
- It runs as **one replica**, because handoff codes are single-use and held in
  memory. High availability needs a shared code store first.
- What it remembers in memory, which a restart loses:
  - who has redeemed what. After a restart, a revoked subject reads `denied`
    rather than `revoked`;
  - state reported as `active`, which reads `expired` until the next handoff.
    Both still stop or renew correctly.
- Buying the preview domain is a prerequisite.

## References

- Plan: `docs/plans/adr-039-sandbox-platform-capabilities.md`
- Gateway source at the deployed tag: `http/oidc/session.rs:206-305`,
  `http/oidc/redirect.rs`, `http/oidc/mod.rs:131,243`, `http/ext_authz.rs`
