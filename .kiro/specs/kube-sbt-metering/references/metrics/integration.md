Based on your architecture diagram, the provided `open-sbt` codebase, and the target UI you want your PaaS tenants to offer to their users, here is a comprehensive architectural and implementation roadmap.

Currently, your `open-sbt` codebase implements metering and tiering using PostgreSQL (`internal/opensbt/providers/metering/metering.go` and `tiermanager.go`). However, your architecture diagram correctly identifies that for high-volume, multi-tenant billing events, **ClickHouse** should be the target. Furthermore, to render the requested UI, you need to seamlessly marry **Usage (Metering)** with **Limits (Entitlements/Tiering)**.

Here are the necessary implementations and abstractions to add to `open-sbt` to provide this "out-of-the-box" for your PaaS tenants.

---

### 1. Shift Metering from PostgreSQL to ClickHouse (As per your Architecture)
OLTP databases (Postgres) will bottleneck quickly when receiving continuous telemetry/billing events from multiple spokes. 

**Implementation required in `open-sbt`:**
Create a new provider `internal/opensbt/providers/clickhouse/metering.go` that implements `interfaces.IMetering`.

*   **Ingestion Path**: Instead of synchronous API calls, your Spoke apps should publish events to the local **NATS Leaf Node** on the subject `spoke.{tenant_id}.billing.usage`. The Hub NATS JetStream cluster will ingest this. 
*   **Vector/Benthos Sink**: Deploy an agent (like Vector or Benthos) subscribed to the Hub NATS JetStream that batches these events into ClickHouse via its native HTTP interface.
*   **ClickHouse Schema**:
    ```sql
    -- Raw events (fast inserts)
    CREATE TABLE usage_events_raw (
        tenant_id UUID,
        meter_id String,
        value Float64,
        timestamp DateTime,
        properties String
    ) ENGINE = MergeTree()
    ORDER BY (tenant_id, meter_id, timestamp);

    -- Aggregated Materialized View (for instant UI reads)
    CREATE MATERIALIZED VIEW usage_daily_mv
    ENGINE = AggregatingMergeTree() ORDER BY (tenant_id, meter_id, day)
    AS SELECT
        tenant_id,
        meter_id,
        toStartOfDay(timestamp) AS day,
        sumState(value) AS total_usage
    FROM usage_events_raw GROUP BY tenant_id, meter_id, day;
    ```

### 2. Evolve `TierManager` into an `EntitlementManager`
Currently, your `TierQuotas` struct (`internal/opensbt/models/tier.go`) hardcodes fields like `Users`, `StorageGB`, and `APIRequests`, with a generic `Custom map[string]interface{}`. 

To support the highly dynamic UI in the screenshot (Widgets, Custom APIs, AI Fields, etc.), you need a dynamic Entitlement model.

**Implementation required:**
*   Refactor `TierQuotas` to map Meter IDs to Limits directly.
    ```go
    // internal/opensbt/models/tier.go
    type TierConfig struct {
        Name        string                 `json:"name"`
        // Features are boolean flags (e.g. "SSO_ENABLED")
        Features    map[string]bool        `json:"features"` 
        // Quotas map exactly to meter_id strings
        Quotas      map[string]int64       `json:"quotas"`   // -1 for unlimited
    }
    ```
*   Create a unified **Entitlement API** that merges Tier Quotas with ClickHouse usage.

### 3. Expose a Unified "Usage vs. Entitlements" API for the Spoke
The UI in your screenshot needs a single API response that says: *"For Metric X, the limit is Y, and you have used Z."* 

Since the Spoke Application Plane UI queries **PostgREST** (according to your architecture diagram), but the usage data lives in the Hub's ClickHouse, you have two architectural choices to serve this to the PaaS tenant's users:

