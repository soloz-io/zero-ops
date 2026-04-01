# VictoriaMetrics Infisical Setup Guide

## Prerequisites
- Infisical deployed and accessible at `platform-infisical-infisical-standalone-infisical.zero-ops-system.svc:8080`
- ClusterSecretStore `infisical-backend` configured and Ready
- Project: "platform" (slug: "platform"), Environment: "Production" (slug: "prod")

## Purpose

This secret provides **per-spoke basic authentication for Grafana Alloy remote_write** from spoke clusters to Hub VictoriaMetrics.

**Cross-Cluster Data Flow:**
```
Spoke Cluster (Grafana Alloy)
  → remote_write (HTTPS POST with basic auth)
  → Hub VictoriaMetrics (victoriametrics.hub.nutgraf.in)
  → Centralized metrics storage
```

**Authentication Model:**
- Each spoke cluster gets unique credentials: `spoke-{tenant-id}-metrics`
- Credentials generated during spoke bootstrap
- Stored in spoke's Infisical
- VictoriaMetrics ingress validates via nginx basic auth

**NOT used by:**
- KEDA (queries local spoke metrics, not Hub)
- Platform Console (uses read-only Grafana dashboards)
- Real-time operational decisions (uses local metrics)

## Current Implementation Status

**Phase 1 (Current)**: Shared credential for initial deployment
- All spokes temporarily share `spoke-metrics-writer` credential
- Simplifies initial deployment and testing

**Phase 2 (Next)**: Per-spoke credentials (production-ready)
- Each spoke gets unique `spoke-{tenant-id}-metrics` credential
- Provisioned automatically during spoke bootstrap
- Better security, auditability, and access control

## Current Status

**ExternalSecret Status**: `SecretSyncedError - could not get secret data from provider`

This means the secret `victoriametrics-spoke-writer` does not exist in Infisical yet.

## Required Secret in Infisical

You need to create a secret named `victoriametrics-spoke-writer` in Infisical with two properties:

**Secret Key**: `victoriametrics-spoke-writer`
**Properties**:
- `username`: `spoke-metrics-writer` (string)
- `password`: `<generate-secure-password>` (string)

**Note**: This is a **Phase 1 shared credential** for initial deployment. Production deployment will use per-spoke credentials (`spoke-{tenant-id}-metrics`) provisioned automatically during spoke bootstrap.

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
   - Key: `victoriametrics-spoke-writer`
   - Type: Select "Key-Value" or "Object" type
   - Add properties:
     - Property: `username`, Value: `spoke-metrics-writer`
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
# Should output: spoke-metrics-writer:$apr1$...

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
curl -u spoke-metrics-writer:<password> "https://victoriametrics.hub.nutgraf.in/api/v1/query?query=up"
# Should return JSON with status: success

# Test Grafana Alloy remote_write (from spoke cluster)
# Alloy config should include:
# remote_write:
#   - url: https://victoriametrics.hub.nutgraf.in/api/v1/write
#     basic_auth:
#       username: spoke-metrics-writer
#       password: <from-secret>
```

## Production Deployment: Per-Spoke Credentials

**Target Architecture** (Phase 2):

Each spoke gets unique credentials during bootstrap:

**Spoke Bootstrap Flow:**
1. Crossplane Composition provisions spoke cluster
2. Generate unique credentials: `spoke-{tenant-id}-metrics` / `<random-password>`
3. Store in spoke's Infisical: `spoke-{tenant-id}-metrics-password`
4. ExternalSecret syncs to spoke cluster
5. Grafana Alloy mounts secret for `remote_write`

**Benefits:**
- Security: Credential compromise affects only one spoke
- Auditability: Can track which spoke sent metrics
- Access Control: Can revoke individual spoke access
- Compliance: Meets security audit requirements

**Implementation:**
- Credentials provisioned by Crossplane Composition
- Stored in spoke's Infisical project
- VictoriaMetrics ingress accepts multiple valid credentials
- Metrics tagged with `cluster_id` and `tenant_id` labels

## Security Notes

- Secret is owned by ExternalSecret (creationPolicy: Owner)
- Credentials never stored in Git
- Password rotation: Update in Infisical, ESO syncs within 1h
- For immediate rotation: Delete the K8s secret, ESO recreates it
