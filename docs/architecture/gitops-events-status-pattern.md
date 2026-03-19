# GitOps-First Architecture with Status Sync

**Version:** 1.0  
**Date:** 2026-03-19  
**Pattern:** GitOps (95%) + Events (5%) + Crossplane Status Controller

## Core Principle

**GitOps handles infrastructure provisioning. Events handle coordination. A controller syncs status to the database.**

```
User Request → Git Commit → ArgoCD Sync → Crossplane Provision
                                              ↓
                                    Status Controller watches
                                              ↓
                                    Updates PostgreSQL
                                              ↓
                                    Hub API reads DB
```

## The Three Layers

### 1. GitOps: Infrastructure Provisioning (95%)

All infrastructure changes go through Git:

```
Hub API receives tenant creation
  ↓
Commits AINativeSaaS Claim to Git Fleet registry
  ↓
ArgoCD detects change and syncs
  ↓
Crossplane reconciles infrastructure
  ↓
Claim gets Conditions (Ready, Synced)
```

**No EventBridge/NATS for provisioning.** Git is the source of truth.

### 2. Status Controller: Kubernetes → PostgreSQL

A small Go controller watches Crossplane Claims and updates the tenant database:

```go
// cmd/tenant-controller/main.go
func (r *AINativeSaaSReconciler) Reconcile(ctx context.Context, req ctrl.Request) {
    claim := &platformv1alpha1.AINativeSaaS{}
    r.Get(ctx, req.NamespacedName, claim)
    
    // Read Crossplane standard conditions
    ready := isConditionTrue(claim, "Ready")
    synced := isConditionTrue(claim, "Synced")
    
    // Derive platform status
    status := deriveStatus(ready, synced, claim.Status.Conditions)
    
    // Write to PostgreSQL
    r.db.Exec(ctx, `
        UPDATE tenants 
        SET provisioning_status = $1, 
            provisioning_message = $2,
            provisioned_at = CASE WHEN $1 = 'ready' THEN NOW() ELSE provisioned_at END
        WHERE id = $3
    `, status.Phase, status.Message, claim.Name)
}
```

**Status Mapping:**

| Crossplane State | DB Status | Message |
|-----------------|-----------|---------|
| Claim created, no conditions | `provisioning` | Initialising |
| Synced=True, Ready=False | `provisioning` | Creating infrastructure |
| Synced=False | `failed` | Config error + condition message |
| Ready=True | `ready` | null |
| DeletionTimestamp set | `deleting` | null |

**Database Schema:**

```sql
CREATE TYPE provisioning_status AS ENUM (
    'pending',       -- Git commit made, ArgoCD not yet synced
    'provisioning',  -- Crossplane reconciling
    'ready',         -- Ready=True on claim
    'failed',        -- Ready=False with terminal error
    'deleting'       -- Deletion in progress
);

ALTER TABLE tenants ADD COLUMN provisioning_status provisioning_status DEFAULT 'pending';
ALTER TABLE tenants ADD COLUMN provisioning_message text;
ALTER TABLE tenants ADD COLUMN provisioned_at timestamptz;
```

### 3. Events: Coordination Only (5%)

NATS events are used ONLY for:

**a) Cross-cluster communication:**
```
Management cluster → NATS → Spoke cluster
(e.g., trigger spoke cluster reconciliation)
```

**b) User-initiated actions not in Git:**
```
User clicks "Restart Service" → NATS event → Agent executes
```

**c) Status notifications (optional):**
```
Tenant ready → NATS event → Email notification service
```

**c) Control plane - App plane communication:**
```
App deployment status and orchestration 
```

**NOT used for:**
- ❌ Infrastructure provisioning (use GitOps)
- ❌ Status updates (use controller)
- ❌ Configuration changes (use Git)

## Complete Tenant Onboarding Flow

### Step 1: User Creates Environment

```
User: "Create production environment"
  ↓
Hub API (zero_ops_api)
  ↓
INSERT INTO tenants (id, name, provisioning_status)
VALUES ('acme-corp', 'production', 'pending')
  ↓
Commit AINativeSaaS Claim to Git
  ↓
Return 202 Accepted
```

### Step 2: GitOps Provisions Infrastructure

```
Git commit detected
  ↓
ArgoCD syncs Claim to cluster
  ↓
Crossplane reconciles:
  - Creates CAPI cluster
  - Provisions CNPG database
  - Sets up networking
  ↓
Claim conditions update:
  - Synced=True
  - Ready=False (provisioning)
  - Ready=True (complete)
```

### Step 3: Controller Updates Database

```
Controller watches Claim
  ↓
Detects Ready=False
  ↓
UPDATE tenants SET provisioning_status='provisioning'
  ↓
Detects Ready=True
  ↓
UPDATE tenants SET provisioning_status='ready', provisioned_at=NOW()
```

### Step 4: User Queries Status

```
User: "Is my environment ready?"
  ↓
Hub API queries PostgreSQL (NOT Kubernetes)
  ↓
SELECT provisioning_status, provisioning_message 
FROM tenants WHERE id='acme-corp'
  ↓
Returns: {"status": "ready", "message": null}
```

