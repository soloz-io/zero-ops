# Zero-Ops API

REST API for tenant lifecycle management with PostgreSQL backend.

## Quick Start

### Prerequisites
- Go 1.21+
- Docker (for local PostgreSQL)
- Make

### Local Development

```bash
# Install dependencies
go mod download

# Start PostgreSQL
docker run -d \
  --name zero-ops-postgres \
  -e POSTGRES_DB=zeroops \
  -e POSTGRES_USER=zeroops \
  -e POSTGRES_PASSWORD=zeroops \
  -p 5432:5432 \
  postgres:15-alpine

# Run migrations
export DATABASE_URL="postgres://zeroops:zeroops@localhost:5432/zeroops?sslmode=disable"
make migrate-up

# Start API server
export SERVER_PORT=8080
go run cmd/zero-ops-api/main.go
```

### Run Tests

```bash
# Run all E2E tests (uses Testcontainers)
go test ./internal/api/handlers/e2e/... -v -timeout 5m

# Run specific test suite
go test ./internal/api/handlers/e2e/... -v -run TestCompleteTenantLifecycle
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `SERVER_PORT` | HTTP server port | `8080` |
| `DATABASE_URL` | PostgreSQL connection string | `postgres://localhost:5432/zeroops?sslmode=disable` |
| `LOG_LEVEL` | Logging level (info, debug, error) | `info` |
| `ENVIRONMENT` | Environment (development, production) | `development` |

## API Endpoints

### Create Tenant
```bash
POST /api/v1/tenants
Content-Type: application/json

{
  "name": "acme-corp",
  "email": "admin@acme.com",
  "plan": "professional",
  "quotas": {
    "maxClusters": 15
  }
}

# Response: 201 Created (first call) or 200 OK (idempotent retry)
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "name": "acme-corp",
  "email": "admin@acme.com",
  "plan": "professional",
  "status": "active",
  "quotas": {
    "maxClusters": 15,
    "maxNodes": 50,
    "maxCPU": "200",
    "maxMemory": "500Gi"
  },
  "created_at": "2026-03-09T10:00:00Z",
  "updated_at": "2026-03-09T10:00:00Z",
  "created": true
}
```

### Get Tenant
```bash
GET /api/v1/tenants/{id}

# Response: 200 OK
```

### List Tenants
```bash
GET /api/v1/tenants?status=active&plan=professional&page=1&limit=50

# Response: 200 OK
{
  "tenants": [...],
  "pagination": {
    "page": 1,
    "limit": 50,
    "total": 127,
    "totalPages": 3,
    "nextPage": 2
  }
}
```

### Update Tenant
```bash
PATCH /api/v1/tenants/{id}
Content-Type: application/json

{
  "plan": "enterprise",
  "quotas": {
    "maxClusters": 50
  }
}

# Response: 200 OK
```

### Delete Tenant (Soft Delete)
```bash
DELETE /api/v1/tenants/{id}?confirm=true

# Response: 204 No Content
```

## Health Checks

```bash
# Liveness probe
GET /healthz

# Readiness probe (checks DB connectivity)
GET /readyz
```

## Metrics

Prometheus metrics available at `/metrics`:
- `zero_ops_api_http_requests_total` - Request count by method, path, status
- `zero_ops_api_http_request_duration_seconds` - Request latency histogram

## Database Strategy

### Local Development
Use Docker PostgreSQL container (see Quick Start above).

### Testing
E2E tests use Testcontainers for ephemeral PostgreSQL instances. Each test suite gets an isolated container that's automatically cleaned up.

### Production
Deploy with CloudNativePG (CNPG) operator on Kubernetes. See `docs/deployment/cnpg.md` for production setup.

## Architecture

- **HTTP Layer**: Gin framework with middleware (logging, metrics, error handling)
- **Service Layer**: Business logic, validation, quota resolution
- **Data Layer**: sqlc-generated type-safe queries with pgxpool
- **Database**: PostgreSQL 15+ with JSONB for quotas/metadata

## Development

### Generate SQL Code
```bash
make sqlc-generate
```

### Run Migrations
```bash
make migrate-up    # Apply migrations
make migrate-down  # Rollback migrations
```

### Build Binary
```bash
make build
./bin/zero-ops-api
```

## License

Proprietary - Soloz.io
