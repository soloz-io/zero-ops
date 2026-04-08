┌─────────────────────────────────────────────────────────────┐
│                    TENANT INTERFACES                         │
├─────────────────┬──────────────────┬────────────────────────┤
│   MCP Client    │  Platform Console│    Backstage (Phase 2) │
│   (Goose/IDE)   │  (Monitoring/    │    (Discovery/Catalog) │
│                 │   Approvals)     │                        │
└────────┬────────┴────────┬─────────┴──────────┬─────────────┘
         │                 │                    │
         │ tenant_create   │ View status        │ Browse catalog
         │ MCP call        │ Approve PRs        │ Read-only
         ▼                 ▼                    ▼
    ┌────────────────────────────────────────────────────┐
    │           AgentGateway (JWT validation)            │
    └────────────────────┬───────────────────────────────┘
                         │
                         ▼
    ┌────────────────────────────────────────────────────┐
    │         zero-ops-api (MCP Server)                  │
    │  - Validates request                               │
    │  - Creates AINativeSaaS CR in Hub K8s              │
    │  - Returns 202 Accepted                            │
    └────────────────────┬───────────────────────────────┘
                         │
                         ▼
    ┌────────────────────────────────────────────────────┐
    │         hub-operator (Kubernetes Operator)         │
    │  Controllers:                                      │
    │  - TenantController (watches AINativeSaaS CR)      │
    │  - PREnvironmentController                         │
    │  - ProviderMigrationController                     │
    └────────────────────┬───────────────────────────────┘
                         │
         ┌───────────────┼───────────────┐
         │               │               │
         ▼               ▼               ▼
    ┌─────────┐   ┌──────────┐   ┌──────────────┐
    │PostgreSQL│   │GitHub API│   │ArgoCD API    │
    │(Hub DB) │   │(Commit   │   │(Create       │
    │         │   │TenantDesc│   │ApplicationSet│
    └─────────┘   │to Git)   │   │)             │
                  └──────────┘   └──────────────┘
                         │
                         ▼
    ┌────────────────────────────────────────────────────┐
    │   fleet-registry/tenants/tenant-acme.yaml (Git)    │
    └────────────────────┬───────────────────────────────┘
                         │
                         ▼
    ┌────────────────────────────────────────────────────┐
    │   ArgoCD (syncs from Git)                          │
    │   - Deploys to Spoke clusters                      │
    │   - Triggers Atlas Operator                        │
    └────────────────────┬───────────────────────────────┘
                         │
                         ▼
    ┌────────────────────────────────────────────────────┐
    │   Spoke Cluster (tenant infrastructure)            │
    │   - CNPG, PostgREST, NATS, etc.                    │
    └────────────────────────────────────────────────────┘
