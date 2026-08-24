# WS4 — Hub Public Domain Authority Migration

**Status:** Proposed (plan only — no code or manifest changes made)
**Date:** 2026-08-24
**Implements:** ADR-051 — Decision, and the *base-domain authority and the derivation boundary* addendum (2026-08-24)
**Relates to:** ADR-035 (PKI), ADR-037 (environment promotion), ADR-041 (controller responsibility), ADR-045 (generated GitOps artifacts), ADR-046 (hybrid cell), ADR-050 (tenant auth), ADR-051 (environment DNS naming)

---

## 1. Decision

> **`HubEnvironment.spec.domain` is the single source of truth for hub public DNS. Shared hub
> manifests contain no environment-specific hostname literals. ArgoCD remains the sole
> reconciler of live resources. Spoke public DNS remains Gateway-derived and outside
> hub-domain rendering.**

Settled points:

| Question | Decision |
|---|---|
| Authority | `HubEnvironment.spec.domain`, declared per environment overlay |
| `DOMAIN` bootstrap key | **Retired as an authority** — not kept as a fallback |
| Rendering boundary | `manifests/**/generated/**`, committed to Git (ADR-045) |
| Reconciler | **ArgoCD, unchanged** — ADR-051's ownership row is not amended |
| Scope | Hub platform hostnames only; spoke/tenant DNS stays Gateway-derived |
| ADR work | **Landed** — ADR-051 addendum (2026-08-24); reconciler ownership unchanged |

### 1.1 Alignment with ADR-051

ADR-051 is Accepted and already mandates this work. It records that `HubEnvironment.spec.domain`
has no consumer, that literals "remain authoritative in practice," and explicitly: *"This ADR does
not treat that as an acceptable end state."* WS4 is the retirement of that debt.

Two ADR-051 constraints shape the scope:

- **Ownership table** assigns *Zone-to-environment mapping* reconciler = **ArgoCD**, consumers =
  external-dns + cert-manager. WS4 preserves this. Rendering into a generated boundary is not
  reconciliation, so no controller-responsibility change (ADR-041) is required.
- **Public ingress terminates at the spoke.** The hub retains "only platform services and the MCP
  resource-server endpoint." Tenant hostnames, spoke ACME solvers and tenant Gateway routes are
  governed by different ownership rows and are **out of scope** — folding them into hub rendering
  would break ADR-051's "record sets are disjoint by construction" property.

### 1.2 Correction to the proposed design: which component renders

The agreed data flow is unchanged, but the renderer cannot be hub-operator:

- hub-operator is an in-cluster controller with **no Git-write capability** (verified: no `go-git`,
  no `git` exec anywhere under `operators/hub-operator/`).
- `manifests/**/generated/**` is a **Git working-tree path**. The established writer is the
  **hub-CLI at bootstrap** — `internal/hub-cli/components/secrets_database_impl.go` resolves
  `os.Getwd()` and writes `security/generated/infisical-fleet-issuer-patch.yaml` and
  `environments/base/generated/hub-bootstrap-config-patch.yaml`, which
  `internal/hub-cli/bootstrap/orchestrator.go` then auto-commits (ADR-045).

Making hub-operator the renderer would require giving an in-cluster operator Git push credentials —
a new capability, a push loop to manage, and a second thing that can rewrite the repo.

**Therefore: the hub-CLI is the renderer.** Every stated invariant still holds — single declarative
authority, generated boundary, ArgoCD as sole reconciler, no Helm migration, no per-resource
patches. Only the component that performs derivation changes, and it changes to the one already
doing this job.

```text
environment overlay  ──►  HubEnvironment.spec.domain
                                   │  (authoritative declarative input)
                                   ▼
                            hub-CLI derivation            [ADR-045 generated artifact]
                                   │
                                   ▼
                       manifests/**/generated/**          [committed to Git]
                                   │
                                   ▼
                                ArgoCD                    [sole reconciler — ADR-051 unchanged]
                                   │
                        ┌──────────┴──────────┐
                        ▼                     ▼
                  Hub platform          Spoke Gateway-derived
                  resources             resources (out of scope)
                        │                     │
                        ▼                     ▼
                 external-dns          external-dns
                 cert-manager          cert-manager
```

