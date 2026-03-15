#!/bin/bash
# Health Check Verification Script
set -e

echo "========================================="
echo "Demo 1: Health Check Verification"
echo "========================================="
echo ""

# Function to check pod readiness
check_pod_ready() {
    local namespace=$1
    local label=$2
    local name=$3
    
    echo "Checking $name..."
    kubectl wait --for=condition=Ready pod -l "$label" -n "$namespace" --timeout=60s 2>/dev/null
    if [ $? -eq 0 ]; then
        echo "✓ $name is ready"
        return 0
    else
        echo "✗ $name is not ready"
        return 1
    fi
}

# Function to check endpoint
check_endpoint() {
    local url=$1
    local name=$2
    
    echo "Checking $name endpoint..."
    response=$(curl -s -o /dev/null -w "%{http_code}" "$url" 2>/dev/null || echo "000")
    if [ "$response" = "200" ]; then
        echo "✓ $name endpoint is healthy (HTTP $response)"
        return 0
    else
        echo "✗ $name endpoint returned HTTP $response"
        return 1
    fi
}

echo "Step 1: Checking CNPG Cluster..."
kubectl wait --for=condition=Ready cluster/identity-postgres -n ory-system --timeout=300s
echo "✓ CNPG cluster is ready"
echo ""

echo "Step 2: Checking Database CRDs..."
for db in hydra-db kratos-db keto-db; do
    kubectl get database "$db" -n ory-system >/dev/null 2>&1
    if [ $? -eq 0 ]; then
        echo "✓ $db exists"
    else
        echo "✗ $db not found"
    fi
done
echo ""

echo "Step 3: Checking Ory Stack Pods..."
check_pod_ready "ory-system" "app.kubernetes.io/name=hydra" "Hydra"
check_pod_ready "ory-system" "app.kubernetes.io/name=kratos" "Kratos"
check_pod_ready "ory-system" "app.kubernetes.io/name=keto" "Keto"
check_pod_ready "ory-system" "app=kratos-selfservice-ui-node" "Kratos UI"
echo ""

echo "Step 4: Checking auth-proxy..."
check_pod_ready "identity-services" "app=auth-proxy" "auth-proxy"
echo ""

echo "Step 5: Checking AgentGateway..."
check_pod_ready "api-gateway" "app=agentgateway" "AgentGateway"
check_pod_ready "api-gateway" "app=demo-echo" "demo-echo"
echo ""

echo "Step 6: Checking Ingress..."
kubectl get ingress -A | grep -E "api.nutgraf.in|auth.nutgraf.in|console.nutgraf.in" >/dev/null
if [ $? -eq 0 ]; then
    echo "✓ Ingress resources exist"
else
    echo "✗ Ingress resources not found"
fi
echo ""

echo "Step 7: Checking Endpoints (requires DNS and TLS setup)..."
echo "Note: These checks require local DNS (/etc/hosts) and TLS certificates"
echo ""

# Only check if DNS is configured
if grep -q "api.nutgraf.in" /etc/hosts 2>/dev/null; then
    check_endpoint "https://api.nutgraf.in/health" "AgentGateway"
    check_endpoint "https://auth.nutgraf.in/health/ready" "auth-proxy"
    check_endpoint "https://console.nutgraf.in" "Kratos UI"
else
    echo "⚠️  DNS not configured in /etc/hosts, skipping endpoint checks"
    echo "   Run: ./manifests/ingress/setup-local-dns.sh 127.0.0.1"
fi
echo ""

echo "========================================="
echo "Health Check Summary"
echo "========================================="
kubectl get pods -n ory-system
echo ""
kubectl get pods -n identity-services
echo ""
kubectl get pods -n api-gateway
echo ""

echo "========================================="
echo "✅ Health Check Complete"
echo "========================================="
