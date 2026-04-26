# NATS Leaf Node Integration Analysis

**Spec**: spoke-pool-provisioner  
**Component**: NATS Leaf Node (Spoke) → NATS JetStream (Hub)  
**Purpose**: Billing event forwarding from Spoke Pool to Hub with buffering during Hub unavailability  
**Status**: Analysis Complete

---

## 1. Overview

NATS Leaf Node enables Spoke Pool clusters to forward billing events to Hub JetStream while maintaining operational independence. The leaf node acts as a transparent bridge, buffering events locally when Hub is unreachable and forwarding them when connectivity is restored.

**Key Principle**: NATS is strictly for coordination (5%) such as billing events and lifecycle notifications. Infrastructure provisioning (95%) is handled via GitOps and Status Controller.

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
   - Wave 4: NATS Leaf Node ← THIS COMPONENT
   - Wave 4: Spire Agent
   - Wave 4: Grafana Alloy
7. Tenant workloads publish billing events to local NATS
8. Leaf Node forwards events to Hub JetStream
```

### 2.2 Spec Requirements Mapping

| Requirement | Description | Implementation |
|-------------|-------------|----------------|
| **FR-2.3** | NATS Leaf Node connects to Hub JetStream for billing event forwarding | Leaf node deployed in wave 4, connects to Hub using mTLS |
| **NFR-3.3** | NATS Leaf Node buffers events during Hub unavailability (no data loss) | JetStream persistence in Spoke, automatic reconnect with buffering |
| **AC-4** | NATS Leaf Node connects to Hub JetStream | Leaf node configuration with `remotes` pointing to Hub NATS |

---

## 3. NATS Leaf Node Architecture

### 3.1 Hub-and-Spoke Topology

```
┌─────────────────────────────────────────────────────────────┐
│ Hub Cluster                                                  │
│                                                              │
│  ┌────────────────────────────────────────────────────┐    │
│  │ NATS JetStream (Full Server)                       │    │
│  │ - Subjects: spoke.{cell-id}.billing.usage          │    │
│  │ - Subjects: spoke.{cell-id}.lifecycle.>            │    │
│  │ - mTLS Server (accepts leaf connections)           │    │
│  │ - Persistent storage for all events                │    │
│  └────────────────────────────────────────────────────┘    │
│                          ▲                                   │
│                          │ mTLS                              │
│                          │ (leaf connections)                │
└──────────────────────────┼───────────────────────────────────┘
                           │
         ┌─────────────────┼─────────────────┐
         │                 │                 │
         │                 │                 │
