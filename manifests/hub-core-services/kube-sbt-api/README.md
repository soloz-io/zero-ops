# kube-sbt-api Deployment

## Overview
kube-sbt-api is the REST API server for the kube-sbt metering and billing system. It provides HTTP endpoints for:
- User management (with Ory Kratos integration)
- Usage queries (tenant and user-scoped)
- Entitlement checking
- Subscription management
- Invoice operations
- Read-only catalog access (meters, features, plans)

## Architecture
- **Namespace**: `hub-platform-billing`
- **Replicas**: 2 (HA deployment)
- **Node Selector**: Worker nodes only
- **Service Mesh**: Istio sidecar injection enabled

## Dependencies
- **OpenMeter API**: `openmeter-api.hub-platform-billing.svc.cluster.local`
- **Ory Kratos**: `kratos-public.hub-platform-identity.svc.cluster.local`
- **NATS**: `nats.hub-platform-messaging.svc.cluster.local:4222`
- **Redis**: `redis.hub-platform-billing.svc.cluster.local:6379`

## Deployment

### Prerequisites
1. OpenMeter deployed and healthy
2. Ory Stack (Kratos) deployed
3. NATS JetStream deployed
4. Redis deployed

### Build Docker Image
```bash
# Build the image
docker build -t ghcr.io/soloz-io/kube-sbt-api:latest -f cmd/kube-sbt-api/Dockerfile .

# Push to registry
docker push ghcr.io/soloz-io/kube-sbt-api:latest
```

### Deploy via ArgoCD
```bash
# Apply ArgoCD Application
kubectl apply -f manifests/argocd/apps/kube-sbt-api.yaml

# Wait for sync
argocd app sync kube-sbt-api
argocd app wait kube-sbt-api
```

### Deploy via kubectl (for testing)
```bash
# Apply manifests directly
kubectl apply -k manifests/hub-core-services/kube-sbt-api/

# Check deployment status
kubectl get pods -n hub-platform-billing -l app=kube-sbt-api
kubectl logs -n hub-platform-billing -l app=kube-sbt-api --tail=100
```

## Testing

### Health Check
```bash
kubectl port-forward -n hub-platform-billing svc/kube-sbt-api 8080:80
curl http://localhost:8080/health
```

### API Endpoints
All API endpoints require JWT authentication except `/health`.

Example authenticated request:
```bash
curl -H "Authorization: Bearer <JWT_TOKEN>" \
  http://localhost:8080/api/v1/tenants/tenant-123/usage
```

## Network Policy
The deployment includes a NetworkPolicy that:
- **Ingress**: Allows traffic only from AgentGateway
- **Egress**: Allows traffic to:
  - DNS (kube-dns)
  - OpenMeter API
  - Ory Kratos
  - NATS
  - Redis

## Monitoring
- **Liveness Probe**: `/health` endpoint (10s interval)
- **Readiness Probe**: `/health` endpoint (5s interval)
- **Metrics**: Exposed via Istio sidecar

## Troubleshooting

### Pod not starting
```bash
# Check pod events
kubectl describe pod -n hub-platform-billing -l app=kube-sbt-api

# Check logs
kubectl logs -n hub-platform-billing -l app=kube-sbt-api --tail=100
```

### Connection issues
```bash
# Test OpenMeter connectivity
kubectl exec -n hub-platform-billing -it <pod-name> -- wget -O- http://openmeter-api.hub-platform-billing.svc.cluster.local/health

# Test Kratos connectivity
kubectl exec -n hub-platform-billing -it <pod-name> -- wget -O- http://kratos-public.hub-platform-identity.svc.cluster.local/health/ready
```

### Network Policy issues
```bash
# Check network policy
kubectl get networkpolicy -n hub-platform-billing kube-sbt-api -o yaml

# Temporarily disable for testing
kubectl delete networkpolicy -n hub-platform-billing kube-sbt-api
```

## Configuration

### Environment Variables
- `PORT`: HTTP server port (default: 8080)
- `OPENMETER_URL`: OpenMeter API URL
- `ORY_KRATOS_URL`: Ory Kratos public API URL
- `NATS_URL`: NATS server URL
- `REDIS_URL`: Redis server URL

### Resource Limits
- **Requests**: 100m CPU, 128Mi memory
- **Limits**: 500m CPU, 512Mi memory

## Security
- Istio mTLS enabled for service-to-service communication
- SPIFFE workload identity via SPIRE
- Network policies enforce zero-trust networking
- JWT validation for all API endpoints
- RBAC enforcement for admin endpoints
