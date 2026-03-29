# Agent Core Integration - Technical Design

**Spec ID:** agent-core-integration  
**Status:** Draft  
**Created:** 2026-03-26  
**Last Updated:** 2026-03-26

## 1. Overview

This design implements the missing integration components for agents-core identified in the pending implementation review. The implementation addresses critical gaps that prevent end-to-end testing and production deployment of the agents-core MCP server.

**CRITICAL MISSING COMPONENTS:**
- MCP Server executable entrypoint (`cmd/mcp-server/main.go`)
- Kubernetes Deployment Adapter for direct Spoke cluster CRD application
- Spoke Controller for status synchronization
- Hub Event Router for pg_notify → NATS bridge (`cmd/hub-event-router/main.go`)
- NATS Status Subscriber for closing the status loop
- Cluster deployment manifests and RBAC
- Telemetry integration with opensbt libraries

**IMPORTANT - Architecture Boundaries:**
- **opensbt**: Generic SaaS framework (Auth, Billing, NATS, multi-tenant primitives) - NO agent logic
- **agents-core**: Platform service with business domain logic - MCP API, authorization, orchestration
- **AgentRegistry OSS**: CRUD-only database wrapper - does NOT generate CRDs or talk to Kubernetes

### 1.1 Design Principles

- **Strict Boundary Separation**: opensbt provides generic SaaS patterns, agents-core handles business logic
- **Direct Kubernetes API**: Business agents use Deployment Adapter for immediate CRD application
- **Status Controller Pattern**: Hub API never queries Kubernetes directly, reads from PostgreSQL cache
- **Telemetry First**: All components instrumented with metrics, tracing, and logging
- **Production Ready**: Includes deployment manifests, RBAC, and cluster integration

### 1.2 Integration Architecture (CORRECTED)

```
MCP Client (Cursor/Goose)
    ↓ HTTPS + JWT
AgentGateway + Auth-Proxy (validates JWT, injects X-Auth-* headers)
    ↓ HTTP + X-Auth-* headers
cmd/mcp-server (NEW - MCP JSON-RPC server)
    ↓ Orchestrates via internal/agent-core/service
AgentRegistry API (CRUD only)
    ↓ Creates deployment record
Deployment Adapter (NEW - internal/agent-core/adapter)
    ↓ Generates Kagent CRD + applies to Spoke via K8s API
Kagent Controller (Spoke)
    ↓ Reconciles Agent CRD → Pod + KEDA
Spoke Controller (NEW - operators/spoke-controller)
    ↓ Watches pods → writes status to Hub PostgREST
Hub Centralised DB
    ↓ pg_notify trigger
Hub Event Router (NEW - cmd/hub-event-router)
    ↓ Listens to pg_notify → publishes to NATS
NATS hub.platform.agent.infra_status subject
    ↓ NATS subscription
NATS Status Subscriber (NEW - cmd/nats-subscriber)
    ↓ Updates AgentRegistry deployment status
```

**CRITICAL CORRECTION - pg_notify Bridge:**
PostgreSQL pg_notify cannot natively publish to NATS. A Hub Event Router service is required to bridge pg_notify events to NATS subjects.

## 2. Missing Component Implementations

### 2.1 MCP Server Entrypoint

**Location:** `cmd/mcp-server/main.go`

**Purpose:** JSON-RPC server that exposes agents-core MCP tools to MCP clients

