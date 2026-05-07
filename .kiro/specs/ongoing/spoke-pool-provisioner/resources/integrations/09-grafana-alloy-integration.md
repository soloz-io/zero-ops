# Grafana Alloy Integration Analysis

**Spec**: spoke-pool-provisioner  
**Component**: Grafana Alloy (Spoke) → VictoriaMetrics (Hub)  
**Purpose**: Metrics collection and forwarding from Spoke Pool to Hub for centralized observability  
**Status**: Analysis Complete

---

## 1. Overview

Grafana Alloy is an OpenTelemetry Collector distribution that scrapes metrics from Spoke Pool components and forwards them to Hub VictoriaMetrics via remote_write. It provides tenant-aware observability by injecting `cell_id` labels into all metrics.

**Key Principle**: Push-based metrics collection. Spokes push metrics to Hub (not pull), enabling Hub to remain stateless and Spokes to operate independently.

---

## 2. Spoke Pool Provisioning Context

### 2.1 Position in Provisioning Flow

```
1. Platform Admin applies SpokePool XR
2. Crossplane generates CAPI Cluster resources
3. CAPI provisions Hetzner VMs and Kubernetes cluster
4. ClusterResourceSet injects ArgoCD Agent + mTLS certs
5. ArgoCD Agent connects to Hub and pulls edge catalog
6. Edge catalog deploys (sync waves):
   - Wave 1: CNPG Cluster + PgBouncer
   - Wave 2: Atlas Operator
   - Wave 3: PostgREST
   - Wave 4: NATS Leaf Node
   - Wave 4: Grafana Alloy ← THIS COMPONENT
7. Alloy scrapes KSM, CNPG, NATS, PostgREST metrics
8. Alloy forwards metrics to Hub VictoriaMetrics
9. Grafana dashboards visualize metrics
```

### 2.2 Spec Requirements Mapping

| Requirement | Description | Implementation |
|-------------|-------------|----------------|
| **FR-2.4** | Alloy scrapes metrics and forwards to Hub VictoriaMetrics | DaemonSet deployment, prometheus.scrape + remote_write |
| **NFR-5.1** | All Spoke Pool metrics are forwarded to Hub VictoriaMetrics | Alloy remote_write with bearer token authentication |
| **NFR-5.3** | CNPG cluster metrics include: connection count, replication lag, disk usage | Alloy scrapes CNPG exporter endpoint |
| **AC-4** | Grafana Alloy forwards metrics to Hub VictoriaMetrics | prometheus.remote_write with static bearer token |

---

## 3. Grafana Alloy Architecture

### 3.1 Hub-and-Spoke Metrics Flow

```
┌─────────────────────────────────────────────────────────────┐
│ Hub Cluster                                                  │
│                                                              │
│  ┌────────────────────────────────────────────────────┐    │
│  │ VictoriaMetrics                                     │    │
│  │ - /api/v1/write endpoint                            │    │
│  │ - Bearer token authentication                       │    │
│  │ - Stores metrics with cell_id label                 │    │
│  └────────────────────────────────────────────────────┘    │
│                          ▲                                   │
│                          │ HTTPS remote_write                │
│                          │ (bearer token)                    │
└──────────────────────────┼───────────────────────────────────┘
                           │
         ┌─────────────────┼─────────────────┐
         │                 │                 │
         │                 │                 │
┌────────▼─────────┐  ┌───▼──────────┐  ┌──▼───────────┐
│ Spoke Pool 01    │  │ Spoke Pool 02│  │ Spoke Pool N │
│                  │  │              │  │              │
│ ┌──────────────┐ │  │ ┌──────────┐ │  │ ┌──────────┐ │
│ │ Grafana      │ │  │ │ Grafana  │ │  │ │ Grafana  │ │
│ │ Alloy        │ │  │ │ Alloy    │ │  │ │ Alloy    │ │
│ │ - Scrape KSM │ │  │ │          │ │  │ │          │ │
│ │ - Scrape CNPG│ │  │ │          │ │  │ │          │ │
│ │ - Scrape NATS│ │  │ │          │ │  │ │          │ │
│ │ - remote_write│ │  │ │          │ │  │ │          │ │
│ └──────────────┘ │  │ └──────────┘ │  │ └──────────┘ │
│        ▲         │  │              │  │              │
│        │ scrape  │  │              │  │              │
│  ┌─────┴──────┐  │  │              │  │              │
│  │ Targets    │  │  │              │  │              │
│  │ - KSM      │  │  │              │  │              │
│  │ - CNPG     │  │  │              │  │              │
│  │ - NATS     │  │  │              │  │              │
│  │ - PostgREST│  │  │              │  │              │
│  └────────────┘  │  │              │  │              │
└──────────────────┘  └──────────────┘  └──────────────┘
```

