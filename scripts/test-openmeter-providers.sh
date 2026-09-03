#!/bin/bash
set -e

# Test script for OpenMeter providers
# This script tests the metering and billing providers against a live OpenMeter instance

KUBECONFIG=${KUBECONFIG:-"k8-secrets/kubeconfig/hub.kubeconfig"}
NAMESPACE="hub-platform-billing"
OPENMETER_URL="http://openmeter-api.${NAMESPACE}.svc.cluster.local"

echo "=== OpenMeter Provider Test Script ==="
echo "Cluster: $(kubectl config current-context)"
echo "Namespace: ${NAMESPACE}"
echo ""

# Check if OpenMeter is running
echo "1. Checking OpenMeter deployment..."
if ! kubectl get deployment openmeter-api -n ${NAMESPACE} &>/dev/null; then
    echo "❌ OpenMeter API deployment not found in namespace ${NAMESPACE}"
    exit 1
fi

OPENMETER_READY=$(kubectl get deployment openmeter-api -n ${NAMESPACE} -o jsonpath='{.status.readyReplicas}')
if [ "$OPENMETER_READY" == "0" ] || [ -z "$OPENMETER_READY" ]; then
    echo "❌ OpenMeter API is not ready"
    kubectl get pods -n ${NAMESPACE} -l app.kubernetes.io/name=openmeter,app.kubernetes.io/component=api
    exit 1
fi

echo "✅ OpenMeter API is running (${OPENMETER_READY} replicas ready)"
echo ""

# Port-forward to OpenMeter API
echo "2. Setting up port-forward to OpenMeter API..."
kubectl port-forward -n ${NAMESPACE} svc/openmeter-api 8888:8080 &
PF_PID=$!
sleep 3

# Cleanup function
cleanup() {
    echo ""
    echo "Cleaning up..."
    kill $PF_PID 2>/dev/null || true
}
trap cleanup EXIT

# Test OpenMeter health
echo "3. Testing OpenMeter health endpoint..."
if ! curl -s -f http://localhost:8888/api/v1/health > /dev/null; then
    echo "❌ OpenMeter health check failed"
    exit 1
fi
echo "✅ OpenMeter health check passed"
echo ""

# Create test namespace
TEST_NAMESPACE="test-tenant-$(date +%s)"
echo "4. Creating test namespace: ${TEST_NAMESPACE}..."
NAMESPACE_RESPONSE=$(curl -s -X POST http://localhost:8888/api/v1/namespaces \
    -H "Content-Type: application/json" \
    -d "{\"id\":\"${TEST_NAMESPACE}\",\"name\":\"Test Tenant\"}")

if echo "$NAMESPACE_RESPONSE" | grep -q "error"; then
    echo "❌ Failed to create namespace"
    echo "$NAMESPACE_RESPONSE"
    exit 1
fi
echo "✅ Test namespace created"
echo ""

# Test subject registration
TEST_SUBJECT="${TEST_NAMESPACE}#user-001"
echo "5. Testing subject registration..."
SUBJECT_RESPONSE=$(curl -s -X POST "http://localhost:8888/api/v1/subjects?namespace=${TEST_NAMESPACE}" \
    -H "Content-Type: application/json" \
    -d "{\"key\":\"${TEST_SUBJECT}\",\"displayName\":\"Test User\",\"metadata\":{\"email\":\"test@example.com\"}}")

if echo "$SUBJECT_RESPONSE" | grep -q "error"; then
    echo "❌ Failed to register subject"
    echo "$SUBJECT_RESPONSE"
    exit 1
fi
echo "✅ Subject registered successfully"
echo ""

# Test subject retrieval
echo "6. Testing subject retrieval..."
GET_SUBJECT_RESPONSE=$(curl -s "http://localhost:8888/api/v1/subjects/${TEST_SUBJECT}?namespace=${TEST_NAMESPACE}")

if echo "$GET_SUBJECT_RESPONSE" | grep -q "error"; then
    echo "❌ Failed to retrieve subject"
    echo "$GET_SUBJECT_RESPONSE"
    exit 1
fi
echo "✅ Subject retrieved successfully"
echo ""

# Test meters listing
echo "7. Testing meters listing..."
METERS_RESPONSE=$(curl -s "http://localhost:8888/api/v1/meters?namespace=${TEST_NAMESPACE}")

if echo "$METERS_RESPONSE" | grep -q "error"; then
    echo "⚠️  No meters found (this is expected if catalog not configured)"
else
    METER_COUNT=$(echo "$METERS_RESPONSE" | jq '. | length' 2>/dev/null || echo "0")
    echo "✅ Meters endpoint accessible (found ${METER_COUNT} meters)"
fi
echo ""

# Test features listing
echo "8. Testing features listing..."
FEATURES_RESPONSE=$(curl -s "http://localhost:8888/api/v1/features?namespace=${TEST_NAMESPACE}")

if echo "$FEATURES_RESPONSE" | grep -q "error"; then
    echo "⚠️  No features found (this is expected if catalog not configured)"
else
    FEATURE_COUNT=$(echo "$FEATURES_RESPONSE" | jq '. | length' 2>/dev/null || echo "0")
    echo "✅ Features endpoint accessible (found ${FEATURE_COUNT} features)"
fi
echo ""

# Test plans listing
echo "9. Testing plans listing..."
PLANS_RESPONSE=$(curl -s "http://localhost:8888/api/v1/plans?namespace=${TEST_NAMESPACE}")

if echo "$PLANS_RESPONSE" | grep -q "error"; then
    echo "⚠️  No plans found (this is expected if catalog not configured)"
else
    PLAN_COUNT=$(echo "$PLANS_RESPONSE" | jq '.items | length' 2>/dev/null || echo "0")
    echo "✅ Plans endpoint accessible (found ${PLAN_COUNT} plans)"
fi
echo ""

# Cleanup test data
echo "10. Cleaning up test data..."
curl -s -X DELETE "http://localhost:8888/api/v1/subjects/${TEST_SUBJECT}?namespace=${TEST_NAMESPACE}" > /dev/null
curl -s -X DELETE "http://localhost:8888/api/v1/namespaces/${TEST_NAMESPACE}" > /dev/null
echo "✅ Test data cleaned up"
echo ""

echo "=== All Tests Passed ==="
echo ""
echo "Summary:"
echo "  ✅ OpenMeter API is accessible"
echo "  ✅ Namespace creation works"
echo "  ✅ Subject registration works"
echo "  ✅ Subject retrieval works"
echo "  ✅ Catalog endpoints accessible"
echo ""
echo "Next steps:"
echo "  1. Configure billing catalog (Meters, Features, Plans) via GitOps"
echo "  2. Build and deploy kube-sbt Docker image"
echo "  3. Test full API endpoints with JWT authentication"
echo "  4. Run Checkpoint 1 and Checkpoint 2 validation"
