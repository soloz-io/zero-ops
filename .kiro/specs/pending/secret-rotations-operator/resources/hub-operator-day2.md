I'll extract the complete logic from these Day 2 operation functions.

Here's a complete breakdown of the Day 2 operation logic currently in hub-operator:

---

## **1. handleCertificateRotation()** - Certificate Rotation Logic

### **Purpose**: Detect when `platform-db-ca` certificate changes and propagate to services

### **Logic Flow**:

1. **Read Secrets** (using UncachedClient):
   - `platform-db-ca` (namespace: platform-data)
   - `infisical-secrets` (namespace: platform-data)

2. **Extract & Compare**:
   - Extract `ca.crt` from `platform-db-ca`
   - Extract `DB_ROOT_CERT` from `infisical-secrets`
   - Compare: if identical, skip rotation

3. **Update Secret**:
   - Update `infisical-secrets.Data["DB_ROOT_CERT"]` with new CA cert
   - Call `r.Update(ctx, infisicalSecrets)`

4. **Restart Services** (hardcoded list):
   - `infisical` (Deployment, platform-ops)
   - `redis` (StatefulSet, platform-data)
   - `hydra` (Deployment, ory-system)
   - `kratos` (Deployment, ory-system)
   - `keto` (Deployment, ory-system)
   - `spire-server` (StatefulSet, spire-system)
   - `mcp-server` (Deployment, platform-ops)

5. **Track Failures**:
   - Collect restart failures in `restartFailures` array
   - Update `HubEnvironment.Status.Conditions`:
     - `CertificateRotationFailed` (if failures)
     - `CertificateRotationInProgress` (waiting for Infisical)

6. **Wait for Infisical Readiness**:
   - Call `waitForInfisicalReadiness()` to check Deployment status
   - Update status conditions based on readiness

---

## **2. handlePasswordRotation()** - Password Rotation Logic

### **Purpose**: Detect password changes in ESO-managed secrets and update PostgreSQL

### **Logic Flow**:

1. **Iterate Database Roles**:
   - Loop through `hubEnv.Spec.Database.Roles`
   - Construct secret name: `{roleName}-db-credentials`

2. **Read & Validate Secret** (using UncachedClient):
   - Check if secret exists
   - Verify label: `ops.nutgraf.in/db-credentials=true` (ESO-managed)
   - Extract `username` and `password` fields

3. **Drift Detection**:
   - Create `database.RoleManager`
   - Call `PasswordNeedsUpdate(username, password)`
   - Compares SHA-256 hash of password vs PostgreSQL `pg_authid` hash

4. **Execute ALTER ROLE**:
   - If drift detected, call `UpdateRolePassword(username, password)`
   - Executes: `ALTER ROLE "{username}" WITH PASSWORD '{password}'`
   - Escapes single quotes in password

5. **Restart Consuming Service** (hardcoded mapping):
   - Map role name to service:
     - `infisical` → Deployment `infisical` (platform-ops)
     - `redis` → StatefulSet `redis` (platform-data)
     - `hydra` → Deployment `hydra` (ory-system)
     - `kratos` → Deployment `kratos` (ory-system)
     - `keto` → Deployment `keto` (ory-system)
     - `spire_server` → StatefulSet `spire-server` (spire-system)
     - `mcp_server` → Deployment `mcp-server` (platform-ops)
     - `spoke_controller` → Deployment `spoke-controller` (platform-data)

6. **Error Handling**:
   - Non-blocking: logs errors but continues processing
   - No status condition updates

---

## **3. restartDeployment()** - Deployment Restart Helper

### **Logic**:
```go
1. Get Deployment by name/namespace
2. If not found, skip (return nil)
3. Add/update annotation: 
   deployment.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)
4. Call r.Update(ctx, deployment)
5. Kubernetes triggers rolling restart due to pod template change
```

---

## **4. restartStatefulSet()** - StatefulSet Restart Helper

### **Logic**:
```go
1. Get StatefulSet by name/namespace
2. If not found, skip (return nil)
3. Add/update annotation:
   statefulSet.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)
4. Call r.Update(ctx, statefulSet)
5. Kubernetes triggers rolling restart due to pod template change
```

---

## **5. waitForInfisicalReadiness()** - Readiness Check Helper

### **Logic**:
```go
1. Get Deployment "infisical" (namespace: platform-ops)
2. If not found, return false
3. Check deployment.Status.Conditions for:
   - Type: DeploymentAvailable
   - Status: True
4. Return true if available, false otherwise
```

---

## **Key Observations**

### **Hardcoded Dependencies**:
1. **Service Names**: All service names are hardcoded in arrays/maps
2. **Namespaces**: Hardcoded namespaces (platform-ops, ory-system, spire-system)
3. **Role-to-Service Mapping**: Static mapping in `serviceMap`

### **Coupling Issues**:
1. **Certificate Rotation** updates `infisical-secrets` directly (should be ESO's job)
2. **Password Rotation** executes `ALTER ROLE` directly (should be dedicated controller)
3. **Pod Restarts** managed by operator (should be Kubernetes or dedicated controller)

### **Missing Features**:
1. No retry logic for failed restarts
2. No validation that services actually restarted successfully
3. No rollback mechanism if rotation fails
4. Password rotation doesn't update status conditions (unlike cert rotation)

### **Scope Violation**:
All 4 functions are **Day 2 operations** that should NOT be in a Day 0 bootstrap operator. They should be moved to:
- Dedicated certificate rotation controller
- Dedicated password rotation controller
- Or handled by Kubernetes native mechanisms (Reloader, secret watchers)