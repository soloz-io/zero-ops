Now I have a thorough understanding of both systems. Here is the complete analysis.

---

## What Is Cluster Stacks, Really?

Before answering the scope question, you need to understand precisely what it is and what problem it solves.

Kubernetes and Cluster API solve one specific purpose perfectly but leave other tasks out of scope. A user still has to answer: how do I get node images? How can I manage core cluster components like CCM and CNI? How can I safely and efficiently upgrade Kubernetes clusters? Cluster Stacks give an answer by working hand-in-hand with Cluster API.

It is built from three distinct layers working together:

Cluster stacks are implemented by two Helm charts. The first contains all Cluster API objects and is applied in the **management cluster**. The second Helm chart contains the cluster addons — the core components every cluster needs — and is installed in the **workload clusters**. Furthermore there are node images that can look quite different depending on the provider.

So the three components are:
1. **ClusterClass + CAPI objects** → applied in the management cluster
2. **Cluster addons (CNI, CSI, CCM)** → installed automatically into each workload cluster
3. **Node images** → pre-built and versioned, referenced by the stack

The key innovation vs. plain ClusterClass is captured here: The Cluster Stack Operator facilitates usage of Cluster Stacks by automating all steps. It takes Cluster Stack release assets that consist mainly of two Helm charts — one to deploy in the management cluster, the other to deploy in workload clusters — as well as provider-specific node image build information.

Looking at your screenshots, the `ClusterStack` CRD with `autoSubscribe: true` and `channel: stable` means the operator watches for new releases and automatically pulls updated Helm charts, keeping tenant clusters in sync without manual intervention.

---

## Should Cluster Stacks Be In Phase 1 (Management Cluster) Scope?

**Short answer: No for Phase 1. Yes for Phase 2. But it changes your Phase 2 design significantly.**

Here is the precise breakdown:

---

### What Cluster Stacks Does For The Management Cluster

The Cluster Stack Operator itself **runs in the management cluster** and is installed there. So yes, in that narrow sense, it touches Phase 1 — you'd install the operator during bootstrap. But what it actually *does* is manage the lifecycle of **tenant (workload) clusters**, not the management cluster itself.

The Cluster Stack Operator has to be installed in the management cluster and can be interacted with by applying custom resources. It extends the functionality of the Cluster API operators. It mainly applies the two Helm charts from a cluster stack implementation and is also able to automatically fetch a remote GitHub repository to see whether there are new releases of a certain cluster stack.

So the correct scoping is:

| Component | Phase 1 (Management Cluster Bootstrap) | Phase 2 (Tenant Cluster Management) |
|---|---|---|
| Cluster Stack Operator (the controller) | ✅ Install it | — |
| `ClusterStack` CRD applied | — | ✅ Per tenant stack |
| Helm chart 1 (CAPI objects / ClusterClass) | — | ✅ Auto-applied by operator |
| Helm chart 2 (CCM, CNI, CSI for tenant) | — | ✅ Auto-applied into tenant cluster |
| Node images | — | ✅ Provider integration handles |

---

### Does It Replace Your Existing ClusterClass Design?

**Yes, and this is the most important thing to understand.** If you adopt Cluster Stacks for Phase 2, your current design of manually maintaining `hetzner-prod-talos-v1.yaml`, `hetzner-dev-talos-v1.yaml` etc. inside `manifests/classes/` gets **replaced** by the Cluster Stack framework. You would no longer hand-craft and embed ClusterClass YAMLs. Instead:

```
Your current design:                    With Cluster Stacks:
────────────────────────────────────    ────────────────────────────────────
manifests/classes/                      GitHub Release (or OCI registry)
  hetzner-prod-talos-v1.yaml    →        cluster-stacks/hetzner-talos-v1-k8s1.31/
  hetzner-dev-talos-v1.yaml               ├── clusteraddon.yaml  (Helm chart 2)
  hetzner-staging-talos-v1.yaml           ├── clusterclass.yaml  (Helm chart 1)
                                          └── metadata.yaml

CLI applies YAML directly           →   ClusterStack CR + autoSubscribe=true
                                        Operator fetches & applies automatically
```

The Cluster Stack operator enables an "Infrastructure as Software" approach for managing Kubernetes clusters in self-service. Users can take existing releases of Cluster Stacks and the operator and will be able to create clusters easily.

---

### Does It Replace Your `catalog/` Selectable Services Design?

**Partially, for the mandatory addons.** The Cluster Stacks second Helm chart handles CCM, CNI, and CSI installation into tenant workloads automatically. Your `catalog/` pattern would still apply for *optional* tenant services (ArgoCD per tenant, Prometheus, KEDA, etc.) — things that aren't part of the core cluster stack definition.

```
Cluster Stacks handles automatically:     Your catalog/ handles:
─────────────────────────────────────     ──────────────────────────────
CNI (Cilium) → into tenant cluster        ArgoCD (optional, per tenant)
CCM (hetzner) → into tenant cluster       Prometheus (optional)
CSI (hetzner) → into tenant cluster       KEDA, NATS, etc. (optional)
K8s version upgrades → coordinated
Node image rotation → automated
```

---

### The Honest Trade-Off

| Dimension | Plain ClusterClass (current design) | Cluster Stacks |
|---|---|---|
| **Complexity to adopt** | Low — you control everything | Medium — new CRDs, new operator, new release process |
| **Addon lifecycle management** | Manual (you update YAML, re-apply) | Automatic (`autoSubscribe` pulls new releases) |
| **K8s version upgrades** | Manual ClusterClass update | Coordinated — stack tests node image + CCM + CNI together |
| **Node images** | You build/maintain (Packer or ISO) | Stack release includes tested images |
| **Hetzner + Talos support** | Usage of Cluster Stacks outside of SCS-conforming infrastructure is growing on Hetzner Cloud, strengthening the technology | Community-supported, not officially maintained by Syself |
| **Your team's control** | Full — every YAML is yours | Partial — you depend on upstream stack releases |

---

## Recommendation

**Phase 1: Don't change anything.** The management cluster bootstrap design is correct as-is. The only Phase 1 addition is installing the Cluster Stack Operator binary into the management cluster alongside ArgoCD and CNPG in the `ManagementClusterInstaller`. One extra Helm install.

**Phase 2: Evaluate seriously, don't commit yet.** Cluster Stacks is the right conceptual direction but comes with a significant prerequisite: you need either a pre-existing Hetzner+Talos cluster stack release in a registry, or you need to build and publish your own. The `csctl` tool enables automated testing of Cluster Stacks if integrated in a CI process that first builds all necessary files and node images, then uses them to create a workload cluster based on the Cluster Stack. This is non-trivial build infrastructure work that doesn't belong in Phase 1.

The concrete addition to your design doc is small — add a note to the Phase 2 section:

> **Phase 2 Decision Point:** Tenant cluster provisioning will evaluate Cluster Stacks (SCS) as the ClusterClass + addon lifecycle layer. If adopted, `manifests/classes/` templates for tenant tiers are replaced by `ClusterStack` CRs pointing to versioned releases. The `catalog/` pattern continues to apply for optional tenant services not covered by the stack. The Cluster Stack Operator is pre-installed in the management cluster during Phase 1 bootstrap to enable this path.