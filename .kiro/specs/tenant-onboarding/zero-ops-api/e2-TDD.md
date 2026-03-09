Here is a comprehensive suite of End-to-End (E2E) Behavior-Driven Development (BDD) test scenarios for the `zero-ops-api` Core Tenant CRUD. 

These scenarios are designed to be implemented using a Go testing framework like `net/http/httptest` combined with `testify/suite` or `gavv/httpexpect`. They treat the API as a black box, focusing entirely on user/agent journeys and system state.

---

### E2E Test Suite 1: The Complete Tenant Lifecycle (Happy Path)
**Goal:** Verify a tenant can be created, retrieved, upgraded, and safely decommissioned.

*   **Step 1: Create Tenant**
    *   **Action:** `POST /api/v1/tenants` with name `acme-corp`, plan `free`, and email `admin@acme.com`.
    *   **Expectation:** Status `201 Created`. Body contains `id`, `created: true`, and `quotas.maxClusters: 1` (defaulting logic applied).
*   **Step 2: Retrieve Tenant**
    *   **Action:** `GET /api/v1/tenants/{id}` using the ID from Step 1.
    *   **Expectation:** Status `200 OK`. The body matches the created tenant, and `status` is `active`.
*   **Step 3: Upgrade Plan (Day-2 Operation)**
    *   **Action:** `PATCH /api/v1/tenants/{id}` with `{"plan": "professional"}`.
    *   **Expectation:** Status `200 OK`. The body reflects the new plan, and `quotas.maxClusters` is automatically updated to `10` (recalculation logic applied).
*   **Step 4: Safe Deletion**
    *   **Action:** `DELETE /api/v1/tenants/{id}?confirm=true`.
    *   **Expectation:** Status `204 No Content`.
*   **Step 5: Verify Soft Delete**
    *   **Action:** `GET /api/v1/tenants/{id}`.
    *   **Expectation:** Status `404 Not Found` (Ensure soft-deleted tenants do not leak into active GET requests).

---

### E2E Test Suite 2: Agentic Resilience & Self-Correction
**Goal:** Verify the API provides the necessary safety nets and error contexts for an LLM agent to self-correct and recover from network drops.

*   **Step 1: Invalid Name Format (Triggering LLM Correction)**
    *   **Action:** `POST /api/v1/tenants` with name `Acme Corp!` (Invalid RFC 1123).
    *   **Expectation:** Status `400 Bad Request`. Body contains `error.code: VALIDATION_ERROR` and `error.field: name` with a descriptive message the LLM can read.
*   **Step 2: Successful Correction**
    *   **Action:** `POST /api/v1/tenants` with name `acme-corp`.
    *   **Expectation:** Status `201 Created`. `created: true`.
*   **Step 3: Network Drop Simulation (Idempotent Retry)**
    *   **Action:** `POST /api/v1/tenants` with the **exact same payload** as Step 2 (simulating the agent retrying because it didn't get the first response).
    *   **Expectation:** Status `200 OK`. `created: false`. Data remains perfectly intact without creating duplicates.
*   **Step 4: Conflict Rejection (The Squatter Scenario)**
    *   **Action:** `POST /api/v1/tenants` with name `acme-corp`, but a *different* email/plan (e.g., `hacker@evil.com`).
    *   **Expectation:** Status `409 Conflict`. (See "Critical Missing Flows" below regarding this implementation).

---

### E2E Test Suite 3: Admin Overrides and Guardrails
**Goal:** Verify that Platform Admins can override defaults, but cannot perform destructive actions by accident.

*   **Step 1: Create with Custom Quotas**
    *   **Action:** `POST /api/v1/tenants` with plan `professional`, but explicitly passing `quotas: {"maxClusters": 999}`.
    *   **Expectation:** Status `201 Created`. The tenant is created, and `quotas.maxClusters` is `999` (overriding the default of 10), but `maxNodes` defaults to `50`.
*   **Step 2: Attempt Unconfirmed Deletion**
    *   **Action:** `DELETE /api/v1/tenants/{id}` (omitting `?confirm=true`).
    *   **Expectation:** Status `400 Bad Request`. `error.code: CONFIRMATION_REQUIRED`.
*   **Step 3: Invalid Status Transition**
    *   **Action:** `DELETE /api/v1/tenants/{id}?confirm=true` (successful soft delete). Then attempt `PATCH /api/v1/tenants/{id}` with `{"status": "active"}`.
    *   **Expectation:** Status `404 Not Found` OR `400 Bad Request` with `INVALID_STATUS_TRANSITION`. (Since it's soft-deleted, a standard GET/PATCH should return 404 to hide it from standard operations).

---

### E2E Test Suite 4: Fleet Management (List & Pagination)
**Goal:** Verify the `GET /api/v1/tenants` endpoint correctly filters and paginates for fleet-wide observability.

*   **Setup:** Seed the database with 3 tenants: 
    *   Tenant A: active, free
    *   Tenant B: active, professional
    *   Tenant C: suspended, professional
*   **Step 1: Default List**
    *   **Action:** `GET /api/v1/tenants`.
    *   **Expectation:** Status `200 OK`. Returns 3 items. `pagination.total: 3`.
*   **Step 2: Filter by Plan**
    *   **Action:** `GET /api/v1/tenants?plan=professional`.
    *   **Expectation:** Status `200 OK`. Returns 2 items (B and C).
*   **Step 3: Filter by Status**
    *   **Action:** `GET /api/v1/tenants?status=active`.
    *   **Expectation:** Status `200 OK`. Returns 2 items (A and B).
*   **Step 4: Pagination Boundaries**
    *   **Action:** `GET /api/v1/tenants?limit=2&page=1`.
    *   **Expectation:** Status `200 OK`. Returns 2 items. `pagination.nextCursor` is present.
*   **Step 5: Follow Pagination**
    *   **Action:** `GET /api/v1/tenants?limit=2&page=2`.
    *   **Expectation:** Status `200 OK`. Returns 1 item. `pagination.nextCursor` is null.
