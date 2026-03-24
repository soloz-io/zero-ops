Based on the Salesforce AIOps reference images and the Kagent/Zero-Ops codebase, building this hierarchical, safe-execution agentic mesh is highly feasible. 

Here is the proposed architectural solution using Kagent’s native primitives, alongside how to solve your specific "dynamic uptime" requirement.

### 1. The Multi-Agent Collaboration Topology (A2A)
Instead of manually configuring one massive agent, you use **Kagent's Agent-to-Agent (A2A) protocol** to create a tree structure.

*   **Collaborator Agent (The Router):** A single entry-point agent exposed to the Platform Admin's IDE. It has no direct access to infra tools. Instead, its "tools" are the specialized worker agents.
    *   *Codebase Implementation:* In the `Agent` CRD, you define tools with `type: Agent`.
    ```yaml
    # Collaborator Agent
    spec:
      declarative:
        systemMessage: "You are the lead SRE router. Delegate queries to the appropriate sub-agent."
        tools:
          - type: Agent
            agent: {name: prometheus-worker-agent}
          - type: Agent
            agent: {name: argocd-worker-agent}
          - type: Agent
            agent: {name: remediation-agent}
    ```
*   **Worker Agents (The Experts):** Highly scoped agents (e.g., `promql-agent`, `k8s-agent`) that only have access to specific MCP servers (e.g., Grafana MCP, K8s API MCP).

### 2. Solving "Dynamic Uptime" (Scale-to-Zero)
**The Challenge:** Out-of-the-box, Kagent translates `Agent` CRDs into standard Kubernetes `appsv1.Deployment` resources (`go/core/internal/controller/translator/agent/deployments.go:121`) which run continuously (default replicas: 1).
**The Solution:** 
Because Kagent routes all A2A communication as HTTP JSON-RPC through the central controller (`go/core/internal/httpserver/server.go`), you can integrate **KEDA (Kubernetes Event-driven Autoscaling) with the HTTP Add-on**.
*   Configure a KEDA `ScaledObject` to scale the Worker Agent deployments to `0`.
*   When the Collaborator Agent sends an A2A HTTP request to a sleeping worker, KEDA intercepts the request, wakes the Pod (0 → 1), and forwards the traffic. 
*   *Result:* Worker agents only consume compute when actively queried by the Collaborator.

### 3. Safe Operations & Human-in-the-Loop (Guardrails)
The "Safe Operations" slide relies on guardrails and human approval before executing destructive actions (e.g., Pod restarts, scaling). Kagent and Zero-Ops perfectly map to this:

*   **Human Approval:** Kagent has native HITL. You flag destructive tools in the Remediation Agent's CRD.
    *   *Codebase Implementation:* `spec.declarative.tools[].mcpServer.requireApproval: [safe_pod_restart]`. 
    *   When the agent tries to run this, Kagent suspends the A2A task (`go/adk/pkg/a2a/hitl.go`) and fires an approval request to the UI/IDE.
*   **Argo Workflows (Guardrails):** Instead of giving agents raw `kubectl delete` permissions, the MCP tool server exposes guarded Argo Workflows. 
    *   *Zero-Ops Architecture Mapping:* The agent calls `trigger_workflow(safe-pod-restart)`. This executes your pre-defined `argo-workflows/safe-pod-restart.yaml` which contains the actual logic to respect PodDisruptionBudgets (PDBs) and wait for health checks.

### 4. Policy Validation Flow
If the admin asks, *"Validate that Tenant C's network policies isolate their database"*:
1. **IDE (Goose/Cursor)** sends the prompt via MCP to the **Collaborator Agent**.
2. **Collaborator** delegates to the **Cilium Policy Worker Agent** (A2A).
3. The **Cilium Worker** uses its attached MCP tool to query the cluster state.
4. The **Cilium Worker** uses its attached RAG Memory (`go/adk/pkg/memory/kagent_service.go`) to fetch the company's baseline security policies.
5. It compares the live state against the RAG policy, returning the validation result back up the chain to the Admin's IDE.

### Summary of required tweaks to the platform:
To achieve this exact Salesforce-style AIOps architecture on Zero-Ops, you only need to add **KEDA HTTP** to your management cluster to handle the scale-to-zero requirement. Kagent's existing A2A, HITL, and MCP CRDs already handle the rest of the orchestration natively.