**Key Requirements:**
- Initialize opensbt telemetry libraries (metrics, tracing, logging)
- Register all MCP tools with proper middleware
- Extract tenant context from X-Auth-* headers
- Instrument all tool executions with telemetry
**Implementation Pattern:**
```go
// cmd/mcp-server/main.go
func main() {
    // 1. Initialize opensbt telemetry
    tracer := tracing.New(ctx, "mcp-server", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
    metrics := metrics.NewManager("agents_core")
    logger := logging.NewStructuredLogger("mcp-server")
    
    // 2. Initialize opensbt control plane (NO IProvisioner for business agents)
    cp := controlplane.New(controlplane.Config{
        EventBus:   natsClient,
        AuthClient: oryClient,
        Tracer:     tracer,
        Metrics:    metrics,
        Logger:     logger,
    })
    
    // 3. Initialize K8s Spoke Adapter (CRITICAL - Direct Kubernetes API)
    hubClient, _ := client.New(hubConfig, client.Options{})
    deploymentAdapter := adapter.NewK8sSpokeAdapter(adapter.Config{
        HubClient: hubClient,  // To read CAPI kubeconfig secrets
        Tracer:    tracer,
        Metrics:   metrics,
        Logger:    logger,
    })
    
    // 4. Initialize agent-core services
    agentService := service.NewAgentService(service.Config{
        AgentRegistryClient: agentRegistryClient,
        HubClient:           hubClient,
        ControlPlane:        cp,
        DeploymentAdapter:   deploymentAdapter,  // Direct K8s API - NO GitOps
        DB:                  queries,
    })
    
    // 5. Initialize MCP JSON-RPC server
    mcpServer := jsonrpc.NewServer()
    
    // 6. Register MCP tools with telemetry middleware
    mcpServer.RegisterTool("create_agent", withTelemetry(agentService.CreateAgent))
    mcpServer.RegisterTool("deploy_agent", withTelemetry(agentService.DeployAgent))
    mcpServer.RegisterTool("get_agent_status", withTelemetry(agentService.GetAgentStatus))
    mcpServer.RegisterTool("list_agents", withTelemetry(agentService.ListAgents))
    mcpServer.RegisterTool("update_agent", withTelemetry(agentService.UpdateAgent))
    mcpServer.RegisterTool("delete_agent", withTelemetry(agentService.DeleteAgent))
    mcpServer.RegisterTool("list_authorized_tools", withTelemetry(agentService.ListAuthorizedTools))
    
    // 7. Start HTTP server with tenant context middleware
    http.Handle("/mcp", tenantContextMiddleware(mcpServer))
    http.Handle("/metrics", promhttp.Handler())  // Prometheus metrics for VictoriaMetrics
    http.Handle("/health", healthCheckHandler())
    log.Fatal(http.ListenAndServe(":8080", nil))
}

func withTelemetry(handler MCPToolHandler) MCPToolHandler {
    return func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
        toolName := ctx.Value("mcp_tool_name").(string)
        
        // Start trace span
        ctx, span := tracer.StartSpan(ctx, "mcp_tool."+toolName)
        defer span.End()
        
        // Record metrics
        start := time.Now()
        defer func() {
            duration := time.Since(start)
            metrics.RecordMCPToolDuration(toolName, getTenantID(ctx), "success", duration)
        }()
        
        // Log execution
        logger.InfoContext(ctx, "MCP tool started", "tool", toolName, "params", params)
        
        result, err := handler(ctx, params)
        
        if err != nil {
            span.RecordError(err)
            logger.ErrorContext(ctx, "MCP tool failed", "tool", toolName, "error", err)
            metrics.RecordMCPToolDuration(toolName, getTenantID(ctx), "error", time.Since(start))
        } else {
            logger.InfoContext(ctx, "MCP tool completed", "tool", toolName)
        }
        
        return result, err
    }
}
```

### 2.2 Kubernetes Deployment Adapter

**Location:** `internal/agent-core/adapter/k8s_spoke_adapter.go`

**Purpose:** Apply Kagent Agent CRDs directly to Spoke clusters via Kubernetes API

**CRITICAL:** AgentRegistry OSS is CRUD-only and does NOT handle Kubernetes operations

**Key Requirements:**
- Maintain secure connections to multiple Spoke clusters
- Generate Kagent Agent CRDs from agent definitions
- Use Server-Side Apply for idempotency
- Handle kubeconfig rotation and connection failures
- Instrument all operations with telemetry

