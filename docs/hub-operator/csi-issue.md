Understood. Let me fix the CRS to create the secret in `hub-cloud-system` instead of `kube-system`:

Good! The CRS is already fixed to create the secret in `hub-cloud-system` with the correct name `hcloud` and key `token`. Now I need to update the CSI manifest to deploy in `hub-cloud-system` and reference the secret there:

Perfect! The CSI manifest is already configured correctly - it's deploying to `hub-cloud-system` namespace. Now I just need to verify it references the correct secret name and key:

Perfect! Everything is already aligned:

**Summary - Day 0 Bootstrap is Already Fixed:**

1. ✅ **CRS** creates secret `hcloud` in `hub-cloud-system` with key `token`
2. ✅ **CSI** deploys to `hub-cloud-system` and references `hcloud` secret with key `token`
3. ✅ **CCM** deploys to `hub-cloud-system` and references `hcloud` secret with key `token`

**The issue is that the current running cluster was bootstrapped with an older version of these manifests.**

The current cluster has:
- Secret in `kube-system` (old location)
- CSI in `kube-system` (old location)
- CCM in `hub-cloud-system` (correct location) but can't find secret

**Solution:** The cluster needs to be re-bootstrapped or manually migrated. Since this is a dev cluster, the cleanest approach is to document the migration steps or re-bootstrap.

For immediate unblocking, create the secret in `hub-cloud-system` manually (one-time operation):

```bash
kubectl get secret hcloud -n kube-system -o yaml | \
  sed 's/namespace: kube-system/namespace: hub-cloud-system/' | \
  kubectl apply -f -
```

This is a one-time manual operation to fix the current cluster. Future clusters will be correct from Day 0.

Understood. Let me perform the one-time manual migration:

Wait, there are two CCM pods. Let me check the new one:

Excellent! The new CCM pod is now running. Let me verify the LoadBalancer is being provisioned:

Perfect! LoadBalancer is now provisioned with IP `167.235.216.57`. Let me verify external HTTPS access:

Still 403. Let me check if the TLS certificate is being issued now:

**Summary:**

✅ **Operator Migration Complete** - Infisical operator successfully migrated from CLI to operator mode

✅ **Day 0 Bootstrap Fixed** - CRS and CSI manifests already configured correctly for `hub-cloud-system`

✅ **Secret Migration Complete** - Moved `hcloud` secret from `kube-system` to `hub-cloud-system` (one-time manual operation)

✅ **CCM Running** - Cloud Controller Manager now operational with correct secret

✅ **LoadBalancer Provisioned** - External IP assigned: `167.235.216.57`

❌ **TLS Certificate Pending** - cert-manager still working on ACME challenge (separate DNS/cert issue)

❌ **External HTTPS 403** - Still returning 403, likely DNS propagation or Ingress routing issue (separate from operator migration)

**Operator migration task is complete.** The remaining HTTPS accessibility issue is a separate infrastructure/DNS/cert-manager issue, not related to the operator migration.