Your understanding is partially correct, but we need to clarify the Kubernetes terminology (CRD vs. CR) and how Kagent’s API actually works to avoid manual overhead.

Here are the precise answers to your three questions based on the Kagent codebase:

### 1. "Each time a new component is added, a new agent CRD must be included manually?"
**No, you do not create a new CRD.** You create a new **CR (Custom Resource)** instance of the existing `Agent` CRD. 

The `Agent` Custom Resource Definition (CRD) is installed exactly *once* into the cluster when you install Kagent (`helm/kagent-crds/templates/kagent.dev_agents.yaml`). 

To create a new expert agent for a new component, you just create an instance of that `Agent` resource. **This does not have to be manual.** In your Zero-Ops architecture, this should be automated in two ways:
*   **GitOps (Crossplane/ArgoCD):** When your `AINativeSaaS` XRD provisions a new Postgres database, the Crossplane Composition automatically stamps out the `Agent` YAML manifest alongside the DB manifests and pushes them to Git.
*   **REST API:** Your system can programmatically create the agent via Kagent's REST API (see below).

### 2. "Will we be able to control the CRUD operations of the agent via CRD?"
**Yes, absolutely.** Kagent is a strictly Kubernetes-native operator. The `Agent` custom resource is the absolute source of truth.
*   **Code Reference:** `go/core/internal/controller/agent_controller.go` (Line 38).
*   **How it works:** If you `kubectl apply`, `edit`, or `delete` an `Agent` YAML, the Kagent Controller detects the change and automatically handles the CRUD operations for the underlying Deployments, ConfigMaps, and Pods.

### 3. "Is there a Kubebuilder that Kagent comes with to create CRDs from the UI?"
**No.** *Kubebuilder* is a Go framework used by Kagent's developers at compile-time to write the Go controllers. It is not a runtime UI tool. 

**However, Kagent DOES allow you to create agents dynamically from the UI without writing YAML.**

Here is how Kagent bridges the UI to the Kubernetes CRDs:
*   **Code Reference:** `go/core/internal/httpserver/handlers/agents.go` (Line 132: `HandleCreateAgent`).
*   **How it works:** Kagent runs an HTTP server alongside the controller. When a user fills out the "Create Agent" form in the UI, the UI sends a JSON payload to `POST /api/agents`. 
*   The Kagent Go backend takes that JSON, formats it into a Kubernetes `v1alpha2.Agent` struct, and executes `h.KubeClient.Create(r.Context(), &agentReq)`. 
*   Kagent literally translates UI clicks directly into Kubernetes Custom Resources on your behalf.

### Summary for your Zero-Ops Architecture
To prevent manual work when a new component is added, you have two native options:

1.  **The GitOps Way (Recommended for your Spoke Clusters):** Update your Crossplane `Composition B` to include the Expert `Agent` YAML. When the Spoke is provisioned, ArgoCD deploys the database *and* the DB-Expert Agent simultaneously.
2.  **The UI/API Way (For Ad-Hoc Agents):** Your Platform Console (UI) makes a REST call to Kagent's `POST /api/agents` endpoint. Kagent creates the K8s resource, spins up the Pod (or lets KEDA scale it to 0), and the Collaborator Agent immediately discovers it via the `/mcp` `list_agents` tool.

Yes, you can absolutely restrict agents to read-only access, and Kagent supports dynamic prompt construction. However, Kagent's architecture dictates *how* you achieve this, which differs slightly from traditional middleware patterns.

Here is how you implement restricted access, harness/middlewares, and dynamic prompts for your expert agents based on the Kagent codebase.

---

### 1. Read-Only Access (Restricting Create/Update/Delete)

**How to restrict an agent:** You restrict the agent by restricting the **Tools** it is allowed to use, *not* by restricting the Agent CR itself.

In Kagent, an agent's permissions are defined entirely by the MCP server tools attached to it.
*   **The Kagent Way:** If you want a "Read-Only Database Expert", you attach an MCP server that *only* exposes `query_select` or `get_logs` tools. You do *not* attach tools like `execute_sql_update` or `delete_pod`.
*   **Code Reference:** `go/api/v1alpha2/agent_types.go` (Lines 400-410). The `McpServerTool` struct has a `toolNames` array.
*   **Implementation:** Even if an MCP server offers 50 tools (both read and write), you can explicitly restrict an agent to a read-only subset in its YAML:

