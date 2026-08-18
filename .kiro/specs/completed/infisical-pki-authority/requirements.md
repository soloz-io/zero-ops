# Infisical PKI Authority — P0 Gate Record

Date: 2026-08-19
Status: COMPLETED (P0 PASSED)
Incident ref: 2026-08-18/19 Infra Identity Lockout incident (see
docs/runbooks/backup-credential-chain-recovery.md)

## Objective

Identify and designate the single authoritative Infisical PKI backend for fleet
certificate issuance before any further issuer-topology change, per the GATE P0
sequence: authority -> owner -> lifecycle -> DB/Redis authority -> end-to-end
signing proof. Only then modify issuer topology.

## Gate P0: PASSED

The authoritative PKI service is the single in-cluster Infisical instance
(Deployment `infisical-standalone-infisical`, namespace `platform-security`,
1 replica, pod `infisical-standalone-infisical-756c6f99d8-wd67r`
`10.244.1.80`, image `infisical/infisical:v0.160.11`).

Both ClusterIssuers already target this one instance:

| Consumer | URL | Route |
|---|---|---|
| Hub issuer (`infisical-fleet-issuer`) | `http://infisical-standalone-infisical.platform-security.svc:8080` | svc -> pod .80 |
| Spoke issuer (`infisical-fleet-issuer`, spoke) | `https://infisical.nutgraf.in` | DNS -> Hetzner CCM LB -> ingress-nginx -> svc -> pod .80 |

DB: in-cluster CNPG Postgres (`infisical-postgres-connection`, TLS via
PgBouncer/CNPG CA). Redis, `ENCRYPTION_KEY`, `AUTH_SECRET`: `infisical-secrets`.
All rendered from `manifests/hub-core-services/infisical/`.

## Disposition (decisions)

1. Authoritative Infisical authority: POD .80 (single instance) — no second
   authority exists; `infisical.nutgraf.in` is a GitOps-declared ingress front
   for it (`external-dns.alpha.kubernetes.io/hostname` on `infisical-ingress`).
2. Issuer URL migration: NOT NEEDED. Spoke stays on `https://infisical.nutgraf.in`.
3. Public DNS `infisical.nutgraf.in`: KEEP.
4. Root cause of the lockout incident: spoke `cert-manager/infisical-auth`
   lacked the `clientSecret` data key that infisical-issuer v0.2.0 hardcodes
   (`secretData["clientSecret"]`, `signer.go`) -> empty-secret login ->
   401 Invalid credentials x3 -> lockout. Fixed: live secret patch + operator
   renderer fix (commit `fix(spoke-identity): render clientSecret key in
   infisical-auth CRS wrapper`). All spoke CRs READY.
5. HA for Infisical: worthwhile next architecture improvement, separate from
   this incident.
6. Workload `10.244.1.176`: investigate independently; not a second authority,
   not the locker; do not conflate with the authority question.
7. Historical 401 string `"This identity auth method is not allowed for the
   current project"`: CLOSED as unreproduced (absent from deployed binary and
   current logs). Re-open only if it returns.
8. Wave 2 (CNPG cutover): remains NO-GO while lifecycle hardening incomplete.
9. Phase 2 (lockout self-heal operator): resumed only after this record, and
   targets the confirmed single authority; deliberate-lock test must prove
   recovery against the production topology.

## Falsified hypotheses (with evidence)

- Two authorities with divergent state: FALSIFIED. Same-minute dual probe
  (external + in-cluster, same clientId/clientSecret) returned identical
  identityId `94a8cbac-7abe-42a2-8bd0-76a6957b6ef3`; reqIds from external
  probes and the spoke issuer all land in pod .80 logs; svc has a single
  endpoint (`10.244.1.80:8080`). Earlier "hub pod -> 401" was a probe artifact
  (fabricated secret value in the in-cluster curl); retried with the real
  secret: 200 on both paths.
- Independent lock state: FALSIFIED. Clear-lockout via admin path unlocked the
  external path too; the spoke issuer then re-locked it for both paths.
  422/500 sign responses were test artifacts (`{}` body vs schema; malformed
  CSR -> ASN.1 parse error at `pki-templates-service.ts:440`), both with
  reqIds present in pod .80 logs.
- 65.109.41.89 as external VM: FALSIFIED. It is Hetzner CCM LoadBalancer
  `a2de0f69f13004789a187611f2328a20` (created 2026-08-14T07:17:47Z, type lb11,
  targets hub workers mb7w8/wh2pr) = EXTERNAL-IP of the `ingress-nginx-controller`
  Service. Lifecycle owner: hcloud CCM in the hub cluster, driven by GitOps-rendered
  Service manifests. Not drift, not an orphan, not a VM.

## Acceptance evidence (end-to-end signing proof)

- Hub CR `argocd-agent-spoke-pool-hybrid-dev-01-3`: READY=True (signed via
  issuer v0.2.0 on the hub path).
- Spoke CRs `argocd-agent-client-cert-1`, `nats-leafnode-client-cert-1`,
  `alloy-client-cert-1`: READY=True after the `clientSecret` key fix.
- Identity login matrix (real secrets): external 200, in-cluster 200; locked
  state and cleared state identical across both paths.

## Entry conditions for Phase 2

- This record exists (satisfied by this file).
- Phase 2 self-heal targets the single authority (svc URL); no wildcard of
  endpoints, no per-path probing of multiple authorities.