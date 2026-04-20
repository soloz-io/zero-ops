Here is a comprehensive architecture and implementation guide for your team to transition from the imperative CLI approach to a declarative, Operator-driven architecture.

Before diving into the design, I must push back on one expectation to ensure you adopt a truly idiomatic Kubernetes pattern.

### 🛑 The Idiomatic Correction: "Who Installs Who?"
You mentioned: *"I wish the hub-operator takes care of both installer and secrets phases."*

**The Pushback:** The `hub-operator` **cannot** be the root installer of the cluster, nor should it install ArgoCD. If the operator installs ArgoCD, but ArgoCD manages the GitOps state, you create a circular dependency. 

**The Idiomatic Pattern (The "GitOps Handshake"):**
1. **The CLI (Phase 1)** provisions the bare Kubernetes cluster, installs ArgoCD, points ArgoCD to your Git repository, and *exits forever*.
2. **ArgoCD** reads the Git repository and deploys the `hub-operator` and a Custom Resource (e.g., `HubEnvironment`).
3. **The `hub-operator` (Phase 2)** wakes up, sees the `HubEnvironment` CR, and generates the dynamic prerequisites (crypto keys, secrets, API tokens). 
4. **ArgoCD (via Sync Waves)** waits for the operator's secrets to exist, then proceeds to deploy the databases, Infisical, and Ory.
5. **The `hub-operator`** watches these applications come online and executes "Day-2" orchestration (e.g., running DB migrations, setting up roles, hitting REST APIs).

This creates a clean separation of concerns: **ArgoCD moves YAML; the Operator executes domain logic.**

---

# The Zero-Ops `hub-operator` Design Guide

## Phase 1: The Imperative Boundary (CLI)
The `cmd/hub/bootstrap.go` file must be stripped down to the bare minimum "Inception" logic. Its only job is to provide a dial-tone.

**Responsibilities to KEEP in the CLI:**
*   Pre-flight checks (Docker, Hetzner API token validation).
*   Create ephemeral `kind` cluster.
*   Install CAPI/CAPH and provision the Hetzner management cluster.
*   Pivot CAPI state to Hetzner.
*   Install ArgoCD on the Hetzner cluster.
*   Apply the "App of Apps" (`manifests/argocd/app-of-apps.yaml`) to the cluster.
*   Generate/Save the `kubeconfig` for the user.

**Responsibilities to REMOVE from the CLI:**
*   Generating `infisical-secrets` or `platform-db-app` passwords (move to Operator).
*   Installing Helm charts imperatively (move to ArgoCD).
*   Waiting for Pods to be healthy (move to Operator/ArgoCD).

---

## Phase 2: The Declarative `hub-operator` (Day-2 Brain)

We will build a standard Kubebuilder/Controller-Runtime operator.

### 1. The Custom Resource Definition (CRD)
You will define a CRD that represents the desired state of the Hub. This file will be committed to your GitOps repository.

```yaml
apiVersion: ops.nutgraf.in/v1alpha1
kind: HubEnvironment
metadata:
  name: zero-ops-hub
  namespace: zero-ops-system
spec:
  # Instructs the operator to generate Secret Zero
  secrets:
    infisical:
      enabled: true
      managedByOperator: true
  
  # Instructs the operator to configure the database once CNPG is ready
  database:
    cnpgClusterRef: platform-db
    managedRoles:
      - name: mcp_server
        database: control_plane
      - name: infisical
        database: infisical
      - name: spoke_controller
        database: hub
        
  # Instructs the operator to call external APIs once available
  identity:
    oauthClients:
      - id: mcp-public-client
        redirectUris:
          - "http://127.0.0.1:54321/callback"
```

### 2. Eradicating the "Bash-in-a-Pod" Anti-Patterns
Currently, your codebase uses Kubernetes Jobs running bash scripts to initialize databases. 
*   **Target 1:** `manifests/hub-core-services/platform-database/setup-platform-roles-job.yaml`
*   **Target 2:** `manifests/hub-core-services/platform-database/password-rotation-job.yaml`
*   **Target 3:** `manifests/hub-core-services/nats/init-streams-job.yaml`

**These jobs must be deleted.** They are brittle, hard to debug, and don't retry cleanly if the database crashes mid-execution. 

Instead, the `hub-operator` will use Controller-Runtime to *Watch* the `Cluster` resource (from `postgresql.cnpg.io/v1`). 

**How the Operator handles it (Go Code inside the Reconciler):**
```go
func (r *HubEnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    hub := &opsv1alpha1.HubEnvironment{}
    if err := r.Get(ctx, req.NamespacedName, hub); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 1. Generate Secret Zero (Before DB boots)
    if err := r.reconcileSecretZero(ctx, hub); err != nil {
        return ctrl.Result{}, err
    }

    // 2. Check if CNPG Database is Ready
    dbReady, err := r.isCNPGClusterReady(ctx, hub.Spec.Database.CnpgClusterRef)
    if err != nil {
        return ctrl.Result{}, err
    }

    // 3. If DB is ready, natively execute SQL using Go's database/sql (No bash scripts!)
    if dbReady {
        if err := r.reconcileDatabaseRoles(ctx, hub); err != nil {
            // If it fails, controller-runtime automatically requeues with exponential backoff
            return ctrl.Result{}, err 
        }
    }

    // 4. Check if Hydra is ready, then configure OAuth clients
    if r.isHydraReady(ctx) {
        if err := r.reconcileOAuthClients(ctx, hub); err != nil {
            return ctrl.Result{}, err
        }
    }

    return ctrl.Result{}, nil
}
```

