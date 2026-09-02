# ADR-058: OIDC Group-Based Kubernetes RBAC

**Date:** 2026-09-01
**Status:** Accepted

## Context

Headlamp (the Kubernetes dashboard) authenticates users via OIDC and forwards the resulting token to the API server. The API server extracts claims from the token and matches them against RBAC bindings to determine authorization. Currently, the OIDC token carries `email`, `role`, and `tenant_id` claims but no `groups` claim. The API server is configured with `--oidc-groups-claim=groups`, so it looks for a `groups` claim that does not exist. Without a matching group, users receive 403 Forbidden on all resource requests.

The platform's identity provider (Ory Kratos) stores user metadata in `metadata_public`, which the auth-proxy reads during the OIDC consent flow and injects into the token session. The auth-proxy already injects `email`, `role`, and `tenant_id` but not `groups`. The Kratos identity schema does not include a `groups` field.

Group-based RBAC is the standard Kubernetes pattern for mapping organizational roles to cluster permissions. It decouples individual users from RBAC bindings, so adding or removing a user from a group automatically grants or revokes access without modifying RBAC manifests.

## Decision

### Group assignments are stored in Git as the source of truth

A ConfigMap (`identity-user-groups`) in the `platform-identity` namespace stores the mapping between user emails and group names. This follows the established pattern where tenant configuration lives in Git and is reconciled to the cluster by ArgoCD.

The ConfigMap is the single source of truth for group assignments. Editing it in Git and committing triggers ArgoCD sync, which applies the ConfigMap to the cluster. No manual Kratos API calls are needed.

### The control plane syncs groups to Kratos on bootstrap

The control plane's `bootstrapAdmin()` method reads the `identity-user-groups` ConfigMap on every startup and calls `SetUserGroups()` for each user. This ensures group assignments in Kratos stay in sync with the Git source of truth, even after cluster recreation or database restoration.

This is a bootstrap-time reconciliation, not a continuous controller. Group assignments change infrequently (on admin action), so startup-time sync is sufficient and avoids the complexity of a watch-based controller.

### The auth-proxy injects groups into the OIDC token

The auth-proxy consent handler reads `groups` from Kratos `metadata_public` and injects it into both the ID token and access token session objects. This makes the `groups` claim available to the API server for RBAC evaluation.

### Kubernetes RBAC uses Group subjects

The `headlamp-viewer` ClusterRoleBinding includes a `Group: platform_admins` subject alongside the existing ServiceAccount. Users in the `platform_admins` group receive the `headlamp-viewer` permissions (get, list, watch on all resources) when they authenticate via OIDC.

### Groups flow through metadata_public, not traits

Groups are platform-assigned metadata, not self-declared user attributes. They belong in `metadata_public`, which self-service flows cannot write, consistent with the existing pattern for `role` and `tenant_id` (ADR-010, ADR-057).

## Ownership

Per ADR-039.

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| User group assignments | Git (identity-user-groups ConfigMap) | platform | ArgoCD sync + control plane bootstrap | auth-proxy, API server | Day-1+ |
| Group claims in OIDC token | Kratos metadata_public | platform | auth-proxy consent handler | Headlamp, API server | Day-1+ |
| Kubernetes RBAC bindings | Git (ClusterRoleBinding manifests) | platform | ArgoCD sync | API server | Day-1+ |

## Consequences

### Positive

- Adding or removing a user from a group is a Git edit, not a cluster operation.
- Group assignments survive cluster recreation because they are stored in Git.
- The auth-proxy and Kratos client changes are backward-compatible: existing tokens without `groups` continue to work (the claim is absent, not invalid).
- The pattern extends naturally to additional groups (e.g., `platform_viewers`) by adding entries to the ConfigMap and corresponding ClusterRoleBindings.

### Negative

- Group assignments require a control plane restart to take effect (bootstrap-time sync, not continuous).
- The `identity-user-groups` ConfigMap contains email addresses in plaintext, which is a minor information disclosure risk within the cluster.
- Adding a new group requires creating both a ConfigMap entry and a ClusterRoleBinding, which are in different manifests.

## Impact

- **Extends the OIDC token format.** The `groups` claim is added to ID and access tokens. Clients that decode tokens will see a new field; this is additive and non-breaking.
- **Amends the auth-proxy consent handler.** The session object now includes `groups` alongside `email`, `role`, and `tenant_id`.
- **Amends the Kratos identity flow.** The `groups` field is read from `metadata_public` during consent, consistent with the existing pattern for `role` and `tenant_id`.
- **Requires the `identity-user-groups` ConfigMap to be applied** before group-based RBAC takes effect. The ConfigMap is included in the identity kustomization and applied by ArgoCD.
- **Requires the control plane to have cluster read access** to the `platform-identity` namespace for reading the ConfigMap during bootstrap.

## References

- ADR-010: Tenant User Management Pattern
- ADR-039: Platform Ownership Model
- ADR-050: Tenant Authentication via AgentGateway
- ADR-057: Tenant User Management
