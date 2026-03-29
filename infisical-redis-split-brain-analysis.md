# Infisical Redis Split-Brain Bug Analysis

## Executive Summary

During the deployment of Infisical on the Zero-Ops platform, we encountered a "split-brain" state where Redis and Infisical pods were using different passwords, causing `WRONGPASS` authentication errors. This document details the root cause analysis and the GitOps-compliant solution.

## Problem Statement

**Symptom:** Infisical pods failed to start with Redis authentication errors:
```
ReplyError: WRONGPASS invalid username-password pair or user is disabled
```

**Evidence:**
- Redis pod was running with password: `changeme`
- Infisical pod was trying to connect with password: `6a3793ce538c7ea0c8f00da33e2928de`
- The `infisical-redis-credentials` secret contained BOTH passwords in different keys

## Root Cause Analysis

### Issue 1: ArgoCD SelfHeal Overwriting Hub-Managed Secrets

**Problem:** The `infisical-redis-credentials.yaml` manifest in Git contained:
```yaml
annotations:
  argocd.argoproj.io/sync-options: Prune=false
stringData:
  password: "changeme"
```

**What Happened:**
1. `hub init-secrets` CLI generated secure password and created secret with:
   - `password: "6a3793ce538c7ea0c3e0c16260076db1"`
   - `url: "redis://:6a3793ce538c7ea0c3e0c16260076db1@redis-master.zero-ops-system.svc:6379"`

2. ArgoCD's selfHeal detected drift and overwrote the `password` key with `"changeme"` from Git

3. Because of `IgnoreExtraneous` annotation, ArgoCD left the `url` key untouched

4. **Split-Brain State:** Secret now contained:
   - `password: "changeme"` (from Git)
   - `url: "redis://:6a3793ce...@redis-master..."` (from hub CLI)

5. Redis pod read `password` key → booted with `"changeme"`

6. Infisical pod read `url` key → tried to connect with `"6a3793ce..."`

7. Result: `WRONGPASS` error

**Solution:** Remove `stringData` from Git manifest and add `Replace=false` annotation:
```yaml
annotations:
  argocd.argoproj.io/sync-options: Prune=false,Replace=false
# NO stringData - secret content managed by hub CLI, not Git
```

This follows the same pattern as database credentials (`control-plane-db-credentials`, `hub-db-credentials`, `infisical-db-credentials`).

### Issue 2: Helm Chart Hardcoded REDIS_URL

**Problem:** The official Infisical Helm chart has a bug in `_helpers.tpl`:

```go
{{- define "infisical.redisConnectionString" -}}
{{- $password := .Values.redis.auth.password -}}
{{- $serviceName := include "infisical.redisServiceName" . -}}
{{- printf "redis://default:%s@%s:6379" $password "redis-master" -}}
{{- end -}}
```

**Bug:** The template computes `$serviceName` but then IGNORES it and hardcodes `"redis-master"` in the connection string.

**Impact:** When `redis.enabled: true`, the Helm chart generates:
```yaml
env:
- name: REDIS_URL
  value: redis://default:mysecretpassword@redis-master:6379
```

Then our `extraEnv` adds:
```yaml
- name: REDIS_URL
  valueFrom:
    secretKeyRef:
      name: infisical-redis-credentials
      key: url
```

**Result:** Kubernetes creates TWO environment variables with the same name. The pod description shows:
```
Environment:
  REDIS_URL: redis://default:mysecretpassword@redis-master:6379
  REDIS_URL: <set to the key 'url' in secret 'infisical-redis-credentials'>
```

Kubernetes doesn't "override" - it just adds both. The application reads the FIRST one (hardcoded with wrong password).

**Solution:** Disable the Helm chart's built-in Redis deployment and deploy Redis separately:

```yaml
redis:
  enabled: false  # Prevents Helm chart from generating REDIS_URL
```

Deploy Redis as a standalone StatefulSet that reads password from `infisical-redis-credentials` secret.

