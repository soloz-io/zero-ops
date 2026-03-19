# Event-Driven Architecture: Control Plane ↔ Application Plane Communication

## The Problem They Solve

### The Core Challenge: Decoupling Tenant Management from Tenant Provisioning

In a multi-tenant SaaS platform, there are two fundamentally different concerns:

1. **Control Plane**: Managing tenant metadata, authentication, billing, user management
2. **Application Plane**: Provisioning actual tenant resources (databases, namespaces, infrastructure)

**The Problem Without Event-Driven Architecture:**

```
❌ TIGHT COUPLING (Bad Approach)
┌─────────────────────────────────────────┐
│  Control Plane API Handler              │
│                                          │
│  POST /tenants                           │
│  ├─ Save tenant to database             │
│  ├─ Create Cognito user pool            │
│  ├─ Provision S3 bucket ← BLOCKING!     │
│  ├─ Create Kubernetes namespace         │
│  ├─ Deploy application pods             │
│  └─ Return response (after 2-5 minutes) │
└─────────────────────────────────────────┘

Problems:
- API timeout (provisioning takes minutes)
- No retry mechanism if provisioning fails
- Can't scale Control Plane and App Plane independently
- Hard to add new provisioning steps
- Difficult to monitor provisioning progress
```

**The Solution With Event-Driven Architecture:**

```
✅ LOOSE COUPLING (Event-Driven Approach)
┌──────────────────────┐         ┌──────────────────────┐
│   Control Plane      │         │  Application Plane   │
│                      │         │                      │
│  POST /tenants       │         │  Listens for events  │
│  ├─ Save to DB       │         │                      │
│  ├─ Create user      │         │                      │
│  ├─ Publish event ───┼────────>│  Receives event      │
│  └─ Return 202       │         │  ├─ Provision S3     │
│     (Accepted)       │         │  ├─ Create namespace │
│                      │         │  ├─ Deploy pods      │
│                      │<────────┼─ Publish success     │
│  Update tenant status│         │                      │
└──────────────────────┘         └──────────────────────┘
        ↑                                  ↓
        └──────── EventBridge ─────────────┘

Benefits:
- API responds instantly (< 1 second)
- Async provisioning (2-5 minutes in background)
- Automatic retry on failure
- Independent scaling
- Easy to add new provisioning steps
- Progress tracking via events
```

## How EventBridge Solves This

### 1. Asynchronous Communication

**Control Plane publishes events, Application Plane subscribes:**

```typescript
// Control Plane: Publish onboarding event
eventManager.publish({
  source: 'sbt.control.plane',
  detailType: 'sbt_aws_onboardingRequest',
  detail: {
    tenantId: 'tenant-123',
    tier: 'premium',
    email: 'admin@acme.com'
  }
});

// Application Plane: Subscribe to onboarding events
eventManager.addTargetToEvent({
  eventDefinition: events.onboardingRequest,
  target: provisioningScriptJob.eventTarget
});
```

### 2. Decoupled Responsibilities

**Control Plane Responsibilities:**
- Tenant CRUD operations
- User management
- Authentication/Authorization
- Billing integration
- Tenant metadata storage

**Application Plane Responsibilities:**
- Infrastructure provisioning
- Resource allocation
- Application deployment
- Tenant-specific configuration
- Resource cleanup

### 3. Standard Event Contracts

**SBT-AWS defines standard events:**

**Control Plane Events** (source: `sbt.control.plane`):
- `sbt_aws_onboardingRequest` - Tenant onboarding initiated
- `sbt_aws_offboardingRequest` - Tenant offboarding initiated
- `sbt_aws_activateRequest` - Tenant activation requested
- `sbt_aws_deactivateRequest` - Tenant deactivation requested
- `sbt_aws_tenantUserCreated` - User created in tenant
- `sbt_aws_tenantUserDeleted` - User deleted from tenant

**Application Plane Events** (source: `sbt.application.plane`):
- `sbt_aws_onboardingSuccess` - Tenant onboarded successfully
- `sbt_aws_onboardingFailure` - Tenant onboarding failed
- `sbt_aws_offboardingSuccess` - Tenant offboarded successfully
- `sbt_aws_offboardingFailure` - Tenant offboarding failed
- `sbt_aws_provisionSuccess` - Resources provisioned successfully
- `sbt_aws_provisionFailure` - Resource provisioning failed

## Real-World Flow Example

### Tenant Onboarding Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                    TENANT ONBOARDING FLOW                        │
└─────────────────────────────────────────────────────────────────┘

Step 1: API Request
─────────────────────
POST /tenants
{
  "name": "acme-corp",
  "tier": "premium",
  "email": "admin@acme.com"
}

Step 2: Control Plane Processing (< 1 second)
──────────────────────────────────────────────
┌─────────────────────────────┐
│ Control Plane               │
│ ├─ Validate request         │
│ ├─ Create tenant record     │
│ │  (status: 'pending')      │
│ ├─ Create Cognito user      │
│ ├─ Publish event to         │
│ │  EventBridge              │
│ └─ Return 202 Accepted      │
└─────────────────────────────┘
         │
         │ EventBridge Event
         ▼
{
  "source": "sbt.control.plane",
  "detailType": "sbt_aws_onboardingRequest",
  "detail": {
    "tenantId": "e6878e03-ae2c-43ed-a863-08314487318b",
    "tier": "premium",
    "name": "acme-corp",
    "email": "admin@acme.com"
  }
}