### 3.2 Metrics Collection Pattern

**Scrape Flow**:
```
1. Alloy discovers targets (KSM, CNPG, NATS, PostgREST)
2. Alloy scrapes metrics from each target (HTTP GET /metrics)
3. Alloy applies relabeling rules (inject cell_id label)
4. Alloy buffers metrics locally (in-memory queue)
5. Alloy forwards metrics to Hub VictoriaMetrics (remote_write)
6. VictoriaMetrics stores metrics with cell_id label
7. Grafana queries VictoriaMetrics (filtered by cell_id)
```

**Buffering During Hub Unavailability**:
```
1. Alloy detects Hub VictoriaMetrics is unreachable
2. Alloy buffers metrics in local WAL (Write-Ahead Log)
3. Alloy continues scraping targets (no data loss)
4. When Hub reconnects: Alloy drains WAL → forwards buffered metrics
5. VictoriaMetrics receives all metrics (eventual consistency)
```

---

## 4. Grafana Alloy Configuration

### 4.1 Alloy Deployment (Spoke Pool)

**Helm Chart**: `grafana/alloy` (official Grafana Alloy Helm chart)  
**Namespace**: `observability`  
**ArgoCD Sync Wave**: `4` (after CNPG, Atlas, PostgREST, NATS)

**DaemonSet Configuration**:
```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: grafana-alloy
  namespace: observability
spec:
  selector:
    matchLabels:
      app: grafana-alloy
  template:
    metadata:
      labels:
        app: grafana-alloy
    spec:
      serviceAccountName: grafana-alloy
      containers:
        - name: alloy
          image: grafana/alloy:v1.0.0  # Pinned version (not latest)
          args:
            - run
            - /etc/alloy/config.alloy
            - --server.http.listen-addr=0.0.0.0:12345
            - --storage.path=/var/lib/alloy/data
          ports:
            - containerPort: 12345
              name: http-metrics
          volumeMounts:
            - name: config
              mountPath: /etc/alloy
            - name: data
              mountPath: /var/lib/alloy/data
          env:
            - name: CELL_ID
              value: "spokepool-01"
            - name: VICTORIAMETRICS_URL
              value: "http://victoria-metrics-cluster-vminsert.platform-observability.svc:8480/insert/0/prometheus/api/v1/write"
            - name: VICTORIAMETRICS_TOKEN
              valueFrom:
                secretKeyRef:
                  name: alloy-remote-write-token
                  key: token
      volumes:
        - name: config
          configMap:
            name: grafana-alloy-config
        - name: data
          emptyDir: {}
```

### 4.2 Alloy Configuration File

**ConfigMap**: `grafana-alloy-config`

