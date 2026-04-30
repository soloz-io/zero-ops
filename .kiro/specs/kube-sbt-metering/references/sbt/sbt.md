Thank you for providing those definitive answers and the excellent UI references from Zoho Creator. This clears up the ambiguity perfectly.

To bring `open-sbt` in line with the `sbt-aws` project—while using your CNCF stack (Ory, NATS, OpenMeter, PostgREST, Infisical)—the recommended approach is the **Event-Driven Orchestration Pattern**. 

In `sbt-aws`, the Control Plane does not use distributed transactions (two-phase commits) to keep disparate systems in sync. Instead, it relies on **Choreography via an Event Bus** (EventBridge in AWS; NATS JetStream in our CNCF stack).

Here is the recommended pattern to solve the critical gaps and implement the toolkits for your SaaS tenants.

---

### Pattern 1: The "Split-Brain" User Creation Flow (NATS Choreography)
**The Gap:** Keeping Ory Kratos (Identity), OpenMeter (Billing), and the Tenant DB (App Data) in sync when a SaaS Admin invites a user.
**The `sbt-aws` Pattern:** API Gateway triggers a Lambda -> updates Auth -> emits event -> downstream services react.

**The `open-sbt` CNCF Solution:**
1. **Admin Action:** The SaaS Admin uses the `sbt-sdk` in their React frontend to invite `kiroagent00@gmail.com`. The SDK calls `POST /api/v1/users` on `open-sbt`.
2. **Identity Creation:** `open-sbt`'s `IAuth` implementation calls Ory Kratos to create an identity. Kratos automatically sends the "You've been invited" email containing a magic link or password reset flow.
3. **Event Emission:** `open-sbt` publishes an `opensbt_tenantUserCreated` event to **NATS JetStream**, containing the `tenant_id` and `user_id`.
4. **Choreographed Listeners (Reacting to NATS):**
   * **Listener A (OpenMeter integration):** The `IMetering` service in the Hub listens to this event and registers the Subject (`{tenant_id}#{user_id}`) in OpenMeter.
   * **Listener B (Database Sync):** A dedicated worker in `open-sbt` catches this event, connects to the Spoke's PostgreSQL database (via standard PG connection or PostgREST), and `INSERT`s the user record into `tenant_<id>_db.public.users`.

*Why this works:* If OpenMeter is temporarily down, NATS JetStream will continually retry Listener A until the Subject is registered, ensuring eventual consistency without blocking the Admin UI.

---

### Pattern 2: 100% OpenMeter Delegation for Entitlements
**The Gap:** Where is the source of truth for quotas, and how do we tolerate async OTLP latency?
**The `sbt-aws` Pattern:** Abstracted via the `IMetering` interface.

**The `open-sbt` CNCF Solution:**
Since async overage is acceptable, you do not need a distributed cache (like Redis) checking quotas on every single API request in the hot path. 

1. **The Hot Path (Spoke):** AgentGateway blindly extracts `tenant_id` and `user_id` from the Kratos JWT and emits OTLP metrics to OpenMeter in the background. It does *not* block requests to check quotas.
2. **The Control Path (Hub):** The SaaS Admin UI (using `sbt-sdk`) renders the "Usage Details" dashboard by calling `sbt.billing.getEntitlements()`. 
3. `open-sbt` implements this via `GET /api/v1/entitlements`. It extracts the `tenant_id` from the Admin's JWT, sets the `OpenMeter-Namespace: <tenant_id>` header, and queries OpenMeter's Entitlement API directly. 
4. **Enforcement (Optional):** If you *do* want to cut off access when limits are reached, OpenMeter can be configured to emit Webhooks when an entitlement is exhausted. `open-sbt` can listen to this and update a flag in `tenant_<id>_db.public.users` or Ory Keto to block future logins/actions.

---

### Pattern 3: Soft-Deletion for Data Retention (GDPR/Invoicing)
**The Gap:** Deleting a user destroys financial records in OpenMeter if not handled properly.
**The `sbt-aws` Pattern:** Emit an `Offboarding` event and let services handle cleanup safely.