---

## 2. Deliverable 1 — Inventory, grouped by semantic contract

54 environment-specific references. Grouped by *contract*, because the migration must move each
contract atomically — not by file.

### C1 — Ingress hostnames

| File | Lines | Hosts |
|---|---|---|
| `hub-core-services/ingress/ingress.yaml` | 15, 18, 43, 46, 69, 72, 105, 108 | api, auth, console, argocd |
| `hub-core-services/infisical/ingress.yaml` | 12, 26, 28 | infisical |
| `hub-core-services/victoriametrics/ingress.yaml` | 13, 18, 21 | victoriametrics.**hub**.\<domain\> |
| `hub-core-services/api-gateway/agentgateway.yaml` | 105, 108 | api |

> `victoriametrics.hub.<domain>` carries an extra `hub.` label no other host uses. WS4 must either
> normalise it or record it as an intentional exception; silently re-deriving it changes the hostname.

### C2 — TLS certificate DNS names

| File | Lines | Notes |
|---|---|---|
| `hub-core-services/ingress/dev-certificates.yaml` | 19, 32, 45, 58 | filename itself encodes `dev` |
| `argocd-principal/certificates.yaml` | 15, 17, 18, 99, 101 | argocd-principal + `-internal` |

> **No wildcard.** ADR-051's wildcard consequence was corrected 2026-08-24: both issuers use HTTP-01
> (`http01.ingress` on the hub, `http01.gatewayHTTPRoute` per tenant on the spoke), and ACME admits
> wildcard identifiers only under DNS-01. WS4 derives one SAN per host. The `*.dev.nutgraf.in` on
> `spoke-catalog/infra/tenant-gateway.yaml:32,39` is a Gateway **listener** hostname — a routing
> match, not a certificate SAN — and must not be read as evidence a wildcard exists.

> `argocd-principal/certificates.yaml` is **fleet/mTLS**, not browser-facing. Under ADR-035 (narrowed
> by audience in ADR-051) it uses the Infisical PKI. It is domain-shaped but a *different* trust
> class — do not merge it into the public ACME contract.

### C3 — OIDC issuer / base URLs (identity)

| File | Lines | Contract |
|---|---|---|
| `identity/ory-hydra/values.yaml` | 6, 7, 8, 11, 26 | `issuer`, login, consent, `base_url`, registration |
| `identity/ory-kratos/values.yaml` | 5, 23, 25, 26, 27, 35, 37 | `base_url`, return URLs, allowed origins, UI URLs |
| `identity/kratos-ui/deployment.yaml` | 31 | console base URL (env var) |
| `identity/auth-proxy/deployment.yaml` | 63, 72, 74 | api + auth base URLs (env vars) |

> `ory-kratos/values.yaml:27` allows `waypoint.<domain>` — a host in **no** other contract. Confirm
> whether it is live before deriving it.

### C4 — JWT issuer / audience (gateway)

| File | Lines | Contract |
|---|---|---|
| `api-gateway/agentgateway-config.yaml` | 51, 53, 57 | `issuer`, audience, protected `resource` |

> Must move in lockstep with C3. A JWT `issuer` that disagrees with Hydra's `issuer` by one label
> fails validation with an opaque error.

### C5 — Infisical public endpoint

| File | Lines |
|---|---|
| `hub-core-services/security/infisical-fleet-issuer.yaml` | 15 |
| `hub-core-services/security/infisical-signing-issuer.yaml` | 19 |

> `security/generated/infisical-fleet-issuer-patch.yaml` **already exists** and already overrides this
> URL at bootstrap. C5 is largely solved — WS4 sources that patch from `spec.domain` instead of a
> hardcoded default.

### C6 — Crossplane composition input

