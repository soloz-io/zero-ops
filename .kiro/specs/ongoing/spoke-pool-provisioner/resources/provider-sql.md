Here is feedback from team. do you agree and think its valid? or you wanted to push back?

🔥 The Critical Issue: Crossplane provider-sql

❌ The recommendation is NOT idiomatic for your design

“Use Crossplane provider-sql to create Database/Role/Grant”

This conflicts with your finalized architecture:
	•	Supabase-style migrations
	•	schema-per-tenant OR logical DB-per-tenant
	•	control plane ownership

⸻

🧠 Why this is a problem

Crossplane is designed for:

infrastructure reconciliation

NOT:

high-frequency, tenant-level data operations

⸻

⚠️ What will break if you follow this

1. CRD Explosion

If you have:
	•	1,000 tenants

1000 Database CRs
1000 Role CRs
1000 Grant CRs

👉 That’s 3,000+ CRDs just for DB internals

⸻

2. Reconciliation Overhead

Crossplane will:
	•	continuously reconcile DB existence
	•	retry failed operations blindly
	•	not understand partial SQL state

👉 This leads to:
	•	noisy control loops
	•	slow recovery
	•	hard debugging

⸻

3. No Transactional Guarantees

Crossplane cannot ensure:
CREATE DATABASE
CREATE ROLE
GRANT

4. Drift ≠ Desired Behavior

If DB is deleted:

Crossplane recreates it empty ❌

But your system expects:

schema + migrations + state consistency