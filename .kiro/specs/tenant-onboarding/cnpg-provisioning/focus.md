In an enterprise-grade SaaS architecture, the database that holds tenant onboarding data, billing, and global metadata **must live in the Management Plane layer**. It **cannot** live in the Tenant Cluster. 

### Summary Recommendation for Your Project

1.  **Hosting Location:** Keep using **CloudNativePG (CNPG)** running inside the **Management Cluster**. It fits perfectly with your `mgmt bootstrap` CLI command, making the platform self-hosting and cloud-agnostic.
2.  **Data Isolation:** Stick to the **Shared Database (Pool Model)** defined in your `design.md`. Using a single Postgres database with an `org_id` column is the most idiomatic, highly-scalable approach for the API backend of an infrastructure SaaS.
3.  **Tenant Cluster Databases:** Do not confuse the *Platform DB* with *Tenant DBs*. If a tenant wants a database for *their* application (e.g., an e-commerce store), they will use your CLI (Phase 2) to deploy a *new* Postgres instance into *their* Workload Cluster. That is completely separate from your Platform DB.