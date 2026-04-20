# Design Document: Hub Operator

## Overview

The hub-operator is a Kubernetes Operator that manages Day-2 operations for the Zero-Ops Hub cluster. It replaces imperative CLI-driven bootstrap logic with declarative, GitOps-native reconciliation. The operator watches the HubEnvironment Custom Resource and orchestrates Secret Zero generation, database migrations, role provisioning, OAuth client registration, NATS stream creation, and Infisical backup operations.

### Architecture Principles

- **GitOps-First**: All operator changes deployed via ArgoCD, never kubectl apply
- **Idempotent Reconciliation**: Safe to retry infinitely, no state corruption
- **Dependency-Aware**: Watches external resources (CNPG, Hydra, Infisical, NATS) and reconciles when ready
- **Status-Driven**: Comprehensive status conditions track reconciliation progress
- **Code Reuse**: Migrates existing CLI logic from `internal/hub/components/` to operator

### High-Level Flow

```
ArgoCD Sync Wave 0: CRDs (HubEnvironment, CNPG, etc.)
ArgoCD Sync Wave 1: hub-operator Deployment + HubEnvironment CR
  ↓
hub-operator starts, watches HubEnvironment CR
  ↓
Phase 1: Generate Secret Zero (infisical-secrets, platform-db-app, infisical-db-credentials)
  ↓
ArgoCD Sync Wave 2: CNPG Cluster, NATS, Redis (use Secret Zero)
  ↓
Phase 2: CNPG Ready → Execute Migrations → Create Database Roles
  ↓
ArgoCD Sync Wave 3: Infisical, SPIRE, Hydra (use database roles)
  ↓
Phase 3: Infisical Ready → Upload Secrets to Infisical API
Phase 4: Hydra Ready → Register OAuth Clients
Phase 5: NATS Ready → Create JetStream Streams
  ↓
ArgoCD Sync Wave 4+: AgentGateway, MCP Server, Console
```

## Architecture

### Component Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                         Hub Cluster                              │
│                                                                   │
│  ┌──────────────┐         ┌─────────────────────────────────┐  │
│  │   ArgoCD     │────────>│      hub-operator               │  │
│  │              │         │                                 │  │
│  │ Syncs:       │         │  Reconciles HubEnvironment CR   │  │
│  │ - CRDs       │         │                                 │  │
│  │ - Operator   │         │  Phases:                        │  │
│  │ - HubEnv CR  │         │  1. Secret Zero Generation      │  │
│  └──────────────┘         │  2. Database Setup              │  │
│                            │  3. External Service Config     │  │
│                            └─────────────────────────────────┘  │
│                                      │                           │
│                                      │ Watches & Reconciles      │
│                                      ↓                           │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │              External Dependencies                        │  │
│  │                                                            │  │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐ │  │
│  │  │   CNPG   │  │ Infisical│  │  Hydra   │  │   NATS   │ │  │
│  │  │ Cluster  │  │          │  │          │  │          │ │  │
│  │  └──────────┘  └──────────┘  └──────────┘  └──────────┘ │  │
│  └──────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```


### Reconciliation State Machine

```
┌─────────────────────────────────────────────────────────────────┐
│                    HubEnvironment Reconciliation                 │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ↓
                    ┌──────────────────┐
                    │  Phase 1: Init   │
                    │  Generate Secret │
                    │      Zero        │
                    └──────────────────┘
                              │
                              ↓
                    ┌──────────────────┐
                    │ Wait for CNPG    │
                    │   Ready=True     │
                    └──────────────────┘
                              │
                              ↓
                    ┌──────────────────┐
                    │  Phase 2: DB     │
                    │  - Run Migrations│
                    │  - Create Roles  │
                    └──────────────────┘
                              │
                              ↓
                    ┌──────────────────┐
                    │ Wait for External│
                    │ Services Ready   │
                    │ (Infisical/Hydra│
                    │     /NATS)       │
                    └──────────────────┘
                              │
                              ↓
                    ┌──────────────────┐
                    │  Phase 3: Config │
                    │  - Upload Secrets│
                    │  - Register OAuth│
                    │  - Create Streams│
                    └──────────────────┘
                              │
                              ↓
                    ┌──────────────────┐
                    │  Status: Ready   │
                    │  All Conditions  │
                    │     = True       │
                    └──────────────────┘
```

### Concurrency & Execution Model

To ensure the Hub cluster's state remains consistent and free of race conditions, the `hub-operator` relies on the `controller-runtime` concurrency guarantees:

#### Single-Flight Reconciliation
- **Per-Resource Mutex:** The controller workqueue ensures that only one reconciliation loop runs for a specific `HubEnvironment` Custom Resource at any given time.
- **Parallel Independence:** Concurrent reconciles for *different* CRs are safe as they do not share in-memory state.
- **Leader Election:** In HA mode (2 replicas), `coordination.k8s.io/Leases` ensures an Active-Passive architecture. Only the elected leader watches resources and executes the reconcile loop. Failover occurs within 15 seconds of leader pod termination.

#### Mid-Reconcile Spec Changes (Optimistic Concurrency)
- If a Platform Admin updates the `HubEnvironment` YAML in Git while the operator is actively reconciling Phase 2 (Database Setup), the Kubernetes API increments the CR's `ResourceVersion`.
- When the current reconcile loop attempts to call `r.Status().Update()`, it will receive a `409 Conflict` from the API server.
- **Resolution:** The operator safely aborts the stale update. The workqueue immediately triggers a fresh reconciliation loop with the new `ResourceVersion` and updated desired state.

## Components and Interfaces

### Operator Directory Structure

Following the `operators/spoke-controller/` pattern:

```
operators/hub-operator/
├── cmd/
│   └── main.go                          # Operator entry point
├── api/
│   └── v1alpha1/
│       ├── hubenvironment_types.go      # CRD type definitions
│       ├── groupversion_info.go         # API group metadata
│       └── zz_generated.deepcopy.go     # Generated deepcopy methods
├── internal/
│   ├── controller/
│   │   └── hubenvironment_controller.go # Main reconciler
│   ├── client/
│   │   ├── infisical.go                 # Migrated from internal/hub/infisical/client.go
│   │   ├── hydra.go                     # Ory Hydra SDK wrapper
│   │   └── nats.go                      # NATS JetStream SDK wrapper
│   ├── secrets/
│   │   └── generator.go                 # Migrated from internal/hub/components/secrets.go
│   ├── database/
│   │   ├── migrator.go                  # golang-migrate/migrate wrapper
│   │   └── roles.go                     # Database role provisioning
│   └── embed/
│       └── migrations.go                # Embedded SQL files
├── config/                              # Kubebuilder generated
│   ├── crd/
│   │   └── bases/
│   │       └── ops.nutgraf.in_hubenvironments.yaml
│   ├── rbac/
│   │   ├── role.yaml
│   │   └── role_binding.yaml
│   ├── manager/
│   │   └── manager.yaml
│   └── default/
│       └── kustomization.yaml
├── manifests/                           # Deployment manifests
│   ├── deployment.yaml
│   ├── service_account.yaml
│   ├── rbac.yaml
│   └── kustomization.yaml
└── tests/
    └── e2e/                             # KUTTL tests
        ├── 01-secret-zero/
        │   ├── 00-install.yaml
        │   ├── 00-assert.yaml
        │   └── verify.sh
        ├── 02-database-setup/
        │   ├── 00-install.yaml
        │   ├── 00-assert.yaml
        │   └── verify.sh
        └── 03-external-services/
            ├── 00-install.yaml
            ├── 00-assert.yaml
            └── verify.sh
```


### Controller Reconciliation Logic

**Main Reconciler (`internal/controller/hubenvironment_controller.go`):**

```go
type HubEnvironmentReconciler struct {
    client.Client
    Scheme          *runtime.Scheme
    InfisicalClient *infisical.Client
    HydraClient     *hydra.Client
    NATSClient      *nats.Client
    DBMigrator      *database.Migrator
}

func (r *HubEnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 1. Fetch HubEnvironment CR
    hubEnv := &opsv1alpha1.HubEnvironment{}
    if err := r.Get(ctx, req.NamespacedName, hubEnv); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 1.5. Check for reconcile-trigger annotation to resume from permanent errors
    if triggerTime, ok := hubEnv.Annotations["ops.nutgraf.in/reconcile-trigger"]; ok {
        // Clear any permanent error conditions to allow retry
        for i := range hubEnv.Status.Conditions {
            if hubEnv.Status.Conditions[i].Reason == "DirtyDatabase" {
                meta.RemoveStatusCondition(&hubEnv.Status.Conditions, hubEnv.Status.Conditions[i].Type)
            }
        }
        // Remove the annotation after processing
        delete(hubEnv.Annotations, "ops.nutgraf.in/reconcile-trigger")
        if err := r.Update(ctx, hubEnv); err != nil {
            return ctrl.Result{}, err
        }
        // Log the manual trigger
        r.Log.Info("Manual reconciliation triggered", "timestamp", triggerTime)
    }

    // 2. Phase 1: Generate Secret Zero (idempotent)
    if !meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "SecretZeroGenerated") {
        if err := r.generateSecretZero(ctx, hubEnv); err != nil {
            return ctrl.Result{RequeueAfter: 10 * time.Second}, err
        }
        meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
            Type:   "SecretZeroGenerated",
            Status: metav1.ConditionTrue,
            Reason: "Generated",
        })
        return ctrl.Result{Requeue: true}, r.Status().Update(ctx, hubEnv)
    }

    // 3. Wait for CNPG Cluster Ready
    cnpgReady, err := r.isCNPGReady(ctx, hubEnv)
    if err != nil || !cnpgReady {
        return ctrl.Result{RequeueAfter: 10 * time.Second}, err
    }

    // 4. Phase 2: Database Setup
    if !meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "MigrationsComplete") {
        if err := r.runMigrations(ctx, hubEnv); err != nil {
            // Check if this is a dirty database error (permanent)
            if isDirtyDatabaseError(err) {
                meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
                    Type:    "MigrationsComplete",
                    Status:  metav1.ConditionFalse,
                    Reason:  "DirtyDatabase",
                    Message: err.Error(),
                })
                // DO NOT requeue - requires manual CLI intervention
                return ctrl.Result{}, r.Status().Update(ctx, hubEnv)
            }
            // Transient error - requeue with backoff
            meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
                Type:    "MigrationsComplete",
                Status:  metav1.ConditionFalse,
                Reason:  "MigrationFailed",
                Message: err.Error(),
            })
            return ctrl.Result{RequeueAfter: 30 * time.Second}, r.Status().Update(ctx, hubEnv)
        }
        meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
            Type:   "MigrationsComplete",
            Status: metav1.ConditionTrue,
            Reason: "Completed",
        })
        return ctrl.Result{Requeue: true}, r.Status().Update(ctx, hubEnv)
    }

    if !meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "DatabaseRolesConfigured") {
        if err := r.createDatabaseRoles(ctx, hubEnv); err != nil {
            meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
                Type:    "DatabaseRolesConfigured",
    // 5. Phase 3: External Services Configuration
    // ------------------------------------------------------------------------
    // CRITICAL OWNERSHIP BOUNDARY: INFISICAL UPLOAD
    // ------------------------------------------------------------------------
    // The Hub Operator is responsible for bootstrapping Secret Zero ONLY.
    // Once SecretsBackedUp=True, Infisical becomes the absolute Source of Truth.
    // The External Secrets Operator (ESO) takes over the lifecycle of syncing
    // secrets from Infisical down to Kubernetes.
    // 
    // The Hub Operator MUST NOT upload to Infisical again after this phase
    // completes, to prevent split-brain and overwriting human-initiated 
    // password rotations in the Infisical UI.
    // ------------------------------------------------------------------------
    if !meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "SecretsBackedUp") {
        infisicalReady, err := r.isInfisicalReady(ctx, hubEnv)
        if err != nil || !infisicalReady {
            return ctrl.Result{RequeueAfter: 10 * time.Second}, err
        }
        
        // Check if infisical-auth secret exists (GAP #6: Missing Universal Auth Secret)
        infisicalAuth := &corev1.Secret{}
        if err := r.UncachedClient.Get(ctx, client.ObjectKey{
            Name:      "infisical-auth",
            Namespace: "hub-platform-ops",
        }, infisicalAuth); err != nil {
            if errors.IsNotFound(err) {
                // Phase 3 suspended, but Phases 1 & 2 can proceed
                meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
                    Type:    "SecretsBackedUp",
                    Status:  metav1.ConditionFalse,
                    Reason:  "AuthenticationFailed",
                    Message: "infisical-auth secret not found. Phase 3 suspended. Phases 1 & 2 can proceed.",
                })
                r.Log.Info("Infisical auth secret missing, suspending Phase 3 only")
                // Continue to next phases - don't block entire reconciliation
                return ctrl.Result{RequeueAfter: 30 * time.Second}, r.Status().Update(ctx, hubEnv)
            }
            return ctrl.Result{RequeueAfter: 10 * time.Second}, err
        }
        
        // ONE-TIME OPERATION: Upload generated passwords
        if err := r.uploadSecretsToInfisical(ctx, hubEnv); err != nil {
            return ctrl.Result{RequeueAfter: 30 * time.Second}, err
        }
        meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
            Type:   "SecretsBackedUp",
            Status: metav1.ConditionTrue,
            Reason: "Uploaded",
        })
        return ctrl.Result{Requeue: true}, r.Status().Update(ctx, hubEnv)
    }
        }
        
        // ONE-TIME OPERATION: Only upload if SecretsBackedUp is False
        // After bootstrap, ESO owns the lifecycle (no upstream overwrites)
        if err := r.uploadSecretsToInfisical(ctx, hubEnv); err != nil {
            return ctrl.Result{RequeueAfter: 30 * time.Second}, err
        }
        meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
            Type:   "SecretsBackedUp",
            Status: metav1.ConditionTrue,
            Reason: "Uploaded",
        })
        return ctrl.Result{Requeue: true}, r.Status().Update(ctx, hubEnv)
    }

    if !meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "OAuthClientsRegistered") {
        hydraReady, err := r.isHydraReady(ctx, hubEnv)
        if err != nil || !hydraReady {
            return ctrl.Result{RequeueAfter: 10 * time.Second}, err
        }
        if err := r.registerOAuthClients(ctx, hubEnv); err != nil {
            return ctrl.Result{RequeueAfter: 30 * time.Second}, err
        }
        meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
            Type:   "OAuthClientsRegistered",
            Status: metav1.ConditionTrue,
            Reason: "Registered",
        })
        return ctrl.Result{Requeue: true}, r.Status().Update(ctx, hubEnv)
    }

    if !meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "NATSStreamsConfigured") {
        natsReady, err := r.isNATSReady(ctx, hubEnv)
        if err != nil || !natsReady {
            return ctrl.Result{RequeueAfter: 10 * time.Second}, err
        }
        if err := r.createNATSStreams(ctx, hubEnv); err != nil {
            return ctrl.Result{RequeueAfter: 30 * time.Second}, err
        }
        meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
            Type:   "NATSStreamsConfigured",
            Status: metav1.ConditionTrue,
            Reason: "Configured",
        })
        return ctrl.Result{Requeue: true}, r.Status().Update(ctx, hubEnv)
    }

    // 6. Set Ready condition
    meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
        Type:   "Ready",
        Status: metav1.ConditionTrue,
        Reason: "AllPhasesComplete",
    })
    return ctrl.Result{}, r.Status().Update(ctx, hubEnv)
}