**Implementation Pattern:**
```go
// internal/agent-core/adapter/k8s_spoke_adapter.go
type K8sSpokeAdapter struct {
    hubClient    client.Client  // To read CAPI kubeconfig secrets from Hub
    spokeClients map[string]dynamic.Interface  // spokeClusterID -> client cache
    tracer       trace.Tracer
    metrics      *metrics.Manager
    logger       *slog.Logger
    mutex        sync.RWMutex
}

func (a *K8sSpokeAdapter) getSpokeClient(ctx context.Context, spokeClusterID string) (dynamic.Interface, error) {
    // Check cache first
    a.mutex.RLock()
    if client, exists := a.spokeClients[spokeClusterID]; exists {
        a.mutex.RUnlock()
        return client, nil
    }
    a.mutex.RUnlock()
    
    // 1. Read CAPI-generated kubeconfig secret from Hub cluster
    secret := &corev1.Secret{}
    err := a.hubClient.Get(ctx, client.ObjectKey{
        Name:      fmt.Sprintf("%s-kubeconfig", spokeClusterID),
        Namespace: "zero-ops-system",  // CAPI management cluster namespace
    }, secret)
    if err != nil {
        return nil, fmt.Errorf("failed to get spoke kubeconfig: %w", err)
    }
    
    // 2. Build REST config from CAPI secret data
    restConfig, err := clientcmd.RESTConfigFromKubeConfig(secret.Data["value"])
    if err != nil {
        return nil, fmt.Errorf("failed to parse kubeconfig: %w", err)
    }
    
    // 3. Create dynamic client for unstructured CRD operations
    dynamicClient, err := dynamic.NewForConfig(restConfig)
    if err != nil {
        return nil, fmt.Errorf("failed to create dynamic client: %w", err)
    }
    
    // 4. Cache the client
    a.mutex.Lock()
    a.spokeClients[spokeClusterID] = dynamicClient
    a.mutex.Unlock()
    
    return dynamicClient, nil
}

func (a *K8sSpokeAdapter) invalidateClient(spokeClusterID string) {
    a.mutex.Lock()
    delete(a.spokeClients, spokeClusterID)
    a.mutex.Unlock()
    a.logger.InfoContext(context.Background(), "Invalidated cached client due to auth failure", 
        "spoke_cluster_id", spokeClusterID)
}

func (a *K8sSpokeAdapter) ApplyCRD(ctx context.Context, spokeClusterID string, agent *models.Agent, deploymentID string) error {
    ctx, span := a.tracer.StartSpan(ctx, "k8s_spoke_adapter.apply_crd")
    defer span.End()
    
    span.SetAttributes(
        attribute.String("spoke_cluster_id", spokeClusterID),
        attribute.String("agent_id", agent.ID),
        attribute.String("deployment_id", deploymentID),
    )
    
    // 1. Get Spoke client using CAPI kubeconfig
    spokeClient, err := a.getSpokeClient(ctx, spokeClusterID)
    if err != nil {
        a.metrics.IncrementSpokeConnectionError(spokeClusterID)
        return fmt.Errorf("failed to get spoke client: %w", err)
    }
    
    // 2. Generate Kagent Agent CRD with required labels
    crd := &unstructured.Unstructured{
        Object: map[string]interface{}{
            "apiVersion": "kagent.dev/v1alpha2",
            "kind":       "Agent",
            "metadata": map[string]interface{}{
                "name":      fmt.Sprintf("agent-%s", agent.ID),
                "namespace": agent.Namespace,
                "labels": map[string]interface{}{
                    "tenant-id":     agent.TenantID,      // Required for Spoke Controller
                    "agent-id":      agent.ID,            // Required for Spoke Controller
                    "deployment-id": deploymentID,        // Required for Spoke Controller
                    "managed-by":    "agents-core",
                },
            },
            "spec": map[string]interface{}{
                "modelProvider": agent.ModelProvider,
                "model":         agent.Model,
                "systemMessage": agent.SystemMessage,
                "tools":         agent.Tools,
                "config":        agent.Config,
            },
        },
    }
    
    // 3. Apply CRD directly to Spoke using Server-Side Apply
    gvr := schema.GroupVersionResource{
        Group:    "kagent.dev",
        Version:  "v1alpha2",
        Resource: "agents",
    }
    
    _, err = spokeClient.Resource(gvr).
        Namespace(agent.Namespace).
        Apply(ctx, crd.GetName(), crd, metav1.ApplyOptions{
            FieldManager: "agents-core-deployment-adapter",
            Force:        true,
        })
    
    if err != nil {
        // Check if error is due to authentication/authorization (kubeconfig rotation)
        if isAuthError(err) {
            a.logger.WarnContext(ctx, "Authentication error detected, invalidating client cache",
                "spoke_cluster_id", spokeClusterID,
                "error", err,
            )
            a.invalidateClient(spokeClusterID)
            
            // Retry once with fresh client
            spokeClient, retryErr := a.getSpokeClient(ctx, spokeClusterID)
            if retryErr != nil {
                a.metrics.IncrementCRDApplyError(spokeClusterID)
                span.RecordError(retryErr)
                return fmt.Errorf("failed to get fresh spoke client after auth error: %w", retryErr)
            }
            
            _, err = spokeClient.Resource(gvr).
                Namespace(agent.Namespace).
                Apply(ctx, crd.GetName(), crd, metav1.ApplyOptions{
                    FieldManager: "agents-core-deployment-adapter",
                    Force:        true,
                })
        }
        
        if err != nil {
            a.metrics.IncrementCRDApplyError(spokeClusterID)
            span.RecordError(err)
            return fmt.Errorf("failed to apply CRD to spoke %s: %w", spokeClusterID, err)
        }
    }
    
    a.metrics.IncrementCRDApplySuccess(spokeClusterID)
    a.logger.InfoContext(ctx, "CRD applied successfully to Spoke cluster",
        "spoke_cluster_id", spokeClusterID,
        "agent_id", agent.ID,
        "deployment_id", deploymentID,
    )
    
    return nil
}

func (a *K8sSpokeAdapter) DeleteCRD(ctx context.Context, spokeClusterID string, agentID, namespace string) error {
    spokeClient, err := a.getSpokeClient(ctx, spokeClusterID)
    if err != nil {
        return fmt.Errorf("failed to get spoke client: %w", err)
    }
    
    gvr := schema.GroupVersionResource{
        Group:    "kagent.dev",
        Version:  "v1alpha2", 
        Resource: "agents",
    }
    
    err = spokeClient.Resource(gvr).
        Namespace(namespace).
        Delete(ctx, fmt.Sprintf("agent-%s", agentID), metav1.DeleteOptions{})
    
    // Handle auth errors for delete operations too
    if err != nil && isAuthError(err) {
        a.invalidateClient(spokeClusterID)
        spokeClient, retryErr := a.getSpokeClient(ctx, spokeClusterID)
        if retryErr != nil {
            return fmt.Errorf("failed to get fresh spoke client for delete: %w", retryErr)
        }
        err = spokeClient.Resource(gvr).
            Namespace(namespace).
            Delete(ctx, fmt.Sprintf("agent-%s", agentID), metav1.DeleteOptions{})
    }
    
    return err
}

// Helper function to detect authentication/authorization errors
func isAuthError(err error) bool {
    if err == nil {
        return false
    }
    errStr := strings.ToLower(err.Error())
    return strings.Contains(errStr, "unauthorized") ||
           strings.Contains(errStr, "forbidden") ||
           strings.Contains(errStr, "x509") ||
           strings.Contains(errStr, "certificate")
}
```

