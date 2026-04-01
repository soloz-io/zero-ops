# Karmada Features Catalog

**Source**: Official Karmada codebase analysis (archived/cluster-manager/karmada/)
**Analysis Date**: 2026-04-01
**Karmada Version**: v1.17 (latest analyzed)

## Core Multi-Cluster Orchestration Features

### 1. Resource Propagation & Scheduling

#### 1.1 PropagationPolicy (Namespace-scoped)
- **Purpose**: Propagates resources within a namespace to one or more clusters
- **API**: `policy.karmada.io/v1alpha1/PropagationPolicy`
- **Key Capabilities**:
  - Resource selection via APIVersion, Kind, Name, Namespace, LabelSelector
  - Dependency propagation (`PropagateDeps`) - automatically propagate referenced ConfigMaps/Secrets
  - Priority-based policy resolution (0-N, higher wins)
  - Preemption control (Always/Never)
  - Conflict resolution (Abort/Overwrite)
  - Activation preference (Lazy activation for gradual rollout)
  - Suspension controls (suspend dispatching globally or per-cluster)
  - Preserve resources on deletion flag

#### 1.2 ClusterPropagationPolicy (Cluster-scoped)
- **Purpose**: Propagates cluster-level resources and resources in any namespace
- **API**: `policy.karmada.io/v1alpha1/ClusterPropagationPolicy`
- **Scope**: Can propagate resources across all namespaces except system reserved (karmada-system, karmada-cluster, karmada-es-*)
- **Capabilities**: Same as PropagationPolicy but cluster-wide

#### 1.3 Placement & Cluster Selection
- **ClusterAffinity**: Select clusters by labels, fields (provider/region/zone), names, or exclusions
- **ClusterAffinities**: Multi-group scheduling with fallback (primary → secondary groups)
- **ClusterTolerations**: Tolerate cluster taints
- **SpreadConstraints**: Spread workloads across cluster groups
  - SpreadByField: cluster, region, zone, provider
  - SpreadByLabel: custom label-based grouping
  - MinGroups/MaxGroups: control distribution breadth

#### 1.4 Replica Scheduling
- **Duplicated Mode**: Same replicas to each cluster
- **Divided Mode**: Split replicas across clusters
  - **Aggregated**: Minimize cluster count while respecting capacity
  - **Weighted**: Distribute by static weights or dynamic factors
    - StaticWeightList: Manual cluster weights
    - DynamicWeight: AvailableReplicas-based auto-weighting

#### 1.5 Workload Affinity & Anti-Affinity
- **Inter-workload affinity**: Co-locate workloads with same label value
- **Inter-workload anti-affinity**: Separate workloads with same label value
- **GroupByLabelKey**: Define affinity groups via labels
- **Namespace-scoped**: Groups don't span namespaces

### 2. Override Policies

#### 2.1 OverridePolicy (Namespace-scoped)
- **Purpose**: Customize resources per cluster during propagation
- **API**: `policy.karmada.io/v1alpha1/OverridePolicy`
- **Override Types**:
  - **ImageOverrider**: Modify container images (Registry/Repository/Tag components)
  - **CommandOverrider**: Override container commands
  - **ArgsOverrider**: Override container args
  - **LabelsOverrider**: Add/remove/replace labels
  - **AnnotationsOverrider**: Add/remove/replace annotations
  - **FieldOverrider**: Modify structured fields (JSON/YAML) with JSONPath operations
  - **PlaintextOverrider**: Generic field overrides with add/remove/replace operators

#### 2.2 ClusterOverridePolicy (Cluster-scoped)
- **Purpose**: Cluster-wide override rules
- **API**: `policy.karmada.io/v1alpha1/ClusterOverridePolicy`
- **Capabilities**: Same as OverridePolicy but cluster-wide

#### 2.3 RuleWithCluster
- **TargetCluster**: Apply overrides only to matching clusters
- **Overriders**: Chain multiple override rules with execution order

### 3. Failover & High Availability

#### 3.1 Application Failover
- **DecisionConditions**: TolerationSeconds before triggering failover
- **PurgeMode**: 
  - Directly (formerly Immediately): Evict legacy app immediately
  - Gracefully (formerly Graciously): Wait for new app healthy or timeout
  - Never: Manual cleanup