// handleCertificateRotation detects platform-db-ca changes and updates dependent secrets
// GAP #5: Complete CA Certificate Rotation Scope
func (r *HubEnvironmentReconciler) handleCertificateRotation(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) error {
    namespace := hubEnv.Spec.Database.Namespace
    
    // Read CA certificate from CNPG-managed secret using UNCACHED client
    // This bypasses the cache transformer that strips secret data
    caSecret := &corev1.Secret{}
    if err := r.UncachedClient.Get(ctx, client.ObjectKey{
        Name:      "platform-db-ca",
        Namespace: namespace,
    }, caSecret); err != nil {
        return fmt.Errorf("failed to read platform-db-ca: %w", err)
    }
    
    caCert, ok := caSecret.Data["ca.crt"]
    if !ok {
        return fmt.Errorf("ca.crt not found in platform-db-ca secret")
    }
    
    // Read current infisical-secrets
    infisicalSecret := &corev1.Secret{}
    if err := r.UncachedClient.Get(ctx, client.ObjectKey{
        Name:      "infisical-secrets",
        Namespace: namespace,
    }, infisicalSecret); err != nil {
        return fmt.Errorf("failed to read infisical-secrets: %w", err)
    }
    
    // Check if DB_ROOT_CERT needs update
    currentCert := infisicalSecret.Data["DB_ROOT_CERT"]
    newCert := base64.StdEncoding.EncodeToString(caCert)
    
    if string(currentCert) != newCert {
        // Update DB_ROOT_CERT in infisical-secrets
        infisicalSecret.Data["DB_ROOT_CERT"] = []byte(newCert)
        if err := r.Update(ctx, infisicalSecret); err != nil {
            return fmt.Errorf("failed to update infisical-secrets: %w", err)
        }
        
        // Restart ALL database-connected services to reload CA certificate
        // This prevents x509: certificate signed by unknown authority errors
        
        // Infisical Deployment
        if err := r.restartDeployment(ctx, "platform-infisical-standalone", namespace); err != nil {
            return fmt.Errorf("failed to restart Infisical: %w", err)
        }
        
        // Redis StatefulSet
        if err := r.restartStatefulSet(ctx, "redis-master", namespace); err != nil {
            return fmt.Errorf("failed to restart Redis: %w", err)
        }
        
        // Ory Hydra Deployment
        if err := r.restartDeployment(ctx, "ory-hydra", "ory-system"); err != nil {
            return fmt.Errorf("failed to restart Hydra: %w", err)
        }
        
        // Ory Kratos Deployment
        if err := r.restartDeployment(ctx, "ory-kratos", "ory-system"); err != nil {
            return fmt.Errorf("failed to restart Kratos: %w", err)
        }
        
        // Ory Keto Deployment
        if err := r.restartDeployment(ctx, "ory-keto", "ory-system"); err != nil {
            return fmt.Errorf("failed to restart Keto: %w", err)
        }
        
        // SPIRE Server StatefulSet
        if err := r.restartStatefulSet(ctx, "spire-server", "hub-platform-identity"); err != nil {
            return fmt.Errorf("failed to restart SPIRE Server: %w", err)
        }
        
        // MCP Server Deployment
        if err := r.restartDeployment(ctx, "mcp-server", "hub-platform-core"); err != nil {
            return fmt.Errorf("failed to restart MCP Server: %w", err)
        }
        
        r.Log.Info("Certificate rotation complete, all database-connected services restarted")
    }
    
    return nil
}

// restartDeployment patches a Deployment with restartedAt annotation to trigger rolling restart
func (r *HubEnvironmentReconciler) restartDeployment(ctx context.Context, name, namespace string) error {
    deployment := &appsv1.Deployment{}
    if err := r.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, deployment); err != nil {
        return err
    }
    
    // Patch with restartedAt annotation
    patch := client.MergeFrom(deployment.DeepCopy())
    if deployment.Spec.Template.Annotations == nil {
        deployment.Spec.Template.Annotations = make(map[string]string)
    }
    deployment.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)
    
    return r.Patch(ctx, deployment, patch)
}

// restartStatefulSet patches a StatefulSet with restartedAt annotation to trigger rolling restart
func (r *HubEnvironmentReconciler) restartStatefulSet(ctx context.Context, name, namespace string) error {
    statefulSet := &appsv1.StatefulSet{}
    if err := r.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, statefulSet); err != nil {
        return err
    }
    
    // Patch with restartedAt annotation
    patch := client.MergeFrom(statefulSet.DeepCopy())
    if statefulSet.Spec.Template.Annotations == nil {
        statefulSet.Spec.Template.Annotations = make(map[string]string)
    }
    statefulSet.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)
    
    return r.Patch(ctx, statefulSet, patch)
}

// isDirtyDatabaseError checks if an error is a permanent dirty database state
func isDirtyDatabaseError(err error) bool {
    var dirtyErr *database.DirtyDatabaseError
    return errors.As(err, &dirtyErr)
}
```


### Watch Configuration

```go
func (r *HubEnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&opsv1alpha1.HubEnvironment{}).
        // Own all secrets created by operator
        Owns(&corev1.Secret{}).
        // Watch CNPG Cluster status changes
        Watches(
            &cnpgv1.Cluster{},
            handler.EnqueueRequestsFromMapFunc(r.findHubEnvironmentForCNPG),
            builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
        ).
        // Watch platform-db-ca secret for certificate rotation (GAP #5)
        Watches(
            &corev1.Secret{},
            handler.EnqueueRequestsFromMapFunc(r.findHubEnvironmentForSecret),
            builder.WithPredicates(
                predicate.ResourceVersionChangedPredicate{},
                predicate.NewPredicateFuncs(func(obj client.Object) bool {
                    return obj.GetName() == "platform-db-ca" && obj.GetNamespace() == "hub-platform-data"
                }),
            ),
        ).
        // Watch ESO-managed secrets for password rotation (GAP #2)
        Watches(
            &corev1.Secret{},
            handler.EnqueueRequestsFromMapFunc(r.findHubEnvironmentForSecret),
            builder.WithPredicates(
                predicate.ResourceVersionChangedPredicate{},
                predicate.NewPredicateFuncs(func(obj client.Object) bool {
                    // Watch secrets with label ops.nutgraf.in/db-credentials=true
                    labels := obj.GetLabels()
                    return labels != nil && labels["ops.nutgraf.in/db-credentials"] == "true"
                }),
            ),
        ).
        // Watch Hydra Deployment status changes
        Watches(
            &appsv1.Deployment{},
            handler.EnqueueRequestsFromMapFunc(r.findHubEnvironmentForDeployment),
            builder.WithPredicates(
                predicate.ResourceVersionChangedPredicate{},
                predicate.NewPredicateFuncs(func(obj client.Object) bool {
                    return obj.GetName() == "ory-hydra" && obj.GetNamespace() == "ory-system"
                }),
            ),
        ).
        // Watch Infisical Deployment status changes
        Watches(
            &appsv1.Deployment{},
            handler.EnqueueRequestsFromMapFunc(r.findHubEnvironmentForDeployment),
            builder.WithPredicates(
                predicate.ResourceVersionChangedPredicate{},
                predicate.NewPredicateFuncs(func(obj client.Object) bool {
                    return obj.GetName() == "platform-infisical-standalone" && obj.GetNamespace() == "hub-platform-data"
                }),
            ),
        ).
        // Watch NATS StatefulSet status changes
        Watches(
            &appsv1.StatefulSet{},
            handler.EnqueueRequestsFromMapFunc(r.findHubEnvironmentForStatefulSet),
            builder.WithPredicates(
                predicate.ResourceVersionChangedPredicate{},
                predicate.NewPredicateFuncs(func(obj client.Object) bool {
                    return obj.GetName() == "nats" && obj.GetNamespace() == "hub-platform-core"
                }),
            ),
        ).
        Complete(r)
}
```

### Memory Optimization

Following the `gitops-operator` pattern from reference-patterns.md, with critical modification for operational secrets:

```go
// In cmd/main.go
func main() {
    // ... setup code ...

    // Strip secret data from cache EXCEPT for operational secrets
    // Operational secrets (platform-db-superuser, platform-db-ca, infisical-auth) 
    // are read via uncached client.Reader to bypass cache transformation
    mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
        Scheme: scheme,
        Cache: cache.Options{
            ByObject: map[client.Object]cache.ByObject{
                &corev1.Secret{}: {
                    // Strip data for all secrets in cache
                    Transform: cacheutils.StripDataFromSecretOrConfigMapTransform(),
                },
            },
        },
        // ... other options ...
    })
    
    // Create uncached reader for operational secrets
    // This bypasses the cache transformer and reads directly from API server
    uncachedClient, err := client.New(mgr.GetConfig(), client.Options{
        Scheme: mgr.GetScheme(),
    })
    if err != nil {
        return err
    }
    
    // Pass both cached and uncached clients to reconciler
    if err = (&controller.HubEnvironmentReconciler{
        Client:         mgr.GetClient(),        // Cached client for most operations
        UncachedClient: uncachedClient,         // Uncached client for reading operational secrets
        Scheme:         mgr.GetScheme(),
    }).SetupWithManager(mgr); err != nil {
        return err
    }
}
```

**Critical Pattern for Reading Operational Secrets:**

```go
// WRONG: Uses cached client, secret data will be stripped
secret := &corev1.Secret{}
if err := r.Client.Get(ctx, key, secret); err != nil {
    return err
}
password := string(secret.Data["password"]) // EMPTY! Data was stripped by cache

// CORRECT: Uses uncached client, bypasses cache transformer
secret := &corev1.Secret{}
if err := r.UncachedClient.Get(ctx, key, secret); err != nil {
    return err
}
password := string(secret.Data["password"]) // Contains actual password
```

**When to Use Uncached Client:**
- Reading `platform-db-superuser` for database migrations
- Reading `platform-db-ca` for certificate extraction
- Reading `infisical-auth` for Infisical API authentication
- Reading any secret where you need the actual `Data` payload

**When to Use Cached Client:**
- Checking if a secret exists (metadata only)
- Listing secrets
- Watching for secret changes
- All non-secret resources

## Data Models

### HubEnvironment CRD Schema

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: hubenvironments.ops.nutgraf.in
spec:
  group: ops.nutgraf.in
  names:
    kind: HubEnvironment
    listKind: HubEnvironmentList
    plural: hubenvironments
    singular: hubenvironment
  scope: Cluster
  versions:
  - name: v1alpha1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              domain:
                type: string
                description: "Base domain for the Hub cluster (e.g., nutgraf.in)"
              tls:
                type: object
                properties:
                  issuer:
                    type: string
                    description: "cert-manager ClusterIssuer name"
                  email:
                    type: string
                    description: "Email for Let's Encrypt notifications"
              observability:
                type: object
                properties:
                  victoriaMetrics:
                    type: object
                    properties:
                      retentionPeriod:
                        type: string
                        default: "30d"
                  grafanaAlloy:
                    type: object
                    properties:
                      scrapeInterval:
                        type: string
                        default: "30s"
              database:
                type: object
                properties:
                  clusterRef:
                    type: string
                    description: "Name of CNPG Cluster (e.g., platform-db)"
                  namespace:
                    type: string
                    description: "Namespace of CNPG Cluster"
                  roles:
                    type: array
                    items:
                      type: object
                      properties:
                        name:
                          type: string
                        database:
                          type: string
                        permissions:
                          type: array
                          items:
                            type: string
              nats:
                type: object
                properties:
                  streams:
                    type: array
                    items:
                      type: object
                      properties:
                        name:
                          type: string
                        subjects:
                          type: array
                          items:
                            type: string
                        retention:
                          type: string
                          enum: ["limits", "interest", "workqueue"]
                        storage:
                          type: string
                          enum: ["file", "memory"]
              oauth:
                type: object
                properties:
                  clients:
                    type: array
                    items:
                      type: object
                      properties:
                        clientId:
                          type: string
                        clientName:
                          type: string
                        redirectUris:
                          type: array
                          items:
                            type: string
                        grantTypes:
                          type: array
                          items:
                            type: string
                        responseTypes:
                          type: array
                          items:
                            type: string
              secrets:
                type: object
                properties:
                  infisical:
                    type: object
                    properties:
                      projectSlug:
                        type: string
                        default: "platform"
                      environmentSlug:
                        type: string
                        default: "prod"
          status:
            type: object
            properties:
              conditions:
                type: array
                items:
                  type: object
                  properties:
                    type:
                      type: string
                    status:
                      type: string
                    reason:
                      type: string
                    message:
                      type: string
                    lastTransitionTime:
                      type: string
                      format: date-time
              observedGeneration:
                type: integer
    subresources:
      status: {}
```


