# OpenMeter Provider

This package implements the `IMetering` and `IBilling` interfaces using OpenMeter as the backend.

## Architecture

**Namespace Isolation**: All methods accept an explicit `namespace` parameter for multi-tenant isolation. The namespace maps 1:1 with `tenant_id`.

**Fail-Open Pattern**: Entitlement checks fail-open when OpenMeter is unreachable, allowing access with `IsFallback: true`.

**Retry Logic**: Rate limit errors trigger exponential backoff retry (1s, 2s, 4s, 8s, 16s, max 5min).

## Files

- `metering.go` - IMetering implementation (subject management, usage queries, entitlements)
- `billing.go` - IBilling implementation (subscriptions, invoices)
- `retry.go` - Exponential backoff retry logic

## Usage

```go
// Create metering provider
meteringProvider, err := openmeter.NewMeteringProvider("http://openmeter:8080")
if err != nil {
    return err
}

// Register subject with namespace isolation
err = meteringProvider.RegisterSubject(ctx, "tenant-123", "tenant-123#user-456", metadata)

// Create billing provider
billingProvider, err := openmeter.NewBillingProvider("http://openmeter:8080")
if err != nil {
    return err
}

// Create subscription
err = billingProvider.CreateSubscription(ctx, "tenant-123", "tenant-123#user-456", "plan-pro", opts)
```

## OpenMeter SDK Integration

**Status**: Skeleton implementation. SDK integration pending OpenMeter deployment (Phase 5).

All methods currently return `"not yet implemented - requires OpenMeter SDK integration"` errors. SDK will be integrated after OpenMeter is deployed to the Hub cluster.

## Design References

- **Namespace Injection**: `archived/billing-metering/openmeter/openmeter/entitlement/adapter/entitlement.go:L62-L68`
- **Namespace Manager**: `archived/billing-metering/openmeter/openmeter/namespace/namespace.go:L42-L48`
- **Proration Support**: `archived/billing-metering/openmeter/openmeter/productcatalog/pro_rating.go:L23-L29`
- **Subscription Service**: `archived/billing-metering/openmeter/openmeter/subscription/service/service.go:L138-L180`

## Correctness Properties

1. **Namespace Isolation**: All SDK calls include explicit namespace parameter
2. **Input Validation**: All methods validate required parameters before SDK calls
3. **Fail-Open**: Entitlement checks allow access when OpenMeter unreachable
4. **Retry Safety**: Exponential backoff prevents rate limit cascades
5. **Idempotency**: Subject registration uses upsert semantics
