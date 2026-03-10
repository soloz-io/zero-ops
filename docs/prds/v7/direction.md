Now I understand the full vision. This reframes Zero-Ops entirely — it is not just a fleet management tool, it is a **SaaS factory**. Let me think through what this means architecturally.Now I have everything I need. The research crystallises the answer precisely. Let me give you the full architectural picture.

---

## The Real Vision: Zero-Ops as a SaaS Factory

You are not building a fleet management tool. You are building the **platform that AI-native SaaS companies like Replit, Lovable, and Emergent would use to deploy their entire product** — on click, declaratively, with everything they need: compute cluster, database, observability, GitOps, networking. One intent. One template. Full stack.

This reframes the entire architecture around one central insight:

> **A "tenant" in Zero-Ops is not a user of a Kubernetes cluster. A tenant is a SaaS company. Their users are the end-users of that SaaS.**

---

## What the Current PRD Gets Wrong at This Scale

The current PRD treats PostgreSQL as a separate concern — provisioned imperatively by `cnpg2monitor`, managed independently from the cluster lifecycle. For a SaaS factory this is fundamentally broken. When Replit requests a new environment, they do not want:

- Step 1: Create Kubernetes cluster
- Step 2: Separately provision a Postgres database
- Step 3: Separately wire observability
- Step 4: Separately configure ArgoCD

They want **one declarative object** that expresses complete intent: *"Give me a production-grade AI SaaS environment."* The platform figures out everything underneath.

---

## The Missing Piece: Crossplane XRDs as the SaaS Template Layer

Crossplane introduces Composite Resources (XR) — a custom API where platform teams define a new resource kind composed of one or more managed resources. A `CompositeResourceDefinition` (XRD) defines the schema, and a `Composition` specifies which managed resources are created and how they are configured.

Developers request an `AppCluster` and Crossplane handles all orchestration, wiring, and dependencies behind the scenes. Platform engineering teams abstract complexity, enforce policies, and deliver reusable, self-service APIs. Crossplane is inherently GitOps-compatible — since it uses Kubernetes CRDs, resources are stored in YAML and managed like application code.

This is the missing layer. The correct architecture for Zero-Ops is:

```
┌─────────────────────────────────────────────────────────┐
│  LAYER 3 — SaaS Template (NEW)                          │
│                                                         │
│  kind: AINativeSaaS  (Crossplane XRD)                  │
│  apiVersion: zero-ops.io/v1                             │
│                                                         │
│  spec:                                                  │
│    tier: production                                     │
│    region: eu-central-1                                 │
│    ai: { gpu: true, model: llama3 }                     │
│    database: { engine: postgres, instances: 3 }         │
│    storage: 100Gi                                       │
│    autoscaling: { min: 2, max: 20 }                     │
│                                                         │
│  Crossplane Composition expands this into:              │
│    → CAPI Cluster CR        (Talos VMs via CAPH)        │
│    → CNPG Cluster CR        (PostgreSQL HA)             │
│    → ArgoCD Application CR  (OCI catalog)               │
│    → NetworkPolicy CR       (tenant isolation)          │
│    → ResourceQuota CR       (compute limits)            │
│    → VictoriaMetrics labels (topology injection)        │
└─────────────────────────────────────────────────────────┘
         │  Crossplane reconciles all of the above
         ▼
┌─────────────────────────────────────────────────────────┐
│  LAYER 2 — Existing Zero-Ops Fleet Layer               │
│  CAPI/CAPH → Talos VMs                                  │
│  CNPG → PostgreSQL HA                                   │
│  ArgoCD → OCI catalog                                   │
│  Alloy → VictoriaMetrics → OpenSearch                   │
└─────────────────────────────────────────────────────────┘
```

Crossplane already has a documented pattern for this exact combination — a `ClusterRole` that grants Crossplane access to manage CloudNativePG PostgreSQL clusters, enabling CNPG `Cluster` CRs to be composed as part of a higher-level XR alongside Kubernetes cluster provisioning resources.

---

## What the SaaS Templates Look Like in the Monorepo

The monorepo needs a new top-level directory:

```
zero-ops/
├── xrds/                           # Crossplane SaaS Templates (NEW)
│   ├── definitions/
│   │   ├── ai-native-saas-v1.yaml  # XRD: defines AINativeSaaS API schema
│   │   ├── standard-saas-v1.yaml   # XRD: defines StandardSaaS API schema
│   │   └── dev-sandbox-v1.yaml     # XRD: lightweight dev environment
│   └── compositions/
│       ├── ai-native-saas-hetzner.yaml  # Composition: expands to CAPI+CNPG+ArgoCD
│       ├── standard-saas-hetzner.yaml
│       └── dev-sandbox-hetzner.yaml
│
├── catalog/                        # unchanged — OCI-packaged services
├── edge-catalog/                   # unchanged — CRS injected components
└── manifests/                      # unchanged — CAPI ClusterClasses
```