## GitOps-Compliant Solution

### Step 1: Fix Redis Secret Manifest

**File:** `manifests/platform-infisical/infisical-redis-credentials.yaml`

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: infisical-redis-credentials
  namespace: zero-ops-system
  annotations:
    argocd.argoproj.io/compare-options: IgnoreExtraneous
    argocd.argoproj.io/sync-options: Prune=false,Replace=false
  labels:
    app.kubernetes.io/managed-by: zero-ops-hub-cli
    app.kubernetes.io/component: secret-zero
type: Opaque
# NO stringData - secret content managed by hub CLI, not Git
```

### Step 2: Disable Helm Chart Redis

**File:** `manifests/platform-infisical/values.yaml`

```yaml
redis:
  enabled: false  # Prevents hardcoded REDIS_URL generation
```

### Step 3: Deploy Standalone Redis

**File:** `manifests/platform-infisical/redis.yaml`

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: redis-master
  namespace: zero-ops-system
spec:
  serviceName: redis-master
  replicas: 1
  template:
    spec:
      containers:
      - name: redis
        image: redis:7.2-alpine
        command:
        - redis-server
        - --requirepass
        - $(REDIS_PASSWORD)
        env:
        - name: REDIS_PASSWORD
          valueFrom:
            secretKeyRef:
              name: infisical-redis-credentials
              key: password
---
apiVersion: v1
kind: Service
metadata:
  name: redis-master
  namespace: zero-ops-system
spec:
  type: ClusterIP
  ports:
  - port: 6379
    targetPort: redis
  selector:
    app.kubernetes.io/name: redis
    app.kubernetes.io/component: master
```

### Step 4: Hub CLI Manages Secret Content

**File:** `internal/hub/components/installer.go`

```go
func (i *Installer) InstallInfisicalSecrets(ctx context.Context) error {
    // Generate secure Redis password
    redisBytes := make([]byte, 32)
    rand.Read(redisBytes)
    redisPassword := hex.EncodeToString(redisBytes)[:32]

    // Create secret with synchronized password and url
    redisSecret := &corev1.Secret{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "infisical-redis-credentials",
            Namespace: "zero-ops-system",
        },
        Type: corev1.SecretTypeOpaque,
        StringData: map[string]string{
            "password": redisPassword,
            "url": fmt.Sprintf("redis://:%s@redis-master.zero-ops-system.svc:6379", redisPassword),
        },
    }
    // Create or update secret
    clientset.CoreV1().Secrets(namespace).Create(ctx, redisSecret, metav1.CreateOptions{})
}
```

## Deployment Workflow

### GitOps-First Approach

1. **Commit and Push Changes:**
   ```bash
   git add manifests/platform-infisical/
   git commit -m "fix: Disable Helm Redis, deploy standalone Redis, fix secret annotations"
   git push origin main
   ```

2. **Force ArgoCD Sync:**
   ```bash
   kubectl patch application platform-infisical-prerequisites -n argocd \
     --type merge \
     -p '{"operation":{"initiatedBy":{"username":"admin"},"sync":{"syncStrategy":{"hook":{},"apply":{"force":true}}}}}'
   ```

3. **Wipe Bad State:**
   ```bash
   # Delete corrupted secret
   kubectl delete secret infisical-redis-credentials -n zero-ops-system
   
   # Delete old Redis pod and PVC
   kubectl delete pod -l app.kubernetes.io/name=redis -n zero-ops-system
   kubectl delete pvc -l app.kubernetes.io/name=redis -n zero-ops-system
   
   # Delete old Infisical pods
   kubectl delete pod -l app.kubernetes.io/name=infisical -n zero-ops-system
   ```

4. **Re-inject Secret Zero:**
   ```bash
   make build-hub
   ./bin/hub init-secrets --kubeconfig ./secrets/mothership.kubeconfig
   ```

