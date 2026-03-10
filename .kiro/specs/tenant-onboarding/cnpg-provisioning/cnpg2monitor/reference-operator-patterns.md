# cnpg2monitor Operator - Reference Patterns from capi2argo

**Source:** `.kiro/specs/tenant-onboarding/cnpg-provisioning/archived/capi2argo-cluster-operator`  
**Purpose:** Document proven patterns from production capi2argo operator to adopt in cnpg2monitor  
**Status:** REFERENCE GUIDE  
**Created:** 2026-03-10

---

## Pattern Categories

### 1. Main Entrypoint Patterns
### 2. Controller Architecture Patterns
### 3. Configuration Management Patterns
### 4. Reconciliation Logic Patterns
### 5. Metrics & Observability Patterns
### 6. RBAC & Security Patterns
### 7. Testing Patterns

---

## 1. Main Entrypoint Patterns (main.go)

### P1.1: Flag-Based Configuration
**Pattern:** Use standard Go flags for runtime configuration

```go
// capi2argo example
var (
    metricsAddr          string
    probeAddr            string
    syncDuration         time.Duration
    enableLeaderElection bool
    enableDryRun         bool
    enableDebugMode      bool
)

flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "...")
flag.DurationVar(&syncDuration, "sync-duration", 45*time.Second, "...")
flag.BoolVar(&enableDebugMode, "debug", false, "...")
```

**Adopt for cnpg2monitor:**
- `--metrics-bind-address` (default `:8080`)
- `--health-probe-bind-address` (default `:8081`)
- `--sync-duration` (default `30s` - faster than capi2argo since CNPG changes are less frequent)
- `--debug` (enable verbose logging)
- `--leader-elect` (enable for multi-replica deployments)

---

### P1.2: Structured Logging with Zap
**Pattern:** Use controller-runtime's zap logger with development mode flag

```go
opts := zap.Options{
    Development: enableDebugMode,
}
opts.BindFlags(flag.CommandLine)
ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
```

**Adopt for cnpg2monitor:** Identical pattern - provides JSON structured logs in production, human-readable in debug mode.

---

### P1.3: Health & Readiness Probes
**Pattern:** Register standard Kubernetes health checks

```go
if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
    setupLog.Error(err, "unable to set up health check")
    os.Exit(1)
}
if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
    setupLog.Error(err, "unable to set up ready check")
    os.Exit(1)
}
```

**Adopt for cnpg2monitor:** Identical - enables Kubernetes liveness/readiness probes at `/healthz` and `/readyz`.

---

### P1.4: Scheme Registration
**Pattern:** Register all CRD types the operator watches

```go
func init() {
    utilruntime.Must(clusterv1.AddToScheme(scheme))
    utilruntime.Must(clientgoscheme.AddToScheme(scheme))
}
```

**Adopt for cnpg2monitor:**
```go
import (
    cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
    monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func init() {
    utilruntime.Must(cnpgv1.AddToScheme(scheme))
    utilruntime.Must(monitoringv1.AddToScheme(scheme))
    utilruntime.Must(clientgoscheme.AddToScheme(scheme))
}
```

---

## 2. Controller Architecture Patterns

### P2.1: Controller Struct with Embedded Client
**Pattern:** Embed controller-runtime client for direct K8s API access

```go
type Capi2Argo struct {
    client.Client
    Log        logr.Logger
    Scheme     *runtime.Scheme
    SyncPeriod time.Duration
    Config     Config
}
```

**Adopt for cnpg2monitor:**
```go
type Cnpg2Monitor struct {
    client.Client
    Log        logr.Logger
    Scheme     *runtime.Scheme
    Recorder   record.EventRecorder  // NEW: for K8s event emission
    SyncPeriod time.Duration
    Config     Config
}
```

**Key Addition:** `Recorder` field for emitting K8s Events (PRD requirement).

---

### P2.2: Predicate-Based Event Filtering
**Pattern:** Filter events at the watch level to reduce reconciliation load

```go
func (r *Capi2Argo) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&corev1.Secret{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
            nn := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
            return ValidateCapiNaming(nn) && r.Config.IsNamespaceAllowed(obj.GetNamespace())
        }))).
        Complete(r)
}
```

**Adopt for cnpg2monitor:**
```go
func (r *Cnpg2Monitor) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&cnpgv1.Cluster{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
            // Only watch CNPG Clusters with monitoring label
            labels := obj.GetLabels()
            return labels != nil && labels["zero-ops.io/monitored"] == "true"
        }))).
        Complete(r)
}
```

**Rationale:** Avoids reconciling CNPG Clusters that don't need monitoring (e.g., test databases).

---

### P2.3: Requeue with SyncPeriod
**Pattern:** Always requeue after SyncPeriod for periodic reconciliation

```go
return ctrl.Result{RequeueAfter: r.SyncPeriod}, nil
```

**Adopt for cnpg2monitor:** Identical - ensures PodMonitors stay in sync even if watch events are missed.

---

## 3. Configuration Management Patterns

### P3.1: Environment Variable Configuration
**Pattern:** Load config from env vars with sensible defaults

```go
func LoadConfigFromEnv() Config {
    argoNS := os.Getenv("ARGOCD_NAMESPACE")
    if argoNS == "" {
        argoNS = "argocd"
    }
    gc, _ := strconv.ParseBool(os.Getenv("ENABLE_GARBAGE_COLLECTION"))
    
    return Config{
        ArgoNamespace:           argoNS,
        EnableGarbageCollection: gc,
    }
}
```

**Adopt for cnpg2monitor:**
```go
func LoadConfigFromEnv() Config {
    return Config{
        MonitoringNamespace: getEnvOrDefault("MONITORING_NAMESPACE", "zero-ops-system"),
        EnableEventEmission: parseBoolEnv("ENABLE_EVENT_EMISSION", true),
        TopologyLabelPrefix: getEnvOrDefault("TOPOLOGY_LABEL_PREFIX", "zero-ops.io/"),
    }
}
```

---

### P3.2: Namespace Filtering
**Pattern:** Allow restricting operator to specific namespaces

```go
type Config struct {
    AllowedNamespaces []string
}

func (c *Config) IsNamespaceAllowed(namespace string) bool {
    if len(c.AllowedNamespaces) == 0 {
        return true  // Watch all namespaces
    }
    return slices.Contains(c.AllowedNamespaces, namespace)
}
```

**Adopt for cnpg2monitor:** Identical - useful for multi-tenant scenarios where operator should only watch specific namespaces.

---

## 4. Reconciliation Logic Patterns

### P4.1: Early Exit on Validation Failure
**Pattern:** Validate resource early and exit without requeue if invalid

