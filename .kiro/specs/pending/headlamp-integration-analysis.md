# Headlamp Integration Analysis: Replacing Custom Spoke Controller

## Executive Summary

**Recommendation**: ✅ **Use Headlamp for resource monitoring** with automated kubeconfig generation via Crossplane Composition sidecar pattern.

**Key Finding**: Headlamp's built-in `fsnotify` watcher + `ContextStore` architecture is **perfectly suited** for dynamic multi-cluster monitoring without requiring a custom Spoke Controller for status updates.

---

## 1. Current Architecture (Status Controller Pattern)

### 1.1 Custom Spoke Controller (To Be Replaced)
```
Spoke Controller (controller-runtime)
  ├── Watches: AINativeSaaS XR conditions
  ├── On condition change → derives status (provisioning/ready/failed)
  ├── Writes to: Hub Centralised DB via AgentGateway → PostgREST
  ├── Auth: mTLS (SPIFFE/SPIRE) → AgentGateway issues JWT → PostgREST
  └── Problem: Custom code, maintenance overhead, single-purpose
```

**Issues with Custom Controller:**
- Requires custom Go code (controller-runtime)
- Single-purpose: only writes status to Hub DB
- No UI/visualization
- Maintenance burden
- Doesn't provide resource browsing/debugging capabilities

---

## 2. Headlamp Architecture Analysis

### 2.1 Core Components

**Backend (Go):**
```go
// KubeConfigStore: In-memory cache of cluster contexts
type ContextStore interface {
    AddContext(headlampContext *Context) error
    GetContexts() ([]*Context, error)
    GetContext(name string) (*Context, error)
    RemoveContext(name string) error
    AddContextWithKeyAndTTL(headlampContext *Context, key string, ttl time.Duration) error
}

// Watcher: fsnotify-based file watcher
func LoadAndWatchFiles(
    ctx context.Context,
    kubeConfigStore ContextStore,
    paths string,
    source int,
    ignoreFunc shouldBeSkippedFunc,
)
```

**Key Features:**
1. **fsnotify Integration**: Watches kubeconfig files for changes (Create, Write, Remove, Rename)
2. **Automatic Reload**: Detects file changes → reloads contexts → updates UI
3. **Multi-Cluster Support**: Cluster dropdown in UI (hub, spoke-pool-01, spoke-silo-tenant1, etc.)
4. **Context Sources**: Supports multiple sources (kubeconfig, in-cluster, dynamic)
5. **TTL Support**: Contexts can have expiration (useful for temporary access)

### 2.2 Watcher Implementation

```go
// Triggers: Create, Write, Remove, Rename events
triggers := []fsnotify.Op{fsnotify.Create, fsnotify.Write, fsnotify.Remove, fsnotify.Rename}

// Sync logic: Compares existing contexts vs new contexts
func syncContexts(kubeConfigStore ContextStore, paths string, source int, ignoreFunc shouldBeSkippedFunc) error {
    // 1. Read new contexts from kubeconfig files
    newContexts, _, err := LoadContextsFromMultipleFiles(paths, source)
    
    // 2. Get existing contexts from store
    existingContexts, err := kubeConfigStore.GetContexts()
    
    // 3. Remove contexts that no longer exist
    for _, existingCtx := range existingContexts {
        if existingCtx.Source != KubeConfig { continue } // Skip other sources
        if !found { kubeConfigStore.RemoveContext(existingCtx.Name) }
    }
    
    // 4. Load and store new configurations
    err = LoadAndStoreKubeConfigs(kubeConfigStore, paths, source, ignoreFunc)
}
```

**Reload Interval**: 10 seconds (configurable)

---

## 3. Proposed Solution: Headlamp + Crossplane Sidecar Pattern

### 3.1 Architecture

