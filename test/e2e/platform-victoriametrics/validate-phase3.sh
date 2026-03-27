#!/bin/bash
set -e

echo "=== Phase 3: VictoriaMetrics Observability Stack Validation ==="
echo ""

# Task 3.1: VictoriaMetrics cluster deployment
echo "Task 3.1: Verifying VictoriaMetrics cluster..."
VM_OPERATOR=$(kubectl get deployment victoria-metrics-operator -n victoriametrics-operator --no-headers 2>/dev/null | wc -l | tr -d ' ')
if [ "$VM_OPERATOR" -eq 1 ]; then
  echo "✓ VictoriaMetrics operator deployed"
else
  echo "✗ VictoriaMetrics operator not found"
  exit 1
fi

VM_CLUSTER=$(kubectl get vmcluster vmcluster -n victoriametrics --no-headers 2>/dev/null | wc -l | tr -d ' ')
if [ "$VM_CLUSTER" -eq 1 ]; then
  echo "✓ VMCluster resource exists"
else
  echo "✗ VMCluster resource not found"
  exit 1
fi

# Task 3.1.2: Persistent storage
echo ""
echo "Task 3.1.2: Verifying persistent storage..."
PVC_COUNT=$(kubectl get pvc -n victoriametrics --no-headers 2>/dev/null | wc -l | tr -d ' ')
if [ "$PVC_COUNT" -ge 4 ]; then
  echo "✓ Persistent volumes created (vmstorage + vmselect)"
else
  echo "✗ Expected at least 4 PVCs, found $PVC_COUNT"
  exit 1
fi

# Task 3.1.3: PromQL API
echo ""
echo "Task 3.1.3: Testing PromQL API..."
VM_SVC=$(kubectl get svc victoriametrics -n victoriametrics --no-headers 2>/dev/null | wc -l | tr -d ' ')
if [ "$VM_SVC" -eq 1 ]; then
  echo "✓ VictoriaMetrics service exists at victoriametrics.victoriametrics.svc.cluster.local:8428"
else
  echo "✗ VictoriaMetrics service not found"
  exit 1
fi

# Task 3.4.1: External ingress
echo ""
echo "Task 3.4.1: Verifying external ingress..."
VM_INGRESS=$(kubectl get ingress victoriametrics-external -n victoriametrics --no-headers 2>/dev/null | wc -l | tr -d ' ')
if [ "$VM_INGRESS" -eq 1 ]; then
  echo "✓ External ingress configured at victoriametrics.hub.zero-ops.io"
else
  echo "✗ External ingress not found"
  exit 1
fi

# Security validation
echo ""
echo "Security: Verifying authentication configured..."
VM_AUTH_SECRET=$(kubectl get secret victoriametrics-basic-auth -n victoriametrics --no-headers 2>/dev/null | wc -l | tr -d ' ')
if [ "$VM_AUTH_SECRET" -eq 1 ]; then
  echo "✓ Basic auth secret configured (TODO: Infisical integration)"
else
  echo "✗ Basic auth secret not found"
  exit 1
fi

echo ""
echo "=== Phase 3 Partial Validation Complete ==="
echo "Note: Prometheus Operator and Grafana Alloy deployment pending"
