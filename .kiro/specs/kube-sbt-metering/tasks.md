# Task List

## Phase 1: Foundation - OpenMeter Provider & Core Interfaces

- [ ] 1. Implement IMetering and IBilling interface definitions
  - [ ] Create `internal/opensbt/interfaces/metering.go` with IMetering interface
  - [ ] Create `internal/opensbt/interfaces/billing.go` with IBilling interface
  - [ ] Add interface documentation with architectural notes about GitOps vs REST API separation

- [ ] 2. Implement core domain models for metering and billing
  - [ ] Create `internal/opensbt/models/metering.go` with Meter, Feature, Plan, Subject models
  - [ ] Create `internal/opensbt/models/billing.go` with Subscription, Invoice, EntitlementStatus models
  - [ ] Implement GenerateSubjectID and ParseSubjectID helper functions
  - [ ] Add model validation logic

- [ ] 3. Implement OpenMeter MeteringProvider with namespace isolation
  - [ ] Create `internal/opensbt/providers/openmeter/metering.go`
  - [ ] Implement NewMeteringProvider with OpenMeter Go SDK client initialization
  - [ ] Implement RegisterSubject and DeleteSubject methods with explicit namespace parameters
  - [ ] Implement GetSubject and ListSubjects methods
  - [ ] Implement read-only catalog methods: GetMeter, ListMeters, GetFeature, ListFeatures, GetPlan, ListPlans

- [ ] 4. Implement usage query methods with tenant aggregation
  - [ ] Implement GetUsage method with GroupBy support for tenant-level aggregation
  - [ ] Implement GetTenantUsage method using GroupBy["tenant_id"] pattern
  - [ ] Implement GetUserUsage method with subject-scoped filtering
  - [ ] Add exponential backoff retry logic for rate limit errors

- [ ] 5. Implement entitlement checking with fail-open pattern
  - [ ] Implement CheckEntitlement method querying OpenMeter Entitlement API
  - [ ] Add fail-open logic when OpenMeter is unreachable
  - [ ] Return EntitlementStatus with IsFallback flag
  - [ ] Add logging for fallback scenarios

- [ ] 6. **CHECKPOINT 1: OpenMeter Provider Foundation**
  - **Deliverable**: Functional OpenMeter provider implementing IMetering interface with subject management, usage queries, and entitlement checking
  - **Verification Criteria**:
    - Subject registration creates subjects in OpenMeter with correct namespace isolation
    - Usage queries return aggregated metrics for tenant-level and user-level scopes
    - Entitlement checking returns correct hasAccess, used, limit values
    - Fail-open behavior activates when OpenMeter is unreachable
  - **Test Script**:
    - Validate OpenMeter provider by running `/scripts/test-openmeter-provider.sh` in a real Hub cluster environment with OpenMeter deployed
    - Script must verify subject CRUD operations, usage query responses, and entitlement API responses
    - Test fail-open behavior by temporarily blocking OpenMeter connectivity
  - **Success Criteria**: All OpenMeter provider methods functional, namespace isolation enforced, fail-open pattern working

## Phase 2: Billing Provider & Subscription Management

- [ ] 7. Implement OpenMeter BillingProvider for subscription management
  - [ ] Create `internal/opensbt/providers/openmeter/billing.go`
  - [ ] Implement NewBillingProvider with OpenMeter Go SDK client initialization
  - [ ] Implement CreateSubscription method with namespace and subject parameters
  - [ ] Implement GetSubscription and ListSubscriptions methods with namespace filtering

- [ ] 8. Implement subscription lifecycle operations
  - [ ] Implement UpdateSubscription method supporting plan changes and metadata updates
  - [ ] Implement CancelSubscription method marking subscriptions as inactive
  - [ ] Add proration support via ProRatingConfig in subscription updates
  - [ ] Implement MigrateSubscription method for plan version transitions

- [ ] 9. Implement invoice operations
  - [ ] Implement PreviewInvoice method for draft invoice generation
  - [ ] Implement GetInvoice method for retrieving invoices by ID
  - [ ] Implement ListInvoices method with pagination and filtering
  - [ ] Add invoice response mapping from OpenMeter format to internal models