**config.alloy**:
```alloy
// Logging configuration
logging {
  level  = "info"
  format = "logfmt"
}

// Kubernetes Service Discovery for KSM
discovery.kubernetes "ksm" {
  role = "service"
  
  namespaces {
    names = ["kube-system"]
  }
  
  selectors {
    role  = "service"
    label = "app.kubernetes.io/name=kube-state-metrics"
  }
}

// Scrape Kubernetes State Metrics
prometheus.scrape "ksm" {
  targets    = discovery.kubernetes.ksm.targets
  forward_to = [prometheus.relabel.add_cell_id.receiver]
  
  scrape_interval = "30s"
  scrape_timeout  = "10s"
}

// Kubernetes Service Discovery for CNPG
discovery.kubernetes "cnpg" {
  role = "pod"
  
  namespaces {
    names = ["spoke-pool-system"]
  }
  
  selectors {
    role  = "pod"
    label = "cnpg.io/cluster=shared-cnpg"
  }
}

// Scrape CNPG Metrics
prometheus.scrape "cnpg" {
  targets    = discovery.kubernetes.cnpg.targets
  forward_to = [prometheus.relabel.add_cell_id.receiver]
  
  scrape_interval = "30s"
  scrape_timeout  = "10s"
}

// Kubernetes Service Discovery for NATS
discovery.kubernetes "nats" {
  role = "service"
  
  namespaces {
    names = ["spoke-pool-system"]
  }
  
  selectors {
    role  = "service"
    label = "app=nats"
  }
}

// Scrape NATS Metrics
prometheus.scrape "nats" {
  targets    = discovery.kubernetes.nats.targets
  forward_to = [prometheus.relabel.add_cell_id.receiver]
  
  scrape_interval = "30s"
  scrape_timeout  = "10s"
}

// Kubernetes Service Discovery for PostgREST
discovery.kubernetes "postgrest" {
  role = "service"
  
  namespaces {
    names = ["spoke-pool-system"]
  }
  
  selectors {
    role  = "service"
    label = "app=postgrest"
  }
}

// Scrape PostgREST Metrics
prometheus.scrape "postgrest" {
  targets    = discovery.kubernetes.postgrest.targets
  forward_to = [prometheus.relabel.add_cell_id.receiver]
  
  scrape_interval = "30s"
  scrape_timeout  = "10s"
}

// Relabel: Inject cell_id label
prometheus.relabel "add_cell_id" {
  forward_to = [prometheus.remote_write.victoriametrics.receiver]
  
  rule {
    target_label = "cell_id"
    replacement  = env("CELL_ID")
  }
}

// Remote Write to Hub VictoriaMetrics
prometheus.remote_write "victoriametrics" {
  endpoint {
    url = env("VICTORIAMETRICS_URL")
    
    // Bearer token authentication
    bearer_token = env("VICTORIAMETRICS_TOKEN")
    
    // TLS configuration
    tls_config {
      insecure_skip_verify = false
    }
    
    // Queue configuration for buffering
    queue_config {
      capacity          = 10000
      max_shards        = 10
      min_shards        = 1
      max_samples_per_send = 1000
      batch_send_deadline  = "5s"
      min_backoff          = "30ms"
      max_backoff          = "5s"
    }
    
    // Write-Ahead Log for durability
    write_relabel_config {
      source_labels = ["__name__"]
      regex         = "up|scrape_.*"
      action        = "drop"
    }
  }
  
  // WAL configuration
  wal {
    truncate_frequency = "2h"
    min_keepalive_time = "5m"
    max_keepalive_time = "8h"
  }
}
```

### 4.3 Service Account and RBAC

**ServiceAccount**:
```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: grafana-alloy
  namespace: observability
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: grafana-alloy
rules:
  # Discover services and pods
  - apiGroups: [""]
    resources: ["services", "pods", "endpoints"]
    verbs: ["get", "list", "watch"]
  # Discover nodes
  - apiGroups: [""]
    resources: ["nodes"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: grafana-alloy
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: grafana-alloy
subjects:
  - kind: ServiceAccount
    name: grafana-alloy
    namespace: observability
```

### 4.4 Bearer Token Secret

**Secret Generation** (Hub):
```bash
# Generate random bearer token
TOKEN=$(openssl rand -base64 32)

# Create secret in Hub (for VictoriaMetrics validation)
kubectl create secret generic victoriametrics-bearer-tokens \
  --from-literal=spokepool-01=$TOKEN \
  -n hub-platform

# Create secret in Spoke Pool (for Alloy authentication)
kubectl create secret generic alloy-remote-write-token \
  --from-literal=token=$TOKEN \
  -n observability \
  --context=spokepool-01
```

**VictoriaMetrics Configuration** (Hub):
```yaml
# VictoriaMetrics with bearer token authentication
apiVersion: v1
kind: ConfigMap
metadata:
  name: victoriametrics-config
  namespace: hub-platform
data:
  victoriametrics.yaml: |
    # Enable bearer token authentication
    auth:
      enabled: true
      bearer_tokens:
        - token_file: /etc/vm-auth/bearer-tokens
```

---

## 5. Metrics Targets

### 5.1 Kubernetes State Metrics (KSM)

**Deployment**:
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kube-state-metrics
  namespace: kube-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: kube-state-metrics
  template:
    metadata:
      labels:
        app.kubernetes.io/name: kube-state-metrics
    spec:
      containers:
        - name: kube-state-metrics
          image: registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.10.0
          ports:
            - containerPort: 8080
              name: http-metrics
```

**Metrics Exposed**:
- `kube_pod_status_phase{phase="Running|Pending|Failed"}`
- `kube_deployment_status_replicas{deployment="..."}`
- `kube_node_status_condition{condition="Ready"}`
- `kube_persistentvolumeclaim_status_phase{phase="Bound"}`

### 5.2 CNPG Metrics

**Metrics Endpoint**: `http://shared-cnpg-rw.spoke-pool-system.svc:9187/metrics`