### Example HubEnvironment CR

```yaml
apiVersion: ops.nutgraf.in/v1alpha1
kind: HubEnvironment
metadata:
  name: hub-production
spec:
  domain: nutgraf.in
  tls:
    issuer: letsencrypt-prod
    email: ops@nutgraf.in
  observability:
    victoriaMetrics:
      retentionPeriod: 90d
    grafanaAlloy:
      scrapeInterval: 15s
  database:
    clusterRef: platform-db
    namespace: hub-platform-data
    roles:
    - name: mcp_server
      database: control_plane
      permissions:
      - SELECT
      - INSERT
      - UPDATE
      - DELETE
    - name: infisical
      database: infisical
      permissions:
      - SELECT
      - INSERT
      - UPDATE
      - DELETE
    - name: spoke_controller
      database: hub
      permissions:
      - SELECT
      - INSERT
      - UPDATE
    - name: spire_server
      database: spire
      permissions:
      - SELECT
      - INSERT
      - UPDATE
      - DELETE
    # GAP #3: Ory Identity Stack roles
    - name: hydra
      database: hydra
      permissions:
      - SELECT
      - INSERT
      - UPDATE
      - DELETE
    - name: kratos
      database: kratos
      permissions:
      - SELECT
      - INSERT
      - UPDATE
      - DELETE
    - name: keto
      database: keto
      permissions:
      - SELECT
      - INSERT
      - UPDATE
      - DELETE
  nats:
    streams:
    - name: spoke-events
      subjects:
      - "spoke.*.lifecycle.>"
      - "spoke.*.billing.>"
      retention: limits
      storage: file
    - name: hub-events
      subjects:
      - "hub.notifications.>"
      retention: interest
      storage: file
  oauth:
    clients:
    - clientId: mcp-server
      clientName: "MCP Server"
      redirectUris:
      - "https://mcp.nutgraf.in/callback"
      grantTypes:
      - authorization_code
      - refresh_token
      responseTypes:
      - code
    - clientId: spoke-controller
      clientName: "Spoke Controller"
      redirectUris: []
      grantTypes:
      - client_credentials
      responseTypes:
      - token
  secrets:
    infisical:
      projectSlug: platform
      environmentSlug: prod
```

## Correctness Properties

*A property is a characteristic or behavior that should hold true across all valid executions of a system—essentially, a formal statement about what the system should do. Properties serve as the bridge between human-readable specifications and machine-verifiable correctness guarantees.*

### Property Reflection

After analyzing all acceptance criteria, I identified the following redundancies:

**Redundant Properties to Eliminate:**
- Individual secret generation properties (2.1, 2.2, 2.3, 4.1-4.12) → Combined into comprehensive "Secret Zero Generation" property
- Individual role creation properties (2.5-2.8, 6.1-6.4) → Combined into "Database Role Provisioning" property
- Individual error handling properties (5.5-5.6, 6.7-6.8, 7.5-7.6, 8.5-8.6, 9.11-9.12) → Combined into "Error Handling with Exponential Backoff" property
- Individual status condition properties (5.7, 6.9, 7.7, 8.7, 9.13, 11.1-11.9) → Combined into "Status Condition Reflection" property
- Individual dependency watch properties (2.12, 2.13, 2.14, 5.1, 7.1, 8.1) → Combined into "Dependency-Aware Reconciliation" property

**Unique Properties Retained:**
- Idempotency (4.13, 7.4, 8.4, 10.1-10.8) → Core correctness guarantee
- OwnerReference management (4.14) → Kubernetes garbage collection
- Secret upload to Infisical (2.11, 9.1-9.10) → Backup and recovery
- OAuth client registration (7.1-7.7) → Identity integration
- NATS stream creation (8.1-8.7) → Messaging infrastructure
- CR parsing round-trip (21.1-21.4) → Configuration validation

### Property 1: Secret Zero Generation Completeness

*For any* HubEnvironment CR, when the operator reconciles, all required Secret Zero resources SHALL be created with correct structure and cryptographically secure values:
- `infisical-secrets` (ENCRYPTION_KEY, AUTH_SECRET, REDIS_URL, DB_ROOT_CERT)
- `platform-db-app` (BasicAuth type with username/password)
- `infisical-db-credentials` (username/password)
- `infisical-postgres-connection` (DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME, DB_SSL_MODE)

**Validates: Requirements 2.1, 2.2, 2.3, 4.1-4.12**

### Property 2: Secret Generation Idempotency

*For any* existing Secret Zero resource, when the operator reconciles multiple times, the password/key values SHALL remain unchanged to maintain data integrity and prevent service disruption.

**Validates: Requirements 4.13, 10.1-10.8**

### Property 3: OwnerReference Propagation

*For any* Kubernetes Secret created by the operator, the Secret SHALL have an ownerReference pointing to the HubEnvironment CR, enabling automatic garbage collection when the CR is deleted.

**Validates: Requirements 4.14**

### Property 4: Database Migration Execution

*For any* HubEnvironment CR, when the CNPG Cluster status becomes Ready, the operator SHALL execute all embedded SQL migration files in order using golang-migrate/migrate library and update the MigrationsComplete condition.

**Validates: Requirements 2.4, 5.1, 5.7**

### Property 5: Database Role Provisioning

*For any* HubEnvironment CR with database role specifications, when migrations complete successfully, the operator SHALL create all specified database roles with appropriate permissions using idempotent CREATE ROLE IF NOT EXISTS statements and update the DatabaseRolesConfigured condition.

**Validates: Requirements 2.5-2.8, 6.1-6.4, 6.6, 6.9**

### Property 6: Infisical Secret Backup

*For any* Secret Zero resource created by the operator, when Infisical becomes ready, the operator SHALL upload the secret values to Infisical API (projectSlug/environmentSlug from CR spec) and update the SecretsBackedUp condition.

**Validates: Requirements 2.11, 9.1-9.10, 9.13**

### Property 7: OAuth Client Registration

*For any* HubEnvironment CR with OAuth client specifications, when Hydra becomes ready, the operator SHALL register all specified OAuth clients using the Ory Hydra Go SDK with idempotent client_id-based registration and update the OAuthClientsRegistered condition.

**Validates: Requirements 2.9, 7.1-7.7**

### Property 8: NATS Stream Creation

*For any* HubEnvironment CR with NATS stream specifications, when NATS becomes ready, the operator SHALL create all specified JetStream streams using the NATS Go SDK with idempotent stream name-based creation and update the NATSStreamsConfigured condition.

**Validates: Requirements 2.10, 8.1-8.7**

### Property 9: Dependency-Aware Reconciliation

*For any* HubEnvironment CR, the operator SHALL watch external dependency status changes (CNPG Cluster, Hydra Deployment, Infisical Deployment, NATS StatefulSet) and trigger reconciliation when dependencies transition to Ready, ensuring correct execution order without manual intervention.

**Validates: Requirements 2.12, 2.13, 2.14, 5.1, 7.1, 8.1, 12.1-12.7**

### Property 10: Error Handling with Exponential Backoff

*For any* reconciliation phase that encounters a transient error (database connection failure, API 5xx response, network timeout), the operator SHALL update the corresponding status condition to False with error message and requeue with exponential backoff, never panicking or crashing.

**Validates: Requirements 5.5-5.6, 6.7-6.8, 7.5-7.6, 8.5-8.6, 9.11-9.12, 20.1-20.9**

### Property 11: Status Condition Reflection

*For any* HubEnvironment CR, the operator SHALL maintain accurate status conditions (SecretZeroGenerated, MigrationsComplete, DatabaseRolesConfigured, OAuthClientsRegistered, NATSStreamsConfigured, SecretsBackedUp, Ready) reflecting the current reconciliation state, with Ready=True only when all other conditions are True.

**Validates: Requirements 5.7, 6.9, 7.7, 8.7, 9.13, 11.1-11.9**

### Property 12: HubEnvironment CR Round-Trip

*For any* valid HubEnvironment CR YAML, parsing the YAML into a HubEnvironment object, then serializing back to YAML, then parsing again SHALL produce an equivalent object with identical spec fields.

**Validates: Requirements 21.1-21.4**


## Error Handling

### Error Classification

**Transient Errors (Requeue with Backoff):**
- Database connection failures
- External API 5xx responses (Hydra, Infisical, NATS)
- Network timeouts
- Dependency not ready (CNPG, Hydra, Infisical, NATS)

**Permanent Errors (Update Status, No Requeue):**
- Dirty database state (requires manual `migrate force` intervention)
- Invalid CR spec (missing required fields)
- Migration SQL syntax errors (after dirty state check)
- Invalid OAuth client configuration
- Invalid NATS stream configuration

**Handled Errors (No Requeue):**
- Resource already exists (idempotent operations)
- CR deleted (IgnoreNotFound)

### Manual Recovery from Permanent Errors

**GAP #1: Dirty Database Recovery Procedure**

When the operator encounters a dirty database state, it sets `MigrationsComplete=False` with reason `DirtyDatabase` and stops requeuing. To resume reconciliation after manual intervention:

**Step 1: Fix the Database**
```bash
# Connect to the database
kubectl exec -n hub-platform-data platform-db-1 -- psql -U postgres

# Check migration status
SELECT version, dirty FROM schema_migrations;

# If dirty=true, force the migration to the correct version
# (Replace <version> with the actual version number)
\q

# Use migrate CLI to force version
### Retry and Backoff Policy

To prevent the operator from causing API server exhaustion (hot-looping) or silently hanging forever, the operator enforces a strict, multi-tiered backoff policy using `controller-runtime` rate limiters and explicit time bounds.

#### 1. Transient Errors (Network, 5xx API errors)
Recoverable errors caused by temporary network partitions or upstream API instability (e.g., Hydra returns 503).

- **Initial Delay:** 10 seconds
- **Backoff Multiplier:** 2x
- **Max Delay:** 5 minutes (300s)
- **Max Retries:** Infinite (The operator will back off to polling every 5 minutes until the API recovers, keeping the status condition as `False / APIError`).

#### 2. Dependency Checks (Waiting for Pods to be Ready)
When waiting for external resources (CNPG, NATS, Infisical) to report `Ready` status during bootstrapping.

- **Check Interval:** Fixed 10 seconds (`return ctrl.Result{RequeueAfter: 10 * time.Second}, nil`)
- **Max Wait Time:** 30 minutes
- **Timeout Action:** If a dependency fails to become ready after 30 minutes, the operator transitions the corresponding condition (e.g., `DatabaseRolesConfigured`) to `False` with reason `DependencyTimeout`. This surfaces the blocked state to the Platform Console for human investigation.

#### 3. Permanent Errors (Dirty Database, Invalid Config)
Unrecoverable errors that require human intervention (e.g., `golang-migrate` panics leaving a dirty database schema).

- **Retry Policy:** NO automatic retry. (`return ctrl.Result{}, err` with error swallowed to prevent requeue).
- **Action:** Sets condition to `False` with reason `PermanentError` or `DirtyDatabase`.
- **Resolution:** Requires a human to fix the underlying state, then apply the `ops.nutgraf.in/reconcile-trigger: <timestamp>` annotation to the CR to wake the operator up and resume the loop.

### Retry Strategy Implementation


**Step 2: Trigger Operator Reconciliation**
```bash
# Add reconcile-trigger annotation with current timestamp
kubectl annotate hubenvironment hub-production \
  ops.nutgraf.in/reconcile-trigger="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --overwrite
```

The operator watches for this annotation and will:
1. Clear the `DirtyDatabase` condition
2. Remove the annotation
3. Resume reconciliation from Phase 2 (Database Setup)

**Step 3: Verify Recovery**
```bash
# Check operator logs
kubectl logs -n hub-platform-ops deployment/hub-operator -f

# Check HubEnvironment status
kubectl get hubenvironment hub-production -o yaml | grep -A 10 conditions
```

### Retry Strategy

```go
// Transient errors: exponential backoff
if isTransientError(err) {
    return ctrl.Result{RequeueAfter: calculateBackoff(retryCount)}, err
}

// Permanent errors: update status, no automatic retry
if isPermanentError(err) {
    meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
        Type:    conditionType,
        Status:  metav1.ConditionFalse,
        Reason:  "ConfigurationError",
        Message: err.Error(),
    })
    return ctrl.Result{}, r.Status().Update(ctx, hubEnv)
}

// Dirty database error: PERMANENT, requires manual CLI intervention
if isDirtyDatabaseError(err) {
    meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
        Type:    "MigrationsComplete",
        Status:  metav1.ConditionFalse,
        Reason:  "DirtyDatabase",
        Message: fmt.Sprintf("Database in dirty state: %v. Manual intervention required: kubectl exec -n hub-platform-data platform-db-1 -- migrate force <version>", err),
    })
    // DO NOT requeue - operator cannot fix this automatically
    return ctrl.Result{}, r.Status().Update(ctx, hubEnv)
}