- [ ] 10. **CHECKPOINT 2: Billing Provider Complete**
  - **Deliverable**: Functional billing provider implementing IBilling interface with subscription lifecycle and invoice operations
  - **Verification Criteria**:
    - Subscription creation assigns plans to subjects correctly
    - Subscription updates handle plan changes with proration
    - Subscription cancellation marks subscriptions as inactive without data loss
    - Invoice operations return correct line items, totals, and payment status
  - **Test Script**:
    - Validate billing provider by running `/scripts/test-billing-provider.sh` in Hub cluster with OpenMeter
    - Script must verify subscription CRUD operations, plan migrations, and invoice queries
    - Test proration calculations by changing subscription plans mid-cycle
  - **Success Criteria**: All billing operations functional, proration working, invoice data accurate

## Phase 3: REST API Layer & User Management

- [ ] 11. Implement REST API server with Gin framework
  - [ ] Create `cmd/kube-sbt-api/main.go` with Gin HTTP server initialization
  - [ ] Add middleware for JWT validation and tenant context injection
  - [ ] Add middleware for RFC 7807 Problem Details error responses
  - [ ] Configure CORS, rate limiting, and request logging

- [ ] 12. Implement user management API endpoints
  - [ ] Implement POST /api/v1/tenants/{tenantID}/users for user creation with Ory Kratos integration
  - [ ] Implement GET /api/v1/tenants/{tenantID}/users/{userID} for user retrieval
  - [ ] Implement PUT /api/v1/tenants/{tenantID}/users/{userID} for user updates
  - [ ] Implement DELETE /api/v1/tenants/{tenantID}/users/{userID} for user deletion
  - [ ] Implement GET /api/v1/tenants/{tenantID}/users for user listing with pagination

- [ ] 13. Implement Saga pattern for user creation with rollback
  - [ ] Add Ory Kratos user creation step in user creation handler
  - [ ] Add OpenMeter subject registration step after Kratos user creation
  - [ ] Implement rollback logic: delete Kratos user if subject registration fails
  - [ ] Add retry logic with exponential backoff for rollback failures
  - [ ] Publish orphaned subject events to NATS topic `opensbt_orphanedSubjects` on rollback failure

- [ ] 14. Implement usage query API endpoints
  - [ ] Implement GET /api/v1/tenants/{tenantID}/usage for tenant-scoped usage
  - [ ] Implement GET /api/v1/tenants/{tenantID}/users/{userID}/usage for user-scoped usage
  - [ ] Implement GET /api/v1/tenants/{tenantID}/entitlements for entitlement checking
  - [ ] Add query parameter parsing for time period filtering (start_date, end_date, period)
  - [ ] Add HTTP 503 responses with retry-after header when OpenMeter is unavailable

- [ ] 15. Implement read-only catalog API endpoints
  - [ ] Implement GET /api/v1/meters for listing meters
  - [ ] Implement GET /api/v1/features for listing features
  - [ ] Implement GET /api/v1/plans for listing plans
  - [ ] Add namespace filtering for all catalog endpoints

- [ ] 16. **CHECKPOINT 3: REST API Functional**
  - **Deliverable**: Functional REST API with user management, usage queries, and catalog endpoints
  - **Verification Criteria**:
    - User creation creates Ory Kratos user and OpenMeter subject atomically
    - User deletion triggers subscription cancellation via choreography
    - Usage query endpoints return correct tenant and user metrics
    - Catalog endpoints return read-only meter, feature, and plan data
    - Saga rollback works when subject registration fails
  - **Test Script**:
    - Validate REST API by running `/scripts/test-kube-sbt-api.sh` against deployed API in Hub cluster
    - Script must test user CRUD operations, usage queries, entitlement checks, and catalog reads
    - Test Saga rollback by simulating OpenMeter failures during user creation
  - **Success Criteria**: All API endpoints functional, Saga pattern working, error handling correct

## Phase 4: hub-operator CRDs & GitOps Catalog Management

- [ ] 17. Define Meter CRD for billing catalog
  - [ ] Create `operators/hub-operator/config/crd/billing.nutgraf.in_meters.yaml`
  - [ ] Define Meter spec with slug, description, aggregation, eventType, valueProperty, groupBy fields
  - [ ] Define Meter status with conditions for sync status
  - [ ] Add CRD validation rules and OpenAPI schema

- [ ] 18. Define Feature CRD for billable capabilities
  - [ ] Create `operators/hub-operator/config/crd/billing.nutgraf.in_features.yaml`
  - [ ] Define Feature spec with key, name, meterSlugs fields
  - [ ] Define Feature status with conditions for sync status
  - [ ] Add CRD validation rules

- [ ] 19. Define Plan CRD for pricing tiers
  - [ ] Create `operators/hub-operator/config/crd/billing.nutgraf.in_plans.yaml`
  - [ ] Define Plan spec with key, name, currency, phases, rateCards, proRatingConfig fields
  - [ ] Define Plan status with conditions for sync status and validation errors
  - [ ] Add CRD validation rules for pricing models (flat, usage_based, tiered_volume, tiered_graduated)