### 2.3 Spoke Controller

**Location:** `operators/spoke-controller/internal/controller/agent_controller.go`

**Purpose:** Watch Agent CRDs in Spoke clusters and sync status to Hub Centralised DB

**Key Requirements:**
- Use controller-runtime pattern from kagent reference implementation
- Derive infrastructure status from Deployment.status.availableReplicas
- Map KEDA scale-to-zero states to phase field
- Write status to Hub PostgREST with proper authentication
- Skip unlabelled CRDs (platform agents)

**Implementation Pattern:**
```go
// operators/spoke-controller/internal/controller/agent_controller.go
type AgentStatusController struct {
    client.Client
    Scheme      *runtime.Scheme
    HubClient   *client.HubPostgRESTClient
    ClusterID   string
    Tracer      trace.Tracer
    Metrics     *metrics.Manager
    Logger      *slog.Logger
}

func (r *AgentStatusController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    ctx, span := r.Tracer.StartSpan(ctx, "spoke_controller.reconcile")
    defer span.End()
    
    // 1. Fetch Agent CRD
    var agent v1alpha2.Agent
    if err := r.Get(ctx, req.NamespacedName, &agent); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 2. Validate required labels (skip platform agents)
    tenantID := agent.Labels["tenant-id"]
    agentID := agent.Labels["agent-id"]
    deploymentID := agent.Labels["deployment-id"]
    
    if tenantID == "" || agentID == "" || deploymentID == "" {
        r.Logger.InfoContext(ctx, "Skipping agent without required labels",
            "name", agent.Name,
            "namespace", agent.Namespace,
        )
        return ctrl.Result{}, nil
    }
    
    // 3. Derive infrastructure status
    infraStatus, err := r.deriveInfraStatus(ctx, &agent)
    if err != nil {
        return ctrl.Result{RequeueAfter: 30 * time.Second}, err
    }
    
    // 4. Sync to Hub Centralised DB
    if err := r.syncToHub(ctx, &agent, infraStatus); err != nil {
        r.Metrics.IncrementHubSyncError(r.ClusterID)
        return ctrl.Result{RequeueAfter: 30 * time.Second}, err
    }
    
    r.Metrics.IncrementHubSyncSuccess(r.ClusterID)
    return ctrl.Result{}, nil
}

func (r *AgentStatusController) deriveInfraStatus(ctx context.Context, agent *v1alpha2.Agent) (*models.InfraStatus, error) {
    // Get underlying Deployment
    var deployment appsv1.Deployment
    deploymentName := fmt.Sprintf("agent-%s", agent.Name)
    
    err := r.Get(ctx, types.NamespacedName{
        Name:      deploymentName,
        Namespace: agent.Namespace,
    }, &deployment)
    
    if err != nil {
        return &models.InfraStatus{
            Status:   "provisioning",
            Phase:    "Unknown",
            Replicas: 0,
            Message:  "Deployment not found",
        }, nil
    }
    
    // Map Kubernetes status to infrastructure status
    status := "provisioning"
    phase := "Unknown"
    replicas := deployment.Status.AvailableReplicas
    
    if deployment.Status.AvailableReplicas == 0 {
        phase = "Idle"  // KEDA scaled to zero
        if deployment.Status.Conditions != nil {
            for _, condition := range deployment.Status.Conditions {
                if condition.Type == appsv1.DeploymentProgressing && condition.Status == corev1.ConditionTrue {
                    status = "ready"  // Deployment is ready but scaled to zero
                }
            }
        }
    } else if deployment.Status.AvailableReplicas > 0 {
        status = "ready"
        phase = "Running"
    }
    
    // Check for failure conditions
    for _, condition := range deployment.Status.Conditions {
        if condition.Type == appsv1.DeploymentReplicaFailure && condition.Status == corev1.ConditionTrue {
            status = "failed"
            phase = "Failed"
        }
    }
    
    return &models.InfraStatus{
        Status:   status,
        Phase:    phase,
        Replicas: int(replicas),
        Message:  fmt.Sprintf("Deployment %s: %d/%d replicas available", deploymentName, replicas, deployment.Status.Replicas),
    }, nil
}

func (r *AgentStatusController) syncToHub(ctx context.Context, agent *v1alpha2.Agent, infraStatus *models.InfraStatus) error {
    payload := &models.AgentInfraStatus{
        TenantID:       agent.Labels["tenant-id"],
        AgentID:        agent.Labels["agent-id"],
        DeploymentID:   agent.Labels["deployment-id"],
        SpokeClusterID: r.ClusterID,
        Status:         infraStatus.Status,
        Phase:          infraStatus.Phase,
        Replicas:       infraStatus.Replicas,
        Message:        infraStatus.Message,
        LastSyncAt:     time.Now(),
    }
    
    // POST to Hub PostgREST with Bearer JWT
    return r.HubClient.UpsertAgentInfraStatus(ctx, payload)
}
```