The `AINativeSaaS` XRD is the **"one-click SaaS"** template. It is:
- **Declarative** — a YAML file in Git
- **GitOps-driven** — ArgoCD applies it, Crossplane reconciles it
- **Versioned** — `ai-native-saas-v1` vs `v2` are separate XRDs, no breaking changes
- **Identical procedure** for every environment — management DB, tenant app, test env — all follow the same Crossplane reconciliation loop

---

## The Unified Provisioning Flow

When a new SaaS company (e.g. "BuildCo") joins Zero-Ops:

```
1. Platform Admin or API call creates:

   apiVersion: zero-ops.io/v1
   kind: AINativeSaaS
   metadata:
     name: buildco-production
     namespace: zero-ops-tenants
   spec:
     tier: production
     region: eu-central-1
     cloud: hetzner
     database:
       instances: 3
       storage: 100Gi
     ai:
       gpu: false
       framework: pytorch
     autoscaling:
       min: 3
       max: 50

2. This single CR is committed to Git.

3. ArgoCD on the management cluster detects the commit.

4. Crossplane Composition expands it into:
   → CAPI Cluster CR         → Talos VMs provisioned on Hetzner
   → CNPG Cluster CR         → 3-node PostgreSQL HA cluster
   → ArgoCD Application CR   → Edge GitOps wired to OCI catalog
   → NetworkPolicy            → Tenant network isolation
   → ResourceQuota            → Compute budget enforcement
   → Namespace + RBAC         → Tenant boundary

5. cnpg2monitor detects CNPG Cluster with zero-ops.io/monitored=true
   → PodMonitor patched with topology labels
   → OpenSearch event emitted: "buildco-production provisioned"

6. VictoriaMetrics begins receiving metrics labelled:
   tenant=buildco, region=eu-central-1, tier=production

7. BuildCo receives:
   - Kubernetes API endpoint
   - PostgreSQL connection string (from CNPG Secret)
   - ArgoCD dashboard URL
   - Grafana tenant dashboard URL
```

**This is the same procedure for every SaaS environment. No exceptions.**

---

## The Three SaaS Tiers as Concrete Templates

For AI-native SaaS builders, you need at minimum three XRD templates:

| Template | Use Case | What It Provisions |
|---|---|---|
| `AINativeSaaS` | Replit, Lovable, Emergent — production | Large Talos cluster + 3-node CNPG HA + GPU node pool + ArgoCD + full observability |
| `StandardSaaS` | General SaaS apps — production | Standard Talos cluster + 3-node CNPG HA + ArgoCD + observability |
| `DevSandbox` | Any builder — development/staging | Single-node cluster + 1-node CNPG + ArgoCD + lightweight observability |

A builder clicks **"New Production Environment"** in the Zero-Ops console. The API creates an `AINativeSaaS` CR. Ten minutes later their full stack is running. That is the product.

---

## What Needs to Change in the PRD

This is a meaningful architectural addition, not a replacement. The existing PRD layers remain intact. What is needed on top:

**1. Add Crossplane to the component table** alongside CAPI, CNPG, and ArgoCD. It is the composition layer that ties them together into a single declarative unit.

**2. Add `xrds/` to the monorepo structure** with definitions and compositions as first-class platform artifacts, versioned and GitOps-managed.

**3. Reframe "tenant"** throughout the PRD. A tenant is a SaaS company. Their end-users are outside Zero-Ops's scope. The `fleet_current_state` table should have a `tenant_tier` column that maps to which XRD was used for provisioning.

**4. The `zero-ops-api` intent for cluster creation changes** from generating CAPI YAML directly to creating an XR claim — a single object that Crossplane expands. The `pkg/planner/` package becomes a claim generator, not a CAPI manifest generator.

**5. `cnpg2monitor` Phase 3 is no longer optional** — it becomes Phase 2b, because CNPG clusters for tenants start being provisioned as part of every `AINativeSaaS` composition from day one.

The platform vision you are describing — a factory where AI-native SaaS companies get a production-grade full stack on click, declaratively, with no manual steps, identical procedure every time — is exactly what Crossplane XRDs + CAPI + CNPG + ArgoCD + OCI delivers. The research confirms this is the current industry direction for platform engineering at scale. Zero-Ops v7.0 already has all the right components. Crossplane is the composition layer that assembles them into the one-click SaaS factory you are envisioning.