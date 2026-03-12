How does secret management work for enterprise clusters or starter/shared clusters provisioned by the Management Cluster? Is there a lifecycle defined?

Short answer: The lifecycle should exist, but your current spec (from what you showed) only partially defines it. The provisioning pieces are defined via Crossplane compositions, but secret propagation and rotation lifecycle is not fully described yet.

Below is the correct lifecycle model most platforms implement, which fits your architecture.

⸻

1. Secret lifecycle in the Management Cluster (Hub)

The Management Cluster is the root of trust.

It owns:
	•	Platform Age key
	•	Per-tenant Age keys
	•	Cloud credentials
	•	Git credentials
	•	Cluster kubeconfigs

Typical structure:

management-cluster
 ├─ namespace: platform-system
 │   └─ platform-age-key
 │
 ├─ namespace: tenant-foo
 │   ├─ foo-age-key
 │   ├─ foo-db-credentials
 │   └─ foo-s3-credentials
 │
 └─ namespace: tenant-bar
     └─ bar-age-key

ArgoCD + KSOPS run here and decrypt tenant manifests.

⸻

2. Enterprise cluster secret flow (BYOC)

Enterprise clusters are separate clusters in the tenant cloud account.

Secret flow normally looks like this:

Management Cluster (Hub)
        |
        | Crossplane provisions cluster
        v
Enterprise Cluster (Spoke)
        |
        | ArgoCD agent installed
        v
Tenant workloads

Secrets propagate using one of these patterns.

⸻

Pattern A — GitOps rehydration (most common)

Secrets are encrypted in Git with SOPS/KSOPS.

Flow:

1. Tenant secret encrypted with tenant Age key
2. Stored in Git
3. Enterprise cluster ArgoCD pulls repo
4. KSOPS decrypts using tenant Age key
5. Secret applied to cluster

This requires the Age key to exist inside the enterprise cluster.

Lifecycle:

Management Cluster
  └ generate tenant Age key
  └ store secret
  └ push encrypted key to enterprise cluster bootstrap

So bootstrap includes:

argocd
ksops
tenant-age-key


⸻

Pattern B — Secret replication from hub

Less common but simpler:

Management Cluster
  |
  | Secret Sync Controller
  v
Enterprise Cluster

Tools often used:
	•	External Secrets Operator
	•	Secrets Store CSI Driver

But in a BYOC architecture, many platforms avoid direct hub → spoke replication.

⸻

3. Enterprise cluster bootstrap lifecycle

The real missing piece in your spec is likely cluster bootstrap steps.

Typical sequence:

Step 1 — Crossplane provisions cluster

Crossplane
   |
   | CAPH
   v
Hetzner cluster created


⸻

Step 2 — kubeconfig stored in management cluster

Crossplane writes:

Secret
type: connection
data: kubeconfig


⸻

Step 3 — ArgoCD registers cluster

Management ArgoCD registers the new cluster.

argocd cluster add

Or via declarative cluster secret.

⸻

Step 4 — bootstrap apps installed

Usually via app-of-apps pattern.

Cluster receives:

argocd
ksops
tenant-age-key
base-platform


⸻

Step 5 — workloads deploy

Now GitOps begins.

⸻

4. Shared cluster (Starter tier)

Shared cluster secret model is different because it is multi-tenant namespace isolation.

Architecture:

Shared Cluster
 ├─ tenant-a namespace
 ├─ tenant-b namespace
 └─ tenant-c namespace

Secrets are scoped per namespace.

Example:

tenant-a
  └ age-key
  └ db-secret

tenant-b
  └ age-key
  └ db-secret

Access enforced with:
	•	RBAC
	•	NetworkPolicy
	•	DB RLS

⸻

5. Full secret lifecycle (recommended)

Your platform should define lifecycle like this:

Creation

tenant created
  ↓
age key generated
  ↓
stored in management cluster
  ↓
backup to S3


⸻

Distribution

For enterprise:

bootstrap cluster
  ↓
install argocd + ksops
  ↓
inject tenant age key

For shared:

create namespace
  ↓
create tenant age key secret


⸻

Rotation

Rotation flow should be defined.

generate new age key
  ↓
re-encrypt repo secrets
  ↓
update cluster secret


⸻

Deletion

Tenant deletion should trigger:

revoke keys
delete namespace/cluster
remove secrets
archive S3 backup


⸻

6. The real missing part in your spec

From what you shared, these lifecycle steps are not explicitly documented:
	1.	Age key injection into enterprise cluster
	2.	ArgoCD cluster bootstrap
	3.	Secret rotation workflow
	4.	Tenant deletion secret cleanup

These are critical in production platforms.

⸻

7. Recommended architecture summary

Your architecture should end up like:

                    Management Cluster
                 (Hub / Mothership)

      Crossplane | ArgoCD | KSOPS | Ory | API
                    |
                    |
        -----------------------------------------
        |                                       |
   Shared Cluster                          Enterprise Cluster
   (starter tenants)                       (BYOC tenant)
        |                                       |
   namespaces                              full cluster
        |                                       |
   tenant secrets                           tenant secrets

Keys originate in hub, but exist in spoke for decryption.

⸻

✅ Your intuition about S3 vs Kubernetes Secrets is correct.
S3 should not be in the KSOPS runtime path.
