# SPIRE Identity Registration Guide

## Overview
SPIFFE identities must be registered with the SPIRE Server after deployment. This document provides the commands to register required workload identities.

## Prerequisites
- SPIRE Server and Agent pods are running and healthy
- kubectl access to the Hub cluster

## Registration Commands

Execute these commands to register the required SPIFFE identities:

```bash
# Get SPIRE Server pod name
SERVER_POD=$(kubectl get pods -n spire-system -l app=spire-server -o jsonpath='{.items[0].metadata.name}')

# Register Spoke Controller identity
kubectl exec -n spire-system "$SERVER_POD" -- \
  /opt/spire/bin/spire-server entry create \
  -spiffeID spiffe://zero-ops.nutgraf.in/spoke-controller \
  -parentID spiffe://zero-ops.nutgraf.in/spire/agent/k8s_psat/zero-ops-hub \
  -selector k8s:ns:zero-ops-system \
  -selector k8s:sa:spoke-controller \
  -dns spoke-controller.zero-ops-system.svc.cluster.local

# Register Grafana Alloy identity
kubectl exec -n spire-system "$SERVER_POD" -- \
  /opt/spire/bin/spire-server entry create \
  -spiffeID spiffe://zero-ops.nutgraf.in/grafana-alloy \
  -parentID spiffe://zero-ops.nutgraf.in/spire/agent/k8s_psat/zero-ops-hub \
  -selector k8s:ns:observability \
  -selector k8s:sa:grafana-alloy \
  -dns grafana-alloy.observability.svc.cluster.local

# Register Hub AgentGateway identity
kubectl exec -n spire-system "$SERVER_POD" -- \
  /opt/spire/bin/spire-server entry create \
  -spiffeID spiffe://zero-ops.nutgraf.in/hub-agentgateway \
  -parentID spiffe://zero-ops.nutgraf.in/spire/agent/k8s_psat/zero-ops-hub \
  -selector k8s:ns:zero-ops-system \
  -selector k8s:sa:agentgateway \
  -dns agentgateway.zero-ops-system.svc.cluster.local

# Register VictoriaMetrics identity
kubectl exec -n spire-system "$SERVER_POD" -- \
  /opt/spire/bin/spire-server entry create \
  -spiffeID spiffe://zero-ops.nutgraf.in/victoriametrics \
  -parentID spiffe://zero-ops.nutgraf.in/spire/agent/k8s_psat/zero-ops-hub \
  -selector k8s:ns:zero-ops-system \
  -selector k8s:sa:victoriametrics \
  -dns victoriametrics.zero-ops-system.svc.cluster.local
```

## Verification

List all registered entries:
```bash
kubectl exec -n spire-system "$SERVER_POD" -- \
  /opt/spire/bin/spire-server entry show
```

## Per-Tenant Spoke Registration

For each spoke cluster, register the Grafana Alloy identity with tenant-specific SPIFFE ID:

```bash
# Replace {tenant-id} with actual tenant ID
kubectl exec -n spire-system "$SERVER_POD" -- \
  /opt/spire/bin/spire-server entry create \
  -spiffeID spiffe://zero-ops.nutgraf.in/grafana-alloy/{tenant-id} \
  -parentID spiffe://zero-ops.nutgraf.in/spire/agent/k8s_psat/spoke-{tenant-id} \
  -selector k8s:ns:observability \
  -selector k8s:sa:grafana-alloy \
  -dns grafana-alloy.observability.svc.cluster.local
```

## Notes

- Registration is a one-time operation per identity
- Identities persist across SPIRE Server restarts (stored in persistent volume)
- Certificate TTL is 1 hour by default (automatic rotation)
- Use SPIRE Controller Manager CRDs for production automation