```go
if !ValidateCapiNaming(req.NamespacedName) {
    return ctrl.Result{}, nil  // Don't requeue invalid resources
}
```

**Adopt for cnpg2monitor:**
```go
if !hasMonitoringLabel(cnpgCluster) {
    log.Info("CNPG Cluster missing monitoring label, skipping")
    return ctrl.Result{}, nil
}
```

---

### P4.2: Graceful Deletion Handling
**Pattern:** Handle resource deletion with garbage collection

```go
err := r.Get(ctx, req.NamespacedName, &capiSecret)
if err != nil {
    if client.IgnoreNotFound(err) != nil {
        return ctrl.Result{}, err
    }
    // Resource deleted - clean up dependent resources
    if r.Config.EnableGarbageCollection {
        if err := r.deleteArgoSecretByLabels(ctx, log, req.NamespacedName); err != nil {
            return ctrl.Result{}, err
        }
    }
    return ctrl.Result{RequeueAfter: r.SyncPeriod}, nil
}
```

**Adopt for cnpg2monitor:**
```go
err := r.Get(ctx, req.NamespacedName, &cnpgCluster)
if err != nil {
    if client.IgnoreNotFound(err) != nil {
        return ctrl.Result{}, err
    }
    // CNPG Cluster deleted - clean up PodMonitor
    if err := r.deletePodMonitor(ctx, log, req.NamespacedName); err != nil {
        return ctrl.Result{}, err
    }
    return ctrl.Result{RequeueAfter: r.SyncPeriod}, nil
}
```

---

### P4.3: Ownership Validation
**Pattern:** Check if resource is managed by this operator before modifying

```go
func ValidateObjectOwner(s corev1.Secret) error {
    if s.ObjectMeta.Labels["capi-to-argocd/owned"] != "true" {
        return errors.New("not owned by CACO")
    }
    return nil
}
```

**Adopt for cnpg2monitor:**
```go
func ValidateObjectOwner(pm monitoringv1.PodMonitor) error {
    if pm.ObjectMeta.Labels["cnpg2monitor/owned"] != "true" {
        return errors.New("not owned by cnpg2monitor")
    }
    return nil
}
```

**Rationale:** Prevents operator from modifying manually-created PodMonitors.

---

### P4.4: Incremental Update Detection
**Pattern:** Only update if resource has actually changed

```go
changed := false
if !bytes.Equal(existingSecret.Data["name"], []byte(argoCluster.ClusterName)) {
    existingSecret.Data["name"] = []byte(argoCluster.ClusterName)
    changed = true
}
if changed {
    if err := r.Update(ctx, &existingSecret); err != nil {
        return ctrl.Result{}, err
    }
    secretsUpdatedTotal.Inc()
}
```

**Adopt for cnpg2monitor:** Identical pattern - reduces unnecessary K8s API writes and metric noise.

---

### P4.5: Label Synchronization
**Pattern:** Sync labels from source resource to target resource

```go
// Remove stale labels
for k := range existingSecret.Labels {
    if strings.HasPrefix(k, clusterTakenFromClusterKey) {
        key := strings.Split(k, clusterTakenFromClusterKey)[1]
        if !slices.Contains(argoSecretTakenAlongLabels, key) {
            delete(existingSecret.Labels, k)
            changed = true
        }
    }
}
// Add/update current labels
for k, v := range argoCluster.TakeAlongLabels {
    if val, ok := existingSecret.Labels[k]; ok {
        if val != v {
            existingSecret.Labels[k] = v
            changed = true
        }
    } else {
        existingSecret.Labels[k] = v
        changed = true
    }
}
```

**Adopt for cnpg2monitor:** Use for syncing topology labels from Namespace to PodMonitor relabelings.

---

## 5. Metrics & Observability Patterns

### P5.1: Prometheus Metrics Registration
**Pattern:** Define and register custom metrics in init()

```go
var (
    secretsCreatedTotal = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "caco_argocd_secrets_created_total",
        Help: "Total number of ArgoCD cluster secrets created",
    })
    secretsUpdatedTotal = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "caco_argocd_secrets_updated_total",
        Help: "Total number of ArgoCD cluster secrets updated",
    })
)

func init() {
    metrics.Registry.MustRegister(
        secretsCreatedTotal,
        secretsUpdatedTotal,
    )
}
```

**Adopt for cnpg2monitor:**
```go
var (
    podMonitorsCreatedTotal = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "cnpg2monitor_podmonitors_created_total",
        Help: "Total number of PodMonitors created",
    })
    podMonitorsUpdatedTotal = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "cnpg2monitor_podmonitors_updated_total",
        Help: "Total number of PodMonitors updated",
    })
    eventsEmittedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
        Name: "cnpg2monitor_events_emitted_total",
        Help: "Total number of K8s Events emitted",
    }, []string{"reason"})  // reason: CNPGScaled, CNPGConfigChanged, etc.
)
```

---

### P5.2: Structured Logging with Context
**Pattern:** Add context to logs for traceability

```go
log := r.Log.WithValues("secret", req.NamespacedName)
log.Info("Fetched CapiSecret")
log.Error(err, "Failed to unmarshal CapiCluster")
```

**Adopt for cnpg2monitor:**
```go
log := r.Log.WithValues("cnpgCluster", req.NamespacedName)
log.Info("Reconciling CNPG Cluster")
log.V(1).Info("Checking topology labels", "labels", topologyLabels)  // Debug-level log
```

---

## 6. RBAC & Security Patterns

### P6.1: Kubebuilder RBAC Markers
**Pattern:** Use kubebuilder markers for automatic RBAC generation

```go
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=secrets/status,verbs=get;update;patch
```

**Adopt for cnpg2monitor:**
```go
// +kubebuilder:rbac:groups=postgresql.cnpg.io,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=podmonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
```

---

### P6.2: Least Privilege Principle
**Pattern:** Only request permissions actually needed

**capi2argo:** Only watches Secrets, creates Secrets in ArgoCD namespace  
**cnpg2monitor:** Only watches CNPG Clusters, reads Namespaces, creates PodMonitors, emits Events

**Anti-pattern to avoid:** Requesting cluster-admin or wildcard permissions.

---

## 7. Testing Patterns

### P7.1: Test Helpers for Resource Creation
**Pattern:** Create reusable test helpers

```go
// From testhelpers_test.go
func createTestSecret(name, namespace string) *corev1.Secret {
    return &corev1.Secret{
        ObjectMeta: metav1.ObjectMeta{
            Name:      name,
            Namespace: namespace,
        },
        Type: CapiClusterSecretType,
        Data: map[string][]byte{
            "value": []byte(testKubeconfig),
        },
    }
}
```

