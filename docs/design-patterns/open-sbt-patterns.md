---
inclusion: fileMatch
fileMatchPattern: 'pkg/**/*|cmd/opensbt/**/*'
---

# open-sbt Implementation Patterns

**Purpose**: Ensure consistent implementation of the open-sbt abstraction layer across all developers working on multi-tenant SaaS features.

**Auto-Inclusion**: This steering file is automatically included when working in `pkg/` or `cmd/opensbt/` directories.

## Quick Reference

### Core Interfaces Location
- **Interfaces**: `pkg/interfaces/` - Define stable contracts (IAuth, IEventBus, IProvisioner, IStorage, ISecretManager)
- **Models**: `pkg/models/` - Data structures (Tenant, User, Event)
- **Providers**: `pkg/providers/` - Default implementations (ory/, nats/, postgres/, infisical/)
- **Control Plane**: `pkg/controlplane/` - Tenant management, billing, provisioning, MCP server
- **Application Plane**: `pkg/applicationplane/` - Tenant workloads, resource provisioning
- **Events**: `pkg/events/` - Event definitions and handlers
- **Libraries**: `pkg/libraries/` - Multi-tenant microservice utilities

### Architecture Decision Tree

**When adding new functionality, ask:**

1. **Is this tenant management or tenant workload?**
   - Tenant management (CRUD, billing, identity) → Control Plane (`pkg/controlplane/`)
   - Tenant workload (apps, databases, services) → Application Plane (`pkg/applicationplane/`)

2. **Does this need to be swappable?**
   - Yes → Define interface in `pkg/interfaces/`, implement in `pkg/providers/`
   - No → Implement directly in Control/App Plane

3. **Does this trigger async operations?**
   - Yes → Publish NATS event, implement handler in Application Plane
   - No → Synchronous API call

4. **Is this a new provider implementation?**
   - Yes → Create `pkg/providers/{provider-name}/`, implement interface
   - No → Use existing provider

## Interface Implementation Checklist

When implementing a new provider (e.g., Keycloak auth, Kafka event bus):

### Step 1: Verify Interface Exists
```bash
# Check if interface is defined
ls pkg/interfaces/
# If missing, define interface first
```

### Step 2: Create Provider Package
```bash
mkdir -p pkg/providers/{provider-name}
touch pkg/providers/{provider-name}/{provider-name}.go
touch pkg/providers/{provider-name}/config.go
touch pkg/providers/{provider-name}/README.md
```

### Step 3: Implement Interface
```go
// pkg/providers/keycloak/keycloak.go
package keycloak

import "github.com/soloz-io/open-sbt/pkg/interfaces"

type KeycloakAuth struct {
    config Config
    client *keycloak.Client
}

// Verify interface implementation at compile time
var _ interfaces.IAuth = (*KeycloakAuth)(nil)

func NewKeycloakAuth(cfg Config) (*KeycloakAuth, error) {
    // Implementation
}

// Implement all IAuth methods
func (k *KeycloakAuth) CreateUser(ctx context.Context, user interfaces.User) error {
    // Implementation
}
// ... rest of interface methods
```

### Step 4: Add Configuration
```go
// pkg/providers/keycloak/config.go
package keycloak

type Config struct {
    ServerURL    string
    Realm        string
    ClientID     string
    ClientSecret string
}

func (c Config) Validate() error {
    // Validation logic
}
```

### Step 5: Document Provider
```markdown
# Keycloak Auth Provider

## Configuration
- ServerURL: Keycloak server URL
- Realm: Keycloak realm name
- ClientID: OAuth2 client ID
- ClientSecret: OAuth2 client secret

## Usage
\`\`\`go
auth := keycloak.NewKeycloakAuth(keycloak.Config{
    ServerURL: "https://keycloak.example.com",
    Realm: "zero-ops",
})
\`\`\`
```

## Event Naming Conventions

### Event Sources
- Control Plane: `zerosbt.control.plane`
- Application Plane: `zerosbt.application.plane`
- Custom: `zerosbt.custom.{service-name}`

### Event Detail Types
**Pattern**: `opensbt_{action}{Status}`

**Control Plane Events** (requests):
- `opensbt_onboardingRequest`
- `opensbt_offboardingRequest`
- `opensbt_activateRequest`
- `opensbt_deactivateRequest`
- `opensbt_tenantUserCreated`
- `opensbt_tenantUserDeleted`