### 2.4 Hub Event Router (NEW - pg_notify Bridge)

**Location:** `cmd/hub-event-router/main.go`

**Purpose:** Bridge PostgreSQL pg_notify events to NATS subjects

**CRITICAL:** PostgreSQL pg_notify cannot natively publish to NATS. This service is required to bridge the gap.

**Key Requirements:**
- Listen to Hub Centralised DB pg_notify events
- Publish events to NATS `hub.platform.agent.infra_status` subject
- Handle connection failures and retries
- Instrument with telemetry

**Implementation Pattern:**
```go
// cmd/hub-event-router/main.go
type HubEventRouter struct {
    dbListener *pq.Listener
    natsConn   *nats.Conn
    tracer     trace.Tracer
    metrics    *metrics.Manager
    logger     *slog.Logger
}

func (r *HubEventRouter) Start(ctx context.Context) error {
    // 1. Connect to Hub Centralised DB for pg_notify
    r.dbListener = pq.NewListener(r.hubDBConnectionString, 10*time.Second, time.Minute, nil)
    err := r.dbListener.Listen("hub.platform.agent.infra_status")
    if err != nil {
        return fmt.Errorf("failed to listen to pg_notify: %w", err)
    }
    
    // 2. Start event processing loop
    go r.processEvents(ctx)
    
    r.logger.InfoContext(ctx, "Hub event router started")
    <-ctx.Done()
    return nil
}

func (r *HubEventRouter) processEvents(ctx context.Context) {
    for {
        select {
        case notification := <-r.dbListener.Notify:
            if notification != nil {
                r.handleNotification(ctx, notification)
            }
        case <-ctx.Done():
            return
        }
    }
}

func (r *HubEventRouter) handleNotification(ctx context.Context, notification *pq.Notification) {
    ctx, span := r.tracer.StartSpan(ctx, "hub_event_router.handle_notification")
    defer span.End()
    
    // Parse pg_notify payload
    var payload map[string]interface{}
    if err := json.Unmarshal([]byte(notification.Extra), &payload); err != nil {
        r.logger.ErrorContext(ctx, "Failed to parse pg_notify payload", "error", err)
        r.metrics.IncrementEventProcessingError("parse_error")
        return
    }
    
    // Publish to NATS subject
    err := r.natsConn.Publish("hub.platform.agent.infra_status", []byte(notification.Extra))
    if err != nil {
        r.logger.ErrorContext(ctx, "Failed to publish to NATS", "error", err)
        r.metrics.IncrementEventProcessingError("nats_publish_error")
        span.RecordError(err)
        return
    }
    
    r.metrics.IncrementEventProcessingSuccess()
    r.logger.InfoContext(ctx, "Event routed successfully",
        "channel", notification.Channel,
        "payload", notification.Extra,
    )
}
```