- [ ] 20. Implement Meter controller reconciler
  - [ ] Create `operators/hub-operator/internal/controller/meter_controller.go`
  - [ ] Implement Reconcile method syncing Meter CR to OpenMeter via Go SDK
  - [ ] Extract tenantId from CR spec and pass as namespace parameter to OpenMeter
  - [ ] Update CR Status.Conditions with sync status (Synced: True/False)
  - [ ] Implement exponential backoff for failed reconciliation (1s, 2s, 4s, 8s, 16s, max 5min)
  - [ ] Emit Kubernetes Events for reconciliation failures

- [ ] 21. Implement Feature controller reconciler
  - [ ] Create `operators/hub-operator/internal/controller/feature_controller.go`
  - [ ] Implement Reconcile method syncing Feature CR to OpenMeter
  - [ ] Update CR Status.Conditions with sync status
  - [ ] Add validation for referenced meter slugs
  - [ ] Implement exponential backoff and event emission

- [ ] 22. Implement Plan controller reconciler
  - [ ] Create `operators/hub-operator/internal/controller/plan_controller.go`
  - [ ] Implement Reconcile method syncing Plan CR to OpenMeter
  - [ ] Update CR Status.Conditions with sync status
  - [ ] Add validation for referenced feature keys
  - [ ] Implement exponential backoff and event emission
  - [ ] Handle Plan deletion by deleting from OpenMeter

- [ ] 23. **CHECKPOINT 4: GitOps Catalog Management**
  - **Deliverable**: Functional hub-operator with Meter, Feature, Plan CRDs and reconcilers syncing to OpenMeter
  - **Verification Criteria**:
    - Meter CRs render from universal-tenant Helm chart values.yaml
    - ArgoCD syncs rendered CRs to Hub cluster namespace hub-platform-ops
    - hub-operator reconcilers detect CR changes and sync to OpenMeter within 30 seconds
    - CR Status.Conditions reflect sync status with error messages
    - Git commits trigger automatic OpenMeter updates via GitOps pipeline
  - **Test Script**:
    - Validate GitOps catalog by running `/scripts/test-billing-catalog-gitops.sh` in Hub cluster
    - Script must commit billing config changes to fleet-registry, verify ArgoCD sync, check OpenMeter state
    - Test rollback by reverting Git commit and verifying OpenMeter state reverts
  - **Success Criteria**: GitOps workflow functional, CRs sync to OpenMeter, rollback works, audit trail in Git

## Phase 5: OpenMeter Namespace Provisioning & Hub Deployment

- [ ] 24. Implement OpenMeter namespace provisioning in hub-operator
  - [ ] Add namespace provisioning logic to HubEnvironment controller
  - [ ] Use OpenMeter's `namespace.Manager` Go API for namespace lifecycle
  - [ ] Provision namespace in Phase 0 (before database migrations)
  - [ ] Add OpenMeterNamespaceConfigured condition to HubEnvironment status
  - [ ] Handle namespace deletion during tenant offboarding

- [ ] 25. Create OpenMeter Helm deployment manifests
  - [ ] Create `manifests/hub/openmeter/` directory
  - [ ] Add Helm values for OpenMeter official chart
  - [ ] Configure node selectors: `node-role.kubernetes.io/worker=true`
  - [ ] Configure HA settings (3+ replicas, PodDisruptionBudget)
  - [ ] Configure OTLP ingestion endpoint accessible from Spoke clusters
  - [ ] Configure REST API endpoint accessible only from kube-sbt (Hub internal)

- [ ] 26. Create ArgoCD Application for OpenMeter deployment
  - [ ] Create `manifests/hub/argocd-apps/openmeter.yaml`
  - [ ] Configure sync policy and sync waves
  - [ ] Add health checks for OpenMeter pods
  - [ ] Configure namespace: hub-platform-ops

- [ ] 27. **CHECKPOINT 5: OpenMeter Deployed & Namespace Provisioning**
  - **Deliverable**: OpenMeter deployed in Hub cluster with automatic namespace provisioning for tenants
  - **Verification Criteria**:
    - OpenMeter pods running on Hub worker nodes (not control plane)
    - OTLP ingestion endpoint accessible from Spoke clusters
    - REST API endpoint accessible from kube-sbt pods
    - HubEnvironment reconciliation creates OpenMeter namespaces
    - OpenMeterNamespaceConfigured condition appears in HubEnvironment status
  - **Test Script**:
    - Validate OpenMeter deployment by running `/scripts/test-openmeter-deployment.sh` in Hub cluster
    - Script must verify pod readiness, endpoint accessibility, namespace provisioning
    - Test namespace creation by creating HubEnvironment CR and checking OpenMeter namespace exists
  - **Success Criteria**: OpenMeter operational, namespaces auto-provisioned, endpoints accessible

