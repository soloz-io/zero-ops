## Cluster Management

The best answer is not one approach — it is a specific combination:
```
Phase 1 Bootstrap (CLI imperative):
  KinD → CAPI/CAPH install → ClusterClass apply → 
  Cluster apply + CRS for CNI/CCM → Pivot → 
  ArgoCD + capi2argo + CNPG install → Exit

Post-Bootstrap (GitOps):
  ArgoCD watches Git repo
  capi2argo auto-registers tenant clusters
  All day-2 ops via Git commits
  CLI never runs again unless full teardown/rebuild