**Application Plane Events** (responses):
- `opensbt_onboardingSuccess` / `opensbt_onboardingFailure`
- `opensbt_offboardingSuccess` / `opensbt_offboardingFailure`
- `opensbt_provisionSuccess` / `opensbt_provisionFailure`
- `opensbt_deprovisionSuccess` / `opensbt_deprovisionFailure`

### Event Structure Template
```go
type Event struct {
    ID         string                 `json:"id"`          // UUID
    Version    string                 `json:"version"`     // "1.0"
    DetailType string                 `json:"detailType"`  // opensbt_{action}{Status}
    Source     string                 `json:"source"`      // zerosbt.{plane}
    Time       time.Time              `json:"time"`        // RFC3339
    Detail     map[string]interface{} `json:"detail"`      // Event payload
}
```

### Publishing Events
```go
// Control Plane publishes request
event := Event{
    ID:         uuid.New().String(),
    Version:    "1.0",
    DetailType: "opensbt_onboardingRequest",
    Source:     "zerosbt.control.plane",
    Time:       time.Now(),
    Detail: map[string]interface{}{
        "tenantId": tenantID,
        "tier":     tier,
        "name":     name,
    },
}
err := eventBus.Publish(ctx, event)
```

### Subscribing to Events
```go
// Application Plane subscribes to requests
err := eventBus.Subscribe(ctx, "opensbt_onboardingRequest", func(ctx context.Context, event Event) error {
    tenantID := event.Detail["tenantId"].(string)
    // Provision resources
    // Publish success/failure event
    return nil
})
```

## Control Plane vs Application Plane Decision Matrix

| Functionality | Plane | Reason |
|---------------|-------|--------|
| Tenant CRUD API | Control | Tenant management |
| User management API | Control | Identity management |
| Billing integration | Control | Financial operations |
| MCP server | Control | Platform interface |
| Cluster provisioning | Application | Infrastructure workload |
| Database provisioning | Application | Tenant resource |
| Application deployment | Application | Tenant workload |
| Metrics collection | Application | Workload monitoring |
| Event handlers | Application | Async operations |

## Provider Implementation Patterns

### Pattern 1: Auth Provider (IAuth)
**Default**: Ory Stack (Kratos + Hydra + Keto)
**Alternatives**: Keycloak, Auth0, Custom

**Key Methods**:
- `CreateUser`, `GetUser`, `UpdateUser`, `DeleteUser`
- `AuthenticateUser`, `ValidateToken`, `RefreshToken`
- `CreateAdminUser`
- `GetJWTIssuer`, `GetTokenEndpoint`

**Implementation Notes**:
- Must support JWT token validation
- Must provide JWKS endpoint for token verification
- Must support tenant-scoped authorization

### Pattern 2: Event Bus Provider (IEventBus)
**Default**: NATS
**Alternatives**: Kafka, RabbitMQ, AWS EventBridge

**Key Methods**:
- `Publish`, `PublishAsync`
- `Subscribe`, `SubscribeQueue`
- `GetControlPlaneEventSource`, `GetApplicationPlaneEventSource`
- `CreateControlPlaneEvent`, `CreateApplicationPlaneEvent`

**Implementation Notes**:
- Must support pub/sub pattern
- Must support queue groups for load balancing
- Must handle connection failures gracefully

### Pattern 3: Storage Provider (IStorage)
**Default**: PostgreSQL + sqlc
**Alternatives**: MySQL, MongoDB, DynamoDB

**Key Methods**:
- `CreateTenant`, `GetTenant`, `UpdateTenant`, `DeleteTenant`, `ListTenants`
- `CreateTenantRegistration`, `GetTenantRegistration`
- `SetTenantConfig`, `GetTenantConfig`

**Implementation Notes**:
- Must support tenant isolation (RLS or application-level)
- Must support transactions
- Must handle concurrent updates

### Pattern 4: Provisioner Provider (IProvisioner)
**Default**: Crossplane + CAPI
**Alternatives**: Terraform, Pulumi, CloudFormation

**Key Methods**:
- `ProvisionTenant`, `DeprovisionTenant`
- `GetProvisioningStatus`
- `UpdateTenantResources`

**Implementation Notes**:
- Must be idempotent
- Must support async provisioning
- Must report detailed status

### Pattern 5: Secret Manager Provider (ISecretManager)
**Default**: Infisical
**Alternatives**: Vault, AWS Secrets Manager, Azure Key Vault

**Key Methods**:
- `CreateSecret`, `GetSecret`, `UpdateSecret`, `DeleteSecret`
- `CreateProject`, `GetProject`
- `GrantAccess`, `RevokeAccess`