## Phase 6: AgentGateway OTLP Configuration & Metric Collection

- [ ] 28. Create AgentGateway OTLP configuration documentation
  - [ ] Create `docs/metering/agentgateway-otlp-config.md`
  - [ ] Document OpenMeter ingestion endpoint URL format
  - [ ] Document OTLP span attribute format: tenant_id, user_id, meter_id
  - [ ] Document subject format: `{tenant_id}#{user_id}`
  - [ ] Document meter events: api_calls, storage_operations, custom_api_calls, developer_api_calls

- [ ] 29. Create metric collector CronJob template for universal-tenant Helm chart
  - [ ] Create `charts/universal-tenant/templates/metric-collector-cronjob.yaml`
  - [ ] Add configurable schedule (default: */5 * * * * - every 5 minutes)
  - [ ] Add tenant-specific metric selection via values (metering.metrics array)
  - [ ] Configure OTLP emission to local OpenTelemetry Collector

- [ ] 30. Implement metric collector script for stateful metrics
  - [ ] Create `scripts/metric-collector.py` for querying tenant databases
  - [ ] Query stateful metrics: database_rows, storage_bytes, workspace_count, form_count, table_count
  - [ ] Emit OTLP gauge metrics with tenant_id and meter_id attributes
  - [ ] Add error handling and retry logic

- [ ] 31. Configure OpenTelemetry Collector for metric forwarding
  - [ ] Create `manifests/spoke/otel-collector-config.yaml`
  - [ ] Configure receivers for OTLP metrics from metric collectors
  - [ ] Configure exporters for forwarding to OpenMeter (Hub) via mTLS
  - [ ] Add batch processing and retry logic

- [ ] 32. **CHECKPOINT 6: OTLP Emission & Metric Collection**
  - **Deliverable**: AgentGateway configured for OTLP emission and spoke-side metric collectors operational
  - **Verification Criteria**:
    - AgentGateway emits OTLP traces to OpenMeter for authenticated requests
    - Metric collector CronJobs run on schedule in Spoke clusters
    - Stateful metrics (database_rows, storage_bytes, etc.) appear in OpenMeter
    - OpenTelemetry Collector forwards metrics to Hub via mTLS
  - **Test Script**:
    - Validate OTLP emission by running `/scripts/test-otlp-metering.sh` in Spoke cluster
    - Script must verify AgentGateway span emission, metric collector execution, OpenMeter ingestion
    - Check OpenMeter UI for received metrics with correct tenant_id and meter_id attributes
  - **Success Criteria**: OTLP traces flowing to OpenMeter, stateful metrics collected, mTLS working

## Phase 7: Entitlement Caching & Background Sync

- [ ] 33. Implement Redis-based entitlement caching
  - [ ] Add Redis client initialization in kube-sbt API server
  - [ ] Implement cache key format: `entitlement:{namespace}:{subjectID}:{featureKey}`
  - [ ] Set cache TTL to 10 minutes maximum
  - [ ] Implement cache-aside pattern in CheckEntitlement method

- [ ] 34. Implement background entitlement sync worker
  - [ ] Create `internal/opensbt/workers/entitlement_sync.go`
  - [ ] Implement worker querying all active tenant entitlements from OpenMeter every 5 minutes
  - [ ] Update Redis cache with fresh entitlement data
  - [ ] Add metrics for sync duration and cache hit rate

- [ ] 35. Implement synchronous fallback for cache misses
  - [ ] Add fallback logic in AgentGateway when cache miss occurs and OpenMeter unreachable
  - [ ] Call kube-sbt API synchronously before applying fail-open/fail-closed policy
  - [ ] Add timeout and circuit breaker for fallback calls

