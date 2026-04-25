Based on the two dashboards you provided, we are dealing with two distinctly different data problems:

1.  **Image 1 (Billing / Usage Details):** This is an **Entitlements & Quotas** problem. It compares *Current State* (Usage) against a *Contract* (Limit). Some reset daily/monthly (APIs, Emails), and some are absolute persistent counts (Storage, Portal Users, Applications).
2.  **Image 2 (Metrics / User Activity):** This is an **Analytics & Time-Series** problem. It requires aggregating historical data over time (DAU, MAU, trends, hit counts).

To achieve this in your Zero-Ops platform without over-engineering or creating tight coupling, here is the idiomatic, enterprise-grade pattern to implement in `open-sbt`.

---

### Step 1: The Metric Taxonomy (The Most Critical Step)

Before writing any code, you must classify your metrics into two buckets. Your architecture handles these two buckets differently.

**Category A: Consumables (Metered)**
*   *Examples from UI:* Developer APIs (Day), AI Calls (Month), Batch Blocks (Day), Sender Emails.
*   *Characteristic:* They increment continuously and **reset** at the end of a billing cycle (Daily/Monthly).
*   *Engine:* **OpenMeter**.

**Category B: Stateful Resources (Gauges/Counts)**
*   *Examples from UI:* Applications, Portal Users, Storage (MB), Datasources.
*   *Characteristic:* They go up and down (e.g., User created = +1, User deleted = -1). They **do not reset**.
*   *Engine:* **Local Spoke Database (Primary Source of Truth)** synced to OpenMeter.

---

### Step 2: Architecture for Image 1 (Entitlements & Limits)

To render the "Usage Details" cards (e.g., `22 / 1,000,000 Records`), you need an Entitlements Engine. **OpenMeter natively supports Entitlements.**

#### The Ingestion & Sync Pattern
1.  **For Consumables (APIs, Emails):**
    *   Your Spoke API Gateway (Envoy) or SDK emits an event to the local NATS Leaf node: `spoke.tenant123.usage.api_call`.
    *   Hub NATS JetStream consumes this and pushes it to OpenMeter.
2.  **For Stateful Resources (Users, Apps, Records):**
    *   *Eventual Consistency Risk:* Relying solely on `+1` and `-1` events over a message bus for database records is dangerous. If a `-1` event is dropped, the user is permanently locked out of their quota.
    *   *The Enterprise Pattern (Reconciliation):* The Spoke Controller runs a lightweight cron job every 5 minutes. It executes `SELECT count(*) FROM users WHERE tenant_id = '123'`, compares it to the last known state, and emits an absolute value event to the Hub: `spoke.tenant123.state.users = 10`. OpenMeter records this gauge.

#### Serving the UI
When the PaaS Tenant Admin opens the Billing Dashboard:
1.  The UI calls the Hub Control Plane API: `GET /api/v1/billing/entitlements`
2.  The Hub API queries the **OpenMeter Entitlements API**.
3.  OpenMeter returns the exact JSON needed for the UI cards:
    ```json
    {
      "developer_apis": { "used": 5000, "limit": 5000, "reset": "daily" },
      "portal_users": { "used": 2, "limit": 10, "reset": "none" },
      "storage_mb": { "used": 0, "limit": 30720, "reset": "none" }
    }
    ```

---

### Step 3: Real-Time Enforcement (Rate Limiting & Gates)

How do you actually *stop* a user when the UI says `5,000 / 5,000 Developer APIs`? As discussed, we do **not** use GitOps for this.

#### Pattern A: API Gateway Edge Enforcement (For APIs, Bandwidth, Hits)
You use **Envoy Global Rate Limiting (RLS)** backed by **Redis** running in the Spoke cluster.
1.  OpenMeter (in the Hub) knows the source of truth for usage.
2.  OpenMeter has a feature to continuously sync quota balances down to a Redis instance.
3.  You deploy Redis in the Spoke cluster. The Hub syncs the remaining API balances to the Spoke's Redis.
4.  When a request hits the Spoke Envoy Proxy, Envoy checks Spoke Redis (sub-millisecond latency). If the balance is `0`, Envoy returns `429 Too Many Requests`.

