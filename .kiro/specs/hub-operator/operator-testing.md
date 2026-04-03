it is your job to start the cluster and run the test cases. I can assist you with details you need.

1. Cloud Credentials (Hetzner):

HCLOUD_TOKEN

2. Container Registry Access (GHCR)

Based on the design.md, the hub-operator is planned to be deployed exclusively via ArgoCD following a GitOps-first approach. Here's the deployment plan:

## Deployment Strategy

ArgoCD Sync Waves (in order):

Sync Wave 0: CRDs (HubEnvironment, CNPG, etc.)

Sync Wave 1: hub-operator Deployment + HubEnvironment CR

Sync Wave 2: CNPG Cluster, NATS, Redis (use Secret Zero)

Sync Wave 3: Infisical, SPIRE, Hydra (use database roles)

Sync Wave 4+: AgentGateway, MCP Server, Console

## Key Principles

GitOps-First: All operator changes deployed via ArgoCD, never kubectl apply

Idempotent Reconciliation: Safe to retry infinitely

Dependency-Aware: Watches external resources (CNPG, Hydra, Infisical, NATS) and reconciles when ready

Manifest Structure

The design specifies a manifests/ directory with:

deployment.yaml - Operator deployment

service_account.yaml - Service account for the operator

rbac.yaml - RBAC permissions

kustomization.yaml - Kustomize overlay for ArgoCD

Testing Workflow

Per the k8-developer-workflow.md steering rules, developers must test via ArgoCD (not kubectl apply). They can use manual forced sync to test faster:

# Force sync to test changes kubectl patch application hub-operator -n argocd --type merge -p '{"spec":{"source":{"targetRevision":"<branch>"}}}' 

This ensures the sync-waves and ArgoCD logic actually work before production deployment.

3. Clarification on the "Overlap" (Important Architectural Note)

Instruct the team to bypass the init-secrets CLI command when testing the operator. They should run hub bootstrap (which does CAPI, ArgoCD, CNPG), but they must not run hub init-secrets. They should let the hub-operator take over immediately after hub bootstrap finishes so the Operator generates Secret Zero and runs the database migrations.

You do not need a permanent, pre-provisioned staging cluster to run this test. The zero-ops codebase already contains a CLI (hub) that uses Cluster API (CAPI) and Hetzner to bootstrap a fully functional Hub cluster complete with ArgoCD, CNPG, and Cilium.

You should treat this CAPI cluster as ephemeral—spin it up, run your operator tests, and tear it down.

Here is the exact workflow to execute the Task 21 (Resource Pruning) E2E test:

Step 1: Bootstrap the Ephemeral CAPI Hub Cluster

Compile the hub CLI and use it to spin up a real cluster on Hetzner.

code Bash

# 1. Build the CLI make build-hub  
# 2. Export your Hetzner Cloud token export HCLOUD_TOKEN="<your-hetzner-api-token>"  
# 3. Bootstrap the cluster (This will take ~10-15 mins) # It creates a Kind bootstrap cluster, provisions Hetzner VMs, pivots CAPI,  # and installs ArgoCD & CNPG. 

## Command
export HCLOUD_TOKEN="<your-hetzner-api-token>" && ./
cmd/hub/hub bootstrap --name=hub-cp --region=fsn1 --debug 2>&1 | tee /tmp/bootstrap-hub-cp.log


Note: Once complete, the CLI will output the path to your new task21-test.kubeconfig.