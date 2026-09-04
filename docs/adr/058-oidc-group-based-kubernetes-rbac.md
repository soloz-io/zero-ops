# ADR-058: OIDC Group-Based Kubernetes RBAC

**Date:** 2026-09-01
**Status:** Superseded by ADR-059 (2026-09-03)

> **Superseded.** The mechanism below — an `identity-user-groups` ConfigMap
> reconciled by the hub-operator into Kratos `metadata_public.groups`, injected
> into the token by the auth-proxy — was removed when the platform moved to
> Zitadel. Every component in that chain is gone.
>
> The decision it rests on does not survive the provider change. Zitadel models
> authorisation as project roles held within an Organization, and an Organization
> owns the user, so the tenant-less platform identity this ADR works around
> cannot be expressed. Reproducing the pipeline against Zitadel would be building
> a second model beside the provider's own.
>
> What remains valid is the goal: Kubernetes authorises on users and groups, so
> a `groups` claim must reach the API server. ADR-059 keeps that requirement and
> moves the mapping to the edge, rather than into a bespoke reconciler; ADR-060
> records the provider model that replaces this one and states that the
> requirement is currently unmet.

## Context

Headlamp (the Kubernetes dashboard) authenticates users via OIDC and forwards the resulting token to the API server. The API server extracts claims from the token and matches them against RBAC bindings to determine authorization. Currently, the OIDC token carries `email`, `role`, and `tenant_id` claims but no `groups` claim. The API server is configured with `--oidc-groups-claim=groups`, so it looks for a `groups` claim that does not exist. Without a matching group, users receive 403 Forbidden on all resource requests.

The platform's identity provider (Ory Kratos) stores user metadata in `metadata_public`, which the auth-proxy reads during the OIDC consent flow and injects into the token session. The auth-proxy already injects `email`, `role`, and `tenant_id` but not `groups`. The Kratos identity schema does not include a `groups` field.

The opensbt control plane manages identity provisioning via the `IAuth` interface, which provides `SetUserGroups()` for managing group assignments in Kratos `metadata_public`.

Group-based RBAC is the standard Kubernetes pattern for mapping organizational roles to cluster permissions. It decouples individual users from RBAC bindings, so adding or removing a user from a group automatically grants or revokes access without modifying RBAC manifests.

## Decision

### Group assignments are stored in Git as the source of truth

A ConfigMap (`identity-user-groups`) in the `platform-identity` namespace stores the mapping between user emails and group names. This follows the established pattern where tenant configuration lives in Git and is reconciled to the cluster by ArgoCD.

The ConfigMap is the single source of truth for group assignments. Editing it in Git and committing triggers ArgoCD sync, which applies the ConfigMap to the cluster. No manual Kratos API calls are needed.

### The hub-operator provides continuous reconciliation

The `UserGroupsReconciler` in the hub-operator watches the `identity-user-groups` ConfigMap and reconciles Kratos `metadata_public.groups` to match the desired state. This is event-driven reconciliation: when the ConfigMap changes, the controller immediately syncs group assignments to Kratos.

The controller treats the ConfigMap as desired state and reconciles toward it on every change. This ensures new users receive their groups without waiting for a control-plane restart, and existing users get group changes automatically.

### The control plane provides bootstrap reconciliation as a safety net

The opensbt control plane's `bootstrapAdmin()` method reads the `identity-user-groups` ConfigMap on every startup and calls `SetUserGroups()` for each user. This provides recovery after cluster recreation or database restoration, and serves as defense-in-depth alongside the hub-operator's continuous reconciliation.

### Deletion semantics: absent users lose groups

If a user's email is removed from the ConfigMap, reconciliation removes their groups from `metadata_public`. This prevents stale authorization state from accumulating. The ConfigMap is treated as the complete set of desired group assignments.

### Reconciliation is idempotent

`SetUserGroups()` compares desired groups against current groups before writing. If groups already match, no write is performed. This prevents unnecessary API calls and ensures safe re-reconciliation.

### The auth-proxy injects groups into the OIDC token

The auth-proxy consent handler reads `groups` from Kratos `metadata_public` and injects it into both the ID token and access token session objects. This makes the `groups` claim available to the API server for RBAC evaluation.

### Kubernetes RBAC uses Group subjects

The `headlamp-viewer` ClusterRoleBinding includes a `Group: platform_admins` subject alongside the existing ServiceAccount. Users in the `platform_admins` group receive the `headlamp-viewer` permissions (get, list, watch on all resources) when they authenticate via OIDC.

### Groups flow through metadata_public, not traits

Groups are platform-assigned metadata, not self-declared user attributes. They belong in `metadata_public`, which self-service flows cannot write, consistent with the existing pattern for `role` and `tenant_id` (ADR-010, ADR-057).

### Observability is first-class

The `UserGroupsReconciler` exposes Prometheus metrics:
- `hub_operator_usergroups_reconcile_total` — total reconciliation attempts
- `hub_operator_usergroups_sync_total` — per-user sync outcomes (success/failure)
- `hub_operator_usergroups_last_reconcile_timestamp` — timestamp of last successful reconciliation