- **GracePeriodSeconds**: Max wait time before deleting old app (default 600s)
- **StatePreservation**: Extract and restore state data during failover
  - JSONPath-based state extraction
  - AliasLabelName for state injection
  - Requires StatefulFailoverInjection feature gate (alpha)

#### 3.2 Cluster Failover
- **PurgeMode**: Directly or Gracefully
- **StatePreservation**: Same as application failover
- **Controlled by**: Controller's no-execute-taint-eviction-purge-mode parameter (if not specified in policy)

### 4. Cluster Management

#### 4.1 Cluster Registration & Lifecycle
- **API**: `cluster.karmada.io/v1alpha1/Cluster`
- **SyncMode**:
  - **Push**: Karmada control plane pushes resources to clusters
  - **Pull**: Agent on member cluster pulls resources (karmada-agent)
- **Cluster Identity**:
  - Unique ID (from ClusterProperty API or kube-system namespace UID)
  - Provider, Region, Zone(s) metadata
  - Taints for scheduling restrictions

#### 4.2 Cluster Status & Health
- **Conditions**:
  - Ready: Cluster healthy and accepting workloads
  - CompleteAPIEnablements: API discovery complete
- **NodeSummary**: Total and ready node counts
- **ResourceSummary**:
  - Allocatable: Available for scheduling
  - Allocating: Pending scheduling
  - Allocated: Already scheduled
  - AllocatableModelings: Resource modeling grades (0-8)

#### 4.3 Resource Modeling
- **Purpose**: Categorize clusters by resource capacity
- **Grades**: 0-8 (exponential CPU/memory ranges)
- **Custom Models**: Define custom resource quota ranges
- **Supported Resources**: cpu, memory, storage, ephemeral-storage

#### 4.4 API Enablements
- **Discovery**: List of APIs available on member cluster
- **GroupVersion**: API group and version
- **Resources**: APIResource list (Name + Kind)

### 5. Work Distribution

#### 5.1 Work API
- **Purpose**: Represents resources to deploy on member cluster
- **API**: `work.karmada.io/v1alpha1/Work`
- **Manifests**: List of Kubernetes resources (RawExtension)
- **SuspendDispatching**: Pause propagation without affecting status collection
- **PreserveResourcesOnDeletion**: Keep resources on cluster when Work deleted

#### 5.2 Work Status
- **Conditions**:
  - Applied: Successfully applied on cluster
  - Progressing: Being applied
  - Available: Resources exist on cluster
  - Degraded: Current state doesn't match desired
  - Dispatching: Dispatching or suspension status
- **ManifestStatuses**: Per-resource status tracking
  - ResourceIdentifier: Group/Version/Kind/Namespace/Name
  - Health: Healthy/Unhealthy/Unknown
  - Status: Raw resource status

### 6. Advanced Scheduling Features

#### 6.1 Priority-Based Scheduling
- **SchedulePriority**: Define workload priority for scheduling
- **PriorityClassSource**:
  - KubePriorityClass: Use K8s PriorityClass (scheduling.k8s.io/v1)
  - PodPriorityClass: Use PodTemplate PriorityClassName (planned)
  - FederatedPriorityClass: Karmada-native priority (planned)
- **Feature Gates**:
  - PriorityBasedScheduling (alpha)
  - PriorityBasedPreemptiveScheduling (planned)

#### 6.2 Scheduler Selection
- **SchedulerName**: Route policy to specific scheduler
- **Default**: "default-scheduler"
- **Custom Schedulers**: Support for pluggable schedulers

#### 6.3 Dependent Overrides
- **DependentOverrides**: List of OverridePolicy names that must exist before PropagationPolicy takes effect
- **Use Case**: Ensure overrides are applied when creating resources and policies simultaneously

### 7. Resource Interpreter Interface

#### 7.1 InterpretReplica
- **Purpose**: Determine number of replicas and replica requirements
- **Use Case**: Custom resource types with replica semantics

#### 7.2 InterpretHealth
- **Purpose**: Determine health status of resources
- **Use Case**: Custom health checks for CRDs

### 8. Controllers & Automation

