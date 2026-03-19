# open-sbt

A Go-based SaaS builder toolkit for building multi-tenant SaaS applications with a clear separation between Control Plane and Application Plane.

## Overview

open-sbt provides reusable abstractions for building production-grade multi-tenant SaaS backends without vendor lock-in. It is inspired by AWS SBT-AWS but built for the Kubernetes-native, cloud-agnostic world.

**Key Differences from SBT-AWS:**
- Language: Go (not TypeScript/CDK)
- Event Bus: NATS (not AWS EventBridge)
- Auth: Ory Stack (not AWS Cognito)
- Provisioning: Crossplane + ArgoCD + Argo Workflows (not CloudFormation)
- Database: PostgreSQL with sqlc (not DynamoDB)
- API: Gin framework (not API Gateway + Lambda)

## Package Structure

```
open-sbt/
├── pkg/
│   ├── interfaces/       # Core interfaces (IAuth, IEventBus, IProvisioner, etc.)
│   ├── models/           # Data models (Tenant, User, Event, etc.)
│   ├── providers/        # Default implementations
│   │   ├── ory/          # Ory Stack auth provider
│   │   ├── nats/         # NATS event bus provider
│   │   ├── postgres/     # PostgreSQL storage provider
│   │   ├── vault/        # HashiCorp Vault secret manager
│   │   └── ...
│   ├── controlplane/     # Control Plane components
│   ├── applicationplane/ # Application Plane components
│   ├── events/           # Event definitions and handlers
│   ├── mcp/              # MCP server implementation
│   └── libraries/        # Multi-tenant microservice libraries
├── examples/             # Example implementations
├── tests/                # E2E and integration tests
└── docs/                 # Documentation
```

## Quick Start

```go
import (
    zerosbt "github.com/soloz-io/open-sbt/pkg"
    "github.com/soloz-io/open-sbt/pkg/providers/ory"
    "github.com/soloz-io/open-sbt/pkg/providers/nats"
    "github.com/soloz-io/open-sbt/pkg/providers/postgres"
)

func main() {
    ctx := context.Background()

    auth := ory.NewOryAuth(ory.Config{
        KratosURL: "http://kratos:4433",
        HydraURL:  "http://hydra:4444",
        KetoURL:   "http://keto:4466",
    })

    eventBus := nats.NewNATSEventBus(nats.Config{
        URL: "nats://nats:4222",
    })

    storage := postgres.NewPostgresStorage(postgres.Config{
        DSN: "postgres://user:pass@postgres:5432/opensbt",
    })

    controlPlane, err := zerosbt.NewControlPlane(ctx, zerosbt.ControlPlaneConfig{
        Auth:             auth,
        EventBus:         eventBus,
        Storage:          storage,
        SystemAdminEmail: "admin@example.com",
        APIPort:          8080,
    })
    if err != nil {
        log.Fatal(err)
    }

    if err := controlPlane.Start(ctx); err != nil {
        log.Fatal(err)
    }
}
```

## Documentation

- [Getting Started](docs/getting-started.md)
- [Architecture](docs/architecture.md)
- [API Reference](docs/api-reference.md)

## License

Apache 2.0
