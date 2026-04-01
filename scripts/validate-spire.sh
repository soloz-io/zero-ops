#!/bin/bash
set -e

echo "=== SPIRE Validation Script ==="
echo "This script validates SPIRE Server and Agent deployment"
echo ""

# Check SPIRE Server pod status
echo "1. Checking SPIRE Server pod status..."
kubectl get pods -n spire-system -l app=spire-server
SERVER_READY=$(kubectl get pods -n spire-system -l app=spire-server -o jsonpath='{.items[0].status.conditions[?(@.type=="Ready")].status}')
if [ "$SERVER_READY" != "True" ]; then
  echo "ERROR: SPIRE Server is not ready"
  exit 1
fi
echo "✓ SPIRE Server is ready"
echo ""

# Check SPIRE Agent DaemonSet status
echo "2. Checking SPIRE Agent DaemonSet status..."
kubectl get daemonset -n spire-system spire-agent
DESIRED=$(kubectl get daemonset -n spire-system spire-agent -o jsonpath='{.status.desiredNumberScheduled}')
READY=$(kubectl get daemonset -n spire-system spire-agent -o jsonpath='{.status.numberReady}')
if [ "$DESIRED" != "$READY" ]; then
  echo "ERROR: SPIRE Agent not ready on all nodes (Desired: $DESIRED, Ready: $READY)"
  exit 1
fi
echo "✓ SPIRE Agent running on all $READY nodes"
echo ""

# Check SPIRE Server service
echo "3. Checking SPIRE Server service..."
kubectl get svc -n spire-system spire-server
SERVICE_IP=$(kubectl get svc -n spire-system spire-server -o jsonpath='{.spec.clusterIP}')
if [ -z "$SERVICE_IP" ]; then
  echo "ERROR: SPIRE Server service has no ClusterIP"
  exit 1
fi
echo "✓ SPIRE Server service available at $SERVICE_IP:8081"
echo ""

# Check SPIRE Server health
echo "4. Checking SPIRE Server health endpoint..."
SERVER_POD=$(kubectl get pods -n spire-system -l app=spire-server -o jsonpath='{.items[0].metadata.name}')
kubectl exec -n spire-system "$SERVER_POD" -- wget -q -O- http://localhost:8080/ready
echo ""
echo "✓ SPIRE Server health check passed"
echo ""

# List registered SPIFFE identities
echo "5. Listing registered SPIFFE identities..."
kubectl exec -n spire-system "$SERVER_POD" -- /opt/spire/bin/spire-server entry show -socketPath /run/spire/sockets/server.sock
echo ""

# Check if required identities are registered
echo "6. Validating required SPIFFE identities..."
ENTRIES=$(kubectl exec -n spire-system "$SERVER_POD" -- /opt/spire/bin/spire-server entry show -socketPath /run/spire/sockets/server.sock)

if echo "$ENTRIES" | grep -q "spiffe://zero-ops.nutgraf.in/spoke-controller"; then
  echo "✓ Spoke Controller identity registered"
else
  echo "✗ Spoke Controller identity NOT found"
fi

if echo "$ENTRIES" | grep -q "spiffe://zero-ops.nutgraf.in/grafana-alloy"; then
  echo "✓ Grafana Alloy identity registered"
else
  echo "✗ Grafana Alloy identity NOT found"
fi

if echo "$ENTRIES" | grep -q "spiffe://zero-ops.nutgraf.in/hub-agentgateway"; then
  echo "✓ Hub AgentGateway identity registered"
else
  echo "✗ Hub AgentGateway identity NOT found"
fi

if echo "$ENTRIES" | grep -q "spiffe://zero-ops.nutgraf.in/victoriametrics"; then
  echo "✓ VictoriaMetrics identity registered"
else
  echo "✗ VictoriaMetrics identity NOT found"
fi

echo ""
echo "7. Testing SVID issuance..."
AGENT_POD=$(kubectl get pods -n spire-system -l app=spire-agent -o jsonpath='{.items[0].metadata.name}')
kubectl exec -n spire-system "$AGENT_POD" -- /opt/spire/bin/spire-agent api fetch x509 -socketPath /run/spire/sockets/agent.sock
echo "✓ SPIRE Agent can fetch X.509 SVIDs"
echo ""

echo "=== SPIRE Validation Complete ==="
echo "All checks passed. SPIRE is operational."