- [ ] 36. **CHECKPOINT 7: Entitlement Caching Operational**
  - **Deliverable**: Redis-based entitlement caching with background sync and synchronous fallback
  - **Verification Criteria**:
    - Entitlement checks use Redis cache with 10-minute TTL
    - Background worker syncs all tenant entitlements every 5 minutes
    - Cache misses trigger synchronous fallback to kube-sbt API
    - Fail-open policy activates only after fallback fails
  - **Test Script**:
    - Validate entitlement caching by running `/scripts/test-entitlement-cache.sh` in Hub cluster
    - Script must verify cache hit/miss behavior, background sync execution, fallback logic
    - Test fail-open by blocking OpenMeter and verifying access granted with IsFallback=true
  - **Success Criteria**: Caching working, background sync operational, fallback functional

## Phase 8: Reconciliation & DLQ Handling

- [ ] 37. Implement orphaned subject reconciliation controller
  - [ ] Create `cmd/reconciliation-controller/main.go`
  - [ ] Subscribe to NATS topic `opensbt_orphanedSubjects`
  - [ ] Implement reconciliation logic attempting to delete orphaned subjects every 5 minutes
  - [ ] Remove events from DLQ on successful reconciliation
  - [ ] Publish alerts to `opensbt_notifications` after 3 failed attempts

- [ ] 38. Implement DLQ replay API endpoint
  - [ ] Implement POST /api/v1/admin/dlq/replay endpoint
  - [ ] Add `platform_admin` RBAC authorization check
  - [ ] Enforce rate limit of 50 req/sec
  - [ ] Preserve original OTLP Event ID for idempotency
  - [ ] Add audit logging for all replay operations

- [ ] 39. Implement out-of-band billing reconciliation
  - [ ] Create `cmd/billing-reconciliation/main.go`
  - [ ] Implement daily true-up job comparing Spoke metrics against OpenMeter records
  - [ ] Evaluate usage within delay window [NOW - 48h, NOW - 24h]
  - [ ] Query NATS JetStream ConsumerInfo for pending backlog
  - [ ] Abort if backlog exceeds 1,000 messages
  - [ ] Ignore discrepancies below 1% variance or 10 units threshold
  - [ ] Emit correction events to OpenMeter with idempotency keys
  - [ ] Emit Prometheus metric `opensbt_billing_discrepancy_amount`

- [ ] 40. **CHECKPOINT 8: Reconciliation & DLQ Complete**
  - **Deliverable**: Automated reconciliation for orphaned subjects and billing discrepancies with DLQ replay capability
  - **Verification Criteria**:
    - Orphaned subjects automatically cleaned up within 5 minutes
    - DLQ replay API functional with RBAC and rate limiting
    - Billing reconciliation detects and corrects discrepancies daily
    - Audit trail preserved for all reconciliation actions
  - **Test Script**:
    - Validate reconciliation by running `/scripts/test-reconciliation.sh` in Hub cluster
    - Script must simulate orphaned subjects, trigger reconciliation, verify cleanup
    - Test DLQ replay by manually triggering replay and verifying idempotency
    - Test billing reconciliation by injecting discrepancies and verifying corrections
  - **Success Criteria**: Reconciliation working, DLQ replay functional, billing accurate

## Phase 9: Stripe Integration & Subscription Choreography

- [ ] 41. Configure Stripe App in OpenMeter via hub-operator
  - [ ] Create StripeApp CRD (optional, if extending GitOps pattern to secrets)
  - [ ] Implement StripeApp controller retrieving secrets from Infisical via External Secrets Operator
  - [ ] Configure Stripe webhook endpoint: POST /api/v1/apps/{appId}/stripe/webhook
  - [ ] Add webhook signature validation

- [ ] 42. Implement subscription cancellation choreography consumer
  - [ ] Create `internal/opensbt/consumers/subscription_choreography.go`
  - [ ] Subscribe to subject deletion events
  - [ ] Trigger CancelSubscription for all active subscriptions tied to deleted subject
  - [ ] Add idempotency checks to prevent duplicate cancellations

- [ ] 43. Implement subscription migration API
  - [ ] Add MigrateSubscription method to IBilling interface
  - [ ] Implement plan version transition logic
  - [ ] Inherit billing anchor date from canceled subscription
  - [ ] Accept proration_behavior parameters (create_prorated_invoice, none, credit_next_invoice)

- [ ] 44. **CHECKPOINT 9: Stripe Integration & Choreography**
  - **Deliverable**: Stripe payment integration with automated subscription cancellation on subject deletion
  - **Verification Criteria**:
    - Stripe webhooks routed to OpenMeter and processed correctly
    - Invoice state updates (paid/failed/voided) reflected in OpenMeter
    - Subject deletion triggers automatic subscription cancellation
    - Subscription migration preserves billing cycle continuity
  - **Test Script**:
    - Validate Stripe integration by running `/scripts/test-stripe-integration.sh` in Hub cluster
    - Script must simulate Stripe webhook events, verify invoice state updates
    - Test choreography by deleting subject and verifying subscriptions cancelled
    - Test migration by transitioning subscription across plan versions
  - **Success Criteria**: Stripe webhooks working, choreography functional, migration correct

