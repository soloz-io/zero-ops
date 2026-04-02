## Sync Wave Order (Corrected)

```
Wave 0: Placeholder secrets (prevent ArgoCD deletion)
Wave 1: ExternalSecrets (fetch from Infisical)
Wave 2: CNPG Cluster (uses pre-created platform-db-app secret)
Wave 3: Wait for CNPG ready
Wave 4: Role setup jobs + Migration jobs
Wave 5: Password rotation job (PostSync hook)
```

## How Password Rotation Works

1. **Update Infisical:** Change `platform-db-app-password` value in Infisical
2. **ESO Sync:** ExternalSecret refreshes (1h interval or manual trigger)
3. **Secret Updated:** `platform-db-app` secret gets new password
4. **ArgoCD Sync:** Triggers PostSync hook
5. **Rotation Job:** Runs and updates PostgreSQL role password
6. **Validation:** Job compares hashes, only rotates if changed

## Disaster Recovery Flow

1. **Cluster Destroyed:** All Kubernetes resources lost
2. **Cluster Recreated:** ArgoCD syncs manifests
3. **ESO Pulls Secrets:** Gets credentials from Infisical
4. **CNPG Bootstraps:** Uses pre-created `platform-db-app` secret
5. **Database Restored:** CNPG restores from S3 backup (if configured)
6. **Credentials Match:** Same password in both secret and database