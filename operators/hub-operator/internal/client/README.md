# Client Package

This package implements external service clients for the Hub Operator.

## InfisicalClient

The `InfisicalClient` handles secret backup operations to Infisical using Universal Auth.

### Key Features

- **Universal Auth**: Authenticates using client credentials from `infisical-auth` secret (Requirement 6.4)
- **Token Caching**: Caches access token and refreshes before expiration (Requirement 6.5)
- **CreateOrUpdateSecret**: Idempotent secret upload to Infisical API (Requirement 6.6)
- **Retry Logic**: Retries on 5xx errors for transient failures (Requirement 6.7)
- **30 Second Timeout**: All HTTP requests timeout after 30 seconds (Requirement 6.8)
- **Controller-Runtime Integration**: Uses controller-runtime client for Kubernetes operations (Requirement 6.3)

### Usage

```go
import (
    "context"
    "github.com/zero-ops/hub-operator/internal/client"
    "sigs.k8s.io/controller-runtime/pkg/client"
)

// Create Infisical client
infisicalClient, err := client.NewInfisicalClient(ctx, k8sClient, "https://infisical.nutgraf.in")
if err != nil {
    return err
}

// Upload secret (idempotent)
if err := infisicalClient.CreateOrUpdateSecret(ctx, hubEnv, "db-password", "secret123"); err != nil {
    return err
}
```

### Authentication Flow

1. Read `infisical-auth` secret from `platform-ops` namespace
2. Extract `client-id` and `client-secret` from secret data
3. POST to `/api/v1/auth/universal-auth/login` with credentials
4. Cache access token with expiration time
5. Refresh token automatically when expiring within 5 minutes

### Secret Upload Flow

1. Ensure valid authentication token (refresh if needed)
2. Convert `projectSlug` to `workspaceId` via Infisical API
3. Check if secret exists using GET `/api/v3/secrets/raw/{key}`
4. If exists: PATCH `/api/v3/secrets/raw/{key}` to update
5. If not exists: POST `/api/v3/secrets/raw/{key}` to create

### Error Handling

**Transient Errors (5xx)**:
- Authentication failures with status >= 500
- Workspace lookup failures with status >= 500
- Secret existence check failures with status >= 500
- Create/update failures with status >= 500
- All return error with "(transient error)" suffix for controller retry

**Permanent Errors**:
- Missing `infisical-auth` secret
- Invalid credentials (4xx responses)
- Workspace not found
- Malformed API responses

### Configuration

Infisical configuration is read from HubEnvironment CR:

```yaml
spec:
  secrets:
    infisical:
      projectSlug: "platform"
      environmentSlug: "prod"
```

### Security Considerations

- Credentials read from Kubernetes secret (never hardcoded)
- Access token cached in memory only (not persisted)
- Token refreshed before expiration (5 minute buffer)
- All requests use HTTPS
- 30 second timeout prevents hanging connections

### API Endpoints

- **Authentication**: `POST /api/v1/auth/universal-auth/login`
- **List Workspaces**: `GET /api/v1/workspace`
- **Get Secret**: `GET /api/v3/secrets/raw/{key}?workspaceId={id}&environment={env}&secretPath={path}`
- **Create Secret**: `POST /api/v3/secrets/raw/{key}`
- **Update Secret**: `PATCH /api/v3/secrets/raw/{key}`

### Requirement 9 Compliance

The client supports uploading all required secrets per Requirement 9:
- Database usernames and passwords (AC 9.1-9.16)
- Only uploads passwords/keys, not URLs/hostnames (AC 9.23-9.24)
- Tracks uploaded secrets in HubEnvironment status (AC 9.17-9.19)
- Idempotent uploads prevent overwrites (AC 9.19)

## OAuth Client Registration

OAuth client registration is handled by Zitadel (ADR-060). The hub-operator no
longer registers clients directly — Zitadel generates client secrets itself and
discloses them once. The Tenant Identity Service captures and publishes them.

## NATSClient