// Dependency not ready: short requeue
if isDependencyNotReady(err) {
    return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}
```

### Helper Functions

```go
// isDirtyDatabaseError checks if error is a permanent dirty database state
func isDirtyDatabaseError(err error) bool {
    var dirtyErr *database.DirtyDatabaseError
    return errors.As(err, &dirtyErr)
}

// isTransientError checks if error is temporary and can be retried
func isTransientError(err error) bool {
    if err == nil {
        return false
    }
    // Network errors
    if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
        return true
    }
    // Database connection errors
    if strings.Contains(err.Error(), "connection refused") || 
       strings.Contains(err.Error(), "connection reset") {
        return true
    }
    // HTTP 5xx errors
    if strings.Contains(err.Error(), "status 5") {
        return true
    }
    return false
}

// isPermanentError checks if error requires manual intervention
func isPermanentError(err error) bool {
    if err == nil {
        return false
    }
    // Dirty database is handled separately
    if isDirtyDatabaseError(err) {
        return true
    }
    // Validation errors
    if strings.Contains(err.Error(), "invalid") || 
       strings.Contains(err.Error(), "validation failed") {
        return true
    }
    return false
}
```

## Testing Strategy

### E2E Testing with KUTTL (ONLY Testing Approach)

**CRITICAL CONSTRAINT:** The hub-operator follows the Zero-Ops testing philosophy:
- **NO unit tests** - All testing is E2E via KUTTL
- **NO integration tests** - KUTTL tests deploy real dependencies
- **NO property-based tests** - E2E tests validate properties through real cluster state
- **GitOps-First Testing** - All tests deploy via ArgoCD, never kubectl apply

### KUTTL Test Structure

```
tests/e2e/
├── 01-secret-zero-generation/
│   ├── 00-install.yaml          # Deploy HubEnvironment CR via ArgoCD
│   ├── 00-assert.yaml           # Assert secrets exist with correct structure
│   └── verify.sh                # Read-only bash script to verify secret values
├── 02-database-setup/
│   ├── 00-install.yaml          # Deploy CNPG Cluster via ArgoCD
│   ├── 01-assert.yaml           # Assert migrations complete
│   ├── 02-assert.yaml           # Assert roles exist
│   └── verify.sh                # Read-only psql queries to verify roles
├── 03-infisical-backup/
│   ├── 00-install.yaml          # Deploy Infisical via ArgoCD
│   ├── 00-assert.yaml           # Assert secrets uploaded
│   └── verify.sh                # Read-only Infisical API queries
├── 04-oauth-registration/
│   ├── 00-install.yaml          # Deploy Hydra via ArgoCD
│   ├── 00-assert.yaml           # Assert OAuth clients registered
│   └── verify.sh                # Read-only Hydra API queries
└── 05-nats-streams/
    ├── 00-install.yaml          # Deploy NATS via ArgoCD
    ├── 00-assert.yaml           # Assert streams created
    └── verify.sh                # Read-only NATS API queries
```

### Example KUTTL Test: Secret Zero Generation

**`tests/e2e/01-secret-zero-generation/00-install.yaml`:**
```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: hub-operator-test
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://github.com/soloz-io/zero-ops
    targetRevision: HEAD
    path: operators/hub-operator/manifests
  destination:
    server: https://kubernetes.default.svc
    namespace: hub-platform-ops
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
    - CreateNamespace=true
---
apiVersion: ops.nutgraf.in/v1alpha1
kind: HubEnvironment
metadata:
  name: test-hub
spec:
  domain: test.local
  database:
    clusterRef: platform-db
    namespace: hub-platform-data
  secrets:
    infisical:
      projectSlug: test
      environmentSlug: dev
```

**`tests/e2e/01-secret-zero-generation/00-assert.yaml`:**
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: infisical-secrets
  namespace: hub-platform-data
type: Opaque
---
apiVersion: v1
kind: Secret
metadata:
  name: platform-db-app
  namespace: hub-platform-data
type: kubernetes.io/basic-auth
---
apiVersion: v1
kind: Secret
metadata:
  name: infisical-db-credentials
  namespace: hub-platform-data
type: Opaque
---
apiVersion: v1
kind: Secret
metadata:
  name: infisical-postgres-connection
  namespace: hub-platform-data
type: Opaque
---
apiVersion: ops.nutgraf.in/v1alpha1
kind: HubEnvironment
metadata:
  name: test-hub
status:
  conditions:
  - type: SecretZeroGenerated
    status: "True"
```

**`tests/e2e/01-secret-zero-generation/verify.sh`:**
```bash
#!/bin/bash
set -e

echo "=== Verifying Secret Zero Generation ==="

# Verify infisical-secrets structure
echo "1. Checking infisical-secrets..."
kubectl get secret infisical-secrets -n hub-platform-data -o jsonpath='{.data.ENCRYPTION_KEY}' | base64 -d | wc -c | grep -q 32 && echo "  ✓ ENCRYPTION_KEY is 32 characters"
kubectl get secret infisical-secrets -n hub-platform-data -o jsonpath='{.data.AUTH_SECRET}' | base64 -d | wc -c | grep -q 32 && echo "  ✓ AUTH_SECRET is 32 characters"
kubectl get secret infisical-secrets -n hub-platform-data -o jsonpath='{.data.REDIS_URL}' | grep -q "redis://" && echo "  ✓ REDIS_URL is valid"
kubectl get secret infisical-secrets -n hub-platform-data -o jsonpath='{.data.DB_ROOT_CERT}' | base64 -d | grep -q "BEGIN CERTIFICATE" && echo "  ✓ DB_ROOT_CERT is valid"

# Verify platform-db-app structure
echo "2. Checking platform-db-app..."
kubectl get secret platform-db-app -n hub-platform-data -o jsonpath='{.type}' | grep -q "kubernetes.io/basic-auth" && echo "  ✓ Type is BasicAuth"
kubectl get secret platform-db-app -n hub-platform-data -o jsonpath='{.data.username}' | base64 -d | grep -q "app" && echo "  ✓ Username is 'app'"
kubectl get secret platform-db-app -n hub-platform-data -o jsonpath='{.data.password}' | base64 -d | wc -c | grep -q 32 && echo "  ✓ Password is 32 characters"

# Verify ownerReferences
echo "3. Checking ownerReferences..."
kubectl get secret infisical-secrets -n hub-platform-data -o jsonpath='{.metadata.ownerReferences[0].kind}' | grep -q "HubEnvironment" && echo "  ✓ infisical-secrets has HubEnvironment ownerReference"
kubectl get secret platform-db-app -n hub-platform-data -o jsonpath='{.metadata.ownerReferences[0].kind}' | grep -q "HubEnvironment" && echo "  ✓ platform-db-app has HubEnvironment ownerReference"

echo "=== Verification Complete ==="
```

### Example KUTTL Test: Database Setup

**`tests/e2e/02-database-setup/verify.sh`:**
```bash
#!/bin/bash
set -e

echo "=== Verifying Database Setup ==="

# Get database credentials
DB_PASSWORD=$(kubectl get secret platform-db-app -n hub-platform-data -o jsonpath='{.data.password}' | base64 -d)
DB_HOST="platform-db-pooler.hub-platform-data.svc"

# Verify migrations ran
echo "1. Checking migrations..."
kubectl exec -n hub-platform-data platform-db-1 -- psql -U app -d control_plane -c "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1;" | grep -q "2024" && echo "  ✓ Migrations executed"

# Verify roles exist
echo "2. Checking database roles..."
kubectl exec -n hub-platform-data platform-db-1 -- psql -U app -d postgres -c "SELECT rolname FROM pg_roles WHERE rolname='mcp_server';" | grep -q "mcp_server" && echo "  ✓ mcp_server role exists"
kubectl exec -n hub-platform-data platform-db-1 -- psql -U app -d postgres -c "SELECT rolname FROM pg_roles WHERE rolname='infisical';" | grep -q "infisical" && echo "  ✓ infisical role exists"
kubectl exec -n hub-platform-data platform-db-1 -- psql -U app -d postgres -c "SELECT rolname FROM pg_roles WHERE rolname='spoke_controller';" | grep -q "spoke_controller" && echo "  ✓ spoke_controller role exists"
kubectl exec -n hub-platform-data platform-db-1 -- psql -U app -d postgres -c "SELECT rolname FROM pg_roles WHERE rolname='spire_server';" | grep -q "spire_server" && echo "  ✓ spire_server role exists"

# Verify role permissions
echo "3. Checking role permissions..."
kubectl exec -n hub-platform-data platform-db-1 -- psql -U app -d control_plane -c "SELECT has_table_privilege('mcp_server', 'tenants', 'SELECT');" | grep -q "t" && echo "  ✓ mcp_server has SELECT on tenants"

echo "=== Verification Complete ==="
```


### GitOps-First Testing Workflow

**Developer Workflow:**
1. Developer modifies operator code or manifests
2. Developer commits and pushes to feature branch
3. KUTTL test updates ArgoCD Application to point to feature branch
4. ArgoCD syncs operator deployment
5. KUTTL asserts expected cluster state
6. Read-only bash scripts verify detailed state

**CRITICAL:** Tests NEVER use `kubectl apply` directly. All deployments go through ArgoCD to validate sync-wave ordering and GitOps compliance.

## Code Migration from CLI

### Files to Migrate

**From `internal/hub/components/secrets.go` → `operators/hub-operator/internal/secrets/generator.go`:**
- `InstallInfisicalSecrets()` → `GenerateInfisicalSecrets()`
- `InstallPostgresConnectionSecret()` → `GeneratePostgresConnectionSecret()`
- `InstallPlatformDatabaseCredentials()` → `GeneratePlatformDatabaseCredentials()`
- `InstallSPIREServerCredentials()` → `GenerateSPIREServerCredentials()`
- `generateSecurePassword()` → Keep as-is (utility function)

**From `internal/hub/infisical/client.go` → `operators/hub-operator/internal/client/infisical.go`:**
- `Client` struct → Keep as-is
- `NewClient()` → Adapt to use controller-runtime client instead of clientset
- `CreateOrUpdateSecret()` → Keep as-is
- `authenticate()` → Keep as-is

**From `internal/hub/components/installer.go` → DELETE (Day-2 logic removed):**
- `InstallInfisicalSecrets()` → Moved to operator
- `InstallPostgresConnectionSecret()` → Moved to operator
- `InstallPlatformDatabaseCredentials()` → Moved to operator
- `InstallSPIREServerCredentials()` → Moved to operator
- `RestartPlatformWorkloads()` → DELETE (operator owns secrets, no manual restart needed)

### Manifests to Reference

**Database Configuration:**
- `manifests/hub-core-services/platform-database/platform-db.yaml` - CNPG Cluster definition (watch for Ready status)
- `manifests/hub-core-services/platform-database/platform-db-pooler.yaml` - PgBouncer pooler config (connection target)
- `manifests/hub-core-services/platform-database/migrations/*.yaml` - SQL migration files (embed in operator binary)

**NATS Configuration:**
- `manifests/hub-core-services/nats/` - NATS StatefulSet (watch for Ready status)

**Hydra Configuration:**
- `manifests/hub-core-services/platform-identity/ory-hydra/` - Hydra Deployment (watch for Ready status)

**Infisical Configuration:**
- `manifests/hub-core-services/platform-infisical/` - Infisical Deployment (watch for Ready status)

### Manifests to DELETE

**Bash Jobs (Replaced by Operator):**
- `manifests/hub-core-services/platform-database/setup-platform-roles-job.yaml` → DELETE (operator creates roles via database/sql)
- `manifests/hub-core-services/platform-database/password-rotation-job.yaml` → DELETE (operator manages secrets)
- `manifests/hub-core-services/platform-database/setup-infisical-role-job.yaml` → DELETE (operator creates roles)
- `manifests/hub-core-services/nats/init-streams-job.yaml` → DELETE (operator creates streams via NATS SDK)

### Migration Checklist

- [ ] Scaffold operator using Kubebuilder: `kubebuilder init --domain nutgraf.in --repo github.com/soloz-io/zero-ops/operators/hub-operator`
- [ ] Create HubEnvironment CRD: `kubebuilder create api --group ops --version v1alpha1 --kind HubEnvironment`
- [ ] Migrate `generateSecurePassword()` from `internal/hub/components/secrets.go`
- [ ] Migrate `InstallInfisicalSecrets()` logic to `internal/secrets/generator.go`
- [ ] Migrate `InstallPostgresConnectionSecret()` logic to `internal/secrets/generator.go`
- [ ] Migrate `InstallPlatformDatabaseCredentials()` logic to `internal/secrets/generator.go`
- [ ] Migrate `InstallSPIREServerCredentials()` logic to `internal/secrets/generator.go`
- [ ] Migrate Infisical client from `internal/hub/infisical/client.go`
- [ ] Implement Hydra client wrapper using Ory Hydra Go SDK
- [ ] Implement NATS client wrapper using NATS Go SDK
- [ ] Implement database migrator using golang-migrate/migrate
- [ ] Embed SQL migration files from `manifests/hub-core-services/platform-database/migrations/`
- [ ] Implement controller reconciliation logic
- [ ] Implement watch configuration for CNPG, Hydra, Infisical, NATS
- [ ] Implement memory optimization (cache transformers)
- [ ] Create RBAC manifests
- [ ] Create deployment manifests with sync-wave annotations
- [ ] Write KUTTL E2E tests
- [ ] Write read-only bash verification scripts
- [ ] Update CLI to remove Day-2 logic from `internal/hub/components/installer.go`
- [ ] Delete bash job manifests
- [ ] Update ArgoCD app-of-apps to include hub-operator

## RBAC Requirements