| File | Line | Contract |
|---|---|---|
| `crossplane/tenant-platform/compositions/ainativesaas-starter-hetzner.yaml` | 937 | `hostAPI` → Infisical |

> Per the agreed boundary: the composition **consumes an explicit value**; it must not discover or
> invent the hub domain. This is the hub/spoke seam and needs the most scrutiny.

### C7 — OAuth redirect URIs

| File | Line | Host |
|---|---|---|
| `environments/base/hubenvironment.yaml` | 85 | `mcp.<domain>/callback` |

> A `dev` literal inside the object that is supposed to be the authority, in the **base** layer.
> (The second copy of this row, in the unreferenced `hub-core-services/hub-environment/` duplicate,
> was deleted rather than migrated.)
> Self-referential: `spec.oauth` must derive from `spec.domain` of the same object.
> `mcp.<domain>` appears in no other contract.

### C8 — Compiled Go defaults — **not fixable by manifest rendering**

| File | Line | Problem |
|---|---|---|
| `internal/auth-proxy/handlers.go` | 188 | builds `https://console.dev.nutgraf.in/login?...` in code |
| `internal/hub-cli/infisical/client.go` | 56 | default `baseURL` |
| `internal/hub-cli/components/secrets_database_impl.go` | 129 | comment records the hardcoded default |
| `internal/auth-proxy/config.go` | 25 | comment only — no change needed |
| `internal/auth-proxy/config_test.go` | 20, 22, 23, 84 | fixtures — update with C3 |

> ADR-051 says literals are "distributed across manifests **and compiled into binaries**." C8 is the
> binary half. These must become configuration (env var / flag), not rendered YAML. **WS4 is not a
> manifest-only exercise.**

### Out of scope (ADR-051 spoke ownership)

`spoke-catalog/infra/acme-cluster-issuer.yaml` (21), `agentgateway-config.yaml` (7),
`tenant-gateway.yaml` (2), `cluster-issuer.yaml`, `cluster-secret-store.yaml`, `coredns.yaml`,
`nats-leaf-node.yaml`, spoke-pool definitions, tenant chart values.

---

## 3. Deliverable 2 — Authoritative data flow

**Derivation rule.** The hub-CLI derives the complete endpoint set from one value. No per-hostname
knobs are exposed.

```text
spec.domain = <zone>

  api.<zone>            auth.<zone>          console.<zone>
  argocd.<zone>         infisical.<zone>     mcp.<zone>
  victoriametrics.hub.<zone>      (C1 exception — confirm)
  waypoint.<zone>                 (C3 — confirm live)
```

Production exception, per ADR-051: prod uses the apex unlabelled, so `<zone> = nutgraf.in` and the
derivation is identical. The overlay already encodes this — `environments/prod/patch-hubenvironment.yaml`
deliberately does **not** patch `spec.domain`.

**Emitted artifacts** (all under `manifests/**/generated/`, all committed):

| Artifact | Feeds |
|---|---|
| `hub-core-services/ingress/generated/hostnames-patch.yaml` | C1 |
| `hub-core-services/ingress/generated/certificates-patch.yaml` | C2 (public only) |
| `hub-core-services/identity/generated/issuer-values.yaml` | C3 |
| `hub-core-services/api-gateway/generated/jwt-patch.yaml` | C4 |
| `hub-core-services/security/generated/infisical-*-patch.yaml` | C5 (**exists**) |
| `hub-core-services/crossplane/generated/composition-input-patch.yaml` | C6 |
| `environments/base/generated/oauth-redirects-patch.yaml` | C7 |
| `environments/base/generated/hub-endpoints-config.yaml` | C8 (env vars) |

**Mechanism per class.** Four distinct consumption paths; each generated artifact must match its
consumer's mechanism:

1. Kustomize `patches:` — C1, C2, C4, C5, C7 (the pattern `security/kustomization.yaml` already uses)
2. Helm values — C3 (`ory-hydra`, `ory-kratos` are Helm apps; needs a generated values file or an
   appset `helmValues` entry, **not** a kustomize patch)
