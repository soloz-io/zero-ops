# Hub Environment Configuration

## Overview

This directory contains the Hub Environment configuration that defines the bootstrap settings for the Zero-Ops platform. All environment-specific values are centralized in the `hub-bootstrap-config` ConfigMap to ensure consistency across all components.

## Configuration Files

### 1. hub-bootstrap-config.yaml (ConfigMap)
**Purpose**: Single source of truth for all bootstrap configuration values.

**Configurable Values**:
- `CLUSTER_ID`: Unique identifier for this hub cluster (used in AWS backup paths)
- `CLUSTER_REGION`: AWS region for Secrets Manager backups
- `INFISICAL_PROJECT_SLUG`: Infisical cert-manager project identifier
- `INFISICAL_SECRETS_PROJECT_SLUG`: Infisical secret-manager project identifier (for application secrets)
- `INFISICAL_ENVIRONMENT_SLUG`: Infisical environment (dev/staging/prod)
- `DOMAIN`: Base domain for the hub cluster

**Why ConfigMap?**
- Single place to update all environment-specific values
- Prevents configuration drift across manifests
- Easy to override per environment (dev/staging/prod)
- Follows GitOps best practices

### 2. hubenvironment.yaml (Custom Resource)
**Purpose**: Declarative specification of the Hub Environment.

**Configuration Sources**:
- Values are sourced from `hub-bootstrap-config` ConfigMap
- Comments indicate which values come from the ConfigMap
- Spec fields can override ConfigMap defaults if needed

### 3. hub-operator Deployment
**Purpose**: Injects ConfigMap values as environment variables into the operator.

**Environment Variables**:
- `CLUSTER_ID` → Used for AWS backup path construction
- `CLUSTER_REGION` → AWS region for Secrets Manager
- `INFISICAL_PROJECT_SLUG` → Infisical cert-manager project
- `INFISICAL_SECRETS_PROJECT_SLUG` → Infisical secret-manager project
- `INFISICAL_ENVIRONMENT_SLUG` → Infisical environment
- `DOMAIN` → Base domain

## Configuration Precedence

The hub-operator follows this precedence order (highest to lowest):

1. **HubEnvironment Spec** (`spec.secrets.clusterId`)
2. **Environment Variable** (`CLUSTER_ID` from ConfigMap)
3. **Fallback Default** (HubEnvironment name)

Example:
```yaml
# ConfigMap sets CLUSTER_ID=hub-production
# HubEnvironment can override:
spec:
  secrets:
    clusterId: hub-production-override  # This takes precedence
```

## How to Update Configuration

### For a New Environment (dev → staging → prod)

1. **Copy the ConfigMap**:
   ```bash
   cp hub-bootstrap-config.yaml hub-bootstrap-config-staging.yaml
   ```

2. **Update values**:
   ```yaml
   data:
     CLUSTER_ID: "hub-staging"
     CLUSTER_REGION: "us-east-1"
     INFISICAL_ENVIRONMENT_SLUG: "staging"
     DOMAIN: "staging.nutgraf.in"
   ```

3. **Update HubEnvironment reference** (if needed):
   ```yaml
   metadata:
     name: hub-staging
   spec:
     domain: staging.nutgraf.in
   ```

4. **Commit and push** - ArgoCD will sync automatically

### For Changing Existing Values

1. **Edit hub-bootstrap-config.yaml**:
   ```yaml
   data:
     CLUSTER_ID: "hub-production-v2"  # Changed
   ```

2. **Commit and push**

3. **Restart hub-operator** (to pick up new env vars):
   ```bash
   kubectl rollout restart deployment hub-operator -n platform-ops
   ```

## AWS Backup Path Construction

The AWS Secrets Manager backup path is constructed as:
```
/hub-operator/{CLUSTER_ID}/infisical-master-keys
```

Example with `CLUSTER_ID=hub-production`:
```
/hub-operator/hub-production/infisical-master-keys
```

This ensures each cluster has isolated backups in AWS Secrets Manager.

## Validation

After updating configuration:

1. **Verify ConfigMap**:
   ```bash
   kubectl get configmap hub-bootstrap-config -n platform-ops -o yaml
   ```

2. **Verify Environment Variables in Operator**:
   ```bash
   kubectl get pod -n platform-ops -l control-plane=hub-operator -o yaml | grep -A 20 "env:"
   ```

3. **Check Operator Logs**:
   ```bash
   kubectl logs -n platform-ops -l control-plane=hub-operator -f
   ```

## Troubleshooting

### ConfigMap not found
- Ensure sync-wave is correct (ConfigMap=0, HubEnvironment=1)
- Check ArgoCD sync status

### Environment variables not updated
- Restart the hub-operator deployment
- Check ConfigMap exists in correct namespace

### AWS backup path incorrect
- Verify `CLUSTER_ID` in ConfigMap
- Check operator logs for actual path used
- Ensure operator has AWS credentials configured