## Phase 10: Security, Compliance & Backpressure

- [ ] 45. Configure Istio service mesh for kube-sbt
  - [ ] Add Istio sidecar injection label to kube-sbt deployment
  - [ ] Create PeerAuthentication resource enforcing STRICT mTLS mode
  - [ ] Create AuthorizationPolicy restricting access to authorized principals
  - [ ] Verify SPIFFE SVID auto-rotation every 60 minutes

- [ ] 46. Implement audit log export pipeline
  - [ ] Configure Grafana Alloy to ship audit logs to S3 bucket
  - [ ] Enable WORM Object Lock with Compliance mode retention (7 years)
  - [ ] Add audit log fields: user_id, tenant_id, action, timestamp, request_id, response_status
  - [ ] Verify logs immutable after retention period begins

- [ ] 47. Implement Event Time validation for OTLP ingestion
  - [ ] Add validation rejecting events with Event Time > NOW + 5 minutes (400 error)
  - [ ] Add validation rejecting events with Event Time < NOW - 48 hours (400 error)
  - [ ] Configure OpenMeter to use Event Time for billing calculations

- [ ] 48. Implement backpressure and cost ceiling protections
  - [ ] Configure OTel Collector with local disk buffering (up to 1GB)
  - [ ] Set `drop_on_queue_full: true` for load shedding
  - [ ] Emit `dropped_spans_total` metric when shedding load
  - [ ] Subscribe to OpenMeter usage alert events at 80% and 100% thresholds
  - [ ] Implement auto-suspension: update AINativeSaaS CR status to SUSPENDED at 100% threshold
  - [ ] Configure Crossplane Composition to scale tenant workloads to 0 when SUSPENDED

- [ ] 49. **CHECKPOINT 10: Security & Compliance Complete**
  - **Deliverable**: Zero-trust security with mTLS, audit logging, Event Time validation, and backpressure protections
  - **Verification Criteria**:
    - All kube-sbt inter-service communication uses mTLS via Istio
    - SPIFFE SVID auto-rotates every 60 minutes
    - Audit logs exported to S3 with WORM retention
    - OTLP ingestion rejects events outside Event Time window
    - OTel Collector sheds load when buffer full
    - Tenant auto-suspension triggers at 100% billing threshold
  - **Test Script**:
    - Validate security by running `/scripts/test-security-compliance.sh` in Hub cluster
    - Script must verify mTLS enforcement, SVID rotation, audit log export, WORM retention
    - Test Event Time validation by submitting invalid timestamps
    - Test backpressure by flooding OTel Collector and verifying load shedding
    - Test auto-suspension by triggering 100% billing alert
  - **Success Criteria**: mTLS enforced, audit logs immutable, backpressure working, auto-suspension functional

## Phase 11: OpenAPI Documentation & Manual Validation

- [ ] 50. Generate OpenAPI 3.0 specification for all REST endpoints
  - [ ] Create `api/openapi/kube-sbt-metering.yaml`
  - [ ] Document all user management endpoints with request/response schemas
  - [ ] Document all usage query endpoints with query parameters
  - [ ] Document all catalog endpoints
  - [ ] Add authentication requirements (JWT bearer tokens)
  - [ ] Add error response schemas (RFC 7807 Problem Details)
  - [ ] Add example curl commands for each endpoint

- [ ] 51. Create demo tenant provisioning script
  - [ ] Create `scripts/provision-demo-tenant.sh`
  - [ ] Provision "app-creator" tenant in OpenMeter
  - [ ] Create sample users in tenant database
  - [ ] Generate sample usage events in OpenMeter
  - [ ] Create both tenant-scoped and user-scoped usage data

- [ ] 52. **CHECKPOINT 11: Documentation & Manual Validation**
  - **Deliverable**: Complete OpenAPI documentation and validated demo tenant for manual testing
  - **Verification Criteria**:
    - OpenAPI spec documents all REST endpoints with examples
    - Demo tenant "app-creator" provisioned with sample data
    - Manual validation confirms user-scoped queries return correct per-user metrics
    - Manual validation confirms tenant-scoped queries return aggregated metrics
    - Manual validation confirms entitlement checking returns correct used vs limit values
  - **Test Script**:
    - Validate API documentation by running manual tests using Postman or curl against deployed API
    - Use OpenAPI spec to test all endpoints documented in `api/openapi/kube-sbt-metering.yaml`
    - Verify responses match documented schemas
    - Test demo tenant by querying usage and entitlements for "app-creator"
  - **Success Criteria**: OpenAPI spec complete, demo tenant functional, manual validation passes

