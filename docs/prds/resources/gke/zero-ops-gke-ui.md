Short answer:
👉 Yes — you can absolutely build a UI like this with your current (custom operator + DB) approach.
👉 In fact, your architecture is better suited for this UI than pure K8s CRD-based systems at your stage.

Let’s break it down in a practical, non-theoretical way.

⸻

🧠 What This UI Actually Needs

The GKE-style screen you shared is essentially showing:

1) Cluster inventory
	•	Cluster name
	•	Region
	•	Node count
	•	Resources

⸻

2) Health status (aggregated)
	•	“100% healthy”
	•	Per-cluster status

⸻

3) Cost / usage (derived data)
	•	CPU, memory
	•	Estimated cost

⸻

4) Actions / metadata
	•	Labels
	•	Notifications
	•	Upgrade status

⸻

🏗️ Can Your Current Architecture Support This?

Your system:

Spoke Operator → DB → Aggregation → API → UI

👉 This is actually ideal for building dashboards like this

⸻

✅ Why Your Design Works WELL for This UI

1) DB = perfect for aggregation

Unlike K8s CRDs:
	•	You can JOIN data easily
	•	You can precompute fields
	•	You can filter/sort efficiently

👉 Example:

SELECT cluster_name, status, total_cpu, total_memory
FROM cluster_summary;


⸻

2) You control the schema

You can define:

ClusterStatus
TenantStatus
ResourceStatus

👉 Instead of reverse-engineering K8s objects every time

⸻

3) Faster UI queries

K8s API approach:

UI → K8s API → multiple CRDs → aggregate client-side ❌

Your approach:

UI → API → pre-aggregated DB rows ✅

👉 This is exactly how GKE / AWS consoles work internally

⸻

🧠 What Your Operator Needs to Provide

To support this UI, your operator should emit:

⸻

1) Cluster-level status

{
  "cluster_id": "spoke-1",
  "status": "healthy",
  "last_seen": "...",
  "node_count": 3
}


⸻

2) Resource-level status

{
  "cluster_id": "spoke-1",
  "resource_type": "Deployment",
  "name": "litellm",
  "ready": true,
  "replicas": "3/3"
}


⸻

3) Metrics (optional but powerful)

From:
	•	Prometheus / Alloy

CPU
Memory
Network


⸻

4) Events / conditions

Degraded
CrashLoop
Scaling


⸻

🧠 Where Aggregation Happens (Critical)

👉 NOT in UI
👉 NOT in operator

👉 In your Hub layer (Event Router / API)

⸻

Example aggregation logic:

Cluster = Healthy
IF all deployments ready

Tenant = Ready
IF cluster healthy + DB ready + app ready


⸻

⚖️ Compare with Karmada / OCM approach

K8s-native approach:

UI → Karmada CRDs → compute status ❌

Problems:
	•	Multiple API calls
	•	Complex joins
	•	Slow at scale

⸻

Your approach:

UI → API → DB (precomputed) ✅

👉 Much simpler for product UI

⸻

🔥 Important Insight

GKE UI is NOT built by querying raw Kubernetes objects directly

It uses:
	•	Aggregated services
	•	Internal APIs
	•	Precomputed state

👉 Exactly what you’re building

⸻

🧾 What You Still Need to Add

To fully match that UI:

⸻

1) Cluster table (DB)

clusters:
  id
  region
  status
  node_count
  cpu_total
  memory_total


⸻

2) Resource summary table

cluster_resources:
  cluster_id
  deployments_ready
  deployments_total


⸻

3) Status computation layer

Healthy / Degraded / Updating


⸻

4) Metrics ingestion (optional)
	•	Prometheus remote_write
	•	Or periodic scrape

⸻

⚠️ What NOT to Do

❌ Don’t query spokes directly from UI
❌ Don’t compute everything client-side
❌ Don’t rely only on raw K8s objects

⸻

👍 Final Verdict

Can you build this UI?

👉 Yes — and your current architecture is actually better suited for it than Karmada right now

⸻

Why?

Because:
	•	You already have a central data model (DB)
	•	You can aggregate efficiently
	•	You can control latency and UX

⸻

🧠 Final Insight

You are not building “Kubernetes dashboards”

You are building:

A cloud control plane UI (like GKE / AWS)

And those are always backed by:

Aggregated APIs + databases — not raw Kubernetes


⸻

🚀 If you want next step

I can help you:
	•	Design the exact DB schema for this UI
	•	Define API endpoints (cluster list, health, filters)
	•	Or map operator → DB → UI pipeline end-to-end

That’s the final piece to make your platform feel “GKE-level”.