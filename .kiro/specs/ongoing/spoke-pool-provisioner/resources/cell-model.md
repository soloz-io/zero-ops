🧠 Tenant Isolation Strategy (Final Decision)

📌 High-Level Model

Hub (Control Plane)
→ Single global database (Ory, metadata, orchestration)

Cell (Application Plane)
→ One PostgreSQL cluster per cell
→ One schema per tenant
→ Shared service layer (GoTrue, PostgREST)


⸻

🏗️ How Tenant Isolation Works

Cell Database
├── tenant_abc schema
├── tenant_xyz schema
├── tenant_123 schema
└── shared schemas (optional: extensions, system)

	•	Each tenant gets a dedicated PostgreSQL schema
	•	All tenant data lives inside its own schema
	•	No cross-tenant data mixing

⸻

⚙️ Service Layer

Using:
	•	PostgREST
	•	GoTrue

One shared instance per CELL
→ dynamically routes to tenant schema

✅ No per-tenant service duplication
✅ Efficient and scalable

⸻

🔐 Isolation Model

Layer	Isolation Mechanism
Tenant ↔ Tenant	Schema isolation
User ↔ User (within tenant)	Optional RLS


⸻

❌ What We Are NOT Doing
	•	❌ No DB-per-tenant (breaks shared services)
	•	❌ No RLS-only multi-tenancy (too risky)
	•	❌ No Crossplane for tenant DB creation

⸻

✅ Why This Approach

1. Works with Shared Services
	•	PostgREST & GoTrue can serve one DB only
	•	Schema-per-tenant enables multi-tenancy within that DB

2. Strong Isolation (without high cost)
	•	Clear namespace boundary per tenant
	•	Safer than RLS-only

3. Fast Provisioning
	•	Create schema in milliseconds
	•	No need to spin up new DB or services

4. Scales with Cell Architecture
	•	Each cell handles ~100 tenants
	•	Easy to add more cells as you grow

⸻

🧠 Mental Model

Supabase (Platform):
→ DB per tenant

Our Platform:
→ Schema per tenant inside a CELL
→ Shared compute + shared storage


⸻

🏁 Final Rule

Control Plane (Hub) → global, centralized

Application Plane (Cell) → 
  one DB
  one schema per tenant
  shared services