### 2.5 NATS Status Subscriber

**Location:** `cmd/nats-subscriber/main.go`

**Purpose:** Subscribe to Hub NATS events and update AgentRegistry deployment status

**Key Requirements:**
- Subscribe to `hub.platform.agent.infra_status` NATS subject
- Map infrastructure status to application status
- Update AgentRegistry via API (NOT direct DB access)
- Handle connection failures and retries
- Instrument with telemetry

**Implementation Pattern:**
```go
// cmd/nats-subscriber/main.go
type NATSStatusSubscriber struct {
    natsConn            *nats.Conn
    agentRegistryClient *client.AgentRegistryClient
    tracer              trace.Tracer
    metrics             *metrics.Manager
    logger              *slog.Logger
}

func (s *NATSStatusSubscriber) Start(ctx context.Context) error {
    // Subscribe to NATS subject (receives events from Hub Event Router)
    _, err := s.natsConn.Subscribe("hub.platform.agent.infra_status", s.handleStatusUpdate)
    if err != nil {
        return fmt.Errorf("failed to subscribe to NATS: %w", err)
    }
    
    s.logger.InfoContext(ctx, "NATS status subscriber started")
    
    // Block until context is cancelled
    <-ctx.Done()
    return nil
}

func (s *NATSStatusSubscriber) handleStatusUpdate(msg *nats.Msg) {
    ctx := context.Background()
    ctx, span := s.tracer.StartSpan(ctx, "nats_subscriber.handle_status_update")
    defer span.End()
    
    var update models.InfraStatusUpdate
    if err := json.Unmarshal(msg.Data, &update); err != nil {
        s.logger.ErrorContext(ctx, "Failed to unmarshal status update", "error", err)
        s.metrics.IncrementNATSProcessingError("unmarshal_error")
        return
    }
    
    span.SetAttributes(
        attribute.String("deployment_id", update.DeploymentID),
        attribute.String("status", update.Status),
        attribute.String("phase", update.Phase),
    )
    
    // Map infrastructure status to application status
    deploymentStatus := s.mapInfraStatusToDeploymentStatus(update.Status)
    
    // Update AgentRegistry via API
    err := s.updateAgentRegistryDeployment(ctx, update.DeploymentID, deploymentStatus)
    if err != nil {
        s.logger.ErrorContext(ctx, "Failed to update AgentRegistry deployment",
            "deployment_id", update.DeploymentID,
            "error", err,
        )
        s.metrics.IncrementNATSProcessingError("agentregistry_update_error")
        span.RecordError(err)
        return
    }
    
    s.metrics.IncrementNATSProcessingSuccess()
    s.logger.InfoContext(ctx, "Deployment status updated successfully",
        "deployment_id", update.DeploymentID,
        "old_status", "deploying",
        "new_status", deploymentStatus,
    )
}

func (s *NATSStatusSubscriber) mapInfraStatusToDeploymentStatus(infraStatus string) string {
    switch infraStatus {
    case "provisioning":
        return "deploying"
    case "ready":
        return "deployed"
    case "failed":
        return "failed"
    default:
        return "deploying"
    }
}

func (s *NATSStatusSubscriber) updateAgentRegistryDeployment(ctx context.Context, deploymentID, status string) error {
    updateReq := &client.DeploymentUpdateRequest{
        Status:    status,
        UpdatedAt: time.Now(),
    }
    
    return s.agentRegistryClient.UpdateDeployment(ctx, deploymentID, updateReq)
}
```

