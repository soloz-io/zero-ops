# ADR 005: State-Aware Orchestration via Composition Functions

## Status
Approved

## Context
In the `SpokePool` provisioning flow, we rely on asynchronous operators (like `cert-manager`) to generate secrets, which are then distributed to Spoke clusters via the `provider-kubernetes` `Object` resource.

When using declarative YAML pipelines (`patchesFrom`), if a referenced secret does not yet exist in the cluster, the controller throws a hard `Observe` error. This breaks the Crossplane state machine, causing a "Reconciliation Deadlock". Furthermore, YAML pipelines lack the ability to express timeouts, degraded states, or conditional gating.

## Decision
We will deprecate the use of deep `patchesFrom` chains for resources possessing temporal/asynchronous dependencies. Instead, we will implement **Composition Functions** to handle phased rendering logic.

### 1. Pure Reconciliation Logic
Functions must evaluate *Observed State* (passed via gRPC from Crossplane) rather than imperatively querying the *Live Cluster State*. 

### 2. Short-Circuit Rendering (Gating) & Semantic Conditions
If a dependency is not ready, the function omits downstream resources from the *Desired State* rather than throwing a hard error. It must emit explicitly typed, standard Crossplane conditions (e.g., `Type: CertificatesMinted, Status: False, Reason: WaitingForCertManager`) on the XR to provide clear SRE observability. Functions must emit the *complete* set of relevant conditions on every pass to avoid overwrite semantics or state flapping.

### 3. Functionally Deterministic Timeouts
Timeout logic will be implemented as a pure function by comparing the XR's `creationTimestamp` to the current wall-clock time (`time.Since`), escalating to a `Reason: Timeout` degraded state after a threshold (15 minutes). While not strictly "mathematically replay deterministic", this provides necessary operational boundaries without requiring external state stores.

### 4. Domain-Driven Functions (Anti-Monolith)
Functions must be bounded by specific orchestration domains. Instead of a single `function-zero-ops`, we will use scoped functions such as `function-cert-distribution`. 

## Secret Lifecycle & Operational Contracts
By moving secret distribution into `function-cert-distribution`, the function establishes the following operational contracts:
* **Authoritative Source:** `cert-manager` on the Hub cluster owns the cryptographic generation.
* **Distribution Engine:** `function-cert-distribution` dictates *when* and *where* distribution occurs.
* **Target Management:** `provider-kubernetes` acts as the actuator, continuously syncing the secret to the Spoke.
* **Spoke Outages / Invalid ProviderConfig:** If the target Spoke cluster goes offline or the `ProviderConfig` becomes invalid, `provider-kubernetes` handles exponential backoff retries. The XR's composed `Object` will gracefully degrade to `Ready=False` without crashing the Hub's reconciliation loop.
* **Partial Outage During Rotation:** If `cert-manager` rotates the secret while the Spoke is disconnected, the new payload enters the Hub's desired state. `provider-kubernetes` guarantees eventual consistency and will patch the Spoke once connectivity is restored.
* **Garbage Collection:** The downstream secret is strictly bound to the XR's lifecycle. Deleting the `SpokePool` cascades deletion to the `Object`, triggering `provider-kubernetes` to issue a DELETE call to the Spoke cluster before removing its finalizer.
* **Security Caveat (Payload Traversal):** Secret material is extracted from the *Observed State* and injected into the *Desired State*. This means raw secret material traverses the Crossplane gRPC pipeline. Debug logging of full payloads in this function is strictly prohibited. 

## Consequences
* **Positive:** Eliminates Crossplane reconciliation deadlocks and silent infinite waits.
* **Positive:** SREs receive explicit, semantic status conditions for dashboarding and alerting.
* **Negative:** Requires strict discipline to prevent functions from growing into unmanageable monolithic orchestrators.