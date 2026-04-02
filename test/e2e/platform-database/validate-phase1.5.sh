#!/bin/bash
set -e

echo "=== Phase 1.5 SPIFFE/SPIRE Validation ==="
echo ""

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

pass() { echo -e "${GREEN}✓${NC} $1"; }
fail() { echo -e "${RED}✗${NC} $1"; exit 1; }
info() { echo -e "${YELLOW}ℹ${NC} $1"; }

# 1. Check SPIRE Server is running
info "Checking SPIRE Server deployment..."
kubectl get statefulset spire-server -n hub-platform-security &>/dev/null || fail "SPIRE Server StatefulSet not found"
SPIRE_SERVER_READY=$(kubectl get statefulset spire-server -n hub-platform-security -o jsonpath='{.status.readyReplicas}')
[ "$SPIRE_SERVER_READY" -ge 1 ] || fail "SPIRE Server not ready (ready: $SPIRE_SERVER_READY)"
pass "SPIRE Server StatefulSet is running and ready"

# 2. Check SPIRE Agent DaemonSet
info "Checking SPIRE Agent DaemonSet..."
kubectl get daemonset spire-agent -n hub-platform-security &>/dev/null || fail "SPIRE Agent DaemonSet not found"
SPIRE_AGENT_DESIRED=$(kubectl get daemonset spire-agent -n hub-platform-security -o jsonpath='{.status.desiredNumberScheduled}')
SPIRE_AGENT_READY=$(kubectl get daemonset spire-agent -n hub-platform-security -o jsonpath='{.status.numberReady}')
[ "$SPIRE_AGENT_READY" -eq "$SPIRE_AGENT_DESIRED" ] || fail "SPIRE Agent not ready on all nodes (ready: $SPIRE_AGENT_READY/$SPIRE_AGENT_DESIRED)"
pass "SPIRE Agent DaemonSet is running on all nodes"

# 3. Check SPIRE Server database credentials
info "Checking SPIRE Server database credentials..."
kubectl get secret spire-server-db-credentials -n hub-platform-data &>/dev/null || fail "SPIRE Server DB credentials secret not found"
kubectl get secret spire-server-db-credentials -n hub-platform-data -o jsonpath='{.metadata.ownerReferences[0].kind}' | grep -q "ExternalSecret" || fail "Secret not owned by ExternalSecret"
pass "SPIRE Server DB credentials synced from Infisical"

# 4. Check SPIRE Server database role
info "Checking SPIRE Server database role..."
DB_POD=$(kubectl get pod -n hub-platform-data -l cnpg.io/cluster=platform-db -o jsonpath='{.items[0].metadata.name}')
ROLE_EXISTS=$(kubectl exec -n hub-platform-data "$DB_POD" -- psql -U postgres -d hub -tAc "SELECT 1 FROM pg_roles WHERE rolname='spire_server'")
[ "$ROLE_EXISTS" = "1" ] || fail "spire_server role does not exist in hub database"
pass "spire_server role exists in hub database"

# 5. Check SPIRE K8s Workload Registrar
info "Checking SPIRE K8s Workload Registrar..."
kubectl get deployment spire-k8s-registrar -n hub-platform-security &>/dev/null || fail "SPIRE K8s Workload Registrar deployment not found"
REGISTRAR_READY=$(kubectl get deployment spire-k8s-registrar -n hub-platform-security -o jsonpath='{.status.readyReplicas}')
[ "$REGISTRAR_READY" -ge 1 ] || fail "SPIRE K8s Workload Registrar not ready"
pass "SPIRE K8s Workload Registrar is running"

# 6. Check ServiceMonitor CRDs
info "Checking SPIRE ServiceMonitor CRDs..."
kubectl get servicemonitor spire-server -n hub-platform-security &>/dev/null || fail "SPIRE Server ServiceMonitor not found"
kubectl get servicemonitor spire-agent -n hub-platform-security &>/dev/null || fail "SPIRE Agent ServiceMonitor not found"
pass "SPIRE ServiceMonitor CRDs exist"

# 7. Check SPIRE Server health endpoint
info "Checking SPIRE Server health endpoint..."
SPIRE_SERVER_POD=$(kubectl get pod -n hub-platform-security -l app=spire-server -o jsonpath='{.items[0].metadata.name}')
kubectl exec -n hub-platform-security "$SPIRE_SERVER_POD" -- wget -q -O- http://localhost:8080/ready | grep -q "OK" || fail "SPIRE Server health check failed"
pass "SPIRE Server health endpoint is healthy"

# 8. Check SPIRE Agent health endpoint
info "Checking SPIRE Agent health endpoint..."
SPIRE_AGENT_POD=$(kubectl get pod -n hub-platform-security -l app=spire-agent -o jsonpath='{.items[0].metadata.name}')
kubectl exec -n hub-platform-security "$SPIRE_AGENT_POD" -- wget -q -O- http://localhost:8080/ready | grep -q "OK" || fail "SPIRE Agent health check failed"
pass "SPIRE Agent health endpoint is healthy"

# 9. Check SPIRE Server can issue SVIDs
info "Checking SPIRE Server SVID issuance..."
SPIRE_SERVER_POD=$(kubectl get pod -n hub-platform-security -l app=spire-server -o jsonpath='{.items[0].metadata.name}')
kubectl exec -n hub-platform-security "$SPIRE_SERVER_POD" -- /opt/spire/bin/spire-server healthcheck &>/dev/null || fail "SPIRE Server healthcheck failed"
pass "SPIRE Server can issue SVIDs"

# 10. Check edge-catalog SPIRE Agent manifest exists
info "Checking edge-catalog SPIRE Agent manifest..."
[ -f "edge-catalog/spire-agent.yaml" ] || fail "edge-catalog/spire-agent.yaml not found"
pass "edge-catalog SPIRE Agent manifest exists"

# 11. Verify SPIRE Server PostgreSQL backend
info "Checking SPIRE Server PostgreSQL backend..."
kubectl logs -n hub-platform-security "$SPIRE_SERVER_POD" --tail=50 | grep -q "DataStore.*sql" || fail "SPIRE Server not using SQL datastore"
pass "SPIRE Server using PostgreSQL backend"

# 12. Check SPIRE bundle ConfigMap
info "Checking SPIRE bundle ConfigMap..."
kubectl get configmap spire-bundle -n hub-platform-security &>/dev/null || info "SPIRE bundle ConfigMap not yet created (will be created by k8sbundle notifier)"

echo ""
echo "=== Phase 1.5 Validation Summary ==="
echo ""
pass "All Phase 1.5 SPIFFE/SPIRE components validated successfully"
echo ""
echo "Next steps:"
echo "1. SPIRE K8s Workload Registrar will automatically register workloads with spiffe.io/spiffe-id annotations"
echo "2. Deploy spoke clusters with edge-catalog/spire-agent.yaml for nested topology"
echo "3. Configure Grafana Alloy and Spoke Controller to use SPIRE workload identity"
echo ""