```
┌─────────────────────────────────────────────────────────────┐
│ Hub Cluster                                                  │
│                                                              │
│  ┌────────────────────────────────────────────────────┐    │
│  │ Crossplane Composition (SpokePool XR)              │    │
│  │                                                     │    │
│  │  ├── CAPI Cluster (provisions Hetzner VMs)        │    │
│  │  ├── HetznerCluster                                │    │
│  │  ├── MachineDeployment                             │    │
│  │  └── Sidecar Controller (NEW)                      │    │
│  │      ├── Watches: AINativeSaaS XR (Ready condition)│    │
│  │      ├── Extracts: kubeconfig from CAPI Secret     │    │
│  │      └── Writes: /kubeconfigs/spoke-<id>.yaml      │    │
│  └────────────────────────────────────────────────────┘    │
│                                                              │
│  ┌────────────────────────────────────────────────────┐    │
│  │ Headlamp Deployment                                 │    │
│  │                                                     │    │
│  │  ├── Volume: /kubeconfigs (shared with sidecar)   │    │
│  │  ├── fsnotify: Watches /kubeconfigs/*.yaml        │    │
│  │  ├── Auto-reload: Detects new kubeconfig files    │    │
│  │  └── UI: Cluster dropdown (hub, spoke-pool-01...) │    │
│  └────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ Spoke Pool Cluster (spoke-pool-01)                          │
│                                                              │
│  ├── Edge Catalog (CNPG, PostgREST, NATS, Atlas, Alloy)   │
│  ├── Tenant Schemas (tenant_acme, tenant_xyz)              │
│  └── Tenant Workloads (namespaces, pods, deployments)      │
└─────────────────────────────────────────────────────────────┘
```

### 3.2 Sidecar Controller Implementation

**Purpose**: Watch AINativeSaaS XRs and generate kubeconfig files for Headlamp

```go
// Sidecar Controller (controller-runtime)
type KubeconfigGenerator struct {
    client.Client
    OutputDir string // /kubeconfigs
}

func (r *KubeconfigGenerator) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    var xr ainativesaasv1.AINativeSaaS
    if err := r.Get(ctx, req.NamespacedName, &xr); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // Only process if cluster is Ready
    if !meta.IsStatusConditionTrue(xr.Status.Conditions, "Ready") {
        return ctrl.Result{}, nil
    }
    
    // Extract kubeconfig from CAPI Secret
    clusterName := xr.Spec.ClusterRef
    secret := &corev1.Secret{}
    if err := r.Get(ctx, types.NamespacedName{
        Name: fmt.Sprintf("%s-kubeconfig", clusterName),
        Namespace: "default",
    }, secret); err != nil {
        return ctrl.Result{}, err
    }
    
    kubeconfigData := secret.Data["value"]
    
    // Write kubeconfig to shared volume
    outputPath := filepath.Join(r.OutputDir, fmt.Sprintf("spoke-%s.yaml", clusterName))
    if err := os.WriteFile(outputPath, kubeconfigData, 0600); err != nil {
        return ctrl.Result{}, err
    }
    
    log.Info("Generated kubeconfig", "cluster", clusterName, "path", outputPath)
    return ctrl.Result{}, nil
}
```

**Deployment:**
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kubeconfig-generator
  namespace: platform-ops
spec:
  template:
    spec:
      containers:
      - name: generator
        image: kubeconfig-generator:latest
        volumeMounts:
        - name: kubeconfigs
          mountPath: /kubeconfigs
      volumes:
      - name: kubeconfigs
        persistentVolumeClaim:
          claimName: headlamp-kubeconfigs
```

### 3.3 Headlamp Configuration

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: headlamp
  namespace: headlamp
spec:
  template:
    spec:
      containers:
      - name: headlamp
        image: ghcr.io/headlamp-k8s/headlamp:latest
        args:
        - "-kubeconfig=/kubeconfigs/*.yaml"  # Watch all kubeconfig files
        volumeMounts:
        - name: kubeconfigs
          mountPath: /kubeconfigs
          readOnly: true
      volumes:
      - name: kubeconfigs
        persistentVolumeClaim:
          claimName: headlamp-kubeconfigs
```

**Result**: Headlamp automatically discovers and displays all clusters in the dropdown.

---

## 4. Benefits of Headlamp Approach

### 4.1 ✅ Advantages

| Feature | Custom Spoke Controller | Headlamp |
|---------|------------------------|----------|
| **Resource Monitoring** | ❌ No UI | ✅ Full Kubernetes UI |
| **Multi-Cluster Support** | ❌ Single-purpose | ✅ Native multi-cluster |
| **Auto-Discovery** | ❌ Manual | ✅ fsnotify auto-reload |
| **Debugging** | ❌ No visibility | ✅ Pod logs, events, YAML |
| **Maintenance** | ❌ Custom code | ✅ CNCF project |
| **Plugin Ecosystem** | ❌ None | ✅ Crossplane, CAPI, Flux plugins |
| **RBAC** | ❌ Custom | ✅ Kubernetes RBAC |
| **Cost** | ❌ Development time | ✅ Zero development |

