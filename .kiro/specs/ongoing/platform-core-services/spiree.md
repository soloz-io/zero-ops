👉 “Is identity actually being used correctly end-to-end?”

⸻

🧠 SPIFFE/SPIRE Review Checklist (Ask Your Engineers)

Use this as a design review questionnaire 👇

⸻

1️⃣ Identity Model (Foundational)

👉 If this is unclear, everything else is wrong.

Ask:
	•	What is our trust domain?

spiffe://what-domain?


	•	How do you structure SPIFFE IDs?

spiffe://platform/spoke/{tenant}/service
OR
spiffe://cluster/ns/sa


	•	Is the identity:
	•	deterministic? ✅
	•	tied to tenant? ✅
	•	tied to workload? ✅

👉 Red flag:

“We just used defaults”


⸻

2️⃣ Workload Registration (CRITICAL)

👉 This is where most implementations fail.

Ask:
	•	How are workloads registered with SPIRE?
	•	Static (registration-job.yaml) ❌
	•	Dynamic (k8s workload attestor) ✅
	•	What selectors are used?
	•	namespace?
	•	service account?
	•	pod labels?
	•	What happens when:
	•	a pod restarts?
	•	a new tenant is created?

👉 Expected answer:

“No manual registration required”


⸻

3️⃣ Identity Delivery (SVIDs)

Ask:
	•	How do workloads get their identity?
	•	Mounted socket?
	•	File?
	•	Env?
	•	Are you using:
	•	X.509 SVIDs (mTLS)? ✅
	•	JWT SVIDs?
	•	What is the TTL of certificates?
	•	How are they rotated?

👉 Red flag:

“We don’t know how rotation works”


⸻

4️⃣ mTLS Enforcement (REAL SECURITY)

👉 This is the most important part.

Ask:
	•	Which connections use SPIFFE mTLS today?

Example expected:

Spoke Controller → Hub API
Grafana Alloy → VictoriaMetrics
NATS Leaf → Hub NATS

	•	Is TLS:
	•	enforced? ✅
	•	optional? ❌
	•	What validates the client identity?
	•	AgentGateway?
	•	Envoy?
	•	custom Go middleware?

👉 Red flag:

“SPIRE is installed but services still use basic auth”


⸻

5️⃣ Authorization Layer (OFTEN MISSED)

👉 SPIFFE = identity, NOT authorization

Ask:
	•	How do you map:

SPIFFE ID → permissions

	•	Example:
	•	Which SPIFFE ID can write to PostgREST?
	•	Which can send metrics?
	•	Where is this enforced?
	•	AgentGateway?
	•	API layer?
	•	DB layer?

👉 Red flag:

“If you have identity but no authorization rules”


⸻

6️⃣ Integration with Existing Auth (Ory / JWT)

Ask:
	•	Where do we use:
	•	SPIFFE? (machines)
	•	Ory JWT? (humans)
	•	Are these clearly separated?
	•	Any overlap/confusion?

👉 Expected:

Humans → Ory
Workloads → SPIFFE


⸻

7️⃣ Federation (Hub ↔ Spoke)

👉 Only needed if multi-cluster identity is real.

Ask:
	•	Do we have multiple trust domains?
	•	Is SPIFFE federation configured?
	•	How do spokes trust hub identities?

👉 Red flag:

“We assumed clusters trust each other”


⸻

8️⃣ Secrets Integration (Infisical / ESO)

Ask:
	•	Can workloads authenticate to Infisical using SPIFFE?
	•	Or still using:
	•	client secrets?
	•	static tokens?
	•	Is there a plan to move to SPIFFE-based auth?

👉 Ideal:

SPIFFE → Infisical auth → dynamic secrets


⸻

9️⃣ Operational Concerns

Ask:
	•	How do you debug SPIRE issues?
	•	What happens if:
	•	SPIRE server goes down?
	•	agent restarts?
	•	Is there:
	•	HA setup for SPIRE server?
	•	backup for trust bundle?

👉 Red flag:

“No runbook exists”


⸻

🔟 Observability

Ask:
	•	Do we monitor:
	•	certificate issuance?
	•	rotation failures?
	•	auth failures?
	•	Any alerts configured?

⸻

1️⃣1️⃣ Failure & Recovery

Ask:
	•	What happens if:
	•	a spoke cluster is rebuilt?
	•	Will identities:
	•	regenerate automatically? ✅
	•	require manual steps? ❌

⸻

1️⃣2️⃣ Incremental Adoption Strategy

👉 This is a maturity signal.

Ask:
	•	Which services are SPIFFE-enabled today?
	•	What’s the rollout plan?
	•	Are we:
	•	replacing JWT gradually? ✅
	•	or big-bang migration? ❌

⸻

🧠 What Good Looks Like

A strong answer from your team sounds like:

- SPIFFE IDs are deterministic and tenant-scoped
- Workloads auto-register via k8s attestor
- mTLS enforced via AgentGateway
- Authorization mapped via SPIFFE ID
- Humans use Ory, workloads use SPIFFE
- No static secrets in spokes


⸻

🚨 What Bad Looks Like

- “SPIRE is installed but unused”
- “We still use basic auth everywhere”
- “Registration is manual”
- “No mapping from identity to permissions”


⸻

🧾 Final Insight

👉 Installing SPIRE = easy
👉 Using it correctly = HARD

⸻

👍 Bottom Line

Use this mental test:

Can I remove all static credentials from spokes?

If answer is:
	•	YES → you’re doing it right ✅
	•	NO → SPIFFE is not fully integrated ❌