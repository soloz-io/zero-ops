Here is a high-level, architectural proposal for migrating the Zero-Ops platform bootstrap from an imperative CLI orchestration to a declarative Argo Workflows-based approach.

### 1. The Architectural Shift
Currently, the `hub` CLI acts as a monolithic orchestrator. It runs locally, maintains state in a local JSON file, and sequentially executes API calls and shell commands (`kubectl`, `clusterctl`, `packer`). 

In the new paradigm, the CLI becomes a **lightweight initiator and observer**. The heavy lifting is delegated to an Argo Workflow running inside a temporary "seed" cluster, allowing you to leverage Argo's native features: DAGs (Directed Acyclic Graphs), visual UI, step-level retries, and native Kubernetes state management.

### 2. High-Level Workflow Design (The DAG)
The imperative phases currently hardcoded in `internal/hub/bootstrap/orchestrator.go` will be mapped to a declarative Argo Workflow definition. 

The Workflow will consist of the following sequential/parallel steps:
1. **Init:** Generate Hetzner credentials and prepare the CAPI environment.
2. **CAPI-Install:** Deploy Cluster API Operator to the seed cluster.
3. **Provision:** Apply the ClusterClass and Cluster manifests to provision Hetzner infrastructure.
4. **Wait-Ready:** Poll the target cluster until the control plane and workers are healthy.
5. **Extract-Config:** Extract the target cluster's `kubeconfig` and pass it as an Argo Artifact to subsequent steps.
6. **Pivot:** Execute `clusterctl move` to migrate CAPI state to the target management cluster.
7. **Post-Boot:** Deploy ArgoCD, Infisical, VictoriaMetrics, etc., to the new management cluster.

### 3. Required Codebase Changes

To achieve this without getting into implementation details, you will need to refactor the codebase across five main areas:

#### A. CLI Refactoring (The Initiator)
*   **Strip down `cmd/hub/bootstrap.go`:** Remove Phases 4 through 9. The CLI's only job will be to:
    1. Run local preflight checks.
    2. Create the ephemeral local Kind cluster.
    3. Install Argo Workflows controller onto the Kind cluster.
    4. Submit the `Hub-Bootstrap` Workflow Custom Resource (CR).
    5. Port-forward the Argo UI and tail the workflow logs to the user's terminal.
    6. Once the workflow succeeds, download the generated `kubeconfig` artifact and delete the Kind cluster.

#### B. Exposing Internal Tasks (The Worker)
*   **Create Headless CLI Commands:** Argo Workflow pods need a way to execute your existing Go logic. You will need to wrap the logic currently inside `internal/hub/` into hidden subcommands (e.g., `hub internal-task provision-cluster`, `hub internal-task pivot-capi`).
*   This allows the Argo Workflow steps to simply run your existing compiled Go code rather than relying on bash scripts.

#### C. Containerization
*   **Create a Worker Image:** The current `cmd/hub/Dockerfile` builds the CLI for local use. You will need a new "Workflow Worker" Docker image that packages the `hub` Go binary alongside required tools (`clusterctl`, `helm`, `kubectl`).
*   This image will be referenced in your Argo Workflow templates as the execution environment for each step.

#### D. State and Data Management Overhaul
*   **Deprecate Local State:** The `internal/hub/state/manager.go` (which writes to `~/.zero-ops/state/`) will be largely deprecated. State and idempotency will be handled natively by Argo Workflows' execution status.
*   **Artifact Passing:** Currently, variables like `mgmtKubeconfig` are passed in memory inside Go. You will need to refactor these functions to read from and write to Argo **Parameters** and **Artifacts** (e.g., writing the kubeconfig to a file path that Argo extracts and passes to the next pod).

#### E. Manifest Additions
*   **Workflow Templates:** Add a new directory (e.g., `manifests/bootstrap/workflows/`) containing the declarative `WorkflowTemplate` or `ClusterWorkflowTemplate` YAML files. 

### 4. Solving the "Pivot Paradox"
The most complex part of this migration is the "Pivot" phase. Currently, the local CLI initiates the move and then cleans up the Kind cluster. 

In the new model:
*   The Argo Workflow is running *on* the Kind cluster. 
*   The Workflow executes the pivot, moving the CAPI state to Hetzner.
*   **Crucial Change:** The Workflow **cannot** delete the Kind cluster, or it will kill itself before reporting success. The Workflow must finish, report `Succeeded`, and expose the new `kubeconfig` as an artifact. The *local CLI* (which is watching the workflow) will then download the artifact and tear down the Kind cluster.