### 4.2 What Headlamp Provides

1. **Resource Browsing**: View pods, deployments, services, CRs across all clusters
2. **Real-Time Updates**: WebSocket-based live updates
3. **Logs & Events**: Pod logs, Kubernetes events
4. **YAML Editor**: Edit resources directly
5. **Crossplane Plugin**: View XRs, Compositions, Claims
6. **CAPI Plugin**: View Cluster API resources
7. **Custom Plugins**: Extend with custom views

### 4.3 What Headlamp Does NOT Replace

**Still Need Custom Components:**
1. **Hub Centralised DB Writes**: Spoke Controller writes status to Hub DB (keep this)
2. **Billing Events**: NATS Leaf Node forwards billing events (keep this)
3. **Metrics**: Grafana Alloy forwards metrics to VictoriaMetrics (keep this)

**Headlamp is for**: Human operators monitoring/debugging clusters
**Spoke Controller is for**: Machine-to-machine status synchronization

---

## 5. Challenges & Mitigations

### 5.1 Challenge: Kubeconfig Security

**Problem**: Kubeconfig files contain cluster credentials

**Mitigation**:
1. **Encrypted Volume**: Use encrypted PVC for `/kubeconfigs`
2. **RBAC**: Limit Headlamp service account to read-only access
3. **Short-Lived Tokens**: Generate kubeconfigs with TTL (24h)
4. **Rotation**: Sidecar regenerates kubeconfigs daily

```go
// Generate kubeconfig with TTL
func generateKubeconfigWithTTL(cluster string, ttl time.Duration) ([]byte, error) {
    // Use TokenRequest API for short-lived tokens
    tokenRequest := &authv1.TokenRequest{
        Spec: authv1.TokenRequestSpec{
            ExpirationSeconds: ptr.To(int64(ttl.Seconds())),
        },
    }
    // ... generate kubeconfig with token
}
```

### 5.2 Challenge: Kubeconfig File Proliferation

**Problem**: 50 Spoke Pool clusters = 50 kubeconfig files

**Mitigation**:
1. **Cleanup**: Sidecar removes kubeconfig when cluster is deleted
2. **TTL**: Headlamp `AddContextWithKeyAndTTL` expires stale contexts
3. **Filtering**: Headlamp supports context filtering by labels

```go
// Cleanup deleted clusters
func (r *KubeconfigGenerator) cleanupDeletedClusters(ctx context.Context) error {
    files, _ := filepath.Glob(filepath.Join(r.OutputDir, "spoke-*.yaml"))
    
    for _, file := range files {
        clusterName := extractClusterName(file)
        
        // Check if XR still exists
        var xr ainativesaasv1.AINativeSaaS
        if err := r.Get(ctx, types.NamespacedName{Name: clusterName}, &xr); err != nil {
            if apierrors.IsNotFound(err) {
                os.Remove(file) // Cluster deleted, remove kubeconfig
            }
        }
    }
}
```

### 5.3 Challenge: Hub Cluster Kubeconfig

**Problem**: Headlamp needs Hub cluster kubeconfig too

**Mitigation**:
1. **In-Cluster Mode**: Headlamp runs in Hub, uses in-cluster config for Hub
2. **Static Kubeconfig**: Hub kubeconfig at `/kubeconfigs/hub.yaml` (static)
3. **Dynamic Kubeconfigs**: Spoke kubeconfigs generated by sidecar

```yaml
# Headlamp args
args:
- "-in-cluster"  # Use in-cluster config for Hub
- "-kubeconfig=/kubeconfigs/*.yaml"  # Watch dynamic Spoke kubeconfigs
```

### 5.4 Challenge: Status Updates to Hub DB

**Problem**: Headlamp doesn't write status to Hub Centralised DB

**Mitigation**: **Keep Spoke Controller for status writes**

**Revised Architecture**:
```
Spoke Pool Cluster
  ├── Spoke Controller (keeps existing functionality)
  │   ├── Watches: AINativeSaaS XR conditions
  │   └── Writes: Hub Centralised DB via AgentGateway → PostgREST
  │
  └── Headlamp (NEW - for human operators)
      ├── Reads: All Kubernetes resources
      └── UI: Multi-cluster dashboard
```

**Separation of Concerns**:
- **Spoke Controller**: Machine-to-machine status sync (automated)
- **Headlamp**: Human-to-machine monitoring (manual)

