# ADR-054: Platform Mail Server (Stalwart)

**Date:** 2026-08-26
**Status:** Proposed

## Context

The platform requires a mail server to support two functions: outbound transactional email (password resets, notifications, verification flows via Kratos) and inbound email (future tenant mailbox support, IMAP/JMAP access).

Kratos currently sends outbound email through Resend (a third-party SMTP relay), with credentials hardcoded in the Kratos configmap. This violates ADR-003, which mandates that all secrets follow the Generation → Storage (Infisical) → Delivery (ESO) → Consumption → Rotation lifecycle, and that no Kubernetes Secret or ConfigMap may serve as a System of Record for secret material.

The platform already operates a CNPG PostgreSQL cluster (`platform-db`), a Redis instance (`platform-redis`), Hetzner Object Storage (S3), cert-manager with Let's Encrypt and Infisical PKI issuers, and ExternalDNS with the Hetzner webhook provider. These existing services are the storage and infrastructure backends for a self-hosted mail server, eliminating the need for external relay dependencies.

Stalwart Mail Server is an open-source mail server written in Rust (AGPL-3.0 + SELv2) that supports SMTP, IMAP, JMAP, and ManageSieve. It can use PostgreSQL as its data store, S3 for blob storage, and Redis for lookup caching — all of which the platform already operates. The Antigenic-OSS community Helm chart (`antigenic-stalwart-helm-chart` v1.0.5) pre-configures these backends and provides cert-manager integration.

ExternalDNS on the platform watches Gateway API HTTPRoutes and creates A/CNAME records via the Hetzner webhook provider. It does not support MX, SPF, DKIM, or DMARC record types — these require direct Hetzner DNS API calls. The `cert-manager-webhook-hetzner` reference project demonstrates the `hcloud-go/v2` SDK pattern for TXT record management via `Zone.AddRRSetRecords` / `Zone.RemoveRRSetRecords`.

Hetzner Cloud Firewall rules are not codified in the platform (ADR-046 addendum 4 documents this as a known gap). The hub CLI has `hcloud-go/v2` SDK usage for firewall deletion during teardown, but no creation logic exists.

## Decision

### Stalwart is the platform mail server

Stalwart Mail Server is deployed as a Helm chart via ArgoCD ApplicationSet boundary 03 (`03-platform-services-appset.yaml`), following the same pattern as `prometheus-operator`. The Antigenic-OSS chart is used because it pre-configures PostgreSQL, Redis, S3, and cert-manager integration — matching the platform's existing stack exactly.

The deployment uses:
- **PostgreSQL** (`platform-db-pooler.platform-data.svc`) as the primary data store
- **Redis** (`platform-redis.platform-data.svc:6379`) for lookup caching (no authentication, matching platform convention)
- **Hetzner Object Storage** (`hel1.your-objectstorage.com`) for blob storage, using existing `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` from Infisical
- **Let's Encrypt** (`letsencrypt-prod` ClusterIssuer) for public TLS certificates on `mail.nutgraf.in` and `smtp.nutgraf.in`
- **Internal FTS** (skip Meilisearch) for full-text search

### Two hostnames, one server

- `mail.nutgraf.in` — incoming mail (IMAP, POP3, JMAP, Webmail), MX record target
- `smtp.nutgraf.in` — outgoing mail (SMTP submission for apps, plugins, server scripts)

### Secrets follow ADR-003

All Stalwart secrets are stored in Infisical and delivered via ESO:

| Infisical Key | Purpose |
|---|---|
| `hub-stalwart-db-username` | PostgreSQL username (`stalwart`) |
| `hub-stalwart-db-password` | PostgreSQL password (32-char hex, generated) |
| `hub-stalwart-admin-password` | Admin UI password (32-char hex, generated) |
| `hub-stalwart-kratos-smtp-user` | Kratos SMTP username (`kratos`) |
| `hub-stalwart-kratos-smtp-password` | Kratos SMTP password (32-char hex, generated) |

S3 credentials are shared with the existing `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` keys in Infisical — no new S3 keys are created. Redis requires no authentication (platform convention, network-policy-based security).

The hub-operator's `ApplicationSecretMappings` registers these keys. Generation follows the established pattern: the operator generates passwords once, uploads to Infisical, and never regenerates on reconciliation (idempotent). ESO creates Kubernetes Secrets with `creationPolicy: Owner` in the `platform-mail` namespace.