### 3. The ArgoCD Sync Wave "Handshake"

Because the Operator generates the secrets, and the databases/apps depend on those secrets, we must orchestrate the boot sequence using **ArgoCD Sync Waves**.

Here is how you structure your GitOps manifests:

#### Wave 0: The CRDs
*   ArgoCD installs the `postgresql.cnpg.io` CRDs, `external-secrets.io` CRDs, and your custom `ops.nutgraf.in` CRDs.

#### Wave 1: The Operator & Intent
*   ArgoCD deploys the `hub-operator` Deployment.
*   ArgoCD applies the `HubEnvironment` Custom Resource.
*   **[OPERATOR ACTION]**: The operator wakes up, reads the CR, generates secure AES keys, and creates the `infisical-secrets` and `platform-db-app` Kubernetes Secrets natively.

#### Wave 2: The Data Layer
*   ArgoCD deploys the CNPG `Cluster` (which mounts the `platform-db-app` secret created by the operator).
*   ArgoCD deploys Redis & NATS.
*   **[OPERATOR ACTION]**: The operator detects the CNPG cluster is `Ready`. It connects via `database/sql`, creates the `mcp_server`, `infisical`, and `spire_server` roles, and runs schema migrations.

#### Wave 3: Security & Identity
*   ArgoCD deploys Infisical, SPIRE, and Ory (Hydra/Kratos/Keto). (These boot successfully because the Operator already prepped their DB roles in Wave 2).
*   **[OPERATOR ACTION]**: The operator detects Infisical's HTTP API is `200 OK`. It securely uploads the DB passwords (Secret Zero) into the Infisical API to make it the Source of Truth.
*   **[OPERATOR ACTION]**: The operator detects Hydra is `200 OK`. It hits the Hydra Admin REST API to register the `mcp-public-client` OAuth2 client.

#### Wave 4: The Application Layer
*   ArgoCD deploys External Secrets Operator (ESO), `mcp-server`, `agentgateway`, and the Platform Console.
*   ESO connects to Infisical (configured in Wave 3) and begins syncing remaining secrets down to K8s.

---

## Guide for the Team: Step-by-Step Implementation Plan

### Step 1: Scaffold the Operator
1. Install Kubebuilder: `kubebuilder init --domain nutgraf.in --repo github.com/soloz-io/zero-ops/operators/hub-operator`
2. Create the API: `kubebuilder create api --group ops --version v1alpha1 --kind HubEnvironment`

### Step 2: Move Crypto Logic from CLI to Operator
Take the exact logic currently in `internal/hub/components/secrets.go` (`generateSecurePassword`, etc.) and move it into the Operator's reconcile loop. 
*   *Rule:* The operator should check if the secret exists. If it does not, generate it. If it does, leave it alone (Idempotency). Ensure the operator sets `OwnerReferences` on the Secret so it is bound to the `HubEnvironment` CR.

### Step 3: Replace Bash Jobs with Go SQL Drivers
1. Add `github.com/lib/pq` to your operator's `go.mod`.
2. In your Reconciler, read the CNPG superuser secret, open a connection to the `platform-db-rw` service.
3. Query `SELECT 1 FROM pg_roles WHERE rolname = 'X'`. If missing, execute `CREATE ROLE X WITH LOGIN PASSWORD 'Y'`. 
4. *Benefit:* You now have rich Go error handling, metrics, and logs instead of opaque bash errors in a crashed pod.

### Step 4: Replace CLI API calls with Operator API calls
Move the `infisical.NewClient()` and `hydra.registerClient()` code out of the CLI/Init-containers and into the Operator. 
*   *Rule:* Use Controller-Runtime's `RequeueAfter` if the external API (like Hydra) isn't responding yet. ArgoCD will naturally bring the pods online, and the Operator will succeed on its next loop.

### Step 5: Clean up GitOps Repo
1. Delete `manifests/hub-core-services/platform-database/*-job.yaml`.
2. Delete `manifests/hub-core-services/nats/*-job.yaml`.
3. Add your new `hub-operator` Deployment and `HubEnvironment` CR to `manifests/argocd/apps/platform-core.yaml`.

## Conclusion

By adopting this design:
1. Your **CLI** becomes blazingly fast and dumb (just getting K8s + ArgoCD running).
2. **ArgoCD** does what it does best: blindly synchronizing YAML from Git.
3. Your **Hub Operator** becomes the intelligent autonomic nervous system of your platform—handling crypto, cross-system API calls, database provisioning, and self-healing. 

This is the exact architecture used by modern enterprise PaaS teams (like Vercel, Supabase, and PlanetScale) to manage their underlying control planes.