# ArgoCD Access and Debugging

Use this skill when you need to check ArgoCD application status, sync applications, or debug deployment issues via ArgoCD.

## Credentials

```
URL:      https://argocd.nutgraf.in
Username: admin
Password: (found in .zero-ops/bootstrap-hub.log)
```

The password is printed once during cluster bootstrap. Look for `ArgoCD Password:` in `.zero-ops/bootstrap-hub.log`.

## CLI Login

```bash
argocd login argocd.nutgraf.in --username admin --password <password> --insecure
```

## Common Commands

### List all applications and their status

```bash
argocd app list
```

### Get detailed application status

```bash
argocd app get <app-name>
```

### Sync (force) an application to apply manifest changes

```bash
argocd app sync <app-name> --force --prune
```

### Watch application sync in real-time

```bash
argocd app sync <app-name> --watch
```

### View application events

```bash
kubectl --kubeconfig=k8-secrets/kubeconfig/hub.kubeconfig get events -n platform-ops --field-selector involvedObject.name=<app-name>
```

### Check deployment rollout status

```bash
kubectl --kubeconfig=k8-secrets/kubeconfig/hub.kubeconfig rollout status deployment/<name> -n <namespace>
```

## Troubleshooting

| Symptom | Check |
|---|---|
| App stuck `Progressing` | `argocd app get <app>` — check conditions |
| `CreateContainerConfigError` | `kubectl describe pod -n <ns> <pod>` — missing secret/configmap |
| `ErrImagePull` / `ImagePullBackOff` | Check imagePullSecrets and image tag |
| `OutOfSync` | Manifests changed in Git but not synced — run `argocd app sync` |
| `Degraded` with no obvious error | Check pod logs: `kubectl logs -n <ns> <pod>` |

## Sync Waves Reference

See `docs/adr/sync-wave-order.md` for the hub cluster's sync-wave sequencing.