### ClusterRole Permissions

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: hub-operator
rules:
# HubEnvironment CRD
- apiGroups: ["ops.nutgraf.in"]
  resources: ["hubenvironments"]
  verbs: ["get", "list", "watch", "update", "patch"]
- apiGroups: ["ops.nutgraf.in"]
  resources: ["hubenvironments/status"]
  verbs: ["get", "update", "patch"]

# Secrets (create and own)
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]

# ConfigMaps (read Infisical config)
- apiGroups: [""]
  resources: ["configmaps"]
  verbs: ["get", "list", "watch"]

# CNPG Cluster (watch status)
- apiGroups: ["postgresql.cnpg.io"]
  resources: ["clusters"]
  verbs: ["get", "list", "watch"]

# Deployments (watch Hydra, Infisical status)
- apiGroups: ["apps"]
  resources: ["deployments"]
  verbs: ["get", "list", "watch"]

# StatefulSets (watch NATS status)
- apiGroups: ["apps"]
  resources: ["statefulsets"]
  verbs: ["get", "list", "watch"]

# Leader election
- apiGroups: ["coordination.k8s.io"]
  resources: ["leases"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]

# Events
- apiGroups: [""]
  resources: ["events"]
  verbs: ["create", "patch"]
```

## Deployment Architecture

### Sync Wave Configuration

```
Wave 0: CRDs
  - HubEnvironment CRD
  - CNPG CRDs
  - Cert-Manager CRDs

Wave 1: Operators and Bootstrap
  - hub-operator Deployment
  - HubEnvironment CR (triggers Secret Zero generation)

Wave 2: Infrastructure Services
  - CNPG Cluster (uses platform-db-app secret)
  - NATS StatefulSet
  - Redis StatefulSet

Wave 3: Platform Services
  - Infisical Deployment (uses infisical-db-credentials)
  - SPIRE Server
  - Hydra Deployment

Wave 4+: Application Services
  - AgentGateway
  - MCP Server
  - Console
```

### High Availability Configuration

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: hub-operator
  namespace: hub-platform-ops
  annotations:
    argocd.argoproj.io/sync-wave: "1"
spec:
  replicas: 2  # HA mode
  selector:
    matchLabels:
      app: hub-operator
  template:
    metadata:
      labels:
        app: hub-operator
    spec:
      serviceAccountName: hub-operator
      containers:
      - name: manager
        image: ghcr.io/soloz-io/zero-ops/hub-operator:latest
        command:
        - /manager
        args:
        - --leader-elect=true
        - --leader-election-id=hub-operator
        - --leader-election-namespace=hub-platform-ops
        env:
        - name: ENABLE_WEBHOOKS
          value: "false"
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8081
          initialDelaySeconds: 15
          periodSeconds: 20
        readinessProbe:
          httpGet:
            path: /readyz
            port: 8081
          initialDelaySeconds: 5
          periodSeconds: 10
```


## Low-Level Design

### Package Structure and Dependencies

**Go Packages to Import:**

```go
// Kubernetes core
"k8s.io/api/core/v1"
"k8s.io/api/apps/v1"
"k8s.io/apimachinery/pkg/api/errors"
"k8s.io/apimachinery/pkg/api/meta"
"k8s.io/apimachinery/pkg/runtime"
"k8s.io/apimachinery/pkg/types"
"sigs.k8s.io/controller-runtime/pkg/client"
"sigs.k8s.io/controller-runtime/pkg/controller"
"sigs.k8s.io/controller-runtime/pkg/handler"
"sigs.k8s.io/controller-runtime/pkg/predicate"
"sigs.k8s.io/controller-runtime/pkg/reconcile"

// Database
"database/sql"
_ "github.com/lib/pq"
"github.com/golang-migrate/migrate/v4"
"github.com/golang-migrate/migrate/v4/database/postgres"
"github.com/golang-migrate/migrate/v4/source/iofs"

// External APIs
"github.com/ory/hydra-client-go/v2"
"github.com/nats-io/nats.go"

// CNPG
"github.com/cloudnative-pg/cloudnative-pg/api/v1" // For watching Cluster CRD

// Crypto
"crypto/rand"
"encoding/hex"
"encoding/base64"
```

### Secret Zero Generation (`internal/secrets/generator.go`)

**Migrated from `internal/hub/components/secrets.go`:**

```go
package secrets

import (
    "context"
    "crypto/rand"
    "encoding/base64"
    "encoding/hex"
    "fmt"
    
    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type Generator struct {
    client.Client
}

// GenerateSecretZero creates all Secret Zero resources for HubEnvironment
// Migrated from: InstallInfisicalSecrets, InstallPostgresConnectionSecret
// GAP #3: Added Ory stack credentials (hydra, kratos, keto)
func (g *Generator) GenerateSecretZero(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) error {
    namespace := hubEnv.Spec.Database.Namespace
    
    // 1. Generate infisical-secrets
    if err := g.generateInfisicalSecrets(ctx, hubEnv, namespace); err != nil {
        return fmt.Errorf("failed to generate infisical-secrets: %w", err)
    }
    
    // 2. Generate platform-db-app
    if err := g.generatePlatformDBApp(ctx, hubEnv, namespace); err != nil {
        return fmt.Errorf("failed to generate platform-db-app: %w", err)
    }
    
    // 3. Generate infisical-db-credentials
    if err := g.generateInfisicalDBCredentials(ctx, hubEnv, namespace); err != nil {
        return fmt.Errorf("failed to generate infisical-db-credentials: %w", err)
    }
    
    // 4. Generate infisical-postgres-connection
    if err := g.generateInfisicalPostgresConnection(ctx, hubEnv, namespace); err != nil {
        return fmt.Errorf("failed to generate infisical-postgres-connection: %w", err)
    }
    
    // 5. Generate hydra-db-credentials (GAP #3)
    if err := g.generateOryDBCredentials(ctx, hubEnv, namespace, "hydra"); err != nil {
        return fmt.Errorf("failed to generate hydra-db-credentials: %w", err)
    }
    
    // 6. Generate kratos-db-credentials (GAP #3)
    if err := g.generateOryDBCredentials(ctx, hubEnv, namespace, "kratos"); err != nil {
        return fmt.Errorf("failed to generate kratos-db-credentials: %w", err)
    }
    
    // 7. Generate keto-db-credentials (GAP #3)
    if err := g.generateOryDBCredentials(ctx, hubEnv, namespace, "keto"); err != nil {
        return fmt.Errorf("failed to generate keto-db-credentials: %w", err)
    }
    
    return nil
}

// generateOryDBCredentials creates database credentials for Ory stack services
// GAP #3: Ory Identity Stack Credentials
func (g *Generator) generateOryDBCredentials(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment, namespace, service string) error {
    secretName := fmt.Sprintf("%s-db-credentials", service)
    
    // Check if secret exists
    existing := &corev1.Secret{}
    err := g.Get(ctx, client.ObjectKey{Name: secretName, Namespace: namespace}, existing)
    
    var password string
    if err == nil {
        // Reuse existing password to maintain data integrity
        password = string(existing.Data["password"])
    } else {
        // Generate new password (exactly 32 characters)
        password, err = generateSecurePassword(32)
        if err != nil {
            return err
        }
    }
    
    // Create or update secret
    secret := &corev1.Secret{
        ObjectMeta: metav1.ObjectMeta{
            Name:      secretName,
            Namespace: namespace,
            Labels: map[string]string{
                "ops.nutgraf.in/db-credentials": "true", // GAP #2: Label for ESO watch
                "app.kubernetes.io/component":    service,
            },
        },
        Type: corev1.SecretTypeOpaque,
        Data: map[string][]byte{
            "username": []byte(service),
            "password": []byte(password),
        },
    }
    
    // Set owner reference for garbage collection
    if err := controllerutil.SetControllerReference(hubEnv, secret, g.Scheme); err != nil {
        return err
    }
    
    // CreateOrUpdate pattern (idempotent)
    if err := g.Patch(ctx, secret, client.Apply, client.ForceOwnership, client.FieldOwner("hub-operator")); err != nil {
        return err
    }
    
    return nil
}

// generateInfisicalSecrets creates the master infisical-secrets secret
// IDEMPOTENT: Reuses existing ENCRYPTION_KEY and AUTH_SECRET if secret exists
func (g *Generator) generateInfisicalSecrets(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment, namespace string) error {
    secretName := "infisical-secrets"
    
    // Check if secret exists
    existing := &corev1.Secret{}
    err := g.Get(ctx, client.ObjectKey{Name: secretName, Namespace: namespace}, existing)
    
    var encryptionKey, authSecret, redisPassword string
    
    if err == nil {
        // Reuse existing keys to maintain data integrity
        encryptionKey = string(existing.Data["ENCRYPTION_KEY"])
        authSecret = string(existing.Data["AUTH_SECRET"])
        
        // Extract Redis password from URL
        redisURL := string(existing.Data["REDIS_URL"])
        if idx := strings.Index(redisURL, "redis://:"); idx >= 0 {
            start := idx + len("redis://:")
            if end := strings.Index(redisURL[start:], "@"); end >= 0 {
                redisPassword = redisURL[start : start+end]
            }
        }
    } else {
        // Generate new keys (exactly 32 characters for AES-256)
        encryptionKey, err = generateSecurePassword(32)
        if err != nil {
            return err
        }
        authSecret, err = generateSecurePassword(32)
        if err != nil {
            return err
        }
        redisPassword, err = generateSecurePassword(32)
        if err != nil {
            return err
        }
    }
    
    // Read CA certificate from CNPG-managed secret
    caSecret := &corev1.Secret{}
    if err := g.Get(ctx, client.ObjectKey{Name: "platform-db-ca", Namespace: namespace}, caSecret); err != nil {
        return fmt.Errorf("failed to read platform-db-ca: %w", err)
    }
    
    caCert, ok := caSecret.Data["ca.crt"]
    if !ok {
        return fmt.Errorf("ca.crt not found in platform-db-ca secret")
    }
    
    dbRootCert := base64.StdEncoding.EncodeToString(caCert)
    redisURL := fmt.Sprintf("redis://:%s@redis-master.hub-platform-data.svc:6379", redisPassword)
    
    // Create or update secret
    secret := &corev1.Secret{
        ObjectMeta: metav1.ObjectMeta{
            Name:      secretName,
            Namespace: namespace,
        },
        Type: corev1.SecretTypeOpaque,
        Data: map[string][]byte{
            "ENCRYPTION_KEY": []byte(encryptionKey),
            "AUTH_SECRET":    []byte(authSecret),
            "REDIS_URL":      []byte(redisURL),
            "DB_ROOT_CERT":   []byte(dbRootCert),
        },
    }
    
    // Set owner reference for garbage collection
    if err := controllerutil.SetControllerReference(hubEnv, secret, g.Scheme); err != nil {
        return err
    }
    
    // CreateOrUpdate pattern (idempotent)
    if err := g.Patch(ctx, secret, client.Apply, client.ForceOwnership, client.FieldOwner("hub-operator")); err != nil {
        return err
    }
    
    return nil
}

// generateSecurePassword generates cryptographically secure random password
// Migrated from internal/hub/components/secrets.go (unchanged)
func generateSecurePassword(length int) (string, error) {
    bytes := make([]byte, length)
    if _, err := rand.Read(bytes); err != nil {
        return "", err
    }
    return hex.EncodeToString(bytes)[:length], nil
}
```


### Database Migration Execution (`internal/database/migrator.go`)

```go
package database

import (
    "context"
    "database/sql"
    "embed"
    "fmt"
    
    "github.com/golang-migrate/migrate/v4"
    "github.com/golang-migrate/migrate/v4/database/postgres"
    "github.com/golang-migrate/migrate/v4/source/iofs"
    _ "github.com/lib/pq"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Migrator struct {
    connectionString string
}

// NewMigrator creates a migrator that connects directly to CNPG primary (platform-db-rw)
// NOT the PgBouncer pooler to avoid DDL statement issues
func NewMigrator(host, port, user, password, dbname string) *Migrator {
    // Connect to primary service, not pooler
    connStr := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=require",
        user, password, host, port, dbname)
    return &Migrator{connectionString: connStr}
}

func (m *Migrator) RunMigrations(ctx context.Context) error {
    // Connect to database
    db, err := sql.Open("postgres", m.connectionString)
    if err != nil {
        return fmt.Errorf("failed to open database: %w", err)
    }
    defer db.Close()
    
    // Create migrate driver
    driver, err := postgres.WithInstance(db, &postgres.Config{})
    if err != nil {
        return fmt.Errorf("failed to create migrate driver: %w", err)
    }
    
    // Create source from embedded filesystem
    source, err := iofs.New(migrationsFS, "migrations")
    if err != nil {
        return fmt.Errorf("failed to create migration source: %w", err)
    }
    
    // Create migrate instance
    migrator, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
    if err != nil {
        return fmt.Errorf("failed to create migrate instance: %w", err)
    }
    
    // Run migrations
    if err := migrator.Up(); err != nil {
        if err == migrate.ErrNoChange {
            return nil // No new migrations, success
        }
        
        // Check if database is in dirty state
        version, dirty, vErr := migrator.Version()
        if vErr == nil && dirty {
            // Dirty database state - PERMANENT ERROR
            // Requires manual CLI intervention: migrate force <version>
            return &DirtyDatabaseError{
                Version: version,
                Err:     err,
            }
        }
        
        // Transient error (connection timeout, etc.) - can retry
        return fmt.Errorf("failed to run migrations: %w", err)
    }
    
    return nil
}

// DirtyDatabaseError indicates a permanent error requiring manual intervention
type DirtyDatabaseError struct {
    Version uint
    Err     error
}

func (e *DirtyDatabaseError) Error() string {
    return fmt.Sprintf("database in dirty state at version %d: %v (requires manual 'migrate force' intervention)", e.Version, e.Err)
}

func (e *DirtyDatabaseError) IsPermanent() bool {
    return true
}
```