**The `open-sbt` CNCF Solution:**
1. Admin clicks "Delete User" in the UI. SDK calls `DELETE /api/v1/users/<id>`.
2. `open-sbt` calls Ory Kratos to delete/disable the login credentials.
3. `open-sbt` emits an `opensbt_tenantUserDeleted` event to NATS.
4. **Listener A (Tenant DB):** Updates the `public.users` table, setting `status = 'inactive'` or `deleted_at = NOW()`.
5. **Listener B (OpenMeter):** The `IMetering` service *ignores* this event or simply removes active subscriptions. It explicitly **does not** delete the OpenMeter subject, preserving all historical usage and invoices for that user.

---

### Pattern 4: Stripe Webhook Idempotency (The "Inbox Pattern")
**The Gap:** Stripe guarantees at-least-once delivery; duplicates will cause double-counting or errors.

**The `open-sbt` CNCF Solution:**
Instead of processing Stripe webhooks immediately, implement the **Transactional Inbox Pattern** in your Hub PostgreSQL database.
1. Stripe webhook hits `POST /api/v1/billing/webhook` in `open-sbt`.
2. `open-sbt` attempts to `INSERT INTO processed_webhooks (event_id) VALUES ('evt_123')`.
3. If the insert fails (Unique Constraint Violation), `open-sbt` immediately returns `200 OK` to Stripe and stops (it's a duplicate).
4. If it succeeds, `open-sbt` translates the Stripe payload into an `opensbt_billingSuccess` event and publishes it to NATS.

---

### Pattern 5: Infisical Secret Management for Stripe
**The Gap:** SaaS Tenants need to provide their own Stripe API keys securely.

**The `open-sbt` CNCF Solution:**
1. The SaaS Tenant enters their Stripe API keys in the Admin UI. `sbt-sdk` calls `POST /api/v1/billing/config`.
2. `open-sbt` takes the API key and uses the **Infisical SDK/API** to store the secret under a path like `/tenants/<tenant_id>/stripe_api_key`.
3. Because `open-sbt` runs in the Hub, it natively has access to Infisical. When OpenMeter needs to sync with Stripe, `open-sbt` retrieves the key from Infisical on the fly, keeping the secret completely out of the Spoke cluster and out of standard K8s secrets (unless explicitly needed by External Secrets Operator).

---

### Updated Interface Definitions for `open-sbt`

To implement this pattern, your Go interfaces in `open-sbt` should look exactly like `sbt-aws`:

```go
// IAuth handles Identity via Ory Kratos
type IAuth interface {
    CreateUser(ctx context.Context, tenantID string, user User) (string, error)
    DeleteUser(ctx context.Context, tenantID string, userID string) error
    // ...
}

// IMetering handles OpenMeter integration
type IMetering interface {
    // Translates to OpenMeter Plan/Feature APIs with Namespace header
    CreatePlan(ctx context.Context, tenantID string, plan Plan) error
    GetEntitlements(ctx context.Context, tenantID string) ([]Entitlement, error)
    
    // NATS Event Listeners
    OnUserCreated(ctx context.Context, event NatsEvent) error // Registers OpenMeter Subject
}

// IBilling handles Subscriptions & Stripe
type IBilling interface {
    SubscribeUser(ctx context.Context, tenantID string, userID string, planID string) error
    GetInvoicePreview(ctx context.Context, tenantID string, userID string) (Invoice, error)
    
    // Webhook receiver
    HandleStripeWebhook(ctx context.Context, payload []byte) error
}
```

### Summary of the Developer Experience (DX)
By adopting this pattern:
1. **The SaaS Tenant (Your Customer)** gets a beautiful React UI using `sbt-sdk`. They don't know NATS, Kratos, or OpenMeter exist. They just call `sbt.users.create()` and `sbt.billing.getUsage()`.
2. **`open-sbt` (Your Hub API)** acts as the traffic cop. It ensures Tenant A cannot access Tenant B's data by enforcing JWT validation and injecting the OpenMeter Namespace header.
3. **The Spoke Cluster** remains completely dumb and highly scalable. It just runs the tenant's app code, PostgREST, and an AgentGateway that blindly fires OTLP packets. 

Are you ready to move to the **Technical Design Specification** phase, or are there any specific components of this pattern you'd like to refine further?