Step 3: Application Plane Processing (2-5 minutes)
───────────────────────────────────────────────────
┌─────────────────────────────┐
│ Application Plane           │
│ ├─ Receive event            │
│ ├─ Start CodeBuild job      │
│ ├─ Execute provisioning     │
│ │  script:                  │
│ │  ├─ Create S3 bucket      │
│ │  ├─ Create database       │
│ │  ├─ Deploy CloudFormation │
│ │  └─ Configure networking  │
│ ├─ Publish success event    │
│ └─ Return tenant config     │
└─────────────────────────────┘
         │
         │ EventBridge Event
         ▼
{
  "source": "sbt.application.plane",
  "detailType": "sbt_aws_provisionSuccess",
  "detail": {
    "tenantId": "e6878e03-ae2c-43ed-a863-08314487318b",
    "tenantS3Bucket": "tenant-123-bucket",
    "tenantConfig": {
      "userPoolId": "...",
      "apiGatewayUrl": "..."
    },
    "tenantStatus": "created"
  }
}

Step 4: Control Plane Update
─────────────────────────────
┌─────────────────────────────┐
│ Control Plane               │
│ ├─ Receive success event    │
│ ├─ Update tenant record     │
│ │  (status: 'active')       │
│ ├─ Store tenant config      │
│ └─ Send welcome email       │
└─────────────────────────────┘
```

## Key Benefits

### 1. Instant API Response
```
Traditional Approach: 2-5 minutes (blocking)
Event-Driven Approach: < 1 second (async)
```

### 2. Fault Tolerance
```
If provisioning fails:
├─ Application Plane publishes failure event
├─ Control Plane updates tenant status to 'failed'
├─ Retry mechanism can be triggered
└─ User notified of failure
```

### 3. Independent Scaling
```
Control Plane:
├─ Handles high API request volume
├─ Scales based on API traffic
└─ Lightweight operations (DB writes, auth)

Application Plane:
├─ Handles resource-intensive provisioning
├─ Scales based on provisioning queue depth
└─ Heavy operations (CloudFormation, K8s)
```

### 4. Extensibility
```
Adding new provisioning steps:
├─ No changes to Control Plane API
├─ Add new event listener in Application Plane
├─ Publish new event types as needed
└─ Multiple Application Planes can listen to same events
```

### 5. Observability
```
Event flow provides:
├─ Audit trail (all events logged)
├─ Progress tracking (event sequence)
├─ Debugging (event payload inspection)
└─ Metrics (event timing, success/failure rates)
```

## open-sbt Adaptation: NATS Instead of EventBridge

The zero-ops platform uses **NATS** instead of AWS EventBridge for the same pattern:

```
SBT-AWS:                    open-sbt:
─────────                   ─────────
EventBridge                 NATS
├─ AWS-specific             ├─ Cloud-agnostic
├─ Managed service          ├─ Self-hosted
├─ Pay per event            ├─ No per-event cost
└─ AWS SDK integration      └─ NATS client library

Same Pattern, Different Technology:
├─ Control Plane publishes to NATS
├─ Application Plane subscribes to NATS
├─ Same event naming convention (opensbt_*)
└─ Same async communication benefits
```

### open-sbt Event Flow

```go
// Control Plane: Publish onboarding event to NATS
eventBus.Publish(ctx, Event{
    DetailType: "opensbt_onboardingRequest",
    Source:     "zerosbt.control.plane",
    Detail: map[string]interface{}{
        "tenantId": "tenant-123",
        "tier":     "premium",
        "name":     "acme-corp",
        "email":    "admin@acme.com",
    },
})

// Application Plane: Subscribe to NATS events
eventBus.Subscribe(ctx, "opensbt_onboardingRequest", func(event Event) error {
    // Provision tenant resources
    result, err := provisioner.ProvisionTenant(ctx, ProvisionRequest{
        TenantID: event.Detail["tenantId"].(string),
        Tier:     event.Detail["tier"].(string),
    })
    
    if err != nil {
        // Publish failure event
        return eventBus.Publish(ctx, Event{
            DetailType: "opensbt_provisionFailure",
            Source:     "zerosbt.application.plane",
            Detail: map[string]interface{}{
                "tenantId": event.Detail["tenantId"],
                "error":    err.Error(),
            },
        })
    }
    
    // Publish success event
    return eventBus.Publish(ctx, Event{
        DetailType: "opensbt_provisionSuccess",
        Source:     "zerosbt.application.plane",
        Detail: map[string]interface{}{
            "tenantId":     event.Detail["tenantId"],
            "tenantConfig": result.Config,
        },
    })
})
```

## Summary: Problems Solved

### 1. **API Timeout Problem**
- **Before**: API blocks for 2-5 minutes during provisioning
- **After**: API returns in < 1 second, provisioning happens async

### 2. **Failure Handling Problem**
- **Before**: If provisioning fails, API returns error, no retry
- **After**: Failure events trigger retry logic, user notified

### 3. **Scalability Problem**
- **Before**: Control Plane and provisioning tightly coupled
- **After**: Independent scaling based on workload type

### 4. **Extensibility Problem**
- **Before**: Adding provisioning steps requires API changes
- **After**: Add new event listeners without touching API

### 5. **Observability Problem**
- **Before**: No visibility into provisioning progress
- **After**: Event stream provides complete audit trail

### 6. **Multi-Tenant Isolation Problem**
- **Before**: Provisioning errors affect all tenants
- **After**: Per-tenant event streams, isolated failures

## Conclusion

The Event-Driven EventBridge (or NATS) pattern between Control Plane and Application Plane solves the fundamental problem of **decoupling tenant management from tenant provisioning**. This enables:

- **Fast API responses** (< 1 second)
- **Reliable provisioning** (retry on failure)
- **Independent scaling** (API vs. provisioning)
- **Easy extensibility** (add new event handlers)
- **Complete observability** (event audit trail)

This pattern is the foundation of scalable, reliable multi-tenant SaaS architectures.