#### Pattern B: Application Logic Enforcement (For Users, Apps, Custom Entities)
When a user clicks "Create Application":
1.  The PaaS Tenant's code uses the `open-sbt` SDK.
2.  The SDK queries the local Spoke PostgREST / DB to check the current count *locally* against the limit.
    ```typescript
    // Inside the PaaS tenant's backend
    const entitlement = await sbt.entitlements.check('applications', tenantId);
    if (!entitlement.hasAccess) {
        throw new Error(`Upgrade your plan to create more than ${entitlement.limit} applications.`);
    }
    // Proceed with DB insert
    ```
*(Note: You push the limits from the Hub Tier config down to the Spoke Control Plane DB, so this SDK check requires zero cross-cluster network calls).*

---

### Step 4: Architecture for Image 2 (Analytics & Metrics)

The second image requires historical time-series data ("Stickiness average", "User access trend"). OpenMeter is for billing; for pure analytics, you use **ClickHouse** (or VictoriaMetrics, though ClickHouse is industry standard for product analytics).

#### The Telemetry Pipeline
1.  **Ingestion:** The SDK provides an analytics tracker: `sbt.analytics.track('page_view', { userId: 'bob' })`.
2.  **Transport:** SDK -> Spoke NATS -> Hub NATS.
3.  **ETL:** Use **Benthos** (or Vector) in the Hub to consume the NATS stream, batch the JSON, and `INSERT` into ClickHouse.

#### The ClickHouse Schema (Star Schema Pattern)
Create a table optimized for the exact charts on this page:

```sql
CREATE TABLE product_analytics_events (
    tenant_id UUID,
    user_id UUID,
    event_type String, -- e.g., 'app_accessed', 'api_hit'
    application_id String,
    timestamp DateTime,
    date Date MATERIALIZED toDate(timestamp)
) ENGINE = MergeTree()
PARTITION BY date
ORDER BY (tenant_id, event_type, timestamp);
```

#### Serving the Metrics UI
To populate the "Daily average hits per user" and "Stickiness average" (DAU/MAU ratio), your Hub Control Plane API provides an `/analytics` endpoint that executes optimized ClickHouse queries.

```sql
-- Query to get "Users accessed" and "Stickiness" for a specific tenant
SELECT 
    uniqExact(user_id) AS total_users_accessed,
    (uniqExactIf(user_id, date = today()) / uniqExactIf(user_id, date >= today() - 30)) * 100 AS stickiness_percentage
FROM product_analytics_events
WHERE tenant_id = 'your-tenant-id';
```
This is returned directly to the UI to populate those specific widgets.

---

### Summary: The `open-sbt` Developer Experience

To achieve these two dashboards, the SaaS builder (your PaaS tenant) shouldn't need to know about NATS, ClickHouse, or Envoy. Their experience with the SDK should look exactly like this:

**1. Tracking a Consumable (Powers Image 1: "AI Calls"):**
```typescript
// No need to check limits, just report. Envoy/Redis handles the dropping if they go over.
await sbt.metering.increment('ai_calls', 1, { 
    tenantId: currentTenant,
    userId: currentUser 
});
```

**2. Gating a Stateful Resource (Powers Image 1: "Portal Users"):**
```typescript
// Guard clause before doing the action
await sbt.entitlements.enforce('portal_users', 1, currentTenant);

// If it doesn't throw, proceed with DB logic
await db.users.insert(...);
```

**3. Tracking Activity (Powers Image 2: "User Activity"):**
```typescript
// Send a pure telemetry event for analytics
await sbt.analytics.track('application_accessed', {
    tenantId: currentTenant,
    userId: currentUser,
    appId: 'app-xyz'
});
```

### The Takeaway for your Architecture
By combining **OpenMeter** (for Entitlements and Consumable Quotas - Image 1), **ClickHouse** (for time-series Analytics - Image 2), and **Envoy+Redis** (for edge rate limiting), you create a true enterprise-grade PaaS foundation. The `open-sbt` Go codebase simply acts as the control plane orchestrator that provisions these tools and syncs configuration between them.