┌────────▼─────────┐  ┌───▼──────────┐  ┌──▼───────────┐
│ Spoke Pool 01    │  │ Spoke Pool 02│  │ Spoke Pool N │
│                  │  │              │  │              │
│ ┌──────────────┐ │  │ ┌──────────┐ │  │ ┌──────────┐ │
│ │ NATS Leaf    │ │  │ │ NATS Leaf│ │  │ │ NATS Leaf│ │
│ │ Node         │ │  │ │ Node     │ │  │ │ Node     │ │
│ │ - Local pub  │ │  │ │          │ │  │ │          │ │
│ │ - Buffering  │ │  │ │          │ │  │ │          │ │
│ │ - Forward    │ │  │ │          │ │  │ │          │ │
│ └──────────────┘ │  │ └──────────┘ │  │ └──────────┘ │
│        ▲         │  │              │  │              │
│        │         │  │              │  │              │
│  ┌─────┴──────┐  │  │              │  │              │
│  │ Tenant     │  │  │              │  │              │
│  │ Workloads  │  │  │              │  │              │
│  │ (publish)  │  │  │              │  │              │
│  └────────────┘  │  │              │  │              │
└──────────────────┘  └──────────────┘  └──────────────┘
```

### 3.2 Event Flow Pattern

**Standard Event Flow (Hub Available)**:
```
1. Tenant workload publishes: NATS.publish("spoke.cell-01.billing.usage", event)
2. Leaf Node receives event locally (nats://nats.spoke-pool.svc:4222)
3. Leaf Node forwards to Hub JetStream via mTLS connection
4. Hub JetStream persists event
5. Hub Event Router consumes event → writes to Hub Centralised DB
```

**Degraded Event Flow (Hub Unavailable)**:
```
1. Tenant workload publishes: NATS.publish("spoke.cell-01.billing.usage", event)
2. Leaf Node receives event locally
3. Leaf Node detects Hub connection failure
4. Leaf Node buffers event in local JetStream (persistent storage)
5. Leaf Node attempts reconnection with exponential backoff
6. When Hub reconnects: Leaf Node drains buffer → forwards all events
7. Hub JetStream persists events (no data loss)
```

---

## 4. NATS Leaf Node Configuration

### 4.1 Leaf Node Deployment (Spoke Pool)

**Helm Chart**: `nats/nats` (official NATS Helm chart)  
**Namespace**: `spoke-pool-system`  
**ArgoCD Sync Wave**: `4` (after CNPG, Atlas, PostgREST)

**values.yaml** (Spoke Pool):
```yaml
nats:
  # Leaf node configuration
  leafnodes:
    enabled: true
    # Connect to Hub NATS as a leaf node
    remotes:
      - url: "nats-leaf://nats.hub-platform-messaging.svc.cluster.local:7422"
        # mTLS authentication
        tls:
          ca_file: "/etc/nats-certs/ca.crt"
          cert_file: "/etc/nats-certs/tls.crt"
          key_file: "/etc/nats-certs/tls.key"
        # Credentials for authentication (alternative to mTLS)
        # credentials: "/etc/nats-creds/leaf.creds"
  
  # JetStream for local buffering
  jetstream:
    enabled: true
    fileStore:
      pvc:
        size: 10Gi
        storageClassName: hcloud-volumes
    # Memory limits
    memStorage:
      enabled: true
      size: 1Gi
  
  # Resource limits
  resources:
    requests:
      cpu: 100m
      memory: 256Mi
    limits:
      cpu: 500m
      memory: 512Mi
  
  # Service configuration
  service:
    type: ClusterIP
    ports:
      client: 4222
      leafnodes: 7422
  
  # TLS certificate volume mount
  extraVolumes:
    - name: nats-certs
      secret:
        secretName: nats-leaf-mtls-cert
  
  extraVolumeMounts:
    - name: nats-certs
      mountPath: /etc/nats-certs
      readOnly: true
```

### 4.2 Hub NATS Configuration

**Helm Chart**: `nats/nats` (official NATS Helm chart)  
**Namespace**: `hub-platform`  
**Mode**: Full NATS Server with JetStream

**values.yaml** (Hub):
```yaml
nats:
  # Accept leaf node connections
  leafnodes:
    enabled: true
    port: 7422
    # mTLS for leaf connections
    tls:
      ca_file: "/etc/nats-certs/ca.crt"
      cert_file: "/etc/nats-certs/tls.crt"
      key_file: "/etc/nats-certs/tls.key"
      verify: true
  
  # JetStream for persistent event storage
  jetstream:
    enabled: true
    fileStore:
      pvc:
        size: 100Gi
        storageClassName: hcloud-volumes
    memStorage:
      enabled: true
      size: 10Gi
  
  # Cluster mode (HA)
  cluster:
    enabled: true
    replicas: 3
  
  # Resource limits
  resources:
    requests:
      cpu: 500m
      memory: 1Gi
    limits:
      cpu: 2000m
      memory: 4Gi
```

### 4.3 Subject Mapping

**Standard Event Subjects** (from sbt-patterns):

| Subject Pattern | Source | Purpose | Example |
|----------------|--------|---------|---------|
| `spoke.{cell-id}.billing.usage` | Spoke Pool | Billing events | `spoke.cell-01.billing.usage` |
| `spoke.{cell-id}.lifecycle.created` | Spoke Pool | Tenant created | `spoke.cell-01.lifecycle.created` |
| `spoke.{cell-id}.lifecycle.deleted` | Spoke Pool | Tenant deleted | `spoke.cell-01.lifecycle.deleted` |
| `spoke.{cell-id}.lifecycle.>` | Spoke Pool | All lifecycle events | `spoke.cell-01.lifecycle.*` |
| `opensbt.opensbt_billingSuccess` | Hub | Billing success | `opensbt.opensbt_billingSuccess` |
| `opensbt.opensbt_billingFailure` | Hub | Billing failure | `opensbt.opensbt_billingFailure` |

**Event Structure** (from sbt-patterns):
```json
{
  "id": "6a7e8feb-b491-4cf7-a9f1-bf3703467718",
  "version": "1.0",
  "detailType": "opensbt_billingSuccess",
  "source": "opensbt.control.plane",
  "time": "2026-03-16T18:43:48Z",
  "detail": {
    "tenantId": "e6878e03-ae2c-43ed-a863-08314487318b",
    "cellId": "cell-01",
    "usageAmount": 1000,
    "currency": "USD"
  }
}
```

---

## 5. mTLS Certificate Management

### 5.1 Certificate Generation (Hub)

**cert-manager Certificate CR** (Hub):
```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: nats-leaf-ca
  namespace: hub-platform-messaging
spec:
  isCA: true
  commonName: nats-leaf-ca
  secretName: nats-leaf-ca-secret
  privateKey:
    algorithm: RSA
    size: 4096
  issuerRef:
    name: selfsigned-issuer
    kind: ClusterIssuer
---
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: nats-leaf-ca-issuer
  namespace: hub-platform-messaging
spec:
  ca:
    secretName: nats-leaf-ca-secret
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: nats-hub-server-cert
  namespace: hub-platform-messaging
spec:
  secretName: nats-hub-server-cert
  duration: 2160h # 90 days
  renewBefore: 168h # 7 days before expiration
  commonName: nats.hub-platform-messaging.svc.cluster.local
  dnsNames:
    - nats.hub-platform-messaging.svc.cluster.local
    - nats.hub-platform-messaging.svc
    - nats
  issuerRef:
    name: nats-leaf-ca-issuer
    kind: Issuer
```

### 5.2 Certificate Injection (Spoke Pool)

**ClusterResourceSet** (injected during CAPI bootstrap):
```yaml
apiVersion: addons.cluster.x-k8s.io/v1beta1
kind: ClusterResourceSet
metadata:
  name: spokepool-01-nats-certs
  namespace: hub-platform-capi
spec:
  clusterSelector:
    matchLabels:
      cluster.x-k8s.io/cluster-name: spokepool-01
  resources:
    - name: nats-leaf-ca-secret
      kind: Secret
    - name: nats-leaf-client-cert
      kind: Secret
```

**Certificate for Spoke Leaf Node**:
```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: nats-leaf-client-cert-spokepool-01
  namespace: hub-platform
spec:
  secretName: nats-leaf-client-cert-spokepool-01
  duration: 2160h # 90 days
  renewBefore: 168h # 7 days before expiration
  commonName: spokepool-01.leaf.nats
  dnsNames:
    - spokepool-01.leaf.nats
  issuerRef:
    name: nats-leaf-ca-issuer
    kind: Issuer
```

### 5.3 Certificate Rotation

**Automatic Rotation** (cert-manager):
- Certificates auto-renew 7 days before expiration
- NATS server hot-reloads certificates (no restart required)
- Leaf nodes reconnect automatically with new certificates

**Monitoring**:
```yaml
# Prometheus alert for certificate expiration
- alert: NATSLeafCertificateExpiringSoon
  expr: certmanager_certificate_expiration_timestamp_seconds{name=~"nats-leaf.*"} - time() < 604800
  for: 1h
  labels:
    severity: warning
  annotations:
    summary: "NATS Leaf certificate expiring soon"
    description: "Certificate {{ $labels.name }} expires in less than 7 days"
```

---

## 6. Event Buffering and Reliability

### 6.1 JetStream Persistence (Spoke Pool)

**Stream Configuration** (automatic):
```yaml
# NATS JetStream automatically creates streams for subjects
# No manual configuration required - streams are created on first publish
```

**Buffering Behavior**:
1. Leaf node receives event from tenant workload
2. If Hub connection is active: Forward immediately
3. If Hub connection is down:
   - Store event in local JetStream (persistent disk)
   - Continue accepting new events (no blocking)
   - Attempt reconnection with exponential backoff
4. When Hub reconnects: Drain buffer in order (FIFO)

### 6.2 Reconnection Strategy

**Exponential Backoff**:
```
Attempt 1: 1 second
Attempt 2: 2 seconds
Attempt 3: 4 seconds
Attempt 4: 8 seconds
Attempt 5: 16 seconds
Max delay: 30 seconds
```

**Connection Health Check**:
- Leaf node sends PING every 2 minutes
- Hub responds with PONG
- If 3 consecutive PINGs fail: Mark connection as down
- Trigger reconnection logic

### 6.3 Data Loss Prevention

**Guarantees**:
- **At-least-once delivery**: Events may be delivered multiple times (idempotency required)
- **No data loss**: JetStream persistence ensures events survive Spoke Pod restarts
- **Ordered delivery**: Events forwarded in FIFO order per subject

**Idempotency Pattern** (from sbt-patterns):
```go
// Hub Event Router checks if event already processed
func (r *EventRouter) ProcessEvent(event Event) error {
    // Check if event ID already processed
    if r.storage.IsEventProcessed(event.ID) {
        return nil // Skip duplicate
    }
    
    // Process event
    err := r.handleEvent(event)
    if err != nil {
        return err
    }
    
    // Mark as processed
    return r.storage.MarkEventProcessed(event.ID)
}
```

---

## 7. Observability

### 7.1 NATS Metrics

**Prometheus Metrics** (exposed by NATS):
```
# Connection status
nats_leafnode_connected{server="spokepool-01"} 1

# Message counts
nats_leafnode_in_msgs{server="spokepool-01"} 1234
nats_leafnode_out_msgs{server="spokepool-01"} 1234

# Byte counts
nats_leafnode_in_bytes{server="spokepool-01"} 567890
nats_leafnode_out_bytes{server="spokepool-01"} 567890

# JetStream storage
nats_jetstream_storage_bytes{server="spokepool-01"} 1048576
nats_jetstream_storage_reserved_bytes{server="spokepool-01"} 10737418240
```

### 7.2 Grafana Alloy Scraping

**Alloy Configuration** (Spoke Pool):
```yaml
prometheus.scrape "nats_leaf" {
  targets = [{
    __address__ = "nats.spoke-pool-system.svc:7777",
  }]
  
  forward_to = [prometheus.remote_write.hub.receiver]
  
  # Add cell_id label
  relabel_configs = [{
    target_label = "cell_id",
    replacement  = "spokepool-01",
  }]
}
```

### 7.3 Alerts

**Prometheus Alerts**:
```yaml
groups:
  - name: nats_leaf_node
    rules:
      - alert: NATSLeafNodeDisconnected
        expr: nats_leafnode_connected == 0
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "NATS Leaf Node disconnected from Hub"
          description: "Spoke Pool {{ $labels.cell_id }} leaf node has been disconnected for 5 minutes"
      
      - alert: NATSLeafNodeBufferGrowing
        expr: rate(nats_jetstream_storage_bytes[5m]) > 0
        for: 15m
        labels:
          severity: warning
        annotations:
          summary: "NATS Leaf Node buffer growing"
          description: "Spoke Pool {{ $labels.cell_id }} is buffering events (Hub may be unreachable)"
```

---

## 8. Integration with Other Components

### 8.1 Tenant Workload Publishing

**Go Client Example**:
```go
import "github.com/nats-io/nats.go"

// Connect to local NATS in Spoke Pool
nc, err := nats.Connect("nats://nats.spoke-pool-system.svc:4222")
if err != nil {
    return err
}
defer nc.Close()

// Publish billing event
event := Event{
    ID:         uuid.New().String(),
    Version:    "1.0",
    DetailType: "opensbt_billingSuccess",
    Source:     "opensbt.control.plane",
    Time:       time.Now(),
    Detail: map[string]interface{}{
        "tenantId":    "acme",
        "cellId":      "cell-01",
        "usageAmount": 1000,
        "currency":    "USD",
    },
}

data, _ := json.Marshal(event)
err = nc.Publish("spoke.cell-01.billing.usage", data)
```

### 8.2 Hub Event Router Consumption

**Go Consumer Example** (Hub):
```go
import "github.com/nats-io/nats.go"

// Connect to Hub NATS JetStream
nc, err := nats.Connect("nats://nats.hub-platform-messaging.svc:4222")
if err != nil {
    return err
}
defer nc.Close()

js, err := nc.JetStream()
if err != nil {
    return err
}

// Subscribe to all billing events from all cells
sub, err := js.Subscribe("spoke.*.billing.usage", func(msg *nats.Msg) {
    var event Event
    json.Unmarshal(msg.Data, &event)
    
    // Process event (write to Hub Centralised DB)
    err := processEvent(event)
    if err != nil {
        msg.Nak() // Negative acknowledgment - retry
    } else {
        msg.Ack() // Acknowledge - remove from stream
    }
})
```

---

## 9. Deployment Validation

### 9.1 Acceptance Criteria Validation

| Acceptance Criteria | Validation Method | Expected Result |
|---------------------|-------------------|-----------------|
| **AC-4**: NATS Leaf Node connects to Hub JetStream | `kubectl exec -n spoke-pool-system nats-0 -- nats server info` | Shows `leafnode_connected: true` |
| **NFR-3.3**: Leaf Node buffers events during Hub unavailability | Simulate Hub outage, publish events, verify buffer growth | JetStream storage increases, events delivered after reconnect |
| **FR-2.3**: Billing events forwarded to Hub | Publish event in Spoke, verify in Hub JetStream | Event appears in Hub stream `spoke.cell-01.billing.usage` |

### 9.2 Integration Test Script

```bash
#!/bin/bash
# Test NATS Leaf Node integration

SPOKE_CONTEXT="spokepool-01"
HUB_CONTEXT="hub"

echo "1. Verify Leaf Node deployment"
kubectl --context=$SPOKE_CONTEXT get pod -n spoke-pool-system -l app=nats

echo "2. Check Leaf Node connection status"
kubectl --context=$SPOKE_CONTEXT exec -n spoke-pool-system nats-0 -- \
  nats server info | grep leafnode

echo "3. Publish test event from Spoke"
kubectl --context=$SPOKE_CONTEXT exec -n spoke-pool-system nats-0 -- \
  nats pub spoke.cell-01.billing.usage '{"test": "event"}'

echo "4. Verify event received in Hub"
kubectl --context=$HUB_CONTEXT exec -n hub-platform nats-0 -- \
  nats stream info spoke.cell-01.billing.usage

echo "5. Simulate Hub outage (scale Hub NATS to 0)"
kubectl --context=$HUB_CONTEXT scale statefulset nats -n hub-platform-messaging --replicas=0

echo "6. Publish events during outage"
for i in {1..10}; do
  kubectl --context=$SPOKE_CONTEXT exec -n spoke-pool-system nats-0 -- \
    nats pub spoke.cell-01.billing.usage "{\"event\": $i}"
done

echo "7. Check Spoke JetStream buffer"
kubectl --context=$SPOKE_CONTEXT exec -n spoke-pool-system nats-0 -- \
  nats stream ls

echo "8. Restore Hub NATS"
kubectl --context=$HUB_CONTEXT scale statefulset nats -n hub-platform-messaging --replicas=3

echo "9. Wait for reconnection and verify events delivered"
sleep 30
kubectl --context=$HUB_CONTEXT exec -n hub-platform-messaging nats-0 -- \
  nats stream info spoke.cell-01.billing.usage | grep "Messages:"
```

---

## 10. Design Patterns Alignment

### 10.1 sbt-patterns Compliance

| Pattern | Implementation | Compliance |
|---------|----------------|------------|
| **Event-Driven Communication** | NATS for coordination (5%), not infrastructure | ✅ Compliant |
| **Standard Events** | Uses `opensbt_*` and `spoke.*` subjects | ✅ Compliant |
| **Idempotency** | Hub Event Router checks `IsEventProcessed()` | ✅ Compliant |
| **At-Least-Once Delivery** | JetStream persistence + acknowledgments | ✅ Compliant |
| **Buffering** | Local JetStream during Hub unavailability | ✅ Compliant |

### 10.2 GitOps-First Principle

**Compliant**: NATS Leaf Node is deployed via ArgoCD ApplicationSet (edge catalog), not via NATS events. Configuration is stored in Git, not dynamically generated.

---

## 11. Summary

NATS Leaf Node provides reliable, buffered event forwarding from Spoke Pool clusters to Hub JetStream. The leaf node architecture ensures operational independence (Spokes continue functioning during Hub outages) while maintaining eventual consistency for billing and lifecycle events.

**Key Implementation Points**:
1. Deploy NATS Leaf Node in ArgoCD sync wave 4 (after CNPG, Atlas, PostgREST)
2. Use mTLS authentication with cert-manager-generated certificates
3. Enable JetStream in Spoke for local buffering (10Gi persistent volume)
4. Configure `remotes` pointing to Hub NATS (nats-leaf://nats.hub-platform-messaging.svc:7422)
5. Use subject pattern: `spoke.{cell-id}.billing.usage` for billing events
6. Implement idempotency in Hub Event Router (check `IsEventProcessed()`)
7. Monitor connection status and buffer growth with Prometheus alerts
8. Validate with integration tests (publish during Hub outage, verify delivery after reconnect)

**Next Steps**: Proceed to next dependency analysis (Spire Agent for mTLS workload identity).