#### Option A: Cross-Database Foreign Data Wrapper (FDW) / ClickHouse Postgres Interface
ClickHouse exposes a PostgreSQL wire protocol. You can map the ClickHouse aggregated view into the Spoke's PostgreSQL database using `postgres_fdw`. 
*   **Pros**: PostgREST can query it natively. RLS policies can be applied seamlessly at the Spoke Postgres level.
*   **Cons**: Complex to manage FDW connections dynamically per Spoke.

#### Option B: Hub Syncs Aggregates to Spoke Control Plane DB (Recommended)
Add a background worker to your Hub Control plane that queries ClickHouse hourly and pushes the aggregated usage into the `Control Plane Shared DB` on the Spoke.
1. Add a table to the Spoke Control Plane Shared DB: `tenant_entitlements_view`.
2. Update this table asynchronously via NATS or direct connection.
3. The Spoke UI hits PostgREST: `GET /tenant_entitlements_view`

**Data Shape returned to the Spoke UI (PostgREST):**
```json
[
  {
    "meter_id": "records",
    "display_name": "Records",
    "used": 22,
    "limit": 1000000,
    "unit": "Count",
    "reset_period": "Monthly"
  },
  {
    "meter_id": "storage_mb",
    "display_name": "Storage",
    "used": 0,
    "limit": 30720,
    "unit": "MB",
    "reset_period": "None"
  }
]
```

### 4. Provide an SDK for the PaaS Tenants
To make this "zero-ops" for the SaaS builders (your PaaS tenants), you need to provide them with a lightweight SDK they use inside their Application Plane code to emit events.

**Example TS/JS SDK for your Tenants:**
```typescript
import { OpenSBT } from '@opensbt/sdk';

// Initialize automatically reads NATS credentials injected by Crossplane/ESO
const sbt = new OpenSBT(); 

// PaaS Tenant emits an event when their user creates a record
await sbt.metering.recordUsage({
    tenantId: currentContext.tenantId, // The end-user tenant
    meterId: "records",
    value: 1
});

// Middleware for API limiting
app.post('/custom-api', sbt.entitlements.enforceQuota('custom_api_calls'), (req, res) => {
    // Business logic
});
```
*Behind the scenes*, this SDK publishes a message to `spoke.{tenantId}.billing.usage` on the local NATS Leaf Node, ensuring asynchronous, non-blocking execution for the tenant's app.

### 5. Overages and Webhooks
To complete the billing loop, `open-sbt` needs to handle what happens when a limit is reached.

**Implementation required in `Agent-Core` / `ControlPlane`:**
*   **Threshold Alerts**: Create a service in the Hub that tails the ClickHouse materialized view. When `used >= limit * 0.90`, it emits a NATS event: `hub.notifications.alert.quota_nearing`.
*   **Enforcement**: When `used >= limit`, the Hub emits `spoke.{tenantId}.billing.quota_exceeded`. 
*   **Spoke Enforcement**: The Spoke-controller listens to this event and updates a local Redis cache or Postgres flag. The PaaS tenant's API gateway / application code checks this flag to reject new requests with `429 Too Many Requests`.

### Summary of Next Steps for your Codebase

1.  **Deprecate/Convert `internal/opensbt/providers/metering/metering.go`**: Stop using Postgres for `usage_events`. Keep Postgres for `meters` definition, but route `IngestUsageEvent` to NATS -> ClickHouse.
2.  **Refactor `ITierManager`**: Remove hardcoded structs (`StorageGB`, `APIRequests`). Make quotas a dynamic Key-Value map matching `meter_id`s.
3.  **Create an `IEntitlementService`**: A new interface in `open-sbt` that calculates `Limit - Used` by querying the Tier DB and ClickHouse.
4.  **Update `manifests/spoke-catalog/infra/`**: Ensure the NATS Leaf node configuration has subjects configured to forward `spoke.*.billing.usage` upstream to the Hub.
5.  **Build the PostgREST View**: Add a SQL migration to the Spoke Database template that joins the local tenant configs with the usage data pushed from the hub, wrapped in an RLS policy (`USING (tenant_id = current_setting('app.tenant_id'))`), so PaaS tenants can just plug their UI components into it.