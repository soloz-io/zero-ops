# VictoriaMetrics Infisical Setup Guide

## Prerequisites
- Infisical deployed and accessible at `platform-infisical-infisical-standalone-infisical.zero-ops-system.svc:8080`
- ClusterSecretStore `infisical-backend` configured and Ready
- Project: "platform" (slug: "platform"), Environment: "Production" (slug: "prod")
- **SPIFFE/SPIRE deployed** (Phase 1.5 of platform-core-services spec)

## Purpose

**DEPRECATED - This secret is for Phase 1 testing only.**

Production deployment uses **mTLS authentication via SPIFFE/SPIRE** for cross-cluster service-to-service authentication. This Infisical-based basic auth secret is retained only for initial deployment and testing before SPIFFE/SPIRE is fully operational.

**Cross-Cluster Data Flow (Production):**
```
Spoke Cluster (Grafana Alloy with SPIRE Agent)
  → Obtains SVID: spiffe://zero-ops.nutgraf.in/grafana-alloy/{tenant-id}
  → remote_write (HTTPS + mTLS)
  → Hub VictoriaMetrics (validates SPIFFE SVID)
  → Centralized metrics storage
  → Platform Admin dashboards
```

**Authentication Model (Production):**
- **Protocol**: mTLS with SPIFFE/SPIRE workload identity
- **Identity**: Each Alloy instance gets unique SPIFFE ID
- **Certificate Rotation**: Automatic (default 1-hour TTL)
- **Trust**: Hub SPIRE Server issues and validates SVIDs
- **Zero credential management**: No passwords, automatic rotation

**Why mTLS (SPIFFE) instead of Basic Auth:**
- Zero credential management (automatic certificate issuance/rotation)
- Workload identity (cryptographically verifiable)
- Zero-trust architecture (mutual authentication)
- Industry standard for service-to-service auth
- No secrets to store in Infisical or sync via ESO

**NOT used by:**
- KEDA (queries local spoke metrics, not Hub)
- Platform Console (uses read-only Grafana dashboards)
- Real-time operational decisions (uses local metrics)

## Current Implementation Status

**Phase 1 (Current - Testing Only)**: Shared basic auth credential
- Temporary solution for initial deployment before SPIFFE/SPIRE is operational
- All spokes temporarily share `spoke-metrics-writer` credential
- **Will be replaced by mTLS in Phase 2**

**Phase 2 (Production)**: mTLS via SPIFFE/SPIRE
- Zero credential management (automatic certificate provisioning)
- Each Grafana Alloy instance obtains unique SPIFFE identity
- Automatic certificate rotation (default: 1-hour TTL)
- Hub VictoriaMetrics validates SPIFFE SVIDs
- No Infisical secrets required for authentication

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

## Production Deployment: mTLS via SPIFFE/SPIRE

**Target Architecture** (Phase 2 - Production):

Each spoke Grafana Alloy instance authenticates via SPIFFE workload identity:

**Spoke Bootstrap Flow:**
1. SPIRE Agent deployed to spoke cluster (via edge-catalog)
2. SPIRE Agent federates with Hub SPIRE Server
3. Grafana Alloy pod attests to SPIRE Agent via Kubernetes workload attestation
4. SPIRE Agent issues SVID: `spiffe://zero-ops.nutgraf.in/grafana-alloy/{tenant-id}`
5. Alloy uses SVID for mTLS connection to Hub VictoriaMetrics
6. Hub VictoriaMetrics validates SVID against SPIRE trust bundle
7. Certificates automatically rotate (default: 1-hour TTL)

**SPIFFE Identity Registration (Hub SPIRE Server):**
```bash
# Per-spoke registration
spire-server entry create \
  -spiffeID spiffe://zero-ops.nutgraf.in/grafana-alloy/acme-corp \
  -parentID spiffe://zero-ops.nutgraf.in/spoke-agent/acme-prod \
  -selector k8s:ns:observability \
  -selector k8s:sa:grafana-alloy \
  -dns victoriametrics.hub.nutgraf.in
```

**Grafana Alloy Configuration (Production):**
```yaml
prometheus.remote_write "hub" {
  endpoint {
    url = "https://victoriametrics.hub.nutgraf.in/api/v1/write"
    
    tls_config {
      # SPIFFE Workload API provides automatic certificate rotation
      cert_file = "/run/spire/sockets/agent.sock"  # SPIRE Agent socket
      key_file  = "/run/spire/sockets/agent.sock"
      ca_file   = "/run/spire/bundle.crt"          # Trust bundle
      
      server_name = "victoriametrics.hub.nutgraf.in"
    }
  }
  
  external_labels = {
    cluster_id = "spoke-acme-prod"
    tenant_id  = "acme-corp"
    tier       = "enterprise"
    region     = "eu-central-1"
  }
}
```

**Benefits:**
- **Zero credential management**: No passwords to generate, store, or rotate
- **Automatic rotation**: Certificates rotate every hour by default
- **Workload identity**: Cryptographically verifiable service identity
- **Zero-trust architecture**: Mutual authentication (both sides verify)
- **Auditability**: SPIFFE identity in VictoriaMetrics logs
- **Compliance**: Industry standard (CNCF graduated project)

**Implementation:**
- SPIFFE/SPIRE deployed in Phase 1.5 of platform-core-services spec
- VictoriaMetrics ingress configured to validate SPIFFE SVIDs
- No Infisical secrets required for production authentication
- Basic auth ExternalSecret retained only for Phase 1 testing

## Security Notes

- Secret is owned by ExternalSecret (creationPolicy: Owner)
- Credentials never stored in Git
- Password rotation: Update in Infisical, ESO syncs within 1h
- For immediate rotation: Delete the K8s secret, ESO recreates it