**Adopt for cnpg2monitor:**
```go
func createTestCNPGCluster(name, namespace string) *cnpgv1.Cluster {
    return &cnpgv1.Cluster{
        ObjectMeta: metav1.ObjectMeta{
            Name:      name,
            Namespace: namespace,
            Labels: map[string]string{
                "zero-ops.io/monitored": "true",
            },
        },
        Spec: cnpgv1.ClusterSpec{
            Instances: 3,
        },
    }
}
```

---

## Patterns NOT to Adopt

### N1: Secret-Based Resource Watching
**capi2argo pattern:** Watches Secrets with specific naming convention  
**cnpg2monitor:** Watches CNPG Cluster CRDs directly (cleaner, more idiomatic)

### N2: Complex Label Take-Along Logic
**capi2argo pattern:** `take-along-label.capi-to-argocd.*` prefix for label propagation  
**cnpg2monitor:** Simpler - read topology labels from Namespace, inject into PodMonitor relabelings

### N3: Dual Secret Type Support
**capi2argo pattern:** Supports both `cluster.x-k8s.io/secret` and `Opaque` types  
**cnpg2monitor:** Only needs to support CNPG Cluster CRD (no type ambiguity)

---

## Summary: Key Patterns to Adopt

1. **Main Entrypoint:** Flag-based config, Zap logging, health probes, scheme registration
2. **Controller:** Embedded client, predicate filtering, requeue with SyncPeriod, EventRecorder
3. **Configuration:** Env var loading, namespace filtering, sensible defaults
4. **Reconciliation:** Early validation exit, graceful deletion, ownership checks, incremental updates
5. **Metrics:** Prometheus counters for create/update/delete, structured logging
6. **RBAC:** Kubebuilder markers, least privilege
7. **Testing:** Reusable test helpers

**Next Step:** Use these patterns to scaffold cnpg2monitor operator structure.


---

## Additional Patterns from postgres-operator

### P8. Signal Handling & Graceful Shutdown

**Pattern:** Use OS signal handling for graceful shutdown

```go
// postgres-operator main.go
sigs := make(chan os.Signal, 1)
stop := make(chan struct{})
signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)

wg := &sync.WaitGroup{}
c.Run(stop, wg)

sig := <-sigs
log.Printf("Shutting down... %+v", sig)
close(stop)  // Tell goroutines to stop
wg.Wait()    // Wait for all to be stopped
```

**Adopt for cnpg2monitor:**
```go
func main() {
    // ... setup code ...
    
    stopCh := ctrl.SetupSignalHandler()  // controller-runtime provides this
    
    if err := mgr.Start(stopCh); err != nil {
        setupLog.Error(err, "problem running manager")
        os.Exit(1)
    }
}
```

**Note:** controller-runtime's `SetupSignalHandler()` already handles SIGTERM/SIGINT gracefully. No custom signal handling needed.

---

### P9. EventRecorder Integration

**Pattern:** Use Kubernetes EventRecorder for emitting events

```go
// postgres-operator controller.go
eventBroadcaster := record.NewBroadcaster()
scheme := scheme.Scheme
acidv1.AddToScheme(scheme)
recorder := eventBroadcaster.NewRecorder(scheme, v1.EventSource{Component: myComponentName})

// Start recording to K8s API
eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{
    Interface: c.KubeClient.EventsGetter.Events(""),
})
```

**Adopt for cnpg2monitor:**
```go
// In SetupWithManager
func (r *Cnpg2Monitor) SetupWithManager(mgr ctrl.Manager) error {
    // EventRecorder is automatically available from manager
    r.Recorder = mgr.GetEventRecorderFor("cnpg2monitor")
    
    return ctrl.NewControllerManagedBy(mgr).
        For(&cnpgv1.Cluster{}).
        Complete(r)
}

// In Reconcile
func (r *Cnpg2Monitor) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // Emit event when CNPG cluster scales
    r.Recorder.Event(&cnpgCluster, corev1.EventTypeNormal, "CNPGScaled", 
        fmt.Sprintf("Cluster scaled from %d to %d instances", oldReplicas, newReplicas))
    
    return ctrl.Result{}, nil
}
```

**Event Types to Emit (PRD Requirement):**
- `CNPGScaled` - Instance count changed
- `CNPGConfigChanged` - PostgreSQL parameters updated
- `CNPGStorageExpanded` - PVC size increased
- `CNPGBackupConfigured` - Backup settings changed

---

### P10. ConfigMap-Based Configuration

**Pattern:** Load operator configuration from ConfigMap with fallback to defaults

```go
// postgres-operator controller.go
func (c *Controller) initOperatorConfig() {
    configMapData := make(map[string]string)
    
    if c.config.ConfigMapName != (spec.NamespacedName{}) {
        configMap, err := c.KubeClient.ConfigMaps(c.config.ConfigMapName.Namespace).
            Get(context.TODO(), c.config.ConfigMapName.Name, metav1.GetOptions{})
        if err != nil {
            panic(err)
        }
        configMapData = configMap.Data
    } else {
        c.logger.Infoln("no ConfigMap specified. Loading default values")
    }
    
    c.opConfig = config.NewFromMap(configMapData)
}
```

**Adopt for cnpg2monitor:**
```go
// Support both env vars (Phase 2) and ConfigMap (future enhancement)
func LoadConfig() Config {
    cfg := LoadConfigFromEnv()  // Primary method
    
    // Optional: Override from ConfigMap if specified
    if configMapName := os.Getenv("CONFIG_MAP_NAME"); configMapName != "" {
        if cmData, err := loadConfigMap(configMapName); err == nil {
            cfg = mergeConfigMapData(cfg, cmData)
        }
    }
    
    return cfg
}
```

**Rationale:** Env vars are simpler for Phase 2. ConfigMap support can be added later for dynamic reconfiguration.

---

### P11. Namespace Watching Strategy

**Pattern:** Support watching all namespaces or specific namespace

```go
// postgres-operator controller.go
func (c *Controller) getEffectiveNamespace(namespaceFromEnvironment, namespaceFromConfigMap string) string {
    namespace := util.Coalesce(namespaceFromEnvironment, namespaceFromConfigMap, spec.GetOperatorNamespace())
    
    if namespace == "*" {
        namespace = v1.NamespaceAll
        c.logger.Infof("Listening to all namespaces")
    } else {
        if _, err := c.KubeClient.Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{}); err != nil {
            c.logger.Fatalf("Could not find the watched namespace %q", namespace)
        } else {
            c.logger.Infof("Listening to the specific namespace %q", namespace)
        }
    }
    
    return namespace
}
```