## 3. Telemetry Integration

### 3.1 Observability Stack Architecture

**VictoriaMetrics Integration:**
- Applications expose metrics in Prometheus format via `/metrics` endpoint
- Grafana Alloy scrapes metrics and forwards to VictoriaMetrics via `remote_write`
- VictoriaMetrics provides PromQL-compatible query API
- Current Prometheus client library usage is CORRECT

**OpenTelemetry Tracing:**
- All components use opensbt tracing libraries
- Traces propagate from MCP client → Hub → AgentRegistry → Spoke
- Spans include tenant context and operation metadata

**Structured Logging:**
- JSON format logs sent to OpenSearch
- Include tenant_id, agent_id, deployment_id in all log entries
- Error logs include stack traces and context

### 3.2 Custom Metrics

**Agent-Core Specific Metrics:**
```go
// internal/agent-core/telemetry/metrics.go
var (
    // MCP tool execution metrics
    MCPToolDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "agents_mcp_tool_duration_seconds",
            Help: "Duration of MCP tool executions",
        },
        []string{"tool_name", "tenant_id", "status"},
    )
    
    // Agent deployment metrics
    AgentDeployments = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "agents_deployments_total",
            Help: "Total agent deployments by status and phase",
        },
        []string{"status", "phase", "tenant_tier"},
    )
    
    // Spoke cluster connectivity metrics
    SpokeConnections = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "agents_spoke_connections_active",
            Help: "Active connections to spoke clusters",
        },
        []string{"spoke_cluster_id", "status"},
    )
    
    // CRD application metrics
    CRDOperations = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "agents_crd_operations_total",
            Help: "Total CRD operations by type and result",
        },
        []string{"operation", "spoke_cluster_id", "result"},
    )
)
```

## 4. Cluster Deployment Architecture

### 4.1 Deployment Manifests Structure

```
manifests/
└── platform-core/
    ├── mcp-server/
    │   ├── deployment.yaml
    │   ├── service.yaml
    │   ├── servicemonitor.yaml      # NEW - VictoriaMetrics integration
    │   ├── configmap.yaml
    │   └── rbac.yaml
    ├── hub-event-router/            # NEW - pg_notify → NATS bridge
    │   ├── deployment.yaml
    │   ├── configmap.yaml
    │   └── rbac.yaml
    ├── nats-subscriber/
    │   ├── deployment.yaml
    │   ├── configmap.yaml
    │   └── rbac.yaml
    └── spoke-controller/
        ├── deployment.yaml
        ├── service-account.yaml
        ├── cluster-role.yaml
        ├── cluster-role-binding.yaml
        └── configmap.yaml
```

### 4.2 RBAC Requirements

**MCP Server RBAC (CRITICAL - Must read CAPI kubeconfig secrets):**
```yaml
# manifests/platform-core/mcp-server/rbac.yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  namespace: zero-ops-system
  name: mcp-server-spoke-access
rules:
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list", "watch"]
  resourceNames: ["*-kubeconfig"]  # CAPI-generated kubeconfig secrets
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: mcp-server-spoke-access
  namespace: zero-ops-system
subjects:
- kind: ServiceAccount
  name: mcp-server
  namespace: zero-ops-system
roleRef:
  kind: Role
  name: mcp-server-spoke-access
  apiGroup: rbac.authorization.k8s.io
```

**Spoke Controller RBAC:**
- Watch Agent CRDs and Deployments in Spoke cluster
- Read access to KEDA ScaledObjects
- Network access to Hub PostgREST with client_credentials JWT

**Hub Event Router RBAC:**
- Database connection to Hub Centralised DB for pg_notify
- NATS publish permissions to `hub.platform.agent.infra_status` subject
- Network access between Hub DB and NATS cluster

**NATS Subscriber RBAC:**
- NATS subscribe permissions
- Network access to AgentRegistry API (NOT direct DB access)

### 4.3 VictoriaMetrics Integration

**ServiceMonitor for Metrics Discovery:**
```yaml
# manifests/platform-core/mcp-server/servicemonitor.yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: mcp-server-monitor
  namespace: zero-ops-system
  labels:
    release: prometheus  # Must match VictoriaMetrics selector
spec:
  selector:
    matchLabels:
      app: mcp-server
  endpoints:
  - port: http
    path: /metrics
    interval: 15s
```