## Controller Implementation

### Minimal Controller Structure

```go
// internal/controller/ainatovesaas_controller.go
package controller

import (
    "context"
    "github.com/jackc/pgx/v5/pgxpool"
    platformv1alpha1 "github.com/zero-ops/platform/api/v1alpha1"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"
)

type AINativeSaaSReconciler struct {
    client.Client
    DB *pgxpool.Pool
}

func (r *AINativeSaaSReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    claim := &platformv1alpha1.AINativeSaaS{}
    if err := r.Get(ctx, req.NamespacedName, claim); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    status := r.deriveStatus(claim)
    
    _, err := r.DB.Exec(ctx, `
        UPDATE tenants 
        SET provisioning_status = $1, 
            provisioning_message = $2,
            provisioned_at = CASE WHEN $1 = 'ready' THEN NOW() ELSE provisioned_at END,
            updated_at = NOW()
        WHERE id = $3
    `, status.Phase, status.Message, claim.Name)
    
    return ctrl.Result{}, err
}

func (r *AINativeSaaSReconciler) deriveStatus(claim *platformv1alpha1.AINativeSaaS) Status {
    if !claim.DeletionTimestamp.IsZero() {
        return Status{Phase: "deleting", Message: ""}
    }
    
    ready := isConditionTrue(claim, "Ready")
    synced := isConditionTrue(claim, "Synced")
    
    if !synced {
        msg := getConditionMessage(claim, "Synced")
        return Status{Phase: "failed", Message: "Config error: " + msg}
    }
    
    if ready {
        return Status{Phase: "ready", Message: ""}
    }
    
    if hasConditions(claim) {
        return Status{Phase: "provisioning", Message: "Creating infrastructure"}
    }
    
    return Status{Phase: "provisioning", Message: "Initialising"}
}

type Status struct {
    Phase   string
    Message string
}
```

### Deployment

```yaml
# manifests/tenant-controller/deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: tenant-controller
  namespace: zero-ops-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: tenant-controller
  template:
    metadata:
      labels:
        app: tenant-controller
    spec:
      serviceAccountName: tenant-controller
      containers:
      - name: controller
        image: ghcr.io/zero-ops/tenant-controller:latest
        env:
        - name: DATABASE_URL
          valueFrom:
            secretKeyRef:
              name: postgres-credentials
              key: url
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: tenant-controller
  namespace: zero-ops-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: tenant-controller
rules:
- apiGroups: ["platform.zero-ops.io"]
  resources: ["ainativesaas"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: tenant-controller
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: tenant-controller
subjects:
- kind: ServiceAccount
  name: tenant-controller
  namespace: zero-ops-system
```

## Why This Pattern Works

### GitOps for Infrastructure (95%)
- **Declarative**: All infrastructure in Git
- **Auditable**: Git history is audit trail
- **Rollback**: Git revert = infrastructure rollback
- **No API timeouts**: Async by design

### Controller for Status (Critical)
- **Real-time**: Kubernetes Watch API pushes changes
- **Reliable**: Controller-runtime handles reconnections
- **Simple**: ~100 lines of Go
- **Standard**: Idiomatic Kubernetes pattern

### Events for Coordination (5%)
- **Cross-cluster**: Management → Spoke communication
- **User actions**: Non-Git operations (restart, scale)
- **Notifications**: Optional status alerts

## Hub API Implementation

The Hub API never touches Kubernetes:

```go
// cmd/zero-ops-api/handlers/environment.go

func (h *EnvironmentHandler) GetEnvironmentStatus(c *gin.Context) {
    tenantID := c.GetString("tenant_id")
    
    var status struct {
        Phase   string    `json:"status"`
        Message string    `json:"message"`
        ReadyAt time.Time `json:"ready_at"`
    }
    
    err := h.db.QueryRow(c.Request.Context(), `
        SELECT provisioning_status, provisioning_message, provisioned_at
        FROM tenants
        WHERE id = $1
    `, tenantID).Scan(&status.Phase, &status.Message, &status.ReadyAt)
    
    if err != nil {
        c.JSON(404, gin.H{"error": "Tenant not found"})
        return
    }
    
    c.JSON(200, status)
}
```

**No Kubernetes client. No kubectl. Just PostgreSQL.**

## Summary

**The Pattern:**
1. **GitOps** handles all infrastructure provisioning (Git → ArgoCD → Crossplane)
2. **Controller** syncs Crossplane status to PostgreSQL (K8s Watch → DB UPDATE)
3. **Events** handle coordination only (cross-cluster, user actions, notifications)
4. **Hub API** reads PostgreSQL (no Kubernetes access)

**Why It Works:**
- Simple: Each component has one job
- Reliable: Standard Kubernetes patterns
- Scalable: No polling, event-driven
- Maintainable: Minimal custom code

**Implementation Effort:**
- Controller: ~150 lines of Go
- Database migration: ~10 lines of SQL
- Deployment manifests: ~50 lines of YAML
- Total: 4-6 hours

This is the idiomatic Kubernetes way to build a SaaS platform.