**Metrics Exposed**:
- `cnpg_pg_stat_database_xact_commit{datname="postgres"}` - Transaction commits
- `cnpg_pg_stat_database_numbackends{datname="postgres"}` - Active connections
- `cnpg_pg_replication_lag_seconds` - Replication lag
- `cnpg_pg_database_size_bytes{datname="postgres"}` - Database size
- `cnpg_pg_stat_bgwriter_buffers_alloc` - Buffer allocations

### 5.3 NATS Metrics

**Metrics Endpoint**: `http://nats.spoke-pool-system.svc:7777/metrics`

**Metrics Exposed**:
- `nats_leafnode_connected{server="spokepool-01"}` - Leaf node connection status
- `nats_leafnode_in_msgs{server="spokepool-01"}` - Inbound messages
- `nats_leafnode_out_msgs{server="spokepool-01"}` - Outbound messages
- `nats_jetstream_storage_bytes{server="spokepool-01"}` - JetStream storage usage

### 5.4 PostgREST Metrics

**Metrics Endpoint**: `http://postgrest.spoke-pool-system.svc:3000/metrics`

**Metrics Exposed** (if PostgREST Prometheus exporter enabled):
- `postgrest_request_duration_seconds` - Request latency
- `postgrest_requests_total{method="GET|POST",status="200|400|500"}` - Request count
- `postgrest_active_connections` - Active database connections

---

## 6. Observability

### 6.1 Alloy Metrics

**Prometheus Metrics** (exposed by Alloy):
```
# Scrape status
prometheus_scrape_samples_scraped{job="ksm"} 1234
prometheus_scrape_duration_seconds{job="ksm"} 0.5

# Remote write status
prometheus_remote_write_samples_total{url="https://victoriametrics..."} 567890
prometheus_remote_write_samples_failed_total{url="https://victoriametrics..."} 0
prometheus_remote_write_highest_timestamp_in_seconds{url="https://victoriametrics..."} 1678901234

# WAL status
prometheus_remote_write_wal_samples_appended_total 567890
prometheus_remote_write_wal_storage_size_bytes 1048576
```

### 6.2 Grafana Dashboards

**Cell Health Dashboard** (Hub):
```yaml
# Grafana dashboard JSON
{
  "title": "Spoke Pool Cell Health",
  "panels": [
    {
      "title": "Cell Status",
      "targets": [
        {
          "expr": "up{job=\"kube-state-metrics\", cell_id=\"$cell_id\"}"
        }
      ]
    },
    {
      "title": "CNPG Connection Count",
      "targets": [
        {
          "expr": "cnpg_pg_stat_database_numbackends{cell_id=\"$cell_id\"}"
        }
      ]
    },
    {
      "title": "NATS Leaf Node Status",
      "targets": [
        {
          "expr": "nats_leafnode_connected{cell_id=\"$cell_id\"}"
        }
      ]
    }
  ],
  "templating": {
    "list": [
      {
        "name": "cell_id",
        "type": "query",
        "query": "label_values(up, cell_id)"
      }
    ]
  }
}
```

### 6.3 Alerts

**Prometheus Alerts** (Hub):
```yaml
groups:
  - name: spoke_pool_alloy
    rules:
      - alert: AlloyRemoteWriteFailing
        expr: rate(prometheus_remote_write_samples_failed_total[5m]) > 0
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "Alloy remote write failing"
          description: "Alloy in {{ $labels.cell_id }} is failing to write metrics to Hub"
      
      - alert: AlloyScrapeFailing
        expr: up{job=~"ksm|cnpg|nats|postgrest"} == 0
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Alloy scrape target down"
          description: "Alloy in {{ $labels.cell_id }} cannot scrape {{ $labels.job }}"
      
      - alert: CNPGHighConnectionCount
        expr: cnpg_pg_stat_database_numbackends > 400
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "CNPG connection count high"
          description: "CNPG in {{ $labels.cell_id }} has {{ $value }} connections (threshold: 400)"
```

---

## 7. Deployment Validation

### 7.1 Acceptance Criteria Validation

