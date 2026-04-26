there are two repo involved here.. one is the fleet registry and other is the hub-infra.



1. Clear separation of concerns

hub-infra = platform code

fleet-registry = platform state





2. Safer access control

Platform team → full access to infra

Ops / API (MCP) → write only to fleet-registry



3. Cleaner GitOps model

ArgoCD watches fleet-registry

→ drives deployments



Repo 1: hub-infra

→ bootstrap ArgoCD, AppSets, platform components

Repo 2: fleet-registry

→ tenants, spokepools (runtime state)



ArgoCD

→ watches hub-infra (static platform)

→ watches fleet-registry (dynamic state)

👉 Typically:



App-of-Apps from hub-infra

ApplicationSet (Git generator) pointing to fleet-registry



🏁 Why this is correct





✅ Separation of concerns

✅ Independent lifecycle

✅ safer automation (MCP writes only to fleet repo)