### Database Role Provisioning (`internal/database/roles.go`)

```go
package database

import (
    "context"
    "crypto/sha256"
    "database/sql"
    "fmt"
    
    _ "github.com/lib/pq"
)

type RoleManager struct {
    db *sql.DB
}

func NewRoleManager(connectionString string) (*RoleManager, error) {
    db, err := sql.Open("postgres", connectionString)
    if err != nil {
        return nil, fmt.Errorf("failed to open database: %w", err)
    }
    return &RoleManager{db: db}, nil
}

func (rm *RoleManager) CreateOrUpdateRole(ctx context.Context, roleName, password string, database string, permissions []string) error {
    // Check if role exists
    var exists bool
    query := "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1)"
    if err := rm.db.QueryRowContext(ctx, query).Scan(&exists); err != nil {
        return fmt.Errorf("failed to check role existence: %w", err)
    }
    
    if !exists {
        // Create role with password
        query := fmt.Sprintf("CREATE ROLE %s WITH LOGIN PASSWORD '%s'", roleName, password)
        if _, err := rm.db.ExecContext(ctx, query); err != nil {
            return fmt.Errorf("failed to create role %s: %w", roleName, err)
        }
    } else {
        // Role exists - check if password needs update
        needsUpdate, err := rm.passwordNeedsUpdate(ctx, roleName, password)
        if err != nil {
            return fmt.Errorf("failed to check password drift: %w", err)
        }
        
        if needsUpdate {
            // Update password
            query := fmt.Sprintf("ALTER ROLE %s WITH PASSWORD '%s'", roleName, password)
            if _, err := rm.db.ExecContext(ctx, query); err != nil {
                return fmt.Errorf("failed to update password for role %s: %w", roleName, err)
            }
        }
    }
    
    // Grant permissions
    for _, perm := range permissions {
        query := fmt.Sprintf("GRANT %s ON ALL TABLES IN SCHEMA public TO %s", perm, roleName)
        if _, err := rm.db.ExecContext(ctx, query); err != nil {
            return fmt.Errorf("failed to grant %s to %s: %w", perm, roleName, err)
        }
    }
    
    return nil
}

// passwordNeedsUpdate compares the secret hash with pg_authid to detect password drift
func (rm *RoleManager) passwordNeedsUpdate(ctx context.Context, roleName, newPassword string) (bool, error) {
    // Get current password hash from pg_authid
    var currentHash string
    query := "SELECT rolpassword FROM pg_authid WHERE rolname = $1"
    if err := rm.db.QueryRowContext(ctx, query, roleName).Scan(&currentHash); err != nil {
        return false, fmt.Errorf("failed to get current password hash: %w", err)
    }
    
    // PostgreSQL uses SCRAM-SHA-256 or MD5 hashing
    // We can't directly compare, so we attempt a connection with the new password
    // If it fails, password needs update
    testConnStr := fmt.Sprintf("postgres://%s:%s@localhost:5432/postgres?sslmode=require", roleName, newPassword)
    testDB, err := sql.Open("postgres", testConnStr)
    if err != nil {
        return true, nil // Connection failed, password needs update
    }
    defer testDB.Close()
    
    if err := testDB.PingContext(ctx); err != nil {
        return true, nil // Ping failed, password needs update
    }
    
    return false, nil // Password is current
}

func (rm *RoleManager) Close() error {
    return rm.db.Close()
}
```

### Infisical Client (`internal/client/infisical.go`)

**Migrated from `internal/hub/infisical/client.go` with controller-runtime client:**

```go
package client

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"
    
    corev1 "k8s.io/api/core/v1"
    "sigs.k8s.io/controller-runtime/pkg/client"
)

type InfisicalClient struct {
    baseURL    string
    httpClient *http.Client
    token      string
    k8sClient  client.Client
}

// NewInfisicalClient creates an authenticated Infisical API client
// Authentication: Universal Auth (client credentials grant)
// Reads infisical-auth secret from hub-platform-ops namespace
func NewInfisicalClient(ctx context.Context, k8sClient client.Client, baseURL string) (*InfisicalClient, error) {
    ic := &InfisicalClient{
        baseURL:   baseURL,
        k8sClient: k8sClient,
        httpClient: &http.Client{
            Timeout: 30 * time.Second,
        },
    }
    
    // Get Universal Auth credentials from infisical-auth secret
    secret := &corev1.Secret{}
    if err := k8sClient.Get(ctx, client.ObjectKey{
        Name:      "infisical-auth",
        Namespace: "hub-platform-ops",
    }, secret); err != nil {
        return nil, fmt.Errorf("failed to get infisical-auth secret: %w", err)
    }
    
    clientID := string(secret.Data["client-id"])
    clientSecret := string(secret.Data["client-secret"])
    
    if err := ic.authenticate(ctx, clientID, clientSecret); err != nil {
        return nil, err
    }
    
    return ic, nil
}

// authenticate obtains a bearer token using Universal Auth
func (ic *InfisicalClient) authenticate(ctx context.Context, clientID, clientSecret string) error {
    authURL := fmt.Sprintf("%s/api/v1/auth/universal-auth/login", ic.baseURL)
    
    payload := map[string]string{
        "clientId":     clientID,
        "clientSecret": clientSecret,
    }
    
    body, _ := json.Marshal(payload)
    req, err := http.NewRequestWithContext(ctx, "POST", authURL, bytes.NewReader(body))
    if err != nil {
        return err
    }
    req.Header.Set("Content-Type", "application/json")
    
    resp, err := ic.httpClient.Do(req)
    if err != nil {
        return fmt.Errorf("authentication request failed: %w", err)
    }
    defer resp.Body.Close()
    
    if resp.StatusCode != 200 {
        return fmt.Errorf("authentication failed with status %d", resp.StatusCode)
    }
    
    var authResp struct {
        AccessToken string `json:"accessToken"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
        return err
    }
    
    ic.token = authResp.AccessToken
    return nil
}

// CreateOrUpdateSecret creates or updates a secret in Infisical
// ONE-TIME OPERATION: Only called when SecretsBackedUp condition is False
// After bootstrap, ESO owns the lifecycle (no upstream overwrites)
// Migrated from internal/hub/infisical/client.go (unchanged logic)
func (ic *InfisicalClient) CreateOrUpdateSecret(ctx context.Context, projectSlug, environmentSlug, secretPath, key, value string) error {
    // ... (same implementation as internal/hub/infisical/client.go)
}
```

### Hydra Client (`internal/client/hydra.go`)

```go
package client

import (
    "context"
    "fmt"
    
    hydra "github.com/ory/hydra-client-go/v2"
)

type HydraClient struct {
    client *hydra.APIClient
}

// NewHydraClient creates a Hydra Admin API client
// Authentication: None required (internal cluster service)
// Connects to https://hydra-admin.ory-system.svc
func NewHydraClient(baseURL string) *HydraClient {
    config := hydra.NewConfiguration()
    config.Servers = hydra.ServerConfigurations{
        {URL: baseURL},
    }
    return &HydraClient{
        client: hydra.NewAPIClient(config),
    }
}

func (hc *HydraClient) RegisterOAuthClient(ctx context.Context, clientID, clientName string, redirectURIs, grantTypes, responseTypes []string) error {
    // Check if client exists
    _, resp, err := hc.client.OAuth2API.GetOAuth2Client(ctx, clientID).Execute()
    if err == nil {
        // Client exists, update it
        updateReq := hydra.NewOAuth2Client()
        updateReq.SetClientId(clientID)
        updateReq.SetClientName(clientName)
        updateReq.SetRedirectUris(redirectURIs)
        updateReq.SetGrantTypes(grantTypes)
        updateReq.SetResponseTypes(responseTypes)
        
        _, _, err := hc.client.OAuth2API.SetOAuth2Client(ctx, clientID).OAuth2Client(*updateReq).Execute()
        return err
    }
    
    // Client doesn't exist, create it
    if resp != nil && resp.StatusCode == 404 {
        createReq := hydra.NewOAuth2Client()
        createReq.SetClientId(clientID)
        createReq.SetClientName(clientName)
        createReq.SetRedirectUris(redirectURIs)
        createReq.SetGrantTypes(grantTypes)
        createReq.SetResponseTypes(responseTypes)
        
        _, _, err := hc.client.OAuth2API.CreateOAuth2Client(ctx).OAuth2Client(*createReq).Execute()
        return err
    }
    
    return fmt.Errorf("failed to check OAuth client: %w", err)
}
```

### NATS Client (`internal/client/nats.go`)

```go
package client

import (
    "context"
    "fmt"
    
    "github.com/nats-io/nats.go"
)

type NATSClient struct {
    conn *nats.Conn
    js   nats.JetStreamContext
}

// NewNATSClient creates a NATS client
// Authentication: None required (internal cluster service)
// Connects to nats://nats.hub-platform-core.svc:4222
func NewNATSClient(url string) (*NATSClient, error) {
    conn, err := nats.Connect(url, nats.Timeout(10*time.Second))
    if err != nil {
        return nil, fmt.Errorf("failed to connect to NATS: %w", err)
    }
    
    js, err := conn.JetStream()
    if err != nil {
        return nil, fmt.Errorf("failed to get JetStream context: %w", err)
    }
    
    return &NATSClient{
        conn: conn,
        js:   js,
    }, nil
}

func (nc *NATSClient) CreateStream(ctx context.Context, name string, subjects []string, retention nats.RetentionPolicy, storage nats.StorageType) error {
    // Check if stream exists
    streamInfo, err := nc.js.StreamInfo(name)
    if err == nil {
        // Stream exists - check for configuration drift (GAP #4)
        needsUpdate := false
        
        // Compare subjects
        if !equalStringSlices(streamInfo.Config.Subjects, subjects) {
            needsUpdate = true
        }
        
        // Compare retention policy
        if streamInfo.Config.Retention != retention {
            needsUpdate = true
        }
        
        // Compare storage type
        if streamInfo.Config.Storage != storage {
            needsUpdate = true
        }
        
        if needsUpdate {
            // Configuration drift detected, update stream
            _, err := nc.js.UpdateStream(&nats.StreamConfig{
                Name:      name,
                Subjects:  subjects,
                Retention: retention,
                Storage:   storage,
            })
            return err
        }
        
        // No drift, stream is up to date
        return nil
    }
    
    // Stream doesn't exist, create it
    if err == nats.ErrStreamNotFound {
        _, err := nc.js.AddStream(&nats.StreamConfig{
            Name:      name,
            Subjects:  subjects,
            Retention: retention,
            Storage:   storage,
        })
        return err
    }
    
    return fmt.Errorf("failed to check stream: %w", err)
}

// equalStringSlices compares two string slices for equality
func equalStringSlices(a, b []string) bool {
    if len(a) != len(b) {
        return false
    }
    for i := range a {
        if a[i] != b[i] {
            return false
        }
    }
    return true
}

func (nc *NATSClient) Close() {
    nc.conn.Close()
}
```


### Embedded Migrations (`internal/embed/migrations.go`)

**SQL files to embed from `manifests/hub-core-services/platform-database/migrations/`:**

```go
package embed

import (
    "embed"
)

//go:embed migrations/*.sql
var MigrationsFS embed.FS
```

**Migration files structure:**
```
internal/embed/migrations/
├── 001_control_plane_schema.sql      # From control-plane-migrations.yaml
├── 002_hub_schema.sql                # From hub-migrations.yaml
├── 003_agentregistry_rls.sql         # From agentregistry-rls.yaml
└── 004_spire_server_schema.sql       # From spire-server-role.yaml
```

### Dependency Readiness Checks

```go
// isCNPGReady checks if CNPG Cluster is ready
func (r *HubEnvironmentReconciler) isCNPGReady(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) (bool, error) {
    cluster := &cnpgv1.Cluster{}
    if err := r.Get(ctx, client.ObjectKey{
        Name:      hubEnv.Spec.Database.ClusterRef,
        Namespace: hubEnv.Spec.Database.Namespace,
    }, cluster); err != nil {
        return false, client.IgnoreNotFound(err)
    }
    
    // Check if cluster phase is "Cluster in healthy state"
    return cluster.Status.Phase == "Cluster in healthy state", nil
}

// isInfisicalReady checks if Infisical Deployment is ready
func (r *HubEnvironmentReconciler) isInfisicalReady(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) (bool, error) {
    deployment := &appsv1.Deployment{}
    if err := r.Get(ctx, client.ObjectKey{
        Name:      "platform-infisical-standalone",
        Namespace: "hub-platform-data",
    }, deployment); err != nil {
        return false, client.IgnoreNotFound(err)
    }
    
    return deployment.Status.ReadyReplicas > 0, nil
}

// isHydraReady checks if Hydra Deployment is ready
func (r *HubEnvironmentReconciler) isHydraReady(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) (bool, error) {
    deployment := &appsv1.Deployment{}
    if err := r.Get(ctx, client.ObjectKey{
        Name:      "ory-hydra",
        Namespace: "ory-system",
    }, deployment); err != nil {
        return false, client.IgnoreNotFound(err)
    }
    
    return deployment.Status.ReadyReplicas > 0, nil
}