3. Container env vars — C3 partial (`kratos-ui`, `auth-proxy` deployments), C8
4. Crossplane composition input — C6

---

## 4. Deliverable 3 — Ordered migration

Ordering is a correctness requirement. DNS ⇄ TLS ⇄ issuer ⇄ OIDC discovery ⇄ JWT form one
consistency contract; a partial migration produces misleading failures — a host that resolves and
serves a valid certificate while its identity layer still points at dev.

**WS4-0 — Contract + validation. ✅ COMPLETE (2026-08-24).**

- `DOMAIN` retired as an authority. Removed from `environments/base/hub-bootstrap-config.yaml`, the
  dev/stg/prod `patch-config.yaml` overlays, and the operator env wiring in
  `hub-operator/config/manager/manager.yaml`. It was a dangling wire: mapped into the operator
  container but read by no Go code, exactly as ADR-051 recorded. All three overlays still build and
  no longer render the key.
- **C7 cross-environment OAuth callback fixed.** `environments/base/hubenvironment.yaml` declared
  `domain: nutgraf.in` while hardcoding `https://mcp.dev.nutgraf.in/callback`. dev overrides oauth to
  localhost and stg had no oauth patch, so the dev callback was inherited **only by stg and prod** —
  a production authorization code would have redirected to a dev-controlled host. Base now uses the
  apex; stg gained its own patch. Rendered result: dev `localhost:8080`, stg `mcp.stg.nutgraf.in`,
  prod `mcp.nutgraf.in`.
- **Env-as-zone enforced structurally.** Two CEL rules on `HubEnvironmentSpec` require a non-prod
  `environmentSlug` to own a leftmost-labelled zone, and prod to use the apex unlabelled. Deliberately
  apex-agnostic — encoding the apex in the API type would relocate the literal rather than remove it.
  Verified against the live API: `nutgraf.in`+stg REJECTED, `dev.nutgraf.in`+prod REJECTED,
  and dev/stg/prod valid combinations accepted.
- Partial G3 holds already: the stg and prod overlays render **zero** dev hostnames.

Dead code removed in the same step: `manifests/hub-core-services/hub-environment/` was an
unreferenced byte-identical duplicate of the authority object — the `hub-environment` Application
points at `manifests/environments/<slug>`, and no kustomization referenced the directory. Deleted
rather than maintained, so the authority object exists in exactly one place. Also deleted an
abandoned `external-dns/kustomization.yaml` that patched a `generated/external-dns-patch.yaml` which
was never created; it failed `kustomize build` outright and would have broken `platform-external-dns`
on sync. It is superseded by the `publish-status-address` fix, which removes the need for a
generated external-dns patch at all.

**WS4-1 — Derivation, dry-run.** Teach the hub-CLI the derivation and emit artifacts to a scratch
path. Diff against current literals. **Expected: byte-identical for dev.** Any diff is either a bug
or an undocumented exception (expect `victoriametrics.hub.`, `waypoint.`, `mcp.`). **No live change.**

**WS4-2 — C8, compiled defaults.** Plumb config into `auth-proxy` and `hub-cli`; remove hardcoded
URLs; update fixtures. Do this **before** C3/C4 — otherwise a correctly-rendered manifest is
overridden by a binary default and the failure looks like a rendering bug.

**WS4-3 — Identity + gateway together (C3 + C4 + C5).** Hydra/Kratos issuers, AgentGateway JWT
issuer/audience, Infisical endpoint in **one** change. Non-negotiable: Hydra's `issuer`, the OIDC
discovery document, and AgentGateway's expected `issuer` must agree at every instant. Expect
re-authentication; existing tokens carry the old issuer.

**WS4-4 — Ingress + public TLS (C1 + C2).** Only once identity is domain-aware. Rename
`dev-certificates.yaml` (the filename is itself the defect). Certificate SANs and Ingress hosts move
together or ACME fails. Leave `argocd-principal/certificates.yaml` on the fleet PKI.