The Antigenic-OSS chart's built-in `secret.create: false` disables the chart's default secret. An ExternalSecret (`stalwart-secrets-es.yaml`) maps Infisical-stored credentials to the keys the chart expects (`ADMIN_SECRET`, `POSTGRES_PASSWORD`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `REDIS_URL`).

### Database follows ADR-023

A CNPG `Database` CR declares the `stalwart` database owned by the `stalwart` role. The role is declared in `platform-db.yaml` under `spec.managed.roles`, following the same pattern as the `infisical` role. CNPG reconciles both the role and the database continuously.

### TLS follows ADR-035

Public certificates for `mail.nutgraf.in` and `smtp.nutgraf.in` are issued by `letsencrypt-prod` via ACME HTTP-01 challenge. The cert-manager integration in the Antigenic-OSS chart creates the Certificate CR automatically when `certManager.enabled: true`.

### NetworkPolicy follows Cilium port-only pattern

The `platform-mail` namespace has a NetworkPolicy allowing:
- **Ingress**: SMTP/IMAP/Sieve ports from `platform-identity` (Kratos) and `platform-edge` (gateway)
- **Egress**: PostgreSQL (5432), Redis (6379), S3 (443), DNS (53) — all ports-only, no namespace/pod selectors, matching the Cilium pre-DNAT constraint documented across all platform NetworkPolicies

Kratos gains egress to `platform-mail` on ports 465 and 587 (SMTP/SMTPS).

### Credential rotation follows ADR-030

Stakater Reloader annotations on the Stalwart Deployment trigger rolling restarts when `stalwart-secrets` or `stalwart-db-credentials` change. DSN components (`host`, `port`, `username`, `password`, `dbname`) are projected individually in the ExternalSecret template alongside the full connection URI.

### DNS records are GitOps-managed where possible

| Record | Type | Mechanism | GitOps? |
|---|---|---|---|
| `mail.nutgraf.in` A | A | HTTPRoute → ExternalDNS | Yes |
| `smtp.nutgraf.in` A | A | HTTPRoute → ExternalDNS | Yes |
| `nutgraf.in` MX | MX | `DNSEndpoint` CRD + ExternalDNS `--source=crd` | Yes (requires CRD install) |
| `nutgraf.in` SPF | TXT | Hetzner DNS API via `hcloud-go/v2` CronJob | Partial (new component) |
| `stalwart._domainkey.nutgraf.in` DKIM | TXT | Pre-generated key or post-deploy retrieval | Constraint |
| `_dmarc.nutgraf.in` DMARC | TXT | Same CronJob as SPF | Partial (same component) |

The `hcloud-go/v2` SDK pattern for TXT record management is established by the `cert-manager-webhook-hetzner` reference project (`Zone.AddRRSetRecords` / `Zone.RemoveRRSetRecords`). A small CronJob or controller following this pattern can manage SPF, DKIM, and DMARC TXT records.

### Hetzner firewall remains a known gap

Opening ports 25, 465, and 587 for inbound SMTP requires Hetzner Cloud Firewall rules. CAPH's `HetznerCluster` v1beta1 CRD has no `firewall` field (ADR-046 addendum 4). The intended fix is extending `internal/hub-cli/` to manage firewall rules via the Hetzner SDK. This is not addressed by this ADR and remains a manual step until the CLI extension is implemented.

### DKIM key generation

Stalwart generates the DKIM key pair on first boot. The public key must be published as a DNS TXT record. Two approaches are viable:
1. **Pre-generate**: Create the key pair before deployment, store the private key in Infisical, configure Stalwart to use it
2. **Post-deploy**: Retrieve the public key from Stalwart's management API after first boot, then publish the DNS record

The chosen approach will be determined during implementation based on Stalwart's configuration capabilities.

### Kratos SMTP migration

The Kratos configmap is updated to use `smtp.nutgraf.in` instead of `smtp.resend.com`. SMTP credentials are delivered via ESO from the `stalwart-kratos-smtp-credentials` Secret, replacing the hardcoded Resend credentials.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Stalwart DB credentials | Infisical | hub-operator | ESO | Stalwart | Day-1+ |
| Stalwart admin password | Infisical | hub-operator | ESO | Stalwart admin UI | Day-1+ |
| Stalwart Kratos SMTP creds | Infisical | hub-operator | ESO | Kratos | Day-1+ |
| Stalwart database | CNPG | hub-operator | CNPG | Stalwart | Day-1+ |
| Stalwart deployment | Helm chart | ArgoCD | Antigenic-OSS chart | Platform | Day-1+ |
| TLS certificates | Let's Encrypt | cert-manager | cert-manager | Stalwart | Day-1+ |
| DNS A records | Hetzner DNS | ExternalDNS | ExternalDNS | Mail clients | Day-1+ |
| DNS MX/TXT records | Hetzner DNS | TBD (CronJob/controller) | hcloud-go/v2 | Mail clients | Day-1+ |

## Consequences

### Positive

- Outbound email is no longer dependent on a third-party relay (Resend). The platform controls its own mail infrastructure.
- Kratos SMTP credentials follow ADR-003 (Infisical → ESO → K8s Secret), closing the hardcoded credentials violation.
- Stalwart uses the platform's existing PostgreSQL, Redis, and S3 backends — no new storage infrastructure is required.
- TLS certificates are managed by cert-manager following ADR-035, with automatic renewal.
- The Antigenic-OSS Helm chart pre-configures the storage backends, reducing custom configuration.
- Credential rotation is handled by Stakater Reloader, matching the platform's existing rotation pattern.
- DNS A records are fully GitOps-managed via ExternalDNS.
- The hub-operator's `ApplicationSecretMappings` follows the established pattern for application secret registration.

### Negative

- Stalwart adds a new platform service to operate and monitor.
- DNS TXT records (SPF, DKIM, DMARC) require a new component (CronJob or controller) using the Hetzner DNS API, since ExternalDNS does not support TXT record types.
- DKIM key generation has a chicken-and-egg constraint: the key is generated on first boot, but the DNS record must exist before mail clients trust the server.
- Hetzner firewall rules for inbound SMTP (ports 25, 465, 587) remain uncodified (ADR-046 gap). Opening these ports is a manual step until the hub CLI is extended.
- The `platform-mail` namespace adds a new namespace to the platform's namespace topology.
- Stalwart's AGPL-3.0 + SELv2 license has copyleft obligations for modifications.

## Impact

- **Amends ADR-003 scope.** The Kratos SMTP credential migration from hardcoded Resend to ESO-managed Stalwart closes a tracked ADR-003 violation.
- **Requires ExternalDNS extension.** MX records require installing the `DNSEndpoint` CRD and adding `--source=crd` to the ExternalDNS deployment. This is a backward-compatible addition.
- **Requires hub-operator rebuild.** The `ApplicationSecretMappings` and `ApplicationSecretUploader` changes require a new hub-operator image.
- **Requires Hetzner firewall (manual).** Ports 25, 465, 587 must be opened manually in the Hetzner Cloud Console until the hub CLI extension is implemented.
- **Requires DNS records (manual or CronJob).** SPF, DKIM, and DMARC TXT records must be created after Stalwart is deployed and DKIM keys are generated.
- **New namespace.** `platform-mail` is added to the platform's namespace topology.
- **New ArgoCD Application.** `platform-stalwart-mail` in boundary 03 (`03-platform-services-appset.yaml`).

## References

- ADR-003: Secret Management Architecture
- ADR-014: Platform-Wide Placement Rule (worker nodeSelector)
- ADR-023: Unified Declarative Database Management (CNPG Database CR)
- ADR-030: Autonomous Credential Rotation Lifecycle
- ADR-035: Enterprise PKI and Delegated Trust via Infisical OSS
- ADR-036: Pluggable Infrastructure Provider Architecture
- ADR-039: Platform Ownership Model
- ADR-046: Hybrid Provider Home Worker (firewall gap, addendum 4)
- ADR-051: Environment DNS Naming and Public Gateway TLS
- Antigenic-OSS Stalwart Helm Chart: `ghcr.io/antigenic-oss/charts/antigenic-stalwart-helm-chart` v1.0.5
- cert-manager-webhook-hetzner reference: `reference-projects/hetzner/cert-manager-webhook-hetzner/`
