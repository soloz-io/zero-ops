I did a web review of GitOps + multi-cluster secrets patterns used with Argo CD, SOPS, and KSOPS, and there is a fairly clear industry-idiomatic architecture.

Your intuition is already very close. The missing pieces are mostly about key scope and where decryption happens.

Below is the actual pattern used by most serious GitOps platforms.

⸻

1. First principle: “Secrets decrypt where they run”

The most important rule in GitOps secret design:

Secrets should only be decrypted inside the cluster that consumes them.

This avoids leaking secrets into CI/CD systems or management clusters.

Typical GitOps flow:
	1.	Secret encrypted locally with SOPS
	2.	Stored encrypted in Git
	3.	GitOps controller fetches repo
	4.	Decrypt happens inside the cluster
	5.	Kubernetes Secret applied

That is the standard model with SOPS + GitOps.  ￼

⸻

2. Industry multi-cluster pattern (Hub + Spoke GitOps)

For multi-cluster platforms the typical architecture is:

                    Management Cluster
                      (Platform Hub)

            ArgoCD (control plane workloads)
            Crossplane
            Platform services
                   |
                   |
         ---------------------------------
         |                               |
     Tenant Cluster A                Tenant Cluster B
         |                               |
   ArgoCD agent + KSOPS            ArgoCD agent + KSOPS

Key points:
	•	Management cluster controls infrastructure
	•	Tenant clusters run their own GitOps agent

This is the most common pattern used by platform teams.

⸻

3. Key architecture (THIS answers your biggest question)

The idiomatic model is:

Platform keys

Used only for platform infrastructure.

Stored in:

management cluster

Example secrets:
	•	cloud credentials
	•	Crossplane provider credentials
	•	cluster bootstrap secrets

⸻

Tenant keys

Each tenant/environment gets its own Age keypair.

Example:

tenant-a-age-key
tenant-b-age-key
tenant-c-age-key

Industry pattern:

1 tenant/environment = 1 encryption key

This is common practice for environments like dev/staging/prod as well.  ￼

⸻

4. Where those keys live

Management cluster

Stores:

tenant-age-key
tenant-cluster-kubeconfig
cloud creds

Purpose:
	•	provisioning
	•	bootstrap

⸻

Tenant cluster

Also stores:

tenant-age-key

Purpose:
	•	decrypt application secrets

Yes — the same tenant key is copied to the tenant cluster.

This is the missing lifecycle step in your spec.

⸻

5. The missing lifecycle (what Crossplane must do)

Correct lifecycle during cluster creation:

Step 1 — tenant created

Management cluster generates key:

age-keypair

Stored as:

Secret
tenant-age-key


⸻

Step 2 — cluster provisioned

Crossplane provisions cluster.

⸻

Step 3 — bootstrap GitOps agent

Cluster receives:

ArgoCD
KSOPS
tenant-age-key

Bootstrap usually via:
	•	Helm chart
	•	bootstrap manifests
	•	cluster template

⸻

Step 4 — GitOps starts

Tenant cluster ArgoCD syncs tenant repo.

KSOPS decrypts secrets locally.

⸻

6. Where tenant application secrets live

Industry standard:

tenant application repo

NOT the platform repo.

Example:

tenant-app-repo
   apps/
   kustomize/
   secrets.enc.yaml

Encrypted with:

tenant-age-key

Decrypted by:

tenant cluster ArgoCD


⸻

7. Platform vs tenant secret boundary

Your two-tier idea is exactly correct, but the boundaries look like this:

Secret Type	Repo	Key	Decrypted In
Platform infra secrets	platform repo	platform key	management cluster
Tenant infra secrets	tenant control plane repo	tenant key	management cluster
Tenant app secrets	tenant app repo	tenant key	tenant cluster

So the same tenant key can decrypt both infra + app secrets.

But:
	•	infra secrets used by hub
	•	app secrets used by spoke

⸻

8. Does tenant cluster run its own ArgoCD?

Industry answer:

Yes — usually.

Two patterns exist.

⸻

Pattern A — central ArgoCD (simple)

management cluster ArgoCD
   |
   deploys to all clusters

Pros
	•	simple

Cons
	•	security risk
	•	large blast radius

⸻

Pattern B — per-cluster ArgoCD (recommended)

management cluster
   |
   bootstrap
   |
tenant cluster
   |
   ArgoCD agent

Pros
	•	isolation
	•	scale
	•	security

Most platforms use this.

⸻

9. Why External Secrets is often used later

At small scale teams use:

SOPS + Git

At enterprise scale many teams switch to:

External Secrets Operator

because Git encryption does not scale well with many teams and clusters.  ￼

In that model:

Git stores references
Secrets stored in Vault/AWS SM

But for your platform stage SOPS is fine.

⸻

10. Final answers to your questions

Q1 — Does tenant cluster get separate Age key?

Yes.

Industry pattern:

1 tenant / environment = 1 keypair

Key is copied to tenant cluster during bootstrap.

⸻

Q2 — Does tenant cluster run its own ArgoCD?

Best practice:

Yes

Tenant cluster runs:

ArgoCD
KSOPS

Management cluster only manages platform.

⸻

Q3 — Where are tenant application secrets stored?

Recommended:

tenant app repo

Encrypted with:

tenant age key

Decrypted by:

tenant cluster ArgoCD


⸻

11. The final idiomatic architecture

This is what your platform should look like.

                    Management Cluster
                       (Hub)

                ArgoCD + Crossplane
                platform-age-key
                tenant-age-keys
                       |
                       |
        --------------------------------------
        |                                    |
  Enterprise Cluster                    Shared Cluster
        |                                    |
   ArgoCD + KSOPS                       ArgoCD + KSOPS
        |                                    |
  tenant app repo                      namespace tenants
        |
   secrets encrypted with tenant key


⸻

12. Your architecture maturity

Honestly:

Your Zero-Ops architecture is already very close to the industry model.

You just need to explicitly define:

1️⃣ tenant key bootstrap
2️⃣ tenant ArgoCD installation
3️⃣ key rotation lifecycle

Those are the missing pieces.