## Phase 12: W3C Trace Propagation & Observability

- [ ] 53. Implement W3C trace context propagation in NATS publisher
  - [ ] Create `internal/opensbt/providers/nats/publisher.go` with PublishWithTraceContext method
  - [ ] Implement NATSHeaderCarrier adapter for propagation.TextMapCarrier interface
  - [ ] Inject W3C trace context into NATS message headers using otel.GetTextMapPropagator().Inject()
  - [ ] Add traceparent and tracestate fields to NATS headers

- [ ] 54. Implement trace context extraction in NATS consumers
  - [ ] Update OTLPForwarder consumer to extract trace context from NATS headers
  - [ ] Use otel.GetTextMapPropagator().Extract() to restore trace context
  - [ ] Create child spans with extracted trace context
  - [ ] Add fallback logic generating new trace context when extraction fails

- [ ] 55. Configure Envoy for W3C trace generation in AgentGateway
  - [ ] Create `manifests/spoke/agentgateway/envoy-config.yaml` with OpenTelemetry tracer
  - [ ] Configure Jaeger backend for trace export
  - [ ] Verify traceparent header generation for all incoming HTTP requests

- [ ] 56. Update all NATS publishers to use trace propagation
  - [ ] Update User_Manager to use PublishWithTraceContext
  - [ ] Update MetricCollector to use PublishWithTraceContext
  - [ ] Update all event publishers in kube-sbt API

- [ ] 57. **CHECKPOINT 12: Trace Propagation Operational**
  - **Deliverable**: End-to-end distributed tracing from HTTP requests through NATS to OpenMeter
  - **Verification Criteria**:
    - HTTP requests generate W3C traceparent headers via Envoy
    - NATS messages include traceparent and tracestate headers
    - OTLPForwarder extracts and propagates trace context to OpenMeter
    - Single trace ID visible in Jaeger spanning entire request lifecycle
    - Trace context extraction failures logged with warnings
  - **Test Script**:
    - Validate trace propagation by running `/scripts/test-trace-propagation.sh` in Hub cluster
    - Script must send HTTP request to AgentGateway, verify NATS message headers, check Jaeger for complete trace
    - Test trace context extraction failure by corrupting NATS headers
    - Verify new trace context generated on extraction failure
  - **Success Criteria**: Traces flow end-to-end, Jaeger displays complete request paths, fallback working

## Phase 13: Cascading Subscription Cleanup

- [ ] 58. Implement SubscriptionCleanupConsumer
  - [ ] Create `internal/opensbt/consumers/subscription_cleanup.go`
  - [ ] Implement Start method subscribing to opensbt.subject.deleted topic
  - [ ] Implement processMessages handler with retry logic
  - [ ] Add handleSubjectDeletion method querying active subscriptions

- [ ] 59. Implement subscription cancellation with retry
  - [ ] Implement cancelWithRetry method with exponential backoff (1s, 2s, 4s)
  - [ ] Query OpenMeter for all active subscriptions tied to deleted subject
  - [ ] Cancel each subscription sequentially
  - [ ] Delete subject from OpenMeter after all subscriptions cancelled

- [ ] 60. Integrate subject deletion event publishing in User_Manager
  - [ ] Update DeleteUser method to publish opensbt.subject.deleted event
  - [ ] Include namespace, subject_id, user_id, deleted_at in event payload
  - [ ] Add error handling for event publishing failures

- [ ] 61. Implement reconciliation hint emission for failed cleanups
  - [ ] Add emitReconciliationHint method in SubscriptionCleanupConsumer
  - [ ] Publish to opensbt.reconciliation.needed after 10 failed retries
  - [ ] Include subscription details in reconciliation hint
  - [ ] Emit Prometheus metric opensbt_subscription_cleanup_total