// isNATSReady checks if NATS StatefulSet is ready
func (r *HubEnvironmentReconciler) isNATSReady(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) (bool, error) {
    statefulSet := &appsv1.StatefulSet{}
    if err := r.Get(ctx, client.ObjectKey{
        Name:      "nats",
        Namespace: "hub-platform-core",
    }, statefulSet); err != nil {
        return false, client.IgnoreNotFound(err)
    }
    
    return statefulSet.Status.ReadyReplicas > 0, nil
}
```

## Deployment Manifests

### Operator Deployment (`manifests/deployment.yaml`)

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: hub-platform-ops
  annotations:
    argocd.argoproj.io/sync-wave: "0"
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: hub-operator
  namespace: hub-platform-ops
  annotations:
    argocd.argoproj.io/sync-wave: "1"
  labels:
    app: hub-operator
    component: platform-operator
spec:
  replicas: 2
  selector:
    matchLabels:
      app: hub-operator
  template:
    metadata:
      labels:
        app: hub-operator
    spec:
      serviceAccountName: hub-operator
      containers:
      - name: manager
        image: ghcr.io/soloz-io/zero-ops/hub-operator:latest
        imagePullPolicy: Always
        command:
        - /manager
        args:
        - --leader-elect=true
        - --leader-election-id=hub-operator
        - --leader-election-namespace=hub-platform-ops
        - --health-probe-bind-address=:8081
        - --metrics-bind-address=:8080
        env:
        - name: ENABLE_WEBHOOKS
          value: "false"
        - name: INFISICAL_BASE_URL
          value: "https://infisical.nutgraf.in"
        - name: HYDRA_BASE_URL
          value: "https://hydra.nutgraf.in"
        - name: NATS_URL
          value: "nats://nats.hub-platform-core.svc:4222"
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8081
          initialDelaySeconds: 15
          periodSeconds: 20
        readinessProbe:
          httpGet:
            path: /readyz
            port: 8081
          initialDelaySeconds: 5
          periodSeconds: 10
        securityContext:
          allowPrivilegeEscalation: false
          capabilities:
            drop:
            - ALL
          runAsNonRoot: true
          runAsUser: 65532
          seccompProfile:
            type: RuntimeDefault
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: hub-operator
  namespace: hub-platform-ops
  annotations:
    argocd.argoproj.io/sync-wave: "1"
  labels:
    app: hub-operator
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: hub-operator
  annotations:
    argocd.argoproj.io/sync-wave: "1"
rules:
# HubEnvironment CRD
- apiGroups: ["ops.nutgraf.in"]
  resources: ["hubenvironments"]
  verbs: ["get", "list", "watch", "update", "patch"]
- apiGroups: ["ops.nutgraf.in"]
  resources: ["hubenvironments/status"]
  verbs: ["get", "update", "patch"]
# Secrets
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
# ConfigMaps
- apiGroups: [""]
  resources: ["configmaps"]
  verbs: ["get", "list", "watch"]
# CNPG Cluster
- apiGroups: ["postgresql.cnpg.io"]
  resources: ["clusters"]
  verbs: ["get", "list", "watch"]
# Deployments
- apiGroups: ["apps"]
  resources: ["deployments"]
  verbs: ["get", "list", "watch"]
# StatefulSets
- apiGroups: ["apps"]
  resources: ["statefulsets"]
  verbs: ["get", "list", "watch"]
# Leader election
- apiGroups: ["coordination.k8s.io"]
  resources: ["leases"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
# Events
- apiGroups: [""]
  resources: ["events"]
  verbs: ["create", "patch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: hub-operator
  annotations:
    argocd.argoproj.io/sync-wave: "1"
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: hub-operator
subjects:
- kind: ServiceAccount
  name: hub-operator
  namespace: hub-platform-ops
```

### Example HubEnvironment CR Deployment (`manifests/hub-core-services/hub-environment.yaml`)

```yaml
apiVersion: ops.nutgraf.in/v1alpha1
kind: HubEnvironment
metadata:
  name: hub-production
  annotations:
    argocd.argoproj.io/sync-wave: "1"
spec:
  domain: nutgraf.in
  tls:
    issuer: letsencrypt-prod
    email: ops@nutgraf.in
  observability:
    victoriaMetrics:
      retentionPeriod: 90d
    grafanaAlloy:
      scrapeInterval: 15s
  database:
    clusterRef: platform-db
    namespace: hub-platform-data
    roles:
    - name: mcp_server
      database: control_plane
      permissions: ["SELECT", "INSERT", "UPDATE", "DELETE"]
    - name: infisical
      database: infisical
      permissions: ["SELECT", "INSERT", "UPDATE", "DELETE"]
    - name: spoke_controller
      database: hub
      permissions: ["SELECT", "INSERT", "UPDATE"]
    - name: spire_server
      database: spire
      permissions: ["SELECT", "INSERT", "UPDATE", "DELETE"]
  nats:
    streams:
    - name: spoke-events
      subjects: ["spoke.*.lifecycle.>", "spoke.*.billing.>"]
      retention: limits
      storage: file
    - name: hub-events
      subjects: ["hub.notifications.>"]
      retention: interest
      storage: file
  oauth:
    clients:
    - clientId: mcp-server
      clientName: "MCP Server"
      redirectUris: ["https://mcp.nutgraf.in/callback"]
      grantTypes: ["authorization_code", "refresh_token"]
      responseTypes: ["code"]
    - clientId: spoke-controller
      clientName: "Spoke Controller"
      redirectUris: []
      grantTypes: ["client_credentials"]
      responseTypes: ["token"]
  secrets:
    infisical:
      projectSlug: platform
      environmentSlug: prod
```

## Summary

The hub-operator design follows Zero-Ops architectural principles:

1. **GitOps-First**: All deployments via ArgoCD with sync-wave ordering
2. **Code Reuse**: Migrates existing CLI logic from `internal/hub/components/` to operator packages
3. **Idempotent Reconciliation**: Safe to retry infinitely, preserves existing secrets
4. **Dependency-Aware**: Watches CNPG, Hydra, Infisical, NATS and reconciles when ready
5. **Status-Driven**: Comprehensive conditions track reconciliation progress
6. **E2E Testing Only**: KUTTL tests with read-only bash verification scripts
7. **Memory Optimized**: Cache transformers strip secret data payloads
8. **High Availability**: Leader election with 2 replicas

The operator eliminates bash jobs and CLI Day-2 logic, replacing them with declarative Kubernetes-native reconciliation that integrates seamlessly with the existing ArgoCD-based GitOps workflow.

### GAP Resolutions (Development-Ready Enhancements)

This design addresses all critical edge cases identified in the final engineering review:

**GAP #1: Dirty Database Recovery Deadlock**
- Added `ops.nutgraf.in/reconcile-trigger` annotation watch
- Manual recovery procedure documented in Error Handling section
- Operator resumes reconciliation after human fixes dirty database state

**GAP #2: Unwatched ESO Secrets Blindspot**
- Added watch for secrets with label `ops.nutgraf.in/db-credentials=true`
- Operator detects ESO-driven password rotations
- Triggers ALTER ROLE reconciliation for password drift

**GAP #3: Forgotten Identity Stack Credentials**
- Added hydra-db-credentials, kratos-db-credentials, keto-db-credentials to Secret Zero
- Added hydra, kratos, keto roles to database provisioning
- Added Ory credentials to Infisical upload phase
- Example HubEnvironment CR includes all Ory roles

**GAP #4: NATS Stream Drift Detection**
- Added configuration drift detection in CreateStream
- Compares subjects, retention, storage against CR spec
- Executes UpdateStream when drift detected

**GAP #5: Incomplete CA Certificate Rotation Scope**
- Extended certificate rotation to ALL database-connected services
- Restarts Hydra, Kratos, Keto, SPIRE Server, MCP Server on CA rotation
- Prevents x509 certificate errors across the platform

**GAP #6: Missing Universal Auth Secret Dependency**
- Phase 3 (Infisical upload) suspended if infisical-auth missing
- Phases 1 & 2 proceed to allow Infisical bootstrap
- Operator resumes Phase 3 when infisical-auth becomes available

**GAP #7: "Secrets Only" Rule**
- Infisical stores ONLY passwords, keys, tokens
- NO URLs, hostnames, ports, database names, TLS certificates
- ESO templates combine Infisical secrets with Kubernetes service discovery

**GAP #8: Self-Signed CA Generation**
- Operator generates self-signed CA in Wave 1 using crypto/x509
- CNPG imports pre-generated CA in Wave 2
- Enables Infisical TLS bootstrap before CNPG starts

**GAP #9: Day-2 Additive Sync**
- UploadedSecrets array tracks individual secret uploads
- New secrets added to Infisical without overwriting existing
- Supports incremental role additions to HubEnvironment CR

**GAP #10: Resource Pruning**
- Database roles: Delete orphaned roles not in CR spec
- OAuth clients: Delete orphaned clients not in CR spec
- NATS streams: Delete orphaned streams not in CR spec

**GAP #11: Password Rotation Restart Scope**
- Password changes trigger ALTER ROLE in PostgreSQL
- Consuming Deployments/StatefulSets restarted via annotation
- Ensures services pick up new credentials

**GAP #12: NATS JetStream Transient Errors**
- Distinguish transient errors (connection timeout) from permanent errors
- Requeue with exponential backoff for transient failures
- Update status condition for permanent configuration errors

## Additional Implementation Details

### Self-Signed CA Generation (GAP #8)

The operator generates a self-signed CA certificate in Wave 1 before CNPG starts. This allows Infisical to boot with TLS enabled.

**Implementation in `internal/secrets/generator.go`:**

```go
import (
    "crypto/rand"
    "crypto/rsa"
    "crypto/x509"
    "crypto/x509/pkix"
    "encoding/pem"
    "math/big"
    "time"
)

// generateSelfSignedCA creates a self-signed CA certificate for database TLS
// GAP #8: Self-Signed CA Generation in Wave 1
func (g *Generator) generateSelfSignedCA(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment, namespace string) error {
    secretName := "platform-db-ca"
    
    // Check if CA already exists (idempotent)
    existing := &corev1.Secret{}
    err := g.Get(ctx, client.ObjectKey{Name: secretName, Namespace: namespace}, existing)
    if err == nil {
        // CA already exists, reuse it
        return nil
    }
    
    // Generate RSA private key
    privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
    if err != nil {
        return fmt.Errorf("failed to generate private key: %w", err)
    }
    
    // Create CA certificate template
    serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
    if err != nil {
        return fmt.Errorf("failed to generate serial number: %w", err)
    }
    
    template := x509.Certificate{
        SerialNumber: serialNumber,
        Subject: pkix.Name{
            Organization: []string{"Zero-Ops Platform"},
            CommonName:   "platform-db-ca",
        },
        NotBefore:             time.Now(),
        NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour), // 10 years
        KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
        BasicConstraintsValid: true,
        IsCA:                  true,
    }
    
    // Self-sign the certificate
    certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
    if err != nil {
        return fmt.Errorf("failed to create certificate: %w", err)
    }
    
    // Encode certificate to PEM
    certPEM := pem.EncodeToMemory(&pem.Block{
        Type:  "CERTIFICATE",
        Bytes: certDER,
    })
    
    // Encode private key to PEM
    keyPEM := pem.EncodeToMemory(&pem.Block{
        Type:  "RSA PRIVATE KEY",
        Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
    })
    
    // Create secret with CA certificate and key
    secret := &corev1.Secret{
        ObjectMeta: metav1.ObjectMeta{
            Name:      secretName,
            Namespace: namespace,
            Labels: map[string]string{
                "app.kubernetes.io/component": "database-ca",
                "ops.nutgraf.in/managed-by":  "hub-operator",
            },
        },
        Type: corev1.SecretTypeTLS,
        Data: map[string][]byte{
            "ca.crt": certPEM,
            "ca.key": keyPEM,
        },
    }
    
    // Set owner reference
    if err := controllerutil.SetControllerReference(hubEnv, secret, g.Scheme); err != nil {
        return err
    }
    
    // Create secret
    if err := g.Create(ctx, secret); err != nil {
        return fmt.Errorf("failed to create platform-db-ca secret: %w", err)
    }
    
    return nil
}
```

**Update `GenerateSecretZero` to call CA generation first:**

```go
func (g *Generator) GenerateSecretZero(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) error {
    namespace := hubEnv.Spec.Database.Namespace
    
    // 0. Generate self-signed CA FIRST (GAP #8)
    if err := g.generateSelfSignedCA(ctx, hubEnv, namespace); err != nil {
        return fmt.Errorf("failed to generate self-signed CA: %w", err)
    }
    
    // 1. Generate infisical-secrets (now can read platform-db-ca)
    if err := g.generateInfisicalSecrets(ctx, hubEnv, namespace); err != nil {
        return fmt.Errorf("failed to generate infisical-secrets: %w", err)
    }
    
    // ... rest of secret generation
}
```

### UploadedSecrets Array Tracking (GAP #9)

The operator tracks which secrets have been uploaded to Infisical to support Day-2 additive sync.

**Update HubEnvironment Status in CRD:**

```yaml
status:
  type: object
  properties:
    uploadedSecrets:
      type: array
      description: "List of secret names already uploaded to Infisical"
      items:
        type: string
    conditions:
      type: array
      items:
        type: object
        properties:
          type:
            type: string
          status:
            type: string
          reason:
            type: string
          message:
            type: string
          lastTransitionTime:
            type: string
            format: date-time
```

**Implementation in `uploadSecretsToInfisical`:**

