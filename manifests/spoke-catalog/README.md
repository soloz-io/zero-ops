# Spoke Catalog (Application Plane)

This directory contains manifests that are deployed to Spoke Pool clusters via ArgoCD Agent after bootstrap.

## Purpose

**Delivery Mechanism**: ArgoCD Agent pulls from Hub (GitOps)  
**Timing**: Phase 2 - Application Plane (after bootstrap completes)  
**Contents**: Application-plane components for tenant workloads

## Why Separate from Spoke Bootstrap?

These components are deployed via GitOps because:
1. **ArgoCD Agent is already running** (bootstrap completed)
2. **Sync waves can enforce ordering** (database before API, etc.)
3. **Hub can update these without reprovisioning clusters** (continuous delivery)
4. **Follows GitOps-First principle** (Git is source of truth)

## Components

### Wave 0: Database Extensions (if needed)
- pgvector, pg_stat_statements, etc.

### Wave 1: CNPG Cluster + PgBouncer
**File**: `components/cnpg-cluster.yaml`  
**Purpose**: Shared PostgreSQL database for tenant workloads  
**Features**: HA, pgvector, PgBouncer connection pooling, S3 backup

### Wave 2: Atlas Operator
**File**: `components/atlas-operator.yaml`  
**Purpose**: Declarative schema migrations  
**Features**: Tenant baseline schemas, migration versioning

### Wave 3: PostgREST
**Files**: `components/postgrest.yaml`  
**Purpose**: Auto-generated REST APIs  
**Features**: RLS enforcement, tenant isolation, JWT validation

### Wave 4: Observability + Event Bus
**Files**: `components/nats-leaf-node.yaml`, `components/grafana-alloy.yaml`, `components/spire-agent.yaml`  
**Purpose**: Billing events, metrics collection, workload identity  
**Features**: NATS leaf node to Hub, remote_write to VictoriaMetrics, SPIRE federation

## Deployment Flow

1. ArgoCD Agent connects to Hub (after bootstrap)
2. Hub ApplicationSet detects cluster with label `spoke-type: pool`
3. ApplicationSet creates umbrella Application for this cluster
4. Umbrella Application creates child Applications with sync waves
5. Wave 0 → Wave 1 → Wave 2 → Wave 3 → Wave 4 (health checks gate progression)
6. Cell becomes Ready for tenant onboarding

## App-of-Apps Pattern

**Umbrella Application**: `edge-catalog-app.yaml`  
**Child Applications**: `components/*.yaml`

The umbrella Application generates child Applications, each with a sync wave annotation to enforce dependency ordering.

## Requirements

- FR-2.1: Edge Catalog Deployment
- FR-2.2: Shared CNPG Cluster
- FR-2.3: NATS Leaf Node
- FR-2.4: Observability Stack
- AC-4: Sync waves enforce ordering

## Related Files

- **Spoke Bootstrap**: `manifests/spoke-bootstrap/` (injected via ClusterResourceSet before ArgoCD)
- **ApplicationSet**: `catalog/argocd/edge-catalog-applicationset.yaml`
- **Composition**: `xrds/compositions/spokepool-hetzner.yaml`
