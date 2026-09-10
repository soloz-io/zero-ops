# breakdown of what `./bin/soloz init-secrets` does:

## `./bin/soloz init-secrets` Command Breakdown

This command bootstraps all Layer 1 (Secret Zero) infrastructure secrets needed for the platform to function. Here's the step-by-step execution:

### Step 1: Generate Infisical Base Cryptographic Secrets
**Function:** `InstallInfisicalSecrets()`

Creates two secrets in `zero-ops-system` namespace:

1. **`infisical-secrets`** - Contains:
   - `ENCRYPTION_KEY`: 32-character hex string for AES-256-GCM encryption
   - `AUTH_SECRET`: 32-character hex string for JWT signing
   - `REDIS_URL`: Connection string with generated Redis password
   - `DB_ROOT_CERT`: Base64-encoded CA certificate from CNPG (enables TLS)

2. **`infisical-redis-credentials`** - Contains:
   - `password`: Redis authentication password

**Idempotency:** If secrets exist, reuses existing keys to avoid breaking encryption. Only updates TLS configuration (DB_ROOT_CERT).

---

### Step 2: Create PostgreSQL Connection Secrets (Layer 1 Bootstrap)
**Function:** `InstallPostgresConnectionSecret()`

Generates and injects Secret Zero for database bootstrap:

1. **`platform-db-app`** (CNPG Secret Zero):
   - `username`: "app"
   - `password`: 32-character secure random password
   - Type: `kubernetes.io/basic-auth`

2. **`infisical-db-credentials`** (Infisical Secret Zero):
   - `username`: "infisical"
   - `password`: 32-character secure random password

3. **`infisical-postgres-connection`** (Connection string for Infisical):
   - `DB_HOST`: "platform-db-pooler.zero-ops-system.svc"
   - `DB_PORT`: "5432"
   - `DB_USER`: "infisical"
   - `DB_PASSWORD`: (from infisical-db-credentials)
   - `DB_NAME`: "infisical"
   - `DB_SSL_MODE`: "require" (enables TLS)

**Idempotency:** Only creates secrets if they don't exist. Never overwrites existing credentials.

---

### Step 3: Wait for Infisical to Become Healthy
**Function:** `WaitForInfisicalHealth()`

- Polls Infisical deployment every 5 seconds
- Waits up to 5 minutes for at least 1 replica to be ready
- If timeout: Warns user and exits (can re-run command later)
- If healthy: Proceeds to Step 4

**Current Blocker:** This is where your CLI is stuck because Infisical pods are crashing due to the pooler authQuery issue.

---

### Step 4: Upload Layer 1 + Generate Layer 2 Credentials in Infisical
**Function:** `InstallPlatformDatabaseCredentials()`

**Part A - Upload Layer 1 to Infisical (making Infisical the SOT):**
- Reads `infisical-db-credentials` from K8s
- Uploads to Infisical: `infisical-db-username`, `infisical-db-password`
- Reads `platform-db-app` from K8s
- Uploads to Infisical: `platform-db-app-username`, `platform-db-app-password`

**Part B - Generate Layer 2 Application Credentials:**
- Generates `control-plane-db-username`: "mcp_server"
- Generates `control-plane-db-password`: 32-char random
- Generates `hub-db-username`: "spoke_controller"
- Generates `hub-db-password`: 32-char random
- Stores all in Infisical (ESO will sync to K8s)

---

### Step 5: Generate SPIRE Server Credentials
**Function:** `InstallSPIREServerCredentials()`

- Generates `spire-server-db-username`: "spire_server"
- Generates `spire-server-db-password`: 32-char random
- Stores in Infisical (ESO will sync to K8s)

---

### Step 6: Restart Workloads (Only if Secrets Changed)
**Function:** `RestartPlatformWorkloads()`

If any secrets were created or modified:
- Patches `redis-master` StatefulSet with restart annotation
- Patches `platform-infisical-standalone` Deployment with restart annotation
- Triggers rolling restart to sync new credentials

**Idempotency:** Only runs if `changed1 || changed2 || changed3 || changed4` is true.