**Adopt for cnpg2monitor:**
```go
// In main.go
var watchNamespace string
flag.StringVar(&watchNamespace, "watch-namespace", "", 
    "Namespace to watch (empty = all namespaces)")

// In SetupWithManager
func (r *Cnpg2Monitor) SetupWithManager(mgr ctrl.Manager) error {
    builder := ctrl.NewControllerManagedBy(mgr).
        For(&cnpgv1.Cluster{})
    
    // If watchNamespace is set, filter by namespace in predicate
    if r.Config.WatchNamespace != "" {
        builder = builder.WithEventFilter(predicate.NewPredicateFuncs(func(obj client.Object) bool {
            return obj.GetNamespace() == r.Config.WatchNamespace
        }))
    }
    
    return builder.Complete(r)
}
```

---

### P12. Ownership Annotation Pattern

**Pattern:** Use annotations to determine controller ownership

```go
// postgres-operator controller.go
func (c *Controller) hasOwnership(postgresql *acidv1.Postgresql) bool {
    if postgresql.Annotations != nil {
        if owner, ok := postgresql.Annotations[constants.PostgresqlControllerAnnotationKey]; ok {
            return owner == c.controllerID
        }
    }
    return c.controllerID == ""
}
```

**Adopt for cnpg2monitor:**
```go
const (
    ControllerAnnotationKey = "cnpg2monitor.zero-ops.io/controller-id"
)

func (r *Cnpg2Monitor) hasOwnership(cnpgCluster *cnpgv1.Cluster) bool {
    if cnpgCluster.Annotations != nil {
        if owner, ok := cnpgCluster.Annotations[ControllerAnnotationKey]; ok {
            return owner == r.ControllerID
        }
    }
    // If no annotation, assume ownership (backward compatibility)
    return r.ControllerID == ""
}
```

**Use Case:** Multi-controller deployments where different operators manage different CNPG clusters.

---

### P13. REST Config QPS/Burst Tuning

**Pattern:** Configure Kubernetes API client rate limits

```go
// postgres-operator main.go
flag.IntVar(&config.KubeQPS, "kubeqps", 10, "Kubernetes api requests per second.")
flag.IntVar(&config.KubeBurst, "kubeburst", 20, "Kubernetes api requests burst limit.")

config.RestConfig.QPS = float32(config.KubeQPS)
config.RestConfig.Burst = config.KubeBurst
```

**Adopt for cnpg2monitor:**
```go
// In main.go
var (
    kubeQPS   int
    kubeBurst int
)

flag.IntVar(&kubeQPS, "kube-qps", 20, "Kubernetes API QPS limit")
flag.IntVar(&kubeBurst, "kube-burst", 30, "Kubernetes API burst limit")

// In manager setup
mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
    Scheme: scheme,
    // ... other options ...
})

// Adjust REST config
mgr.GetConfig().QPS = float32(kubeQPS)
mgr.GetConfig().Burst = kubeBurst
```