5. **Verify Deployment:**
   ```bash
   # Check Redis is running
   kubectl get pods -n zero-ops-system -l app.kubernetes.io/name=redis
   
   # Check Infisical is running
   kubectl get pods -n zero-ops-system -l app.kubernetes.io/name=infisical
   
   # Verify secret synchronization
   kubectl get secret infisical-redis-credentials -n zero-ops-system -o jsonpath='{.data.password}' | base64 -d
   kubectl get secret infisical-redis-credentials -n zero-ops-system -o jsonpath='{.data.url}' | base64 -d
   ```

## Key Learnings

### 1. Secret Zero Pattern

**Hub CLI owns secret data, Git owns secret metadata:**
- Git manifest defines namespace, name, labels, annotations
- Git manifest has NO `stringData` or `data` fields
- Hub CLI injects actual secret values via client-go
- ArgoCD annotation `Replace=false` prevents overwrites

### 2. Helm Chart Late-Binding Override Pattern

**Problem:** Helm charts often hardcode values in templates

**Solution:** Disable built-in subcharts and deploy separately:
```yaml
# values.yaml
redis:
  enabled: false  # Disable subchart

# Separate manifest
# redis.yaml - Deploy standalone with proper secret references
```

**Why this works:**
- Helm chart won't generate hardcoded env vars
- We control the entire deployment via GitOps
- Secrets are properly referenced from hub-managed sources

### 3. ArgoCD Sync Options

**Critical annotations for hub-managed secrets:**
```yaml
annotations:
  argocd.argoproj.io/compare-options: IgnoreExtraneous
  argocd.argoproj.io/sync-options: Prune=false,Replace=false
```

- `IgnoreExtraneous`: ArgoCD ignores keys not in Git (allows hub CLI to add `password`, `url` keys)
- `Prune=false`: ArgoCD won't delete the secret
- `Replace=false`: ArgoCD won't overwrite secret data

### 4. GitOps Workflow Discipline

**Never use `kubectl apply` for platform services:**
- All changes via Git commits
- ArgoCD syncs changes to cluster
- Validates sync-waves and dependencies
- Catches configuration drift early

**Exception:** Secret Zero injection via hub CLI is acceptable because:
- Secrets never stored in Git (security requirement)
- Hub CLI is the single source of truth for secret values
- ArgoCD manages secret metadata, hub CLI manages secret data

## References

### Official Infisical Helm Chart Analysis

**Repository:** `archived/references/identity-auth/infisical/helm-charts/infisical-standalone-postgres/`

**Key Files:**
- `values.yaml` - Default configuration with `redis.enabled: true`
- `templates/_helpers.tpl` - Contains buggy `infisical.redisConnectionString` template
- `templates/infisical.yaml` - Deployment template that generates REDIS_URL env var

**Bug Location:** `_helpers.tpl` line 109
```go
{{- printf "redis://default:%s@%s:6379" $password "redis-master" -}}
                                                    ^^^^^^^^^^^^^^
                                                    Should use $serviceName variable
```

### Related Documentation

- [ArgoCD Sync Options](https://argo-cd.readthedocs.io/en/stable/user-guide/sync-options/)
- [Kubernetes Secret Management Best Practices](https://kubernetes.io/docs/concepts/configuration/secret/)
- [GitOps Principles](https://opengitops.dev/)

## Conclusion

The split-brain bug was caused by a combination of:
1. Missing `Replace=false` annotation allowing ArgoCD to overwrite hub-managed secrets
2. Helm chart bug hardcoding Redis connection string with wrong password
3. Kubernetes creating duplicate environment variables instead of overriding

The solution follows GitOps principles:
- Git is the single source of truth for infrastructure configuration
- Hub CLI is the single source of truth for secret values
- ArgoCD manages the reconciliation loop
- No manual `kubectl apply` commands in production workflow

This pattern is now applied consistently across all platform secrets:
- Database credentials (control-plane, hub, infisical)
- Redis credentials
- Infisical encryption keys
- PostgreSQL connection strings

**Status:** ✅ Fixed and documented for future reference
