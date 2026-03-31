# VictoriaMetrics Infisical Setup Guide

## Prerequisites
- Infisical deployed and accessible
- ClusterSecretStore `infisical-backend` configured
- Project: "platform", Environment: "prod"

## Required Secret in Infisical

Create a secret named `victoriametrics` with the following structure:

```json
{
  "username": "keda-spoke",
  "password": "your-secure-password-here"
}
```

### Steps to Create Secret in Infisical:

1. Access Infisical UI or CLI
2. Navigate to project "platform", environment "prod"
3. Create new secret with key: `victoriametrics`
4. Add two properties:
   - `username`: `keda-spoke`
   - `password`: Generate a strong password (min 16 chars)

### Password Generation Example:

```bash
# Generate secure password
openssl rand -base64 24
```

## How It Works

The ExternalSecret will:
1. Fetch `username` and `password` from Infisical
2. Generate htpasswd format using ESO's `htpasswd` template function
3. Create Kubernetes Secret `victoriametrics-basic-auth` with `auth` field
4. Ingress uses this secret for nginx basic auth

## Validation

After applying the ExternalSecret:

```bash
# Check ExternalSecret status
kubectl get externalsecret victoriametrics-basic-auth -n observability

# Verify secret was created with ownerReference
kubectl get secret victoriametrics-basic-auth -n observability -o yaml

# Test authenticated access
kubectl get secret victoriametrics-basic-auth -n observability -o jsonpath='{.data.auth}' | base64 -d
# Should output: keda-spoke:$apr1$...
```

## Security Notes

- Secret is owned by ExternalSecret (creationPolicy: Owner)
- Credentials never stored in Git
- Password rotation: Update in Infisical, ESO syncs within 1h
- For immediate rotation: Delete the K8s secret, ESO recreates it