#### 8.1 Core Controllers (from pkg/controllers/)
- **applicationfailover**: Application failover orchestration
- **binding**: ResourceBinding management
- **certificate**: Certificate management
- **cluster**: Cluster lifecycle and status
- **cronfederatedhpa**: Scheduled HPA
- **deploymentreplicassyncer**: Sync deployment replicas
- **execution**: Work execution on member clusters
- **federatedhpa**: Federated Horizontal Pod Autoscaler
- **federatedresourcequota**: Multi-cluster resource quotas
- **gracefuleviction**: Graceful workload eviction
- **hpascaletargetmarker**: Mark HPA scale targets
- **mcs**: Multi-Cluster Service (MCS API)
- **multiclusterservice**: Multi-cluster service discovery
- **namespace**: Namespace propagation
- **remediation**: Cluster remediation actions
- **status**: Status aggregation from member clusters
- **taint**: Taint-based eviction
- **unifiedauth**: Unified authentication
- **workloadrebalancer**: Workload rebalancing

### 9. Multi-Cluster Services

#### 9.1 Service Discovery
- **MCS API**: Kubernetes Multi-Cluster Service API support
- **MultiClusterService**: Karmada-native multi-cluster service
- **Service Name Resolution**: DNS-based service discovery across clusters

### 10. Autoscaling

#### 10.1 FederatedHPA
- **Purpose**: Horizontal Pod Autoscaler across multiple clusters
- **Metrics**: Aggregate metrics from member clusters
- **Scaling**: Coordinate replica scaling across clusters

#### 10.2 CronFederatedHPA
- **Purpose**: Scheduled autoscaling
- **Use Case**: Predictable traffic patterns, cost optimization

### 11. Resource Quota Management

#### 11.1 FederatedResourceQuota
- **Purpose**: Enforce resource quotas across multiple clusters
- **Aggregation**: Sum quotas from member clusters
- **Enforcement**: Prevent over-allocation

### 12. Observability & Monitoring

#### 12.1 Metrics Adapter
- **Component**: karmada-metrics-adapter
- **Purpose**: Expose aggregated metrics for HPA
- **API**: Custom Metrics API, External Metrics API

#### 12.2 Search API
- **Purpose**: Global search across member clusters
- **API**: `search.karmada.io/v1alpha1`
- **Capabilities**: Search resources across all registered clusters

### 13. Agent Mode (Pull-based Sync)

#### 13.1 Karmada Agent
- **Component**: karmada-agent
- **Deployment**: Runs on member cluster
- **Responsibilities**:
  - Pull Work resources from control plane
  - Apply resources locally
  - Report status back to control plane
- **Use Case**: Clusters behind firewall, edge clusters

#### 13.2 Scheduler Estimator
- **Component**: karmada-scheduler-estimator
- **Purpose**: Estimate available replicas on member cluster
- **Protocol**: gRPC
- **Use Case**: Accurate replica scheduling decisions

### 14. Descheduler

#### 14.1 Karmada Descheduler
- **Component**: karmada-descheduler
- **Purpose**: Rebalance workloads across clusters
- **Triggers**: Cluster capacity changes, policy updates
- **Strategies**: Configurable descheduling policies

### 15. Webhook & Validation

#### 15.1 Karmada Webhook
- **Component**: karmada-webhook
- **Types**:
  - Validating webhooks
  - Mutating webhooks
- **Scope**: PropagationPolicy, OverridePolicy, Cluster, Work validation

#### 15.2 Resource Interpreter Webhook
- **Component**: karmada-interpreter-webhook-example
- **Purpose**: Custom resource interpretation
- **Extensibility**: Plugin model for CRD support

### 16. Aggregated API Server

#### 16.1 Karmada Aggregated APIServer
- **Component**: karmada-aggregated-apiserver
- **Purpose**: Extend Karmada API surface
- **APIs**: Cluster API, Search API
- **Integration**: Kubernetes API aggregation layer

### 17. Operator

#### 17.1 Karmada Operator
- **Component**: karmada-operator
- **Purpose**: Install and manage Karmada control plane
- **Lifecycle**: Install, upgrade, uninstall
- **Configuration**: Declarative Karmada configuration

### 18. Security & Authentication

#### 18.1 Cluster Authentication
- **SecretRef**: Token and CA bundle for cluster access
- **ImpersonatorSecretRef**: Impersonation token
- **InsecureSkipTLSVerification**: Skip TLS verification (not recommended)
- **ProxyURL**: HTTP proxy for cluster access
- **ProxyHeader**: Custom headers for proxy