| Acceptance Criteria | Validation Method | Expected Result |
|---------------------|-------------------|-----------------|
| **AC-4**: Grafana Alloy forwards metrics to Hub VictoriaMetrics | `kubectl logs -n observability grafana-alloy-xxx` | Shows successful remote_write |
| **NFR-5.1**: All Spoke Pool metrics are forwarded to Hub VictoriaMetrics | Query VictoriaMetrics: `up{cell_id="spokepool-01"}` | Returns metrics from all targets |
| **NFR-5.3**: CNPG cluster metrics include connection count, replication lag, disk usage | Query: `cnpg_pg_stat_database_numbackends{cell_id="spokepool-01"}` | Returns connection count |
| **FR-2.4**: Alloy scrapes KSM and CNPG metrics | Check Alloy UI: `http://localhost:12345` | Shows active scrape targets |

### 7.2 Integration Test Script

```bash
#!/bin/bash
# Test Grafana Alloy integration

SPOKE_CONTEXT="spokepool-01"
HUB_CONTEXT="hub"

echo "1. Verify Alloy deployment"
kubectl --context=$SPOKE_CONTEXT get daemonset -n observability grafana-alloy

echo "2. Check Alloy scrape targets"
kubectl --context=$SPOKE_CONTEXT port-forward -n observability daemonset/grafana-alloy 12345:12345 &
sleep 5
curl -s http://localhost:12345/targets | jq '.activeTargets[] | {job: .labels.job, health: .health}'

echo "3. Verify metrics are being scraped"
kubectl --context=$SPOKE_CONTEXT logs -n observability daemonset/grafana-alloy --tail=50 | grep "scrape"

echo "4. Check remote_write status"
kubectl --context=$SPOKE_CONTEXT logs -n observability daemonset/grafana-alloy --tail=50 | grep "remote_write"

echo "5. Query Hub VictoriaMetrics for Spoke metrics"
kubectl --context=$HUB_CONTEXT port-forward -n hub-platform svc/victoriametrics 8428:8428 &
sleep 5
curl -s "http://localhost:8428/api/v1/query?query=up{cell_id=\"spokepool-01\"}" | jq '.data.result[] | {metric: .metric.__name__, cell_id: .metric.cell_id, value: .value[1]}'

echo "6. Verify CNPG metrics in Hub"
curl -s "http://localhost:8428/api/v1/query?query=cnpg_pg_stat_database_numbackends{cell_id=\"spokepool-01\"}" | jq '.data.result[] | {datname: .metric.datname, connections: .value[1]}'

echo "7. Verify NATS metrics in Hub"
curl -s "http://localhost:8428/api/v1/query?query=nats_leafnode_connected{cell_id=\"spokepool-01\"}" | jq '.data.result[] | {server: .metric.server, connected: .value[1]}'
```

---

## 8. Design Patterns Alignment

### 8.1 sbt-patterns Compliance

| Pattern | Implementation | Compliance |
|---------|----------------|------------|
| **Tenant-Aware Observability** | Inject `cell_id` label into all metrics | ✅ Compliant |
| **Push-Based Metrics** | Alloy pushes to Hub (not pull) | ✅ Compliant |
| **Buffering During Outages** | WAL buffers metrics when Hub unreachable | ✅ Compliant |
| **Bearer Token Authentication** | Static token per Spoke Pool | ✅ Compliant |

### 8.2 GitOps-First Principle

**Compliant**: Grafana Alloy is deployed via ArgoCD ApplicationSet (edge catalog). Configuration is stored in Git as ConfigMap, not dynamically generated.

---

## 9. Summary

Grafana Alloy provides centralized observability for Spoke Pool clusters by scraping metrics from KSM, CNPG, NATS, and PostgREST, then forwarding them to Hub VictoriaMetrics with `cell_id` labels. The push-based architecture with WAL buffering ensures no data loss during Hub unavailability.

**Key Implementation Points**:
1. Deploy Grafana Alloy as DaemonSet in ArgoCD sync wave 4
2. Use Kubernetes service discovery for automatic target detection
3. Inject `cell_id` label via prometheus.relabel for tenant-aware metrics
4. Authenticate to Hub VictoriaMetrics using static bearer token
5. Enable WAL for buffering during Hub unavailability (no data loss)
6. Scrape KSM (cluster state), CNPG (database), NATS (messaging), PostgREST (API)
7. Monitor remote_write status and scrape health with Prometheus alerts
8. Validate with integration tests (scrape targets, remote_write, Hub queries)

**Next Steps**: All edge catalog dependencies analyzed. Proceed to design phase.