**Recommended Values for cnpg2monitor:**
- QPS: 20 (higher than postgres-operator since we're lightweight)
- Burst: 30 (allows handling spikes during cluster creation)

---

### P14. CRD Registration Pattern

**Pattern:** Optionally register CRDs at operator startup

```go
// postgres-operator controller.go
if c.opConfig.EnableCRDRegistration != nil && *c.opConfig.EnableCRDRegistration {
    if err := c.createPostgresCRD(); err != nil {
        c.logger.Fatalf("could not register Postgres CustomResourceDefinition: %v", err)
    }
}
```

**Adopt for cnpg2monitor:**
```go
// In main.go
var enableCRDRegistration bool
flag.BoolVar(&enableCRDRegistration, "enable-crd-registration", false, 
    "Register PodMonitor CRDs at startup")

// In initController
if enableCRDRegistration {
    if err := registerPodMonitorCRD(mgr.GetClient()); err != nil {
        setupLog.Error(err, "failed to register PodMonitor CRD")
        os.Exit(1)
    }
}
```

**Note:** For Phase 2, we'll apply PodMonitor CRDs manually. This flag is for future automation.

---

### P15. Multi-Line Config Logging

**Pattern:** Log multi-line configuration for debugging

```go
// postgres-operator controller.go
func logMultiLineConfig(log *logrus.Entry, config string) {
    lines := strings.Split(config, "\n")
    for _, l := range lines {
        log.Infof("%s", l)
    }
}

// Usage
logMultiLineConfig(c.logger, c.opConfig.MustMarshal())
```

**Adopt for cnpg2monitor:**
```go
func logConfig(log logr.Logger, cfg Config) {
    configJSON, _ := json.MarshalIndent(cfg, "", "  ")
    lines := strings.Split(string(configJSON), "\n")
    
    log.Info("Operator configuration:")
    for _, line := range lines {
        log.Info(line)
    }
}

// In main.go after config loading
logConfig(ctrl.Log.WithName("setup"), cfg)
```

---

### P16. Deprecation Warnings

**Pattern:** Warn users about deprecated configuration parameters

```go
// postgres-operator controller.go
func (c *Controller) warnOnDeprecatedOperatorParameters() {
    if c.opConfig.EnableLoadBalancer != nil {
        c.logger.Warningf("Operator configuration parameter 'enable_load_balancer' is deprecated. " +
            "Consider using 'enable_master_load_balancer' instead.")
    }
}
```

**Adopt for cnpg2monitor:**
```go
func (c *Config) warnOnDeprecatedParameters(log logr.Logger) {
    // Example: If we rename a config parameter in future
    if os.Getenv("OLD_PARAMETER_NAME") != "" {
        log.Info("Warning: OLD_PARAMETER_NAME is deprecated, use NEW_PARAMETER_NAME instead")
    }
}
```

**Use Case:** Smooth migration when config parameters change between versions.

---

## Patterns NOT to Adopt from postgres-operator

### N4: Custom Informer Setup
**postgres-operator pattern:** Manually creates SharedIndexInformers with custom ListWatch  
**cnpg2monitor:** Use controller-runtime's built-in informer management (simpler, less code)

### N5: Worker Queue Architecture
**postgres-operator pattern:** Custom FIFO queues per worker with manual event distribution  
**cnpg2monitor:** Use controller-runtime's built-in work queue (automatic, battle-tested)

### N6: Ring Logger
**postgres-operator pattern:** Custom ring buffer logger for per-cluster logs  
**cnpg2monitor:** Use standard structured logging (simpler, integrates with log aggregation)

### N7: API Server
**postgres-operator pattern:** Embedded HTTP API server for operator status  
**cnpg2monitor:** Expose metrics via Prometheus only (simpler, standard observability)

### N8: Teams API Integration
**postgres-operator pattern:** External Teams API for RBAC  
**cnpg2monitor:** Not needed - we only read namespace labels

---

## Summary: Additional Patterns to Adopt

**From postgres-operator:**
1. **Signal Handling:** Use controller-runtime's SetupSignalHandler (already built-in)
2. **EventRecorder:** Emit K8s Events for CNPG lifecycle changes (PRD requirement)
3. **ConfigMap Config:** Support ConfigMap-based config (future enhancement)
4. **Namespace Watching:** Support all-namespaces or single-namespace mode
5. **Ownership Annotations:** Use annotations for multi-controller scenarios
6. **REST Config Tuning:** Configure QPS/Burst for K8s API client
7. **CRD Registration:** Optional CRD registration at startup (future)
8. **Config Logging:** Log full config at startup for debugging
9. **Deprecation Warnings:** Warn on deprecated parameters

**Patterns explicitly NOT adopted:**
- Custom informer setup (use controller-runtime)
- Worker queue architecture (use controller-runtime)
- Ring logger (use structured logging)
- Embedded API server (use Prometheus metrics)
- Teams API integration (not needed)

---

## Final Pattern Checklist for cnpg2monitor

### Must Have (Phase 2)
- [x] Flag-based configuration (P1.1)
- [x] Zap structured logging (P1.2)
- [x] Health/readiness probes (P1.3)
- [x] Scheme registration for CNPG + PodMonitor CRDs (P1.4)
- [x] Embedded client in controller struct (P2.1)
- [x] EventRecorder for K8s event emission (P2.1, P9)
- [x] Predicate-based event filtering (P2.2)
- [x] Requeue with SyncPeriod (P2.3)
- [x] Env var configuration with defaults (P3.1)
- [x] Early validation exit (P4.1)
- [x] Graceful deletion handling (P4.2)
- [x] Ownership validation (P4.3)
- [x] Incremental update detection (P4.4)
- [x] Prometheus metrics (P5.1)
- [x] Structured logging with context (P5.2)
- [x] Kubebuilder RBAC markers (P6.1)
- [x] Least privilege RBAC (P6.2)

### Should Have (Phase 2 or 2.5)
- [ ] Namespace filtering (P3.2, P11)
- [ ] Label synchronization (P4.5)
- [ ] REST config QPS/Burst tuning (P13)
- [ ] Config logging at startup (P15)

### Nice to Have (Future)
- [ ] ConfigMap-based configuration (P10)
- [ ] Ownership annotations for multi-controller (P12)
- [ ] CRD registration at startup (P14)
- [ ] Deprecation warnings (P16)

**Total Patterns Documented:** 16 from capi2argo + 9 from postgres-operator = 25 patterns  
**Patterns to Adopt:** 16 must-have + 4 should-have = 20 patterns  
**Patterns to Avoid:** 8 anti-patterns documented

---

**Document Status:** COMPLETE  
**Next Step:** Use these patterns to create cnpg2monitor operator scaffold and implementation spec.

---

## Additional Patterns from prometheus-operator

### P17. ReconciliationTracker Pattern

**Pattern:** Track reconciliation status per object with thread-safe access

```go
// prometheus-operator operator.go
type ReconciliationTracker struct {
    once sync.Once
    mtx  sync.RWMutex
    statusByObject map[string]ReconciliationStatus
    refTracker     map[string]ReferenceTracker
}

type ReconciliationStatus struct {
    err     error
    reason  string
    message string
}

func (rt *ReconciliationTracker) SetStatus(key string, err error) {
    rt.init()
    rt.mtx.Lock()
    defer rt.mtx.Unlock()
    
    rs := rt.statusByObject[key]
    rs.err = err
    rt.statusByObject[key] = rs
}

func (rt *ReconciliationTracker) GetCondition(k string, gen int64) monitoringv1.Condition {
    condition := monitoringv1.Condition{
        Type:   monitoringv1.Reconciled,
        Status: monitoringv1.ConditionTrue,
        LastTransitionTime: metav1.Time{Time: time.Now().UTC()},
        ObservedGeneration: gen,
    }
    
    reconciliationStatus, found := rt.getStatus(k)
    if !found {
        condition.Status = monitoringv1.ConditionUnknown
        condition.Reason = "NotFound"
    } else if !reconciliationStatus.Ok() {
        condition.Status = monitoringv1.ConditionFalse
        condition.Reason = reconciliationStatus.Reason()
        condition.Message = reconciliationStatus.Message()
    }
    
    return condition
}
```

**Adopt for cnpg2monitor:**
```go
type Cnpg2Monitor struct {
    client.Client
    Log              logr.Logger
    Scheme           *runtime.Scheme
    Recorder         record.EventRecorder
    Reconciliations  *ReconciliationTracker  // Track status per CNPG cluster
}

// In Reconcile
func (r *Cnpg2Monitor) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    r.Reconciliations.ResetStatus(req.String())
    err := r.reconcile(ctx, req)
    r.Reconciliations.SetStatus(req.String(), err)
    return ctrl.Result{}, err
}
```

**Use Case:** Provides detailed reconciliation status for debugging and status reporting.

---

### P18. Instrumented ListerWatcher Pattern

**Pattern:** Wrap Kubernetes ListerWatcher with Prometheus metrics

```go
// prometheus-operator operator.go
type instrumentedListerWatcher struct {
    next        cache.ListerWatcher
    listTotal   prometheus.Counter
    listFailed  prometheus.Counter
    watchTotal  prometheus.Counter
    watchFailed prometheus.Counter
}

func (m *Metrics) NewInstrumentedListerWatcher(lw cache.ListerWatcher) cache.ListerWatcher {
    return &instrumentedListerWatcher{
        next:        lw,
        listTotal:   m.listCounter,
        listFailed:  m.listFailedCounter,
        watchTotal:  m.watchCounter,
        watchFailed: m.watchFailedCounter,
    }
}

func (i *instrumentedListerWatcher) List(options metav1.ListOptions) (runtime.Object, error) {
    i.listTotal.Inc()
    ret, err := i.next.List(options)
    if err != nil {
        i.listFailed.Inc()
    }
    return ret, err
}
```

**Adopt for cnpg2monitor:**
```go
var (
    listOperationsTotal = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "cnpg2monitor_list_operations_total",
        Help: "Total number of list operations",
    })
    listOperationsFailed = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "cnpg2monitor_list_operations_failed_total",
        Help: "Total number of failed list operations",
    })
    watchOperationsTotal = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "cnpg2monitor_watch_operations_total",
        Help: "Total number of watch operations",
    })
)
```

**Note:** controller-runtime handles this automatically. Only needed if using custom informers (which we're not).

---

### P19. WaitForNamedCacheSync with Timeout Pattern

**Pattern:** Wait for cache sync with timeout and periodic warnings

```go
// prometheus-operator operator.go
func WaitForNamedCacheSync(ctx context.Context, controllerName string, logger *slog.Logger, inf cache.SharedIndexInformer) bool {
    ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
    defer cancel()
    
    t := time.NewTicker(time.Minute)
    defer t.Stop()
    
    go func() {
        for {
            select {
            case <-t.C:
                logger.Warn("cache sync not yet completed")
            case <-ctx.Done():
                return
            }
        }
    }()
    
    ok := cache.WaitForNamedCacheSync(controllerName, ctx.Done(), inf.HasSynced)
    if !ok {
        logger.Error("failed to sync cache")
    } else {
        logger.Debug("successfully synced cache")
    }
    
    return ok
}
```

**Adopt for cnpg2monitor:**
```go
// controller-runtime handles cache sync automatically in mgr.Start()
// No custom implementation needed - just log when ready
func (r *Cnpg2Monitor) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&cnpgv1.Cluster{}).
        Complete(r)
}
```

**Note:** controller-runtime's manager handles cache sync. Pattern useful for understanding but not needed.

---

### P20. Metrics with Resource State Tracking Pattern

**Pattern:** Track selected vs rejected resources per object

```go
// prometheus-operator operator.go
type Metrics struct {
    reg      prometheus.Registerer
    mtx      sync.RWMutex
    resources map[resourceKey]map[string]int
}

type resourceKey struct {
    resource string
    state    resourceState  // selected or rejected
}

func (m *Metrics) SetSelectedResources(objKey, resource string, v int) {
    m.setResources(objKey, resourceKey{resource: resource, state: selected}, v)
}

func (m *Metrics) SetRejectedResources(objKey, resource string, v int) {
    m.setResources(objKey, resourceKey{resource: resource, state: rejected}, v)
}

// Prometheus metric: prometheus_operator_managed_resources{resource="ServiceMonitor",state="selected"}
```

**Adopt for cnpg2monitor:**
```go
var (
    managedPodMonitors = prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "cnpg2monitor_managed_podmonitors",
        Help: "Number of PodMonitors managed by cnpg2monitor",
    }, []string{"state"})  // state: created, updated, deleted
)

// In Reconcile
func (r *Cnpg2Monitor) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // After creating PodMonitor
    managedPodMonitors.WithLabelValues("created").Inc()
}
```

---

### P21. Namespace Label Selector Matching Pattern

**Pattern:** Match namespace labels against label selectors for cross-namespace watching

```go
// prometheus-operator operator.go
func (c *Operator) enqueueForNamespace(store cache.Store, nsName string) {
    nsObject, found, err := store.GetByKey(nsName)
    if err != nil || !found {
        return
    }
    ns := nsObject.(*corev1.Namespace)
    
    err = c.promInfs.ListAll(labels.Everything(), func(obj any) {
        p := obj.(*monitoringv1.Prometheus)
        
        // Check if Prometheus selects ServiceMonitors in this namespace
        smNSSelector, err := metav1.LabelSelectorAsSelector(p.Spec.ServiceMonitorNamespaceSelector)
        if err != nil {
            return
        }
        
        if smNSSelector.Matches(labels.Set(ns.Labels)) {
            c.rr.EnqueueForReconciliation(p)
        }
    })
}
```

**Adopt for cnpg2monitor:**
```go
// In Reconcile - read namespace labels
func (r *Cnpg2Monitor) getTopologyLabels(ctx context.Context, namespace string) (map[string]string, error) {
    ns := &corev1.Namespace{}
    if err := r.Get(ctx, types.NamespacedName{Name: namespace}, ns); err != nil {
        return nil, err
    }
    
    topologyLabels := make(map[string]string)
    for k, v := range ns.Labels {
        if strings.HasPrefix(k, "zero-ops.io/") {
            // Extract label name after prefix
            labelName := strings.TrimPrefix(k, "zero-ops.io/")
            topologyLabels[labelName] = v
        }
    }
    
    return topologyLabels, nil
}
```

---

### P22. Controller Option Pattern

**Pattern:** Use functional options for controller configuration

```go
// prometheus-operator operator.go
type ControllerOption func(*Operator)

func WithEndpointSlice() ControllerOption {
    return func(o *Operator) {
        o.endpointSliceSupported = true
    }
}

func WithScrapeConfig() ControllerOption {
    return func(o *Operator) {
        o.scrapeConfigSupported = true
    }
}

func New(ctx context.Context, restConfig *rest.Config, c operator.Config, logger *slog.Logger, r prometheus.Registerer, opts ...ControllerOption) (*Operator, error) {
    o := &Operator{
        kclient: client,
        logger:  logger,
    }
    for _, opt := range opts {
        opt(o)
    }
    return o, nil
}
```

**Adopt for cnpg2monitor:**
```go
type ControllerOption func(*Cnpg2Monitor)

func WithEventEmission(enabled bool) ControllerOption {
    return func(c *Cnpg2Monitor) {
        c.EnableEventEmission = enabled
    }
}

func WithTopologyLabelPrefix(prefix string) ControllerOption {
    return func(c *Cnpg2Monitor) {
        c.TopologyLabelPrefix = prefix
    }
}

// In SetupWithManager
func NewCnpg2Monitor(mgr ctrl.Manager, opts ...ControllerOption) (*Cnpg2Monitor, error) {
    c := &Cnpg2Monitor{
        Client:   mgr.GetClient(),
        Scheme:   mgr.GetScheme(),
        Recorder: mgr.GetEventRecorderFor("cnpg2monitor"),
    }
    for _, opt := range opts {
        opt(c)
    }
    return c, nil
}
```

---

### P23. Resync Period Constant Pattern

**Pattern:** Use consistent resync period across all informers

```go
// prometheus-operator operator.go
const (
    resyncPeriod   = 5 * time.Minute
    controllerName = "prometheus-controller"
)

// Used when creating informers
informers.NewMonitoringInformerFactories(
    c.Namespaces.AllowList,
    c.Namespaces.DenyList,
    mclient,
    resyncPeriod,  // Consistent across all informers
    nil,
)
```

**Adopt for cnpg2monitor:**
```go
const (
    resyncPeriod   = 30 * time.Second  // Faster than prometheus-operator
    controllerName = "cnpg2monitor"
)

// In SetupWithManager - controller-runtime uses default 10h resync
// Override if needed via manager options
```

**Note:** controller-runtime defaults to 10h resync. For cnpg2monitor, default is fine since we rely on watch events.

---

### P24. Status Reporter Pattern

**Pattern:** Separate status reporting logic from reconciliation logic

```go
// prometheus-operator operator.go
type StatusReporter struct {
    Kclient         kubernetes.Interface
    Reconciliations *ReconciliationTracker
    SsetInfs        *informers.ForResource
    Rr              *ResourceReconciler
}

func (c *Operator) RefreshStatusFor(o metav1.Object) {
    c.rr.EnqueueForStatus(o)
}

// Separate goroutine polls for status updates
go operator.StatusPoller(ctx, c)
```

**Adopt for cnpg2monitor:**
```go
// For Phase 2, status updates are simple - just emit events
// No need for complex status reporter pattern

// In Reconcile
func (r *Cnpg2Monitor) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // ... reconciliation logic ...
    
    // Emit event on success
    r.Recorder.Event(&cnpgCluster, corev1.EventTypeNormal, "PodMonitorCreated", 
        fmt.Sprintf("Created PodMonitor %s", podMonitorName))
    
    return ctrl.Result{}, nil
}
```

**Note:** Status subresource updates deferred to future phases. Events sufficient for Phase 2.

---

### P25. Sync Method with Status Tracking Pattern

**Pattern:** Wrap reconciliation logic with status tracking

```go
// prometheus-operator operator.go
func (c *Operator) Sync(ctx context.Context, key string) error {
    c.reconciliations.ResetStatus(key)
    err := c.sync(ctx, key)
    c.reconciliations.SetStatus(key, err)
    return err
}

func (c *Operator) sync(ctx context.Context, key string) error {
    // Actual reconciliation logic
    p, err := operator.GetObjectFromKey[*monitoringv1.Prometheus](c.promInfs, key)
    if err != nil {
        return err
    }
    
    if p == nil {
        c.reconciliations.ForgetObject(key)
        return nil
    }
    
    // ... reconciliation logic ...
}
```

**Adopt for cnpg2monitor:**
```go
// controller-runtime's Reconcile method already provides this pattern
func (r *Cnpg2Monitor) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := r.Log.WithValues("cnpgCluster", req.NamespacedName)
    
    // Get CNPG Cluster
    cnpgCluster := &cnpgv1.Cluster{}
    err := r.Get(ctx, req.NamespacedName, cnpgCluster)
    if err != nil {
        if client.IgnoreNotFound(err) != nil {
            return ctrl.Result{}, err
        }
        // Object deleted - clean up PodMonitor
        return r.handleDeletion(ctx, req.NamespacedName)
    }
    
    // Reconciliation logic
    return r.reconcile(ctx, cnpgCluster)
}
```

---

## Summary: Patterns from prometheus-operator

**Patterns to Adopt:**
1. **P17: ReconciliationTracker** - Track reconciliation status per object (optional for Phase 2)
2. **P18: Instrumented ListerWatcher** - Not needed (controller-runtime handles this)
3. **P19: WaitForNamedCacheSync** - Not needed (controller-runtime handles this)
4. **P20: Metrics with State Tracking** - Track PodMonitor lifecycle metrics
5. **P21: Namespace Label Selector** - Read topology labels from namespace
6. **P22: Controller Option Pattern** - Functional options for configuration
7. **P23: Resync Period** - Use controller-runtime defaults
8. **P24: Status Reporter** - Defer to future phases (use events for Phase 2)
9. **P25: Sync with Status Tracking** - Already provided by controller-runtime

**Must Adopt for Phase 2:**
- P21: Namespace label reading for topology labels
- P20: Basic metrics for PodMonitor lifecycle

**Should Adopt (Phase 2 or 2.5):**
- P22: Controller option pattern for flexibility
- P17: ReconciliationTracker for debugging

**Nice to Have (Future):**
- P24: Status reporter for status subresource updates

---

## Final Pattern Summary

**Total Patterns Documented:**
- capi2argo: 16 patterns
- postgres-operator: 9 patterns  
- prometheus-operator: 9 patterns
- **Total: 34 patterns**

**Patterns to Adopt in Phase 2:**
- Must-have: 18 patterns (P1.1-P1.4, P2.1-P2.3, P3.1, P4.1-P4.4, P5.1-P5.2, P6.1-P6.2, P9, P21)
- Should-have: 6 patterns (P3.2, P4.5, P11, P13, P15, P22)
- Nice-to-have: 5 patterns (P10, P12, P14, P16, P17)

**Patterns to Avoid:**
- N1-N8: Custom informers, worker queues, ring logger, API server, Teams API, etc.

---

## Grafana Alloy Integration Patterns (from Official Documentation)

**Source:** https://grafana.com/docs/alloy/latest/set-up/migrate/from-operator/  
**Date Reviewed:** 2026-03-10

### Key Findings for cnpg2monitor

#### 1. PodMonitor Native Support in Alloy

**Critical Validation:** Grafana Alloy natively supports PodMonitor CRDs via the `prometheus.operator.podmonitors` component.

```alloy
prometheus.operator.podmonitors "primary" {
    forward_to = [prometheus.remote_write.primary.receiver]
    
    // Selector to filter which PodMonitors to discover
    selector {
        match_labels = {instance = "primary"}
    }
}
```

**Implications for cnpg2monitor:**
- ✅ PodMonitors created by cnpg2monitor will be automatically discovered by Alloy
- ✅ No custom scrape configuration needed
- ✅ Alloy watches PodMonitor CRDs and dynamically updates scrape targets
- ✅ Supports label selectors for filtering PodMonitors

#### 2. Alloy Deployment Topology

**Recommended for Zero-Ops:**
```yaml
# StatefulSet mode with clustering for distributed scraping
controller:
  type: 'statefulset'
  replicas: 2
alloy:
  clustering:
    enabled: true
```

**Implications:**
- Alloy runs as StatefulSet (not DaemonSet) for metrics collection
- Built-in clustering distributes scrape load across replicas
- Each Alloy pod discovers all PodMonitors and coordinates scraping

#### 3. PodMonitor Discovery Scope

**From Documentation:**
> "This configuration discovers all PodMonitor, ServiceMonitor, ScrapeConfig, and Probe resources in your cluster that match the label selector."

**Implications for cnpg2monitor:**
- Alloy can discover PodMonitors cluster-wide or namespace-scoped
- Label selectors on Alloy side filter which PodMonitors to use
- cnpg2monitor should add consistent labels to PodMonitors for filtering

**Recommended PodMonitor Labels:**
```yaml
metadata:
  labels:
    app.kubernetes.io/managed-by: cnpg2monitor
    zero-ops.io/component: database
    zero-ops.io/monitored: "true"
```

#### 4. Relabeling in PodMonitors

**Pattern:** PodMonitors support `relabelings` array for metric label manipulation

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
spec:
  podMetricsEndpoints:
  - port: metrics
    relabelings:
    - targetLabel: cluster_id
      replacement: "mothership-01"
    - targetLabel: region
      replacement: "fsn1"
```

**Validation:** This is the correct approach for injecting topology labels (confirmed by Alloy docs).

#### 5. Remote Write Configuration

**Alloy Pattern:**
```alloy
prometheus.remote_write "primary" {
    endpoint {
        url = "https://<PROMETHEUS_URL>/api/v1/push"
        basic_auth {
            username = convert.nonsensitive(remote.kubernetes.secret.credentials.data["username"])
            password = remote.kubernetes.secret.credentials.data["password"]
        }
    }
}
```

**Implications:**
- Alloy handles remote_write to VictoriaMetrics
- cnpg2monitor does NOT need to configure remote_write
- Alloy configuration is separate from PodMonitor creation

#### 6. Dynamic Scrape Target Updates

**From Documentation:**
> "Alloy has components that can scrape Pod logs directly from the Kubernetes API"

**Implications:**
- Alloy watches Kubernetes API for PodMonitor changes
- When cnpg2monitor creates/updates/deletes a PodMonitor, Alloy reacts immediately
- No Alloy restart required when PodMonitors change

---

## Validation of PRD v2.0 Architecture

### ✅ Confirmed Correct Patterns

1. **PodMonitor CRD Usage:** PRD correctly specifies using `monitoring.coreos.com/v1 PodMonitor`
2. **Relabeling for Topology Labels:** PRD correctly shows injecting labels via `relabelings` array
3. **Edge Collection Model:** PRD correctly states Alloy scrapes at edge and ships to VictoriaMetrics
4. **Zero-Touch Discovery:** PRD correctly assumes Alloy auto-discovers PodMonitors

### ⚠️ Clarifications Needed

1. **Alloy Deployment Timing:**
   - PRD states PodMonitors will be "dormant" until Phase 4
   - **Confirmed:** This is correct - PodMonitors are inert until Alloy is deployed
   - Alloy must be configured with `prometheus.operator.podmonitors` component

2. **PodMonitor Selector in Alloy:**
   - Alloy can filter PodMonitors by label selector
   - **Recommendation:** cnpg2monitor should add `zero-ops.io/monitored: "true"` label to all PodMonitors
   - Alloy config should use: `selector { match_labels = {zero-ops.io/monitored = "true"} }`

3. **Namespace Scope:**
   - Alloy can discover PodMonitors cluster-wide or namespace-scoped
   - **Recommendation:** Use cluster-wide discovery with label filtering (more flexible)

---

## Updated Pattern: P26. PodMonitor Generation with Alloy Compatibility

**Pattern:** Generate PodMonitors that Alloy can discover and scrape

```go
func (r *Cnpg2Monitor) generatePodMonitor(cnpgCluster *cnpgv1.Cluster, topologyLabels map[string]string) *monitoringv1.PodMonitor {
    relabelings := []monitoringv1.RelabelConfig{}
    
    // Inject topology labels as static relabelings
    for k, v := range topologyLabels {
        relabelings = append(relabelings, monitoringv1.RelabelConfig{
            TargetLabel: k,
            Replacement: v,
        })
    }
    
    return &monitoringv1.PodMonitor{
        ObjectMeta: metav1.ObjectMeta{
            Name:      fmt.Sprintf("%s-monitor", cnpgCluster.Name),
            Namespace: cnpgCluster.Namespace,
            Labels: map[string]string{
                "app.kubernetes.io/managed-by": "cnpg2monitor",
                "zero-ops.io/monitored":        "true",  // For Alloy selector
                "zero-ops.io/component":        "database",
            },
            OwnerReferences: []metav1.OwnerReference{
                *metav1.NewControllerRef(cnpgCluster, cnpgv1.GroupVersion.WithKind("Cluster")),
            },
        },
        Spec: monitoringv1.PodMonitorSpec{
            Selector: metav1.LabelSelector{
                MatchLabels: map[string]string{
                    "postgresql.cnpg.io/cluster": cnpgCluster.Name,
                },
            },
            PodMetricsEndpoints: []monitoringv1.PodMetricsEndpoint{
                {
                    Port:         "metrics",  // CNPG exposes metrics on port named "metrics" (9187)
                    Interval:     "15s",      // Scrape every 15 seconds
                    Relabelings:  relabelings,
                },
            },
        },
    }
}
```

**Key Elements:**
1. **OwnerReference:** Ensures PodMonitor is deleted when CNPG Cluster is deleted
2. **Selector:** Matches CNPG pods via `postgresql.cnpg.io/cluster` label
3. **Port:** Uses named port "metrics" (CNPG standard)
4. **Relabelings:** Injects topology labels from namespace
5. **Labels:** Adds `zero-ops.io/monitored: "true"` for Alloy filtering

---

## Phase 4 Alloy Configuration (for Reference)

**When deploying Alloy in Phase 4, use this configuration:**

```alloy
// Discover all PodMonitors managed by cnpg2monitor
prometheus.operator.podmonitors "cnpg" {
    forward_to = [prometheus.remote_write.victoriametrics.receiver]
    
    // Only discover PodMonitors created by cnpg2monitor
    selector {
        match_labels = {
            "zero-ops.io/monitored" = "true"
        }
    }
}

// Remote write to VictoriaMetrics
prometheus.remote_write "victoriametrics" {
    endpoint {
        url = "https://<VICTORIAMETRICS_URL>/api/v1/write"
        // Add mTLS configuration here
    }
}
```

**Deployment:**
```bash
helm upgrade alloy-metrics grafana/alloy -i -n zero-ops-system \
  -f values.yaml \
  --set-file alloy.configMap.content=config.alloy
```

---

## Updated Assumptions Document Reference

**Add to `.kiro/specs/tenant-onboarding/cnpg-provisioning/prd/assumptions-and-future-work.md`:**

### A13. Alloy PodMonitor Discovery Configuration

**Assumption:** Alloy will be configured with `prometheus.operator.podmonitors` component and label selector `zero-ops.io/monitored: "true"`.

**Validation:** Confirmed by Grafana Alloy official documentation (2026-03-10).

**Future Work (Phase 4):**
- Deploy Alloy as StatefulSet with clustering enabled
- Configure `prometheus.operator.podmonitors` component
- Set label selector to match cnpg2monitor-created PodMonitors
- Configure remote_write to VictoriaMetrics with mTLS

**Risk:** If Alloy is deployed without the PodMonitor discovery component, metrics will not be collected.

**Mitigation:** Document Alloy configuration requirements in Phase 4 deployment guide.

---

**Document Status:** COMPLETE + VALIDATED  
**Validation Source:** Grafana Alloy Official Documentation  
**Next Step:** Create cnpg2monitor operator requirements.md, design.md, and tasks.md using these 34 documented patterns + Alloy integration validation.
