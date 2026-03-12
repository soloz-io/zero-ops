Yes — your understanding is largely correct from a Hub-and-Spoke architecture perspective. 👍
But there are a few important clarifications and small conceptual adjustments that would make the mapping cleaner and avoid confusion later.

I’ll walk through it.

⸻

1. What the Hub-and-Spoke model actually means here

In infrastructure architecture:

Hub
	•	Central control plane
	•	Platform services
	•	Governance / identity / automation
	•	Observability

Spokes
	•	Isolated environments
	•	Workloads or tenants
	•	Controlled by the hub but operationally separate

In Kubernetes platform engineering this normally means:

                   +----------------------+
                   |   Management Cluster |
                   |      (Hub)           |
                   |----------------------|
                   | Crossplane           |
                   | ArgoCD               |
                   | Identity (Ory)       |
                   | Platform API         |
                   | Observability        |
                   +----------+-----------+
                              |
             -----------------------------------------
             |                  |                    |
        +----v-----+      +-----v-----+        +-----v-----+
        | Tenant   |      | Tenant    |        | Tenant    |
        | Cluster  |      | Cluster   |        | Cluster   |
        | (Spoke)  |      | (Spoke)   |        | (Spoke)   |
        +----------+      +-----------+        +-----------+


⸻

2. Mapping your glossary to Hub-Spoke

HUB

These are hub components

Your Term	Role
Management Cluster	Hub
Mothership	Hub (alias)
Shard	Hub instance (multi-region hub)

So this part is correct.

Your architecture actually describes Multi-Hub Hub-Spoke.

 shard-us-1
 shard-eu-1
 shard-ap-1

Each shard manages its own spokes.

⸻

3. Spokes in your model

Your spokes are:

Enterprise

Component	Hub-Spoke role
Enterprise Tenant Cluster	Spoke

Management Cluster (Hub)
        |
        +---- Tenant Cluster A
        +---- Tenant Cluster B
        +---- Tenant Cluster C

This is classic hub-spoke.

⸻

Starter tier

Starter tenants are slightly different.

Hub
  |
  +--- Shared Cluster (spoke)
           |
           +--- tenant namespace
           +--- tenant namespace

So the Shared Cluster is the spoke, not each tenant.

Component	Hub-Spoke role
Shared Cluster	Spoke
Starter Tenant Namespace	workload inside spoke

This is still valid architecture.

⸻

4. Control Plane vs Data Plane DBs

These are not hub-spoke entities.

They are tenant resources provisioned by the hub.

Meaning:

Hub
 |
 +---- Spoke Cluster
          |
          +---- ControlPlane DB
          +---- DataPlane DB

So they are part of the spoke environment.

Your definitions here are also correct.

⸻

5. Composition A / B mapping

Your Crossplane Compositions map perfectly to hub-spoke provisioning.

Composition A

Hub provisions resources inside existing spoke

Hub
 |
 +--- Shared Cluster
        |
        +--- namespace
        +--- db

Composition B

Hub provisions new spoke

Hub
 |
 +--- new Tenant Cluster
        |
        +--- DBs
        +--- services

This is actually a very clean pattern.

⸻

6. One subtle improvement I’d suggest

This sentence:

Shared Cluster: A dedicated CAPI cluster, separate from the management cluster

Correct — but from hub-spoke language it should explicitly say:

Shared Cluster = spoke cluster hosting multiple tenants

Example improvement:

Shared Cluster
A spoke cluster provisioned by the Management Cluster to host multiple
Starter-tier tenants as isolated namespaces. This cluster is not the
management cluster and contains only tenant workloads.

This removes ambiguity.

⸻

7. The architecture pattern you actually have

Your platform is not just hub-spoke.

It is Multi-Region Hub-Spoke Control Plane.

Pattern name often used by platform teams:

Control Plane Fleet Architecture

                 Global Platform

         +---------------------------+
         | shard-us-1 (Hub)          |
         +---------------------------+
            |        |        |
        spoke     spoke     spoke

         +---------------------------+
         | shard-eu-1 (Hub)          |
         +---------------------------+
            |        |        |
        spoke     spoke     spoke

Each shard independently manages tenants.

This is extremely scalable.

⸻

8. Final verdict

Your glossary definitions are:

Item	Correct
Management Cluster	✅ correct
Mothership alias	✅ fine
Shard	✅ correct
Shared Cluster	✅ correct (minor clarity improvement possible)
Enterprise Tenant Cluster	✅ perfect
Control Plane DB	✅ correct
Data Plane DB	✅ correct
BYOC	✅ correct
Composition A	✅ correct
Composition B	✅ correct

So architecturally it aligns with hub-spoke.

⸻

💡 One extra thing I’d strongly recommend adding to the glossary

A term many platforms add:

Spoke Cluster
A cluster managed by a Management Cluster. Can be either:
- Shared Cluster (multi-tenant)
- Enterprise Tenant Cluster (single tenant)

This helps documentation and diagrams.