```yaml
spec:
  declarative:
    tools:
      - type: McpServer
        mcpServer:
          name: k8s-tools-mcp
          toolNames: [k8s_get_logs, k8s_get_pods] # 🛑 Agent ONLY knows these exist. It cannot see or call k8s_delete_pod.
```

**The Zero-Ops Architecture Security Layer (The "Harness/Middleware"):**
While limiting `toolNames` hides the tools from the LLM, a malicious prompt injection could theoretically guess a tool name.
*   To enforce read-only access at the infrastructure level, your **Rust `AgentGateway`** (from your architecture diagram) acts as the security middleware.
*   As Kagent proxies the tool call out to the MCP server, it passes through the `AgentGateway`. The gateway inspects the JWT and queries Ory Keto: *“Is `db-expert-agent` allowed to execute `k8s_delete_pod`?”* If Keto says no (because it's a read-only agent), the Gateway rejects the request before it hits the tool server.

---

### 2. Does Kagent Support Harnesses and Middlewares?

**Yes, but specifically at the A2A (Agent-to-Agent) HTTP routing layer.**

Kagent controllers proxy all incoming requests (from the UI or other agents) to the individual agent pods. Kagent implements a Go middleware chain for these requests.
*   **Code Reference:** `go/core/internal/a2a/a2a_registrar.go` (Lines 111-126).
*   **Implementation:** When Kagent registers an agent's HTTP handler, it injects middleware:
    ```go
    middlewares := []server.Middleware{authimpl.NewA2AAuthenticator(a.authenticator)}
    if tracing != nil {
        middlewares = append(middlewares, tracing) // Injects OpenTelemetry Spans
    }
    ```
*   **Your Zero-Ops Integration:** As discussed previously, you must implement the `auth.Authorizer` interface (`go/core/pkg/auth/auth.go`). This interface *is* the middleware harness where you integrate Ory Keto to validate if the caller is allowed to invoke the expert agent.

---

### 3. Dynamic System Prompts (Fetching at Runtime)

**Yes, Kagent natively supports assembling dynamic system prompts from external sources at runtime.**

You are not restricted to hardcoding the entire system prompt in the `Agent` YAML. Kagent includes a **Prompt Template Engine** that resolves fragments at reconciliation time.

**How it works (The `promptTemplate` feature):**
1.  You store prompt layers (e.g., global safety rules, specific expert personas) in Kubernetes `ConfigMaps`.
2.  In the `Agent` CR, you use Go `text/template` syntax (`{{include "alias/key"}}`) to stitch them together.
3.  **Code Reference:** `go/core/internal/controller/translator/agent/template.go` (Lines 102-120).

**Example Implementation:**

```yaml
# 1. The ConfigMap (Your DB/Storage of Prompt Layers)
apiVersion: v1
kind: ConfigMap
metadata:
  name: platform-prompts
data:
  safety-rules: "Never execute destructive commands without approval."
  db-expert-persona: "You are a read-only PostgreSQL expert."
---
# 2. The Agent CR
apiVersion: kagent.dev/v1alpha2
kind: Agent
metadata:
  name: db-expert-agent
spec:
  declarative:
    promptTemplate:
      dataSources:
        - kind: ConfigMap
          name: platform-prompts
          alias: core
    systemMessage: |
      {{include "core/safety-rules"}}
      {{include "core/db-expert-persona"}}
      
      You are {{.AgentName}} operating in {{.AgentNamespace}}.
      You have access to these tools: {{range .ToolNames}}{{.}}, {{end}}
```

**Why this is powerful for Zero-Ops:**
*   **Centralized Updates:** If you need to update the "Safety Rules" for all 500 expert agents across your clusters, you just update the `platform-prompts` ConfigMap.
*   **Automatic Reconciliation:** Kagent implements a Kubernetes Watcher specifically for these ConfigMaps (`go/core/internal/controller/agent_controller.go:132`). When the ConfigMap changes, Kagent automatically re-evaluates the template, updates the agent's `config.json`, and the agent immediately uses the new prompt layer—**no agent restart required** (if only the prompt text changes).