#### 18.2 Unified Auth
- **Controller**: unifiedauth controller
- **Purpose**: Centralized authentication across clusters

### 19. Remediation

#### 19.1 Remedy API
- **API**: `remedy.karmada.io/v1alpha1`
- **Purpose**: Define remediation actions for cluster issues
- **Automation**: Automatic remediation based on cluster conditions

### 20. Networking

#### 20.1 Networking API
- **API**: `networking.karmada.io/v1alpha1`
- **Purpose**: Multi-cluster networking configuration
- **Features**: Service routing, ingress federation

### 21. Configuration

#### 21.1 Config API
- **API**: `config.karmada.io/v1alpha1`
- **Purpose**: Karmada configuration objects
- **Scope**: Scheduler config, controller config

### 22. Apps

#### 22.1 Apps API
- **API**: `apps.karmada.io/v1alpha1`
- **Purpose**: Application-level abstractions
- **Features**: Application grouping, lifecycle management

### 23. Autoscaling API

#### 23.1 Autoscaling API
- **API**: `autoscaling.karmada.io/v1alpha1`
- **Resources**: FederatedHPA, CronFederatedHPA
- **Integration**: Kubernetes HPA API

## Feature Maturity

### Alpha Features
- StatefulFailoverInjection (state preservation during failover)
- PriorityBasedScheduling
- PriorityBasedPreemptiveScheduling (planned)
- WorkloadAffinity/AntiAffinity
- PodPriorityClass source (planned)
- FederatedPriorityClass source (planned)

### Beta/Stable Features
- PropagationPolicy
- ClusterPropagationPolicy
- OverridePolicy
- ClusterOverridePolicy
- Cluster management
- Work distribution
- Push/Pull sync modes
- Resource modeling
- Failover (application and cluster)

## Component Architecture

### Control Plane Components
1. **karmada-apiserver**: Kubernetes API server for Karmada
2. **karmada-controller-manager**: Core controllers
3. **karmada-scheduler**: Workload scheduler
4. **karmada-descheduler**: Workload rebalancer
5. **karmada-webhook**: Admission webhooks
6. **karmada-aggregated-apiserver**: API extensions
7. **karmada-search**: Global search
8. **karmada-metrics-adapter**: Metrics aggregation
9. **karmada-operator**: Lifecycle management

### Member Cluster Components
1. **karmada-agent**: Pull-mode agent
2. **karmada-scheduler-estimator**: Capacity estimation

## Deployment Modes

### Push Mode
- Control plane pushes resources to clusters
- Requires network connectivity from control plane to clusters
- Suitable for: Data center clusters, managed clusters

### Pull Mode
- Agent on cluster pulls resources from control plane
- Requires network connectivity from clusters to control plane
- Suitable for: Edge clusters, clusters behind firewall, air-gapped environments

## Integration Points

### Kubernetes API Compatibility
- Compatible with Kubernetes 1.24-1.35
- Native Kubernetes resource support
- CRD support via resource interpreter

### Cloud Provider Integration
- Provider field for cloud identification
- Region/Zone awareness
- Multi-cloud scheduling

### Observability Integration
- Prometheus metrics
- Custom Metrics API
- External Metrics API

## Summary

Karmada provides **23 major feature categories** with **100+ specific capabilities** for multi-cluster Kubernetes orchestration. The platform is designed for:

1. **Workload Distribution**: Intelligent scheduling across clusters
2. **High Availability**: Application and cluster failover
3. **Resource Optimization**: Replica scheduling, autoscaling, rebalancing
4. **Customization**: Override policies, resource interpreters
5. **Multi-Cloud**: Provider-agnostic cluster management
6. **Edge Computing**: Pull-mode agent for disconnected clusters
7. **Service Mesh**: Multi-cluster service discovery
8. **Observability**: Aggregated metrics and search

**Key Differentiators**:
- K8s-native API (no proprietary abstractions)
- Push and Pull sync modes
- Advanced scheduling (affinity, spread, weighted)
- Stateful failover with state preservation
- Resource modeling for capacity-aware scheduling
- Extensible via webhooks and interpreters
