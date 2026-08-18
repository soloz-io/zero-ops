# Infisical PKI Authority — Design & Evidence Record

## 1. Topology (verified)

```
infisical.nutgraf.in  (external-dns, declared in manifests/hub-core-services/infisical/ingress.yaml)
   -> Hetzner CCM LoadBalancer a2de0f69f13004789a187611f2328a20  (lb11, targets mb7w8 + wh2pr)
   -> ingress-nginx-controller Service (EXTERNAL-IP 65.109.41.89), nginx pod 10.244.2.107
   -> svc infisical-standalone-infisical (platform-security)  [single endpoint 10.244.1.80:8080]
   -> pod .80  infisical/infisical:v0.160.11  (1 replica, node mb7w8)

In-cluster path (hub issuer + operators):
   -> svc infisical-standalone-infisical.platform-security.svc:8080 -> pod .80

DB: CNPG Postgres via PgBouncer (DB_HOST/DB_USER/... from infisical-postgres-connection)
Redis + encryption: infisical-secrets (REDIS_URL, ENCRYPTION_KEY, AUTH_SECRET)
```

Both ClusterIssuers, the hub-operator, and the spoke-identity-operator
authenticate against this one instance. All lifecycle is GitOps-owned
(`manifests/hub-core-services/infisical/` via ArgoCD).

## 2. Incident root cause (2026-08-18/19)

infisical-issuer v0.2.0 reads `secretData["clientSecret"]` hardcoded
(`internal/issuer/signer/signer.go`) and never consults `secretRef.key`. The
spoke-identity-operator's CRS renderer emitted `cert-manager/infisical-auth`
with only `client-id`/`client-secret` -> issuer logged in with an empty client
secret -> `401 Invalid credentials` x3 -> identity `94a8cbac` lockout ->
perpetual `401 "temporarily locked"` (issuer ~7s retries re-arm the 300s
lockout) -> all spoke CertificateRequests pending.

Fix: (a) live: add `clientSecret` data key to spoke `cert-manager/infisical-auth`;
(b) permanent: operator renders `clientSecret` in both secret copies
(commit `fix(spoke-identity): render clientSecret key in infisical-auth CRS
wrapper`). All spoke CRs READY.

## 3. Key evidence chain

| Claim | Evidence |
|---|---|
| Single backend | reqIds for external logins (`req-IjSaQEZL3QSird`, `req-kZahAd7BTOQYFx`), spoke-issuer login (`req-1QUO5eMtOsafi5`), sign tests (`req-DuTlXSH0Vo58c3`, `req-YHWap7hI6Rqckb`) all in pod .80 logs |
| Same identity state on both paths | dual probe (external + svc) -> identical identityId `94a8cbac-7abe-42a2-8bd0-76a6957b6ef3`, both 200 |
| Shared lockout state | admin clear-lockout unlocked both paths; issuer re-locked both |
| 65.109.41.89 = LB, not VM | hcloud: LB id `a2de0f69...`, created 2026-08-14T07:17:47Z, targets servers 162078384/162078419; svc EXTERNAL-IP = same IP; DNS from Mac/spoke/in-cluster resolves identically |
| 422/500 = test artifacts | `{}` body fails schema (needs projectId/ttl/csr); fake CSR fails ASN.1 at `pki-templates-service.ts:440`; issuer v0.2.0 source sends `{csr, ttl, projectId, certificateTemplateName}` |

## 4. Decisions

1. Single authority: pod .80. No topology change performed or needed.
2. Public DNS kept; ingress front is GitOps-owned.
3. HA (replicas>1) deferred to a separate track.
4. Wave 2 CNPG cutover: NO-GO (lifecycle hardening incomplete).
5. Phase 2 lockout self-heal: resume, target the single authority (see
   `.kiro/specs/pending/identity-lockout-self-heal/`).

## 5. Residuals (open, non-blocking)

- `10.244.1.176`: in-cluster workload, ~1 login/sec (all 200), hits
  `/api/v2/folders`, `/api/v3/secrets/raw`, signs non-existent template
  `signing-keys` (404). No Cilium endpoint on any agent. Tracked separately.
- Historical 401 string: closed; re-open only on recurrence.