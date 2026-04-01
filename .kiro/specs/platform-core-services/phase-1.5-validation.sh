#!/bin/bash
# Phase 1.5: SPIFFE/SPIRE Workload Identity Validation Script
# This script validates the SPIRE deployment and identity registration

set -e

echo "=== Phase 1.5 SPIRE Validation ==="
echo ""

# Task 1.5.1: Verify SPIRE Server (Hub Cluster)
echo "Task 1.5.1: Validating SPIRE Server deployment..."
kubectl get statefulset spire-server -n spire-system -o jsonpath='{.status.readyReplicas}' | grep -q "1" && echo "✓ SPIRE Server StatefulSet ready" || echo "✗ SPIRE Server not ready"
kubectl get svc spire-server -n spire-system -o jsonpath='{.spec.clusterIP}' | grep -q "." && echo "✓ SPIRE Server service exists" || echo "✗ SPIRE Server service missing"
kubectl get pvc -n spire-system -l app=spire-server | grep -q "Bound" && echo "✓ SPIRE Server persistent storage bound" || echo "✗ Storage not bound"
echo ""

# Task 1.5.2: Verify SPIRE Agent (Hub Cluster)
echo "Task 1.5.2: Validating SPIRE Agent deployment..."
AGENT_COUNT=$(kubectl get daemonset spire-agent -n spire-system -o jsonpath='{.status.numberReady}')
DESIRED_COUNT=$(kubectl get daemonset spire-agent -n spire-system -o jsonpath='{.status.desiredNumberScheduled}')
if [ "$AGENT_COUNT" = "$DESIRED_COUNT" ] && [ "$AGENT_COUNT" -gt "0" ]; then
  echo "✓ SPIRE Agent DaemonSet ready ($AGENT_COUNT/$DESIRED_COUNT nodes)"
else
  echo "✗ SPIRE Agent not ready ($AGENT_COUNT/$DESIRED_COUNT nodes)"
fi

# Check socket mount
kubectl get daemonset spire-agent -n spire-system -o yaml | grep -q "/run/spire/sockets" && echo "✓ SPIRE Agent socket configured" || echo "✗ Socket mount missing"
echo ""

# Task 1.5.3: Verify SPIFFE Identity Registration
echo "Task 1.5.3: Validating SPIFFE identity registration..."
SERVER_POD=$(kubectl get pods -n spire-system -l app=spire-server -o jsonpath='{.items[0].metadata.name}')

if [ -n "$SERVER_POD" ]; then
  echo "Checking registered identities..."
  kubectl exec -n spire-system "$SERVER_POD" -- /opt/spire/bin/spire-server entry show 2>/dev/null | grep -q "spiffe://zero-ops.nutgraf.in" && echo "✓ SPIFFE identities registered" || echo "⚠ No identities registered yet (manual registration required)"
else
  echo "✗ SPIRE Server pod not found"
fi
echo ""

# Task 1.5.4: Verify Spoke SPIRE Agent (edge-catalog)
echo "Task 1.5.4: Validating spoke SPIRE Agent configuration..."
if [ -f "edge-catalog/spire-agent.yaml" ]; then
  grep -q "server_address.*spire-server.zero-ops-system.svc.cluster.local" edge-catalog/spire-agent.yaml && echo "✓ Spoke agent configured for Hub federation" || echo "✗ Federation config missing"
  grep -q "trust_domain.*zero-ops.nutgraf.in" edge-catalog/spire-agent.yaml && echo "✓ Trust domain configured" || echo "✗ Trust domain missing"
else
  echo "✗ edge-catalog/spire-agent.yaml not found"
fi
echo ""

# Task 1.5.5: Integration Validation
echo "Task 1.5.5: Integration validation..."

# Check SPIRE Server health
kubectl exec -n spire-system "$SERVER_POD" -- wget -q -O- http://localhost:8080/ready 2>/dev/null | grep -q "OK" && echo "✓ SPIRE Server health check passing" || echo "✗ SPIRE Server not healthy"

# Check bundle ConfigMap
kubectl get configmap spire-bundle -n spire-system -o jsonpath='{.data.bundle\.crt}' | grep -q "BEGIN CERTIFICATE" && echo "✓ Trust bundle propagated to ConfigMap" || echo "⚠ Trust bundle not yet populated (will be updated by k8sbundle notifier)"

echo ""
echo "=== Validation Complete ==="
echo ""
echo "Manual Steps Required:"
echo "1. Register SPIFFE identities using commands in manifests/platform-core-services/spire/REGISTRATION.md"
echo "2. Configure VictoriaMetrics mTLS authentication (Task 3.4.2-3.4.4)"
echo "3. Deploy spoke clusters and verify cross-cluster trust bundle propagation"
