# Hub Operator

A Kubernetes Operator that manages Day-2 operations for the Zero-Ops Hub cluster.

## Overview

The hub-operator replaces imperative CLI-driven bootstrap logic with declarative, GitOps-native reconciliation. It watches the `HubEnvironment` Custom Resource and orchestrates:

- Secret Zero generation (bootstrap secrets)
- Database migrations
- Database role provisioning
- OAuth client registration with Hydra
- NATS JetStream stream creation
- Infisical secret backup operations

## Architecture

The operator follows a 3-phase reconciliation model:

1. **Phase 1: Secret Zero Generation** - Generates bootstrap secrets required before ArgoCD syncs dependent services
2. **Phase 2: Database Setup** - Executes migrations and creates database roles when CNPG becomes ready
3. **Phase 3: External Services Configuration** - Configures Infisical, Hydra, and NATS when they become ready

## Prerequisites

- Go 1.25+
- Kubebuilder v4.13+
- Access to a Kubernetes cluster with:
  - CloudNativePG operator
  - ArgoCD
  - Ory Hydra
  - NATS JetStream
  - Infisical

## Dependencies

The operator uses the following key dependencies:

- `github.com/golang-migrate/migrate/v4` - Database migration execution
- `github.com/nats-io/nats.go` - NATS JetStream client
- `github.com/ory/hydra-client-go/v2` - Ory Hydra OAuth2 client
- `github.com/cloudnative-pg/cloudnative-pg` - CNPG API types
- `sigs.k8s.io/controller-runtime` - Kubernetes operator framework

## Development

### Building

```bash
make build
```

### Running locally

```bash
make install  # Install CRDs
make run      # Run operator locally
```

### Generating manifests

```bash
make manifests  # Generate CRD and RBAC manifests
```

## Deployment

The operator is deployed via ArgoCD following GitOps principles:

1. CRDs are deployed in sync wave 0
2. Operator deployment is deployed in sync wave 1
3. HubEnvironment CR is deployed in sync wave 1

**Never use `kubectl apply` directly.** All changes must go through Git → ArgoCD.

## Project Structure

```
operators/hub-operator/
├── api/v1alpha1/              # CRD type definitions
├── cmd/                       # Operator entry point
├── config/                    # Kubebuilder generated manifests
├── internal/
│   ├── controller/            # Main reconciler
│   ├── client/                # External service clients (Infisical, Hydra, NATS)
│   ├── secrets/               # Secret generation logic
│   ├── database/              # Migration and role management
│   └── embed/                 # Embedded SQL migration files
└── tests/e2e/                 # KUTTL E2E tests
```

## Testing

E2E tests use KUTTL and deploy real dependencies via ArgoCD:

```bash
make test-e2e
```

## License

Copyright 2025 Soloz.io