**Implementation Notes**:
- Must support encryption at rest
- Must support audit logging
- Must support access control

## Common Pitfalls and Solutions

### Pitfall 1: Mixing Control and Application Plane Logic
**Problem**: Putting tenant provisioning logic in Control Plane API handlers
**Solution**: Control Plane publishes event, Application Plane handles provisioning

### Pitfall 2: Tight Coupling to Provider
**Problem**: Using concrete provider types instead of interfaces
**Solution**: Always use interface types in function signatures

```go
// ❌ Bad: Tight coupling
func NewControlPlane(oryAuth *ory.OryAuth) *ControlPlane

// ✅ Good: Interface-based
func NewControlPlane(auth interfaces.IAuth) *ControlPlane
```

### Pitfall 3: Synchronous Provisioning
**Problem**: Blocking API calls waiting for infrastructure provisioning
**Solution**: Return 202 Accepted, provision async, notify via events

### Pitfall 4: Missing Event Handlers
**Problem**: Publishing events but no subscriber handles them
**Solution**: Always implement both publisher and subscriber, test end-to-end

### Pitfall 5: Inconsistent Event Naming
**Problem**: Using different naming patterns for events
**Solution**: Follow `opensbt_{action}{Status}` convention strictly

## Testing Patterns

### Unit Tests: Mock Interfaces
```go
type MockAuth struct {
    mock.Mock
}

func (m *MockAuth) CreateUser(ctx context.Context, user interfaces.User) error {
    args := m.Called(ctx, user)
    return args.Error(0)
}

func TestControlPlane_CreateTenant(t *testing.T) {
    mockAuth := new(MockAuth)
    mockAuth.On("CreateUser", mock.Anything, mock.Anything).Return(nil)
    
    cp := NewControlPlane(ControlPlaneConfig{
        Auth: mockAuth,
    })
    
    // Test logic
}
```

### Integration Tests: Real Providers
```go
func TestIntegration_TenantOnboarding(t *testing.T) {
    // Use testcontainers for real PostgreSQL, NATS
    postgres := testcontainers.StartPostgres(t)
    nats := testcontainers.StartNATS(t)
    
    auth := ory.NewOryAuth(oryConfig)
    eventBus := nats.NewNATSEventBus(natsConfig)
    storage := postgres.NewPostgresStorage(postgresConfig)
    
    // Test full workflow
}
```

## Code Review Checklist

When reviewing open-sbt code:

- [ ] Interface defined in `pkg/interfaces/` before implementation
- [ ] Provider implements interface with compile-time check (`var _ IAuth = (*Provider)(nil)`)
- [ ] Control Plane logic separated from Application Plane logic
- [ ] Events follow naming convention (`opensbt_{action}{Status}`)
- [ ] Event handlers implemented for all published events
- [ ] Configuration validated before use
- [ ] Errors wrapped with context (`fmt.Errorf("failed to create user: %w", err)`)
- [ ] Logging includes tenant_id and relevant context
- [ ] Tests cover both success and failure cases
- [ ] Documentation updated (README, godoc comments)

## Migration Guide: Adding New Provider

**Scenario**: Replace Ory Stack with Keycloak for authentication

### Step 1: Define Interface (if not exists)
Already exists: `pkg/interfaces/auth.go`

### Step 2: Implement Provider
```bash
mkdir -p pkg/providers/keycloak
# Implement IAuth interface
```

### Step 3: Update Configuration
```go
// Allow users to choose provider
type ControlPlaneConfig struct {
    Auth interfaces.IAuth // Accept any IAuth implementation
}
```

### Step 4: Update Documentation
```markdown
# Supported Auth Providers
- Ory Stack (default)
- Keycloak (new)
```

### Step 5: Test Migration
```go
// Test with both providers
func TestControlPlane_WithOry(t *testing.T) { /* ... */ }
func TestControlPlane_WithKeycloak(t *testing.T) { /* ... */ }
```

## Quick Commands

```bash
# Generate sqlc code
sqlc generate

# Run tests for specific package
go test ./pkg/providers/ory/...

# Check interface implementation
go build ./pkg/providers/keycloak/

# Run integration tests
go test -tags=integration ./tests/...

# Generate mocks
mockery --name=IAuth --dir=pkg/interfaces --output=pkg/mocks
```

## References

- [open-sbt README](../../cmd/opensbt/README.md)
- [SBT Design Principles](./sbt-design-principles.md)
- [Tech Stack](../../memory/tech.md)
- [AWS SBT Documentation](https://docs.aws.amazon.com/sbt/)