The `NATSClient` handles JetStream stream creation and management.

### Key Features

- **NATS Go SDK**: Uses official NATS client library (Requirement 8.1)
- **Idempotent Stream Creation**: Checks stream name before creating (Requirement 8.4)
- **Configuration Drift Detection**: Compares subjects, retention, storage (Requirement 8.5)
- **Stream Update**: Updates streams when configuration changes (Requirement 8.6)
- **Orphaned Stream Pruning**: Deletes streams not in CR spec (Requirement 8.10)
- **Error Classification**: Distinguishes transient from permanent errors (Requirement 8.8)
- **Connection Cleanup**: Implements Close() for resource cleanup (Requirement 8.10)

### Usage

```go
import (
    "context"
    "github.com/zero-ops/hub-operator/internal/client"
)

// Create NATS client
natsClient, err := client.NewNATSClient("nats://nats.platform-core.svc:4222")
if err != nil {
    return err
}
defer natsClient.Close()

// Create/update all streams from CR (idempotent)
if err := natsClient.CreateOrUpdateStreams(ctx, hubEnv); err != nil {
    return err
}
```

### Stream Lifecycle

1. **Creation**: Reads stream specs from HubEnvironment CR, checks if stream exists, creates if not exists
2. **Drift Detection**: Compares existing stream config (subjects, retention, storage) against CR spec
3. **Update**: Executes UpdateStream API call when drift detected
4. **Pruning**: Lists all streams with "hub-" prefix, deletes streams not in CR spec

### Managed Stream Identification

Streams are identified as managed by the hub-operator via:
- **Naming Convention**: Stream names must start with "hub-" prefix
- **Example**: `hub-billing-events`, `hub-lifecycle-events`
- Only streams with this prefix are eligible for pruning

### Configuration Drift Detection

The client detects drift in three areas:
- **Subjects**: Array of subject patterns (e.g., `["hub.>", "spoke.>"]`)
- **Retention**: Policy type (`limits`, `interest`, `workqueue`)
- **Storage**: Storage type (`file`, `memory`)

When any of these differ from CR spec, UpdateStream is called.

### Error Handling

**Transient Errors** (Requirement 8.7-8.8):
- `nats.ErrConnectionClosed`: Connection lost
- `nats.ErrTimeout`: Operation timeout
- `nats.ErrNoServers`: No servers available
- Error strings containing "timeout", "connection refused", "connection reset"
- All return error with "(transient error)" suffix for controller retry

**Permanent Errors**:
- Invalid stream configuration
- Stream name conflicts
- Malformed API responses

### Configuration

NATS stream configuration is read from HubEnvironment CR:

```yaml
spec:
  nats:
    streams:
      - name: "hub-billing-events"
        subjects:
          - "hub.billing.>"
        retention: "limits"
        storage: "file"
      - name: "hub-lifecycle-events"
        subjects:
          - "hub.lifecycle.>"
        retention: "interest"
        storage: "memory"
```

### Retention Policies

- **limits**: Stream retains messages until limits are reached (size/age/count)
- **interest**: Stream retains messages while there are active consumers
- **workqueue**: Stream retains messages until acknowledged by consumer

### Storage Types

- **file**: Persistent storage on disk
- **memory**: In-memory storage (faster, not durable)

### API Operations

- **StreamInfo**: Get stream configuration and status
- **AddStream**: Create new stream
- **UpdateStream**: Update existing stream configuration
- **DeleteStream**: Delete stream
- **StreamNames**: List all stream names

### Requirement 8 Compliance

- Uses NATS Go SDK (AC 8.1)
- Reads stream definitions from CR (AC 8.2)
- Configures stream subjects from CR (AC 8.3)
- Idempotent stream name check (AC 8.4)
- Compares existing config against CR (AC 8.5)
- Executes UpdateStream on drift (AC 8.6)
- Handles unreachable API (AC 8.7)
- Classifies errors for retry (AC 8.8)
- Updates status condition (AC 8.9)
- Deletes orphaned streams (AC 8.10)
