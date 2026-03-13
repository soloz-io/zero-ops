# Project Structure

## Repository Layout (Go-Centric Monorepo)
```
zero-ops/
├── cmd/                    # Application entrypoints
│   ├── zero-ops/          # CLI client
│   ├── zero-ops-api/      # SaaS control plane
│   ├── zero-ops-worker/   # Async worker
│   └── zero-ops-agent/    # Multi-agent system
├── pkg/                   # Shared Go libraries
│   ├── api/              # REST API handlers & models
│   ├── agents/           # AI agent logic
│   ├── db/               # sqlc generated code
│   └── mcp/              # MCP tool servers
├── manifests/            # Infrastructure manifests
├── xrds/                 # Crossplane XRD definitions
├── catalog/              # Selectable services (OCI packaged)
├── .kiro/                # Kiro-specific configurations
│   ├── specs/           # Technical specifications
│   ├── agents/          # Agent configurations
│   └── steering/        # Steering files
└── memory/              # Project memory files
```

## Naming Conventions

### Files & Directories
- **Kebab-case**: Directory names (`zero-ops-api`, `tenant-lifecycle`)
- **Snake_case**: Go files (`tenant_create.go`, `environment_status.go`)
- **PascalCase**: Go types (`TenantRecord`, `AINativeSaaS`)
- **camelCase**: Go variables/functions (`tenantID`, `createTenant`)

### API Endpoints
- **REST**: `/api/v1/tenants/{tenant_id}/environments`
- **MCP Tools**: `tenant_create`, `environment_status`, `credential_submit`
- **Kubernetes CRs**: `AINativeSaaS`, `TenantDescriptor`

## Import Patterns

### Go Module Structure
```go
// Internal packages
import (
    "github.com/zero-ops/zero-ops/pkg/api"
    "github.com/zero-ops/zero-ops/pkg/db"
    "github.com/zero-ops/zero-ops/pkg/agents/mcp"
)

// External dependencies
import (
    "github.com/gin-gonic/gin"
    "github.com/testcontainers/testcontainers-go"
    "sigs.k8s.io/controller-runtime"
)
```

### Package Organization
- **cmd/**: Thin entrypoints, minimal logic
- **pkg/api/**: HTTP handlers, middleware, models
- **pkg/db/**: Database schemas, sqlc queries
- **pkg/agents/**: Agent implementations, MCP servers
- **pkg/mcp/**: MCP protocol implementations

## Architectural Decisions

### Database Layer
- **sqlc**: Generate type-safe Go from SQL
- **PostgreSQL schemas**: Separate tenant/environment tables
- **Migrations**: Version-controlled SQL files
- **Testcontainers**: Real PostgreSQL for E2E tests

### API Layer
- **Gin framework**: HTTP routing and middleware
- **JWT middleware**: Authentication via AgentGateway
- **Error handling**: RFC 7807 Problem Details format
- **Validation**: Request/response schema validation

### Agent System
- **MCP servers**: Separate processes with RBAC
- **Orchestrator pattern**: Delegate to specialist agents
- **State machine**: Tenant lifecycle phases
- **Async pattern**: Submit intent and exit

### Testing Strategy
- **E2E only**: No unit tests per steering files
- **Testcontainers**: Real database instances
- **BDD style**: Given/When/Then structure
- **Same code paths**: Tests use production services

### Configuration Management
- **Environment variables**: Runtime configuration
- **Kubernetes ConfigMaps**: Cluster-specific settings
- **KSOPS**: Encrypted secrets in Git
- **Helm values**: Deployment-time configuration

## Code Style Guidelines
- **gofmt**: Standard Go formatting
- **golangci-lint**: Linting and static analysis
- **Error wrapping**: Use `fmt.Errorf` with `%w` verb
- **Context propagation**: Pass context.Context through call chains
- **Structured logging**: Use structured logger (logrus/zap)
- **Graceful shutdown**: Handle SIGTERM in main functions