---

## 6. Implementation Plan

### Phase 1: Sidecar Controller (Week 1)
- [ ] Implement kubeconfig-generator controller
- [ ] Watch AINativeSaaS XRs for Ready condition
- [ ] Extract kubeconfig from CAPI Secrets
- [ ] Write kubeconfig files to shared volume
- [ ] Implement cleanup for deleted clusters

### Phase 2: Headlamp Deployment (Week 2)
- [ ] Deploy Headlamp to Hub cluster
- [ ] Configure fsnotify watcher for `/kubeconfigs`
- [ ] Test auto-discovery of Spoke clusters
- [ ] Verify cluster dropdown functionality
- [ ] Configure RBAC (read-only access)

### Phase 3: Plugin Integration (Week 3)
- [ ] Install Crossplane plugin (view XRs, Compositions)
- [ ] Install CAPI plugin (view Cluster API resources)
- [ ] Install Atlas plugin (if available, view migrations)
- [ ] Test custom plugin development (tenant schema viewer)

### Phase 4: Security Hardening (Week 4)
- [ ] Implement encrypted PVC for kubeconfigs
- [ ] Generate short-lived tokens (24h TTL)
- [ ] Implement kubeconfig rotation
- [ ] Audit RBAC permissions
- [ ] Test access controls

---

## 7. Comparison: Custom Controller vs Headlamp

### 7.1 Development Effort

| Task | Custom Controller | Headlamp |
|------|------------------|----------|
| **Initial Development** | 2-3 weeks | 1 week (sidecar only) |
| **UI Development** | 4-6 weeks | 0 (built-in) |
| **Multi-Cluster Support** | 2 weeks | 0 (built-in) |
| **Plugin System** | 4 weeks | 0 (built-in) |
| **Maintenance** | Ongoing | Minimal |
| **Total** | 12-15 weeks | 1 week |

### 7.2 Feature Comparison

| Feature | Custom Controller | Headlamp |
|---------|------------------|----------|
| Status Updates to Hub DB | ✅ | ❌ (keep Spoke Controller) |
| Resource Monitoring | ❌ | ✅ |
| Multi-Cluster UI | ❌ | ✅ |
| Pod Logs | ❌ | ✅ |
| Events | ❌ | ✅ |
| YAML Editor | ❌ | ✅ |
| Plugins | ❌ | ✅ |
| RBAC | Custom | ✅ Kubernetes RBAC |

---

## 8. Recommendation

### ✅ Use Headlamp for Resource Monitoring

**Architecture**:
1. **Sidecar Controller**: Generates kubeconfig files from AINativeSaaS XRs
2. **Headlamp**: Monitors all clusters via fsnotify + ContextStore
3. **Spoke Controller**: Keeps existing status sync to Hub DB (unchanged)

**Benefits**:
- 90% reduction in development time
- CNCF-backed project (maintenance handled upstream)
- Rich UI with plugins
- Native multi-cluster support
- Zero custom UI code

**Trade-offs**:
- Requires sidecar controller (1 week development)
- Kubeconfig security considerations (mitigated)
- Spoke Controller still needed for status sync (acceptable)

---

## 9. Next Steps

1. **Prototype**: Build sidecar controller (3 days)
2. **Test**: Deploy Headlamp + sidecar to dev Hub (2 days)
3. **Validate**: Verify auto-discovery of Spoke clusters (1 day)
4. **Security Review**: Audit kubeconfig handling (2 days)
5. **Production**: Deploy to production Hub (1 day)

**Total Time**: 2 weeks (vs 12-15 weeks for custom solution)

---

## 10. Conclusion

Headlamp is **perfectly suited** for replacing the custom Spoke Controller's monitoring responsibilities. The built-in fsnotify watcher + ContextStore architecture aligns exactly with the requirement for dynamic multi-cluster monitoring.

**Key Insight**: The opensbt-architecture-guide.md's "Status Controller Pattern" is about **avoiding synchronous K8s API calls in HTTP handlers**. Headlamp solves this by:
1. **Async Updates**: fsnotify watches kubeconfig files (no polling)
2. **Cached State**: ContextStore caches cluster contexts (no live K8s API calls)
3. **Separation**: Spoke Controller handles status writes, Headlamp handles monitoring

This is an **enterprise-grade solution** that follows the opensbt pattern while providing a superior operator experience.