**Deployment with Telemetry Configuration:**
```yaml
# manifests/platform-core/mcp-server/deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mcp-server
  namespace: zero-ops-system
spec:
  replicas: 2
  selector:
    matchLabels:
      app: mcp-server
  template:
    metadata:
      labels:
        app: mcp-server
    spec:
      serviceAccountName: mcp-server
      containers:
      - name: mcp-server
        image: ghcr.io/zero-ops/mcp-server:latest
        ports:
        - containerPort: 8080
          name: http
        env:
        # OpenTelemetry Configuration
        - name: OTEL_EXPORTER_OTLP_ENDPOINT
          value: "http://tempo.zero-ops-system.svc.cluster.local:4318"
        - name: OTEL_SERVICE_NAME
          value: "mcp-server"
        # API Connections
        - name: AGENTREGISTRY_URL
          value: "http://agentregistry.platform-agentregistry.svc.cluster.local:8080"
        - name: HUB_POSTGREST_URL
          value: "https://postgrest.hub.nutgrat.in"
        - name: NATS_URL
          value: "nats://nats.zero-ops-system.svc.cluster.local:4222"
        # Database Connection
        - name: DATABASE_URL
          valueFrom:
            secretKeyRef:
              name: control-plane-db-credentials
              key: url
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 30
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 5
```

### 4.4 Network Security

**Hub Cluster:**
- MCP Server: Ingress from AgentGateway, egress to AgentRegistry/Hub PostgREST/Spoke clusters
- NATS Subscriber: Egress to AgentRegistry API only (NO direct DB access)

**Spoke Clusters:**
- Spoke Controller: Egress to Hub PostgREST only
- Agent Pods: Egress based on tenant network policies

## 5. Integration Points

### 5.1 opensbt Platform Requirements

**Database Migrations:**
- Control Plane DB: `agentregistry` and `agents` schemas
- Hub Centralised DB: `agent_infra_status` table with pg_notify triggers

**Service Deployments:**
- AgentRegistry OSS in `platform-agentregistry` namespace
- Hub PostgREST with JWT authentication
- NATS cluster with proper subject permissions

**Auth Integration:**
- JWT enrichment with `spoke_cluster_id` claim
- Auth-proxy header injection (`X-Auth-Tenant-ID`, `X-Auth-Spoke-Cluster-ID`)
- Gateway routing for `/mcp/*` endpoints

### 5.2 Tenant Onboarding Integration

**Spoke Cluster Assignment:**
During tenant creation, opensbt MUST:
1. Assign `spoke_cluster_id` based on tenant tier (pool vs silo)
2. Store assignment in `tenants.spoke_cluster_id` column
3. Configure Ory Hydra to include claim in JWT tokens

**Kubeconfig Management:**
- Cluster API generates spoke cluster credentials
- Hub stores kubeconfigs as secrets for Deployment Adapter access
- Automatic rotation and credential refresh

## 6. Testing Strategy

### 6.1 Integration Testing Requirements

**End-to-End Flow:**
1. MCP client calls `create_agent` → agent registered in AgentRegistry
2. MCP client calls `deploy_agent` → CRD applied to Spoke cluster
3. Kagent Controller creates Pod → Spoke Controller detects status
4. Status flows: Spoke → Hub DB → NATS → AgentRegistry
5. MCP client calls `get_agent_status` → returns "deployed"

**Component Testing:**
- Deployment Adapter: Mock Kubernetes API, verify CRD generation
- Spoke Controller: Test status derivation logic with mock deployments
- NATS Subscriber: Test status mapping and AgentRegistry API calls
- MCP Server: Test tool registration and telemetry middleware

### 6.2 Observability Validation

**Metrics Verification:**
- `/metrics` endpoint exposes Prometheus format
- Grafana Alloy scrapes and forwards to VictoriaMetrics
- Grafana dashboards display agent deployment metrics

**Tracing Verification:**
- Traces propagate across all components
- Tenant context preserved in spans
- Error conditions properly recorded

**Logging Verification:**
- Structured JSON logs sent to OpenSearch
- Log correlation via trace IDs
- Error logs include sufficient context for debugging

This design addresses all missing components identified in the pending implementation review and provides a complete, production-ready integration for agents-core.