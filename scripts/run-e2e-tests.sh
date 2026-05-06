#!/bin/bash
# Script to run kube-sbt E2E tests in Hub cluster

set -e

KUBECONFIG=${KUBECONFIG:-"k8-secrets/kubeconfig/hub-cp.kubeconfig"}
NAMESPACE="hub-platform-ops"
JOB_NAME="kube-sbt-e2e-openmeter"

echo "========================================="
echo "kube-sbt OpenMeter E2E Test Runner"
echo "========================================="
echo ""
echo "Cluster: $(kubectl config current-context)"
echo "Namespace: ${NAMESPACE}"
echo ""

# Delete existing job if present
echo "Cleaning up previous test runs..."
kubectl delete job ${JOB_NAME} -n ${NAMESPACE} --ignore-not-found=true
sleep 2

# Apply the test job
echo "Creating test job..."
kubectl apply -f manifests/test-jobs/kube-sbt-e2e-openmeter.yaml

# Wait for job to start
echo "Waiting for job to start..."
kubectl wait --for=condition=ready pod -l app=kube-sbt-e2e-test -n ${NAMESPACE} --timeout=60s

# Get pod name
POD_NAME=$(kubectl get pods -n ${NAMESPACE} -l app=kube-sbt-e2e-test -o jsonpath='{.items[0].metadata.name}')
echo "Test pod: ${POD_NAME}"
echo ""

# Stream logs
echo "========================================="
echo "Test Output:"
echo "========================================="
kubectl logs -f ${POD_NAME} -n ${NAMESPACE}

# Check job status
echo ""
echo "========================================="
echo "Checking job status..."
echo "========================================="

JOB_STATUS=$(kubectl get job ${JOB_NAME} -n ${NAMESPACE} -o jsonpath='{.status.conditions[0].type}')

if [ "$JOB_STATUS" == "Complete" ]; then
    echo "✅ Tests PASSED"
    exit 0
else
    echo "❌ Tests FAILED"
    kubectl get job ${JOB_NAME} -n ${NAMESPACE} -o yaml
    exit 1
fi
