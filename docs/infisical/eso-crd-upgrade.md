# ESO CRD Upgrade Strategy: GitOps-Managed CRDs

## Problem Statement

External Secrets Operator (ESO) v0.11.0 supports the Infisical provider, but upgrading from v0.10.5 requires CRD updates. Helm's `installCRDs: true` flag only installs CRDs on initial deployment and never upgrades them, creating a version mismatch.

## Root Cause

**Helm CRD Limitation:**
- Helm installs CRDs only on first `helm install`
- Helm NEVER upgrades CRDs on `helm upgrade`
- This is a known Helm design decision for safety (CRDs are cluster-wide resources)

**Our Situation:**
- ESO v0.10.5 installed CRDs with `v1alpha1` and `v1beta1` API versions
- ESO v0.11.0 requires CRDs with `v1` API version for Infisical provider
- ArgoCD Helm chart sync doesn't upgrade CRDs automatically

## Solution: GitOps-Managed CRDs

### Architecture

```
Wave 1: platform-external-secrets-crds (CRDs only)
   ↓
Wave 4: platform-external-secrets (ESO operator)
   ↓
Wave 5: platform-cluster-secret-store (Infisical backend)
```

### Implementation

**1. Extract CRDs from Helm Chart:**
```bash
helm template external-secrets external-secrets/external-secrets \
  --version 0.11.0 \
  --include-crds \
  --namespace external-secrets-system \
  | grep -A 10000 "kind: CustomResourceDefinition" \
  > manifests/platform-core-services/eso/crds/external-secrets-crds.yaml
```

**2. Create ArgoCD Application for CRDs:**
- File: `manifests/argocd/apps/platform-external-secrets-crds.yaml`
- Sync-wave: "1" (before operator)
- Auto-prune: false (never delete CRDs automatically)
- Replace strategy: true (allows CRD updates)

**3. Disable Helm CRD Installation:**
- Set `installCRDs: false` in ESO Helm values
- ESO operator assumes CRDs already exist

### CRD List (v0.11.0)

The following CRDs are managed via GitOps:

1. `acraccesstokens.generators.external-secrets.io`
2. `clusterexternalsecrets.external-secrets.io`
3. `clustergenerators.generators.external-secrets.io`
4. `clustersecretstores.external-secrets.io`
5. `ecrauthorizationtokens.generators.external-secrets.io`
6. `externalsecrets.external-secrets.io`
7. `fakes.generators.external-secrets.io`
8. `gcraccesstokens.generators.external-secrets.io`
9. `githubaccesstokens.generators.external-secrets.io`
10. `passwords.generators.external-secrets.io`
11. `pushsecrets.external-secrets.io`
12. `secretstores.external-secrets.io`
13. `stssessiontokens.generators.external-secrets.io`
14. `uuids.generators.external-secrets.io`
15. `vaultdynamicsecrets.generators.external-secrets.io`
16. `webhooks.generators.external-secrets.io`

### Upgrade Path

**From v0.10.5 to v0.11.0:**

1. Commit CRD manifests to Git:
   ```bash
   git add manifests/platform-core-services/eso/crds/external-secrets-crds.yaml
   git add manifests/argocd/apps/platform-external-secrets-crds.yaml
   git commit -m "feat: manage ESO CRDs via GitOps"
   git push
   ```

2. ArgoCD syncs CRDs (wave 1):
   - Replaces existing v1alpha1/v1beta1 CRDs with v1 versions
   - No downtime (CRDs are additive)

3. Update ESO operator manifest:
   - Set `installCRDs: false`
   - Commit and push

4. ArgoCD syncs ESO operator (wave 4):
   - Operator starts with v1 CRDs already present
   - Infisical provider now supported

### Benefits

**GitOps Compliance:**
- All CRDs version-controlled in Git
- Declarative CRD lifecycle management
- Audit trail for CRD changes

**Safe Upgrades:**
- CRDs upgraded before operator
- Sync-wave ordering prevents race conditions
- Replace strategy allows in-place updates

**No Manual kubectl:**
- Zero imperative commands
- Respects GitOps-first principles
- ArgoCD handles all CRD operations

### Day 2 Operations

**Upgrading ESO to v0.12.0:**

1. Extract new CRDs:
   ```bash
   helm template external-secrets external-secrets/external-secrets \
     --version 0.12.0 \
     --include-crds \
     | grep -A 10000 "kind: CustomResourceDefinition" \
     > manifests/platform-core-services/eso/crds/external-secrets-crds.yaml
   ```

2. Update ESO chart version in `platform-external-secrets.yaml`

3. Commit both changes in single PR

4. ArgoCD syncs CRDs first (wave 1), then operator (wave 4)

### Validation

**Verify CRDs installed:**
```bash
kubectl get crd | grep external-secrets.io
```

**Check API versions:**
```bash
kubectl get crd externalsecrets.external-secrets.io -o yaml | grep -A 5 "versions:"
```

**Verify Infisical provider support:**
```bash
kubectl explain clustersecretstore.spec.provider.infisical
```

## References

- [Helm CRD Best Practices](https://helm.sh/docs/chart_best_practices/custom_resource_definitions/)
- [ArgoCD CRD Management](https://argo-cd.readthedocs.io/en/stable/user-guide/resource_tracking/)
- [ESO Helm Chart](https://github.com/external-secrets/external-secrets/tree/main/deploy/charts/external-secrets)