**WS4-5 — Crossplane (C6).** Last, and only after C5 is stable, because the composition consumes the
Infisical endpoint. Pass the value in explicitly. Verify no cross-boundary regression: a spoke must
not acquire a hub-domain literal.

**Rollback.** WS4-0/1 are inert. WS4-2 reverts by binary. WS4-3/4 revert by reverting the generated
artifact and re-syncing — the pre-WS4 literals remain in Git history.

---

## 5. Deliverable 4 — Validation gates

**G1 — no literals in *authored* shared hub manifests.** Per ADR-051, retirement is judged against
authorship: generated artifacts are the applied form and legitimately carry resolved hostnames, so
`/generated/` is excluded here and covered by G7 instead.

```bash
grep -rnE '\.(dev|stg)\.nutgraf\.in' manifests/hub-core-services/ \
  | grep -v '/generated/' | grep -vE '^\s*#'
# must return zero
```

**G2 — no literals compiled into binaries:**

```bash
grep -rnE '[a-z-]+\.(dev|stg)\.nutgraf\.in' internal/ operators/ \
  --include='*.go' | grep -v _test.go
# must return zero
```

**G3 — synthetic stg hub renders zero dev references.** Render with `spec.domain: stg.nutgraf.in`
and assert no `dev.nutgraf.in` in the output. This is the gate that proves the defect class is gone,
and the one worth writing first.

**G4 — identity consistency.** Assert equality across: Hydra `issuer` ≡ OIDC discovery `issuer` ≡
AgentGateway expected `issuer`; Kratos allowed origins ⊇ rendered console/auth hosts; Certificate
SANs ≡ Ingress hosts.

**G5 — hub/spoke boundary.** No hub-domain literal in `manifests/spoke/**`; no spoke ACME/Gateway
host in any hub generated artifact.

**G6 — prod apex exception.** `spec.domain` unset for prod resolves to the apex, and the derivation
emits `api.nutgraf.in`, not `api..nutgraf.in`.

---

**G7 — generated hostnames are mechanically derived.** Regenerate every artifact from
`HubEnvironment.spec.domain` and diff against what is committed. Any difference means a hostname was
hand-edited into a generated artifact rather than derived, which is the failure mode the
authored/generated distinction exists to catch. This gate is what makes G1's exclusion of
`/generated/` safe.

## 6. ADR position

The governing decision is recorded in ADR-051's *base-domain authority and the derivation boundary*
addendum (2026-08-24). This plan implements it and adds no decisions of its own:

- `HubEnvironment.spec.domain` is the sole authoritative base-domain value; the `DOMAIN` bootstrap
  key is retired as an authority and is not a permitted fallback.
- Hub public endpoints are derived into generated GitOps artifacts, which are the applied form.
- Derivation sits at the Day-0 boundary and its output is an immutable Day-1 input — which is also
  why a continuously running controller cannot be the renderer.
- ArgoCD remains the reconciler; the Ownership rows were extended, not reassigned.
- Hub derivation must not emit tenant hostnames.

A second addendum of the same date discharges the 2026-08-23 DNS-provider contingency and supersedes
both its "no automated publisher exists for hub hostnames" statement and ADR-046 §20.3.

## 7. Open questions

1. `victoriametrics.hub.<domain>` — normalise, or record as an intentional exception?
2. `waypoint.<domain>` (`ory-kratos/values.yaml:27`) — live, or dead config to delete?
3. `mcp.<domain>` (C7) — belongs to the hub's MCP resource-server endpoint per ADR-051; confirm it
   is hub-owned and not spoke-terminated.
4. C3 mechanism — generated Helm values file, or appset `helmValues`? The latter is closer to the
   `hubIngressAddress` precedent but puts a second value source in the appset.
5. Sequencing against capacity — WS4-3/4 require reliable ArgoCD sync. `argocd-repo-server` is
   currently `BestEffort` with 113 restarts and cannot generate manifests reliably. **WS4-3 onward
   should not start until hub node capacity is resolved.**