- [ ] 62. **CHECKPOINT 13: Subscription Cleanup Choreography**
  - **Deliverable**: Automated subscription cancellation on subject deletion preventing revenue leakage
  - **Verification Criteria**:
    - Subject deletion publishes opensbt.subject.deleted event
    - SubscriptionCleanupConsumer receives and processes deletion events
    - All active subscriptions cancelled before subject deleted
    - Failed cleanups emit reconciliation hints
    - Idempotent replay of deletion events works correctly
  - **Test Script**:
    - Validate subscription cleanup by running `/scripts/test-subscription-cleanup.sh` in Hub cluster
    - Script must create subject with active subscriptions, delete subject, verify all subscriptions cancelled
    - Test retry logic by simulating OpenMeter failures
    - Verify reconciliation hints emitted after max retries
  - **Success Criteria**: Subscriptions auto-cancelled, revenue leakage prevented, reconciliation working

## Phase 14: Tenant Isolation Security Testing

- [ ] 63. Create tenant isolation test suite
  - [ ] Create `tests/e2e/tenant_isolation_test.go`
  - [ ] Implement TestCrossTenantAPIIsolation test
  - [ ] Implement TestCrossTenantDatabaseIsolation test
  - [ ] Implement TestCrossTenantOpenMeterIsolation test
  - [ ] Implement TestCrossTenantRedisCacheIsolation test

- [ ] 64. Implement test helper functions
  - [ ] Create provisionTenant helper provisioning test tenants
  - [ ] Create generateJWT helper generating tenant-scoped JWTs
  - [ ] Create insertTestData helper inserting test data into tenant databases
  - [ ] Create cacheEntitlement helper caching test entitlements in Redis

- [ ] 65. Configure CI/CD pipeline for security tests
  - [ ] Create `.github/workflows/security-tests.yml`
  - [ ] Configure test execution on pull requests and main branch pushes
  - [ ] Add deployment blocking on test failures
  - [ ] Configure security team alerts on failures

- [ ] 66. **CHECKPOINT 14: Security Testing Complete**
  - **Deliverable**: Automated tenant isolation tests proving multi-tenant security guarantees
  - **Verification Criteria**:
    - Cross-tenant API access returns 403 Forbidden
    - RLS policies prevent cross-tenant database queries (0 rows returned)
    - OpenMeter namespace isolation prevents cross-namespace subject access
    - Redis cache keys include tenant_id preventing cache leakage
    - CI/CD pipeline blocks deployment on test failures
  - **Test Script**:
    - Validate tenant isolation by running `go test ./tests/e2e/tenant_isolation_test.go` locally
    - Verify tests run automatically in CI/CD pipeline
    - Test deployment blocking by intentionally failing isolation test
    - Verify security team receives alerts on failures
  - **Success Criteria**: All isolation tests pass, CI/CD integration working, deployment blocking functional

## Phase 15: Database Migrations & Final Integration

- [ ] 67. Create idempotent database migration for users table
  - [ ] Create `migrations/001_create_users_table.sql`
  - [ ] Add CREATE TABLE IF NOT EXISTS for public.users
  - [ ] Enable Row-Level Security on public.users
  - [ ] Create RLS policy enforcing user_id matching from JWT claims
  - [ ] Create indexes on email and created_at columns
  - [ ] Create trigger for automatic updated_at timestamp updates

- [ ] 68. Integrate kube-sbt API with hub-operator
  - [ ] Add kube-sbt API deployment manifests to `manifests/hub/kube-sbt-api/`
  - [ ] Configure service mesh integration (Istio sidecar)
  - [ ] Add ArgoCD Application for kube-sbt API
  - [ ] Configure environment variables for OpenMeter, Ory, NATS, Redis endpoints

- [ ] 69. **CHECKPOINT 15: End-to-End Integration Complete**
  - **Deliverable**: Fully integrated kube-sbt metering and billing system operational in Hub cluster with trace propagation, subscription cleanup, and security testing
  - **Verification Criteria**:
    - Database migrations applied successfully
    - kube-sbt API deployed and accessible via service mesh
    - All components (OpenMeter, Ory, NATS, Redis) integrated correctly
    - End-to-end user journey works: user creation → usage tracking → entitlement checking → subscription management → invoicing
    - W3C trace propagation working across all components
    - Subscription cleanup choreography operational
    - Tenant isolation tests passing in CI/CD
  - **Test Script**:
    - Validate end-to-end integration by running `/scripts/test-e2e-metering.sh` in Hub cluster
    - Script must execute complete user journey: create user, emit usage events, check entitlements, create subscription, generate invoice
    - Verify all components communicate correctly via service mesh
    - Verify GitOps catalog changes propagate to OpenMeter
    - Verify distributed traces visible in Jaeger
    - Verify subscription cleanup on user deletion
    - Verify tenant isolation tests pass
  - **Success Criteria**: Complete system operational, all integrations working, end-to-end flow functional, observability complete, security validated