Structured logging captures all sync operations and failures. Failed syncs are returned as errors to controller-runtime, which retries with exponential backoff.

### Supported Flows

#### Flow 1: New user created via UI, then added to group in Git

```
1. User registers via Kratos UI
   Kratos identity: { email: "newuser@example.com", metadata_public: {} }

2. Admin adds email to identity-user-groups.yaml in Git
   users:
     - email: newuser@example.com
       groups:
         - platform_admins

3. ArgoCD syncs ConfigMap to cluster

4. Hub-operator detects ConfigMap change

5. UserGroupsReconciler.Reconcile() runs
   → reads ConfigMap
   → builds desired state
   → calls SetUserGroups("newuser@example.com", ["platform_admins"])

6. SetUserGroups() finds user by email in Kratos
   → compares current groups (empty) with desired (["platform_admins"])
   → different → updates metadata_public.groups

7. Next OIDC login → JWT includes groups claim → RBAC grants access
```

#### Flow 2: User already exists, added to group later

Same as Flow 1. The user's creation method (UI, API, bootstrap) is irrelevant — `SetUserGroups` locates the user by email regardless of origin.

#### Flow 3: User removed from group

```
1. Admin removes email from identity-user-groups.yaml in Git

2. ArgoCD syncs ConfigMap to cluster

3. Hub-operator detects ConfigMap change

4. UserGroupsReconciler.Reconcile() runs
   → reads ConfigMap
   → builds desired state (user not present)
   → lists all users
   → finds user with stale groups
   → calls SetUserGroups("user@example.com", [])

5. SetUserGroups() updates metadata_public.groups to empty

6. Next OIDC login → JWT has no groups claim → RBAC denies access
```

#### Flow 4: Cluster recreation

```
1. Cluster recreated, DB wiped

2. Control plane starts, bootstrapAdmin() runs

3. syncUserGroups() reads ConfigMap from cluster (Git-backed)

4. For each user in ConfigMap:
   → creates user if not exists (bootstrapAdmin)
   → syncs groups via SetUserGroups()

5. Hub-operator also watches ConfigMap and reconciles

6. Both paths converge on the same desired state
```

#### Flow 5: ConfigMap has user not in Kratos

```
1. ConfigMap contains email not in Kratos (user hasn't registered yet)

2. SetUserGroups() calls findUserByEmail()
   → returns "user not found" error

3. Error logged, user skipped, reconciliation continues

4. User registers via UI later

5. Next ConfigMap change triggers reconciliation
   → user now exists → groups synced
```

## Ownership

Per ADR-039.

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| User group assignments | Git (identity-user-groups ConfigMap) | platform | hub-operator (continuous) + control-plane bootstrap (recovery) | auth-proxy, API server | Day-1+ |
| Group claims in OIDC token | Kratos metadata_public | platform | auth-proxy consent handler | Headlamp, API server | Day-1+ |
| Kubernetes RBAC bindings | Git (ClusterRoleBinding manifests) | platform | ArgoCD sync | API server | Day-1+ |

## Consequences

### Positive

- Adding or removing a user from a group is a Git edit, not a cluster operation.
- Group assignments take effect immediately on ConfigMap change (no restart required).
- Group assignments survive cluster recreation because they are stored in Git.
- Stale group assignments are automatically cleaned up when users are removed from the ConfigMap.
- The auth-proxy and Kratos client changes are backward-compatible: existing tokens without `groups` continue to work (the claim is absent, not invalid).
- The pattern extends naturally to additional groups (e.g., `platform_viewers`) by adding entries to the ConfigMap and corresponding ClusterRoleBindings.
- Prometheus metrics and structured logging provide full observability into reconciliation state.
- Failed syncs retry with exponential backoff via controller-runtime.

### Negative

- The `identity-user-groups` ConfigMap contains email addresses in plaintext, which is a minor information disclosure risk within the cluster.
- Adding a new group requires creating both a ConfigMap entry and a ClusterRoleBinding, which are in different manifests.
- The hub-operator requires `KRATOS_ADMIN_URL` environment variable for Kratos API access.

## Impact

- **Extends the OIDC token format.** The `groups` claim is added to ID and access tokens. Clients that decode tokens will see a new field; this is additive and non-breaking.
- **Amends the auth-proxy consent handler.** The session object now includes `groups` alongside `email`, `role`, and `tenant_id`.
- **Amends the Kratos identity flow.** The `groups` field is read from `metadata_public` during consent, consistent with the existing pattern for `role` and `tenant_id`.
- **Requires the `identity-user-groups` ConfigMap to be applied** before group-based RBAC takes effect. The ConfigMap is included in the identity kustomization and applied by ArgoCD.
- **Requires the hub-operator to have `KRATOS_ADMIN_URL` configured** for Kratos API access.
- **Requires the control plane to have cluster read access** to the `platform-identity` namespace for reading the ConfigMap during bootstrap.

## References

- ADR-010: Tenant User Management Pattern
- ADR-039: Platform Ownership Model
- ADR-050: Tenant Authentication via AgentGateway
- ADR-057: Tenant User Management