```go
// uploadSecretsToInfisical uploads secrets to Infisical with additive sync
// GAP #9: Day-2 Additive Sync with UploadedSecrets tracking
func (r *HubEnvironmentReconciler) uploadSecretsToInfisical(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) error {
    projectSlug := hubEnv.Spec.Secrets.Infisical.ProjectSlug
    envSlug := hubEnv.Spec.Secrets.Infisical.EnvironmentSlug
    
    // Initialize UploadedSecrets array if nil
    if hubEnv.Status.UploadedSecrets == nil {
        hubEnv.Status.UploadedSecrets = []string{}
    }
    
    // Define all secrets to upload based on CR spec
    secretsToUpload := map[string]struct {
        secretName string
        key        string
    }{
        "infisical-db-username":       {"infisical-db-credentials", "username"},
        "infisical-db-password":       {"infisical-db-credentials", "password"},
        "platform-db-app-username":    {"platform-db-app", "username"},
        "platform-db-app-password":    {"platform-db-app", "password"},
        "control-plane-db-username":   {"control-plane-db-credentials", "username"},
        "control-plane-db-password":   {"control-plane-db-credentials", "password"},
        "hub-db-username":             {"hub-db-credentials", "username"},
        "hub-db-password":             {"hub-db-credentials", "password"},
        "spire-server-db-username":    {"spire-server-db-credentials", "username"},
        "spire-server-db-password":    {"spire-server-db-credentials", "password"},
        "hydra-db-username":           {"hydra-db-credentials", "username"},
        "hydra-db-password":           {"hydra-db-credentials", "password"},
        "kratos-db-username":          {"kratos-db-credentials", "username"},
        "kratos-db-password":          {"kratos-db-credentials", "password"},
        "keto-db-username":            {"keto-db-credentials", "username"},
        "keto-db-password":            {"keto-db-credentials", "password"},
    }
    
    // Upload only secrets NOT in UploadedSecrets array
    for infisicalKey, secretInfo := range secretsToUpload {
        // Check if already uploaded
        if contains(hubEnv.Status.UploadedSecrets, infisicalKey) {
            continue // Skip already uploaded secrets
        }
        
        // Read secret from Kubernetes
        secret := &corev1.Secret{}
        if err := r.UncachedClient.Get(ctx, client.ObjectKey{
            Name:      secretInfo.secretName,
            Namespace: hubEnv.Spec.Database.Namespace,
        }, secret); err != nil {
            return fmt.Errorf("failed to read secret %s: %w", secretInfo.secretName, err)
        }
        
        value := string(secret.Data[secretInfo.key])
        
        // Upload to Infisical (PASSWORDS ONLY, no URLs/hostnames)
        if err := r.InfisicalClient.CreateOrUpdateSecret(ctx, projectSlug, envSlug, "/", infisicalKey, value); err != nil {
            return fmt.Errorf("failed to upload %s: %w", infisicalKey, err)
        }
        
        // Add to UploadedSecrets array
        hubEnv.Status.UploadedSecrets = append(hubEnv.Status.UploadedSecrets, infisicalKey)
    }
    
    // Update status with new UploadedSecrets array
    if err := r.Status().Update(ctx, hubEnv); err != nil {
        return fmt.Errorf("failed to update UploadedSecrets status: %w", err)
    }
    
    return nil
}

// contains checks if a string slice contains a value
func contains(slice []string, value string) bool {
    for _, item := range slice {
        if item == value {
            return true
        }
    }
    return false
}
```

### Resource Pruning (GAP #10)

The operator deletes orphaned resources that exist in the cluster but are no longer defined in the HubEnvironment CR spec.

**Database Role Pruning:**

```go
// pruneOrphanedRoles deletes database roles not in CR spec
// GAP #10: Database Role Pruning
func (rm *RoleManager) pruneOrphanedRoles(ctx context.Context, desiredRoles []string) error {
    // Query all roles managed by hub-operator (with specific comment or prefix)
    query := `
        SELECT rolname 
        FROM pg_roles 
        WHERE rolname LIKE 'zero-ops-%' 
           OR rolcomment LIKE '%managed-by:hub-operator%'
    `
    
    rows, err := rm.db.QueryContext(ctx, query)
    if err != nil {
        return fmt.Errorf("failed to query existing roles: %w", err)
    }
    defer rows.Close()
    
    var existingRoles []string
    for rows.Next() {
        var roleName string
        if err := rows.Scan(&roleName); err != nil {
            return err
        }
        existingRoles = append(existingRoles, roleName)
    }
    
    // Find orphaned roles (exist in DB but not in desired list)
    for _, existingRole := range existingRoles {
        if !contains(desiredRoles, existingRole) {
            // Orphaned role - delete it
            dropQuery := fmt.Sprintf("DROP ROLE IF EXISTS %s", existingRole)
            if _, err := rm.db.ExecContext(ctx, dropQuery); err != nil {
                return fmt.Errorf("failed to drop orphaned role %s: %w", existingRole, err)
            }
            log.Printf("Pruned orphaned database role: %s", existingRole)
        }
    }
    
    return nil
}
```

**OAuth Client Pruning (Req 7.8):**

```go
// pruneOrphanedOAuthClients deletes OAuth clients not in CR spec
// GAP #10: OAuth Client Pruning (Req 7.8)
func (hc *HydraClient) pruneOrphanedOAuthClients(ctx context.Context, desiredClientIDs []string) error {
    // List all OAuth clients
    clients, _, err := hc.client.OAuth2API.ListOAuth2Clients(ctx).Execute()
    if err != nil {
        return fmt.Errorf("failed to list OAuth clients: %w", err)
    }
    
    // Find orphaned clients (exist in Hydra but not in desired list)
    for _, client := range clients {
        clientID := client.GetClientId()
        if !contains(desiredClientIDs, clientID) {
            // Orphaned client - delete it
            _, err := hc.client.OAuth2API.DeleteOAuth2Client(ctx, clientID).Execute()
            if err != nil {
                return fmt.Errorf("failed to delete orphaned OAuth client %s: %w", clientID, err)
            }
            log.Printf("Pruned orphaned OAuth client: %s", clientID)
        }
    }
    
    return nil
}
```

**NATS Stream Pruning (Req 8.10):**

```go
// pruneOrphanedStreams deletes NATS streams not in CR spec
// GAP #10: NATS Stream Pruning (Req 8.10)
func (nc *NATSClient) pruneOrphanedStreams(ctx context.Context, desiredStreamNames []string) error {
    // List all streams
    streamNames := nc.js.StreamNames()
    
    var existingStreams []string
    for name := range streamNames {
        existingStreams = append(existingStreams, name)
    }
    
    // Find orphaned streams (exist in NATS but not in desired list)
    for _, existingStream := range existingStreams {
        if !contains(desiredStreamNames, existingStream) {
            // Orphaned stream - delete it
            if err := nc.js.DeleteStream(existingStream); err != nil {
                return fmt.Errorf("failed to delete orphaned stream %s: %w", existingStream, err)
            }
            log.Printf("Pruned orphaned NATS stream: %s", existingStream)
        }
    }
    
    return nil
}
```

### Password Rotation Restart Scope (GAP #11)

When passwords change, the operator executes ALTER ROLE and restarts consuming services.

**Implementation in reconciler:**

```go
// handlePasswordRotation detects password changes and updates database + restarts services
// GAP #11: Password Rotation Restart Scope (Req 23.15-23.16)
func (r *HubEnvironmentReconciler) handlePasswordRotation(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment, secret *corev1.Secret) error {
    // Determine which service this secret belongs to
    serviceName := secret.Labels["app.kubernetes.io/component"]
    if serviceName == "" {
        return nil // Not a service credential
    }
    
    // Extract new password
    newPassword := string(secret.Data["password"])
    roleName := string(secret.Data["username"])
    
    // Execute ALTER ROLE in PostgreSQL
    connStr := r.buildConnectionString(hubEnv)
    roleManager, err := database.NewRoleManager(connStr)
    if err != nil {
        return fmt.Errorf("failed to create role manager: %w", err)
    }
    defer roleManager.Close()
    
    alterQuery := fmt.Sprintf("ALTER ROLE %s WITH PASSWORD '%s'", roleName, newPassword)
    if _, err := roleManager.db.ExecContext(ctx, alterQuery); err != nil {
        return fmt.Errorf("failed to alter role password: %w", err)
    }
    
    // Restart consuming Deployment/StatefulSet
    switch serviceName {
    case "hydra":
        if err := r.restartDeployment(ctx, "ory-hydra", "ory-system"); err != nil {
            return err
        }
    case "kratos":
        if err := r.restartDeployment(ctx, "ory-kratos", "ory-system"); err != nil {
            return err
        }
    case "keto":
        if err := r.restartDeployment(ctx, "ory-keto", "ory-system"); err != nil {
            return err
        }
    case "mcp-server":
        if err := r.restartDeployment(ctx, "mcp-server", "hub-platform-core"); err != nil {
            return err
        }
    case "spire-server":
        if err := r.restartStatefulSet(ctx, "spire-server", "hub-platform-identity"); err != nil {
            return err
        }
    }
    
    r.Log.Info("Password rotation complete", "service", serviceName, "role", roleName)
    return nil
}
```

### NATS JetStream Transient Error Handling (GAP #12)

Distinguish transient NATS errors from permanent configuration errors.

**Implementation in `createNATSStreams`:**

```go
// createNATSStreams creates NATS JetStream streams with transient error handling
// GAP #12: NATS JetStream Transient Errors (Req 20.13)
func (r *HubEnvironmentReconciler) createNATSStreams(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) error {
    for _, streamSpec := range hubEnv.Spec.NATS.Streams {
        err := r.NATSClient.CreateStream(ctx, streamSpec.Name, streamSpec.Subjects, 
            parseRetention(streamSpec.Retention), parseStorage(streamSpec.Storage))
        
        if err != nil {
            // Check if error is transient (connection timeout, network failure)
            if isNATSTransientError(err) {
                // Transient error - requeue with backoff
                return fmt.Errorf("transient NATS error: %w", err)
            }
            
            // Permanent error (invalid configuration) - update status, no requeue
            meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
                Type:    "NATSStreamsConfigured",
                Status:  metav1.ConditionFalse,
                Reason:  "ConfigurationError",
                Message: fmt.Sprintf("Invalid stream configuration for %s: %v", streamSpec.Name, err),
            })
            return r.Status().Update(ctx, hubEnv)
        }
    }
    
    // Prune orphaned streams (GAP #10)
    desiredStreamNames := make([]string, len(hubEnv.Spec.NATS.Streams))
    for i, stream := range hubEnv.Spec.NATS.Streams {
        desiredStreamNames[i] = stream.Name
    }
    if err := r.NATSClient.pruneOrphanedStreams(ctx, desiredStreamNames); err != nil {
        return fmt.Errorf("failed to prune orphaned streams: %w", err)
    }
    
    return nil
}

// isNATSTransientError checks if a NATS error is transient
func isNATSTransientError(err error) bool {
    if err == nil {
        return false
    }
    errStr := err.Error()
    // Connection errors
    if strings.Contains(errStr, "connection refused") ||
       strings.Contains(errStr, "connection reset") ||
       strings.Contains(errStr, "timeout") ||
       strings.Contains(errStr, "no servers available") {
        return true
    }
    return false
}
```

### ESO Template Pattern Examples (GAP #7)

External Secrets Operator templates combine Infisical passwords with Kubernetes service discovery.

**Example ESO ExternalSecret with Template:**

```yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: mcp-server-db-connection
  namespace: hub-platform-core
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: infisical-secret-store
    kind: SecretStore
  target:
    name: mcp-server-db-connection
    creationPolicy: Owner
    template:
      engineVersion: v2
      data:
        # GAP #7: "Secrets Only" Rule - Combine Infisical password with K8s service discovery
        DB_HOST: "platform-db-pooler.hub-platform-data.svc.cluster.local"
        DB_PORT: "5432"
        DB_NAME: "control_plane"
        DB_USER: "{{ .mcp_server_username }}"
        DB_PASSWORD: "{{ .mcp_server_password }}"
        DB_SSL_MODE: "require"
        # Connection string assembled from template
        DATABASE_URL: "postgres://{{ .mcp_server_username }}:{{ .mcp_server_password }}@platform-db-pooler.hub-platform-data.svc.cluster.local:5432/control_plane?sslmode=require"
  dataFrom:
  - extract:
      key: mcp-server-db-username
      property: value
      decodingStrategy: None
  - extract:
      key: mcp-server-db-password
      property: value
      decodingStrategy: None
```

**Key Points:**
- Infisical stores ONLY `mcp-server-db-username` and `mcp-server-db-password` (passwords/keys)
- ESO template adds `DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_SSL_MODE` (configuration)
- Final secret contains complete connection information
- No URLs, hostnames, or ports stored in Infisical

**Another Example for Hydra:**

```yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: hydra-db-connection
  namespace: ory-system
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: infisical-secret-store
    kind: SecretStore
  target:
    name: hydra-db-connection
    creationPolicy: Owner
    template:
      engineVersion: v2
      data:
        DSN: "postgres://{{ .hydra_username }}:{{ .hydra_password }}@platform-db-pooler.hub-platform-data.svc.cluster.local:5432/hydra?sslmode=require"
  dataFrom:
  - extract:
      key: hydra-db-username
      property: value
  - extract:
      key: hydra-db-password
      property: value
```

This pattern ensures:
1. Infisical remains a pure secret store (passwords only)
2. Service discovery uses Kubernetes DNS (no hardcoded IPs)
3. Configuration changes don't require Infisical updates
4. Secrets rotation works independently of infrastructure changestabase-connected services
- Restarts: Infisical, Redis, Hydra, Kratos, Keto, SPIRE Server, MCP Server
- Prevents x509 certificate errors across entire platform

**GAP #6: Missing Universal Auth Secret Dependency**
- Phase 3 (Upload Secrets) suspended if infisical-auth missing
- Phases 1 & 2 (Secret Zero, Database Setup) proceed independently
- Allows Infisical to boot successfully before auth configuration
- Clear status condition with reason "AuthenticationFailed"

These enhancements ensure the operator handles all Day-2 operational scenarios without manual intervention, while providing clear recovery paths for permanent errors.
