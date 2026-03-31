# VictoriaMetrics Infisical Setup Guide

## Prerequisites
- Infisical deployed and accessible at `platform-infisical-infisical-standalone-infisical.zero-ops-system.svc:8080`
- ClusterSecretStore `infisical-backend` configured and Ready
- Project: "platform" (slug: "platform"), Environment: "Production" (slug: "prod")

## Current Status

**ExternalSecret Status**: `SecretSyncedError - could not get secret data from provider`

This means the secret `victoriametrics` does not exist in Infisical yet.

## Required Secret in Infisical

You need to create a secret named `victoriametrics` in Infisical with two properties:

**Secret Key**: `victoriametrics`
**Properties**:
- `username`: `keda-spoke` (string)
- `password`: `<generate-secure-password>` (string)

### Steps to Create Secret in Infisical UI:

1. Port-forward to Infisical (if not exposed externally):
   ```bash
   kubectl port-forward -n zero-ops-system svc/platform-infisical-infisical-standalone-infisical 8080:8080
   ```

2. Access Infisical UI at `http://localhost:8080`

3. Navigate to:
   - Project: "platform"
   - Environment: "Production"

4. Create new secret:
   - Click "Add Secret"
   - Key: `victoriametrics`
   - Type: Select "Key-Value" or "Object" type
   - Add properties:
     - Property: `username`, Value: `keda-spoke`
     - Property: `password`, Value: `<generated-password>`

5. Save the secret

### Password Generation:

```bash
# Generate secure password (24 characters)
openssl rand -base64 24
```

**Example output**: `xK9mP2vL8nQ4rT6wY1zA3bC5dE7f`

## How It Works

The ExternalSecret will:
1. Fetch `username` and `password` from Infisical
2. Generate htpasswd format using ESO's `htpasswd` template function
3. Create Kubernetes Secret `victoriametrics-basic-auth` with `auth` field
4. Ingress uses this secret for nginx basic auth

## Validation

After creating the secret in Infisical, the ExternalSecret should sync within 1 hour (or immediately if you delete the K8s secret):

```bash
# Check ExternalSecret status (should show Ready=True)
kubectl get externalsecret victoriametrics-basic-auth -n observability

# Expected output:
# NAME                          STORE               REFRESH INTERVAL   STATUS   READY
# victoriametrics-basic-auth    infisical-backend   1h                 Synced   True

# Verify secret was created with ownerReference
kubectl get secret victoriametrics-basic-auth -n observability -o yaml | grep -A 5 ownerReferences
# Should show: kind: ExternalSecret

# Test authenticated access (verify htpasswd format)
kubectl get secret victoriametrics-basic-auth -n observability -o jsonpath='{.data.auth}' | base64 -d
# Should output: keda-spoke:$apr1$...

# Force immediate sync (optional - deletes and recreates secret)
kubectl delete secret victoriametrics-basic-auth -n observability
# ExternalSecret will recreate it within seconds
```

## Testing External Access

After the secret is synced:

```bash
# Run the validation script
./test/e2e/platform-victoriametrics/validate-external-access.sh

# Test authenticated access manually
curl -u keda-spoke:<password> "https://victoriametrics.hub.nutgraf.in/api/v1/query?query=up"
# Should return JSON with status: success
```

## Security Notes

- Secret is owned by ExternalSecret (creationPolicy: Owner)
- Credentials never stored in Git
- Password rotation: Update in Infisical, ESO syncs within 1h
- For immediate rotation: Delete the K8s secret, ESO recreates it
