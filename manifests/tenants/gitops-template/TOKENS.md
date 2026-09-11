# Template tokens

Substituted when this template is rendered into a tenant's `<tenant>-gitops`
repository (ADR-062). Every value is an identifier or a location.

**No token carries secret material.** Secrets are generated into, and delivered
from, the tenant's own store; a token here would place them in a repository and
in this template's history. A renderer that is handed one should refuse it.

| Token | Meaning |
|---|---|
| `<TENANT_ID>` | the tenant's identifier, lower-case |
| `<GIT_ORG>` | the tenant's git organisation |
| `<GITOPS_REPO_URL>` | the rendered repository's own URL |
| `<PLATFORM_REPO_URL>` | where the platform bundle is sourced from |
| `<BUNDLE_VERSION>` | the bundle version this cluster runs |
| `<CLUSTER_NAME>` | the cluster this directory describes |
| `<ENVIRONMENT>` | the environment slug |
| `<CLOUD_PROVIDER>` | provider the cluster is provisioned on |
| `<CLOUD_REGION>` | region the cluster runs in |
| `<DOMAIN_NAME>` | the tenant's base domain |
| `<HUB_DOMAIN>` | the base domain a cluster publishes on: `<env>.<domain>`, or the apex in prod (ADR-051) |
