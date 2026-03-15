# Platform Identity Infrastructure - Task 1

This directory contains the database infrastructure and Ory stack foundation for Demo 1.

## Architecture

- **Namespaces:**
  - `ory-system`: CNPG cluster, Ory Hydra/Kratos/Keto, Kratos UI
  - `api-gateway`: Demo echo service

- **Components:**
  - CloudNativePG cluster (3 instances)
  - Database CRDs for Hydra, Kratos, Keto
  - Ory Hydra (OAuth2 server)
  - Ory Kratos (Identity provider)
  - Ory Keto (Authorization server)
  - Kratos Self-Service UI
  - Demo echo service

## Deployment

### Prerequisites

- Kubernetes cluster with CNPG operator installed
- ArgoCD installed
- kubectl configured

### Step 1: Bootstrap Secrets

```bash
./manifests/platform-identity/bootstrap-secrets.sh
```

This creates the `identity-postgres-passwords` secret with randomly generated passwords.

### Step 2: Deploy CNPG Cluster

```bash
kubectl apply -f manifests/platform-identity/cnpg-cluster.yaml
```

Wait for cluster to be ready:
```bash
kubectl wait --for=condition=Ready cluster/identity-postgres -n ory-system --timeout=300s
```

### Step 3: Deploy Database CRDs

```bash
kubectl apply -f manifests/platform-identity/databases/
```

### Step 4: Deploy Kratos Identity Schema

```bash
kubectl apply -f manifests/platform-identity/ory-kratos/identity-schema-configmap.yaml
```

### Step 5: Deploy ArgoCD Applications

```bash
kubectl apply -f manifests/platform-identity/argocd/
```

ArgoCD will automatically deploy:
- Ory Hydra
- Ory Kratos
- Ory Keto
- Kratos Self-Service UI
- Demo echo service

### Step 6: Verify Deployment

Check all pods are running:
```bash
kubectl get pods -n ory-system
kubectl get pods -n api-gateway
```

Check Ory services health:
```bash
kubectl exec -n ory-system deploy/ory-hydra -- hydra health --endpoint http://localhost:4444
kubectl exec -n ory-system deploy/ory-kratos -- kratos health --endpoint http://localhost:4433
kubectl exec -n ory-system deploy/ory-keto -- keto health --endpoint http://localhost:4466
```

## Service Endpoints

### Internal (cluster DNS)

- **Hydra Public API:** `ory-hydra-public.ory-system.svc.cluster.local:4444`
- **Hydra Admin API:** `ory-hydra-admin.ory-system.svc.cluster.local:4445`
- **Kratos Public API:** `ory-kratos-public.ory-system.svc.cluster.local:4433`
- **Kratos Admin API:** `ory-kratos-admin.ory-system.svc.cluster.local:4434`
- **Keto Read API:** `ory-keto-read.ory-system.svc.cluster.local:4466`
- **Keto Write API:** `ory-keto-write.ory-system.svc.cluster.local:4467`
- **Kratos UI:** `kratos-selfservice-ui.ory-system.svc.cluster.local:3000`
- **Demo Echo:** `demo-echo.api-gateway.svc.cluster.local:8080`

### External (requires ingress - configured in later tasks)

- **API Gateway:** `https://api.nutgraf.in`
- **Auth Proxy:** `https://auth.nutgraf.in`
- **Console (Kratos UI):** `https://console.nutgraf.in`

## Database Configuration

### CNPG Cluster

- **Name:** `identity-postgres`
- **Instances:** 3
- **Storage:** 20Gi per instance
- **Service:** `identity-postgres-rw.ory-system.svc.cluster.local:5432`

### Databases

- `hydra_db` - OAuth2 clients, sessions, tokens
- `kratos_db` - Identities, credentials, sessions
- `keto_db` - Relationships, permissions

### Credentials

All database passwords are stored in the `identity-postgres-passwords` secret:
- `hydra-password`
- `kratos-password`
- `keto-password`

## Identity Schema

Kratos uses a custom identity schema with the following traits:
- `email` (required, string, email format)
- `tenant_id` (optional, string)
- `role` (optional, enum: `tenant_admin`, `platform_admin`)

## Troubleshooting

### Pods in CrashLoopBackOff

This is expected during initial startup. Ory services will retry until CNPG cluster is ready. The initContainers wait for PostgreSQL readiness.

### Database Connection Errors

Verify CNPG cluster is ready:
```bash
kubectl get cluster -n ory-system
```

Check database CRDs:
```bash
kubectl get database -n ory-system
```

### Ory Service Health Checks Failing

Check logs:
```bash
kubectl logs -n ory-system deploy/ory-hydra
kubectl logs -n ory-system deploy/ory-kratos
kubectl logs -n ory-system deploy/ory-keto
```

## Next Steps

Task 2 will implement the auth-proxy service for OAuth2 flow handling and JWT validation.
