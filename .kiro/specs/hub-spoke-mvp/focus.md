Use a Hub–Spoke Kubernetes architecture where all clusters run the same base Kubernetes but are differentiated by role via labels and provisioning profiles: define two ClusterClasses (or equivalent templates) — a hub-cluster-class (larger nodes, persistent storage, higher availability) and a spoke-cluster-class (lightweight, tenant-focused). Register all clusters in Argo CD and label them (role=hub, role=spoke). Deploy all control-plane and shared services only to the hub cluster, including VictoriaMetrics for metrics, ClickHouse for billing/usage, Grafana for dashboards, identity services, and the SaaS control plane APIs. Each spoke cluster hosts isolated tenant workloads (app + DB), includes only lightweight agents like Grafana Alloy for telemetry collection, and optionally minimal local components (e.g., Prometheus only if low-latency alerts are required). Spokes push metrics/logs/usage data asynchronously to the hub with buffering and idempotent batch ingestion, while all provisioning is done via GitOps (repo per platform or tenants) where tenant creation/update results in a Git commit that ArgoCD reconciles into the spoke cluster. Identity between hub and spokes is secured using SPIFFE/SPIRE federation with mTLS, and tenant context is resolved in the control plane (not in agents). DNS and external access are managed centrally (hub-managed domain), while ingress resources are created in the spoke clusters per tenant. This ensures strong tenant isolation (silo at workload level), centralized observability and billing, minimal per-cluster overhead, and scalable multi-tenant operations.

⚙️ How to structure your flow

Step 1 (imperative — keep it thin)

Only do:
	•	create tenant record (DB)
	•	create Git repo (or register repo)
	•	add fleet descriptor (Git commit)

👉 That’s it.

⸻

Step 2 (declarative — everything else)

Triggered by Git / CR:
	•	cluster provisioning (CAPI)
	•	ArgoCD agent install
	•	Vault namespace
	•	secrets
	•	apps

⸻

🔥 Key shift

👉 Don’t orchestrate steps
👉 Declare desired state and let controllers converge