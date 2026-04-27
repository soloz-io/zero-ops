# ADR 008: Federated API Boundary Pattern - Design

## Overview

This design implements the Federated API Boundary Pattern by introducing a `SpokeTenantEnvironment` XRD on Spoke clusters that acts as the API contract between Hub and Spoke. The Hub pushes high-level intent (tenant specification), and the Spoke implements the details (Namespace, RBAC, Pooler, PostgREST, etc.).

## Architecture

```
Hub Cluster
├── AINativeSaaS XR (tenant intent)
├── AINativeSaaS Composition (simplified)
│   ├── TenantDatabase Object MR → Spoke (existing)
│   └── SpokeTenantEnvironment Object MR → Spoke (new)
└── provider-kubernetes (RBAC: 2 XRD types only)

Spoke Cluster
├── TenantDatabase XRD + Composition (existing)
├── SpokeTenantEnvironment XRD + Composition (new)
│   ├── Namespace
│   ├── ServiceAccount
│   ├── Role
│   ├── RoleBinding
│   ├── ResourceQuota
│   ├── CNPG Pooler
│   ├── PostgREST Deployment
│   ├── PostgREST Service
│   └── AtlasMigration
└── Local Crossplane (reconciles XRs locally)
```

## Component Design

### 1. SpokeTenantEnvironment XRD

**File:** `zero-ops/manifests/spoke/xrds/spoketenantenvironment.yaml`

```yaml
apiVersion: apiextensions.crossplane.io/v1
kind: CompositeResourceDefinition
metadata:
  name: spoketenantenvironments.nutgraf.in
spec:
  group: nutgraf.in
  names:
    kind: SpokeTenantEnvironment
    plural: spoketenantenvironments
  claimNames:
    kind: SpokeTenantEnvironmentClaim
    plural: spoketenantenvironmentclaims
  versions:
  - name: v1alpha1
    served: true
    referenceable: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            required:
              - tenantId
              - tier
              - databaseName
            properties:
              tenantId:
                type: string
                description: "Tenant identifier"
                pattern: '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'
                maxLength: 63
              tier:
                type: string
                description: "Tenant tier"
                enum: [starter, enterprise]
              databaseName:
                type: string
                description: "Database name for pooler connection"
              postgrestImage:
                type: string
                description: "PostgREST container image"
                default: "postgrest/postgrest:v12.0.2"
              resourceQuota:
                type: object
                properties:
                  cpu:
                    type: string
                  memory:
                    type: string
                  storage:
                    type: string
                  pods:
                    type: string
          status:
            type: object
            properties:
              ready:
                type: boolean
                description: "True when all resources provisioned"
              message:
                type: string
                description: "Human-readable status message"
              conditions:
                type: array
                items:
                  type: object
                  properties:
                    type:
                      type: string
                    status:
                      type: string
                    lastTransitionTime:
                      type: string
                      format: date-time
                    reason:
                      type: string
                    message:
                      type: string
```

### 2. SpokeTenantEnvironment Composition

**File:** `zero-ops/manifests/spoke/compositions/spoketenantenvironment-default.yaml`

**Key Design Decisions:**

1. **Native Resources (Not Object MRs):** Since resources are local to the Spoke, use native Kubernetes resources directly (Namespace, ServiceAccount, etc.) instead of wrapping in Object MRs

2. **Status Aggregation:** Use Crossplane's built-in status aggregation to set `status.ready` based on all composed resources

3. **Dependency Ordering:** Namespace → ServiceAccount → RBAC → ResourceQuota → Pooler → PostgREST → AtlasMigration

4. **Secret References:** Pooler and PostgREST reference secrets created by `TenantDatabase` XR (cross-XR dependency)

**Composition Structure:**

```yaml
apiVersion: apiextensions.crossplane.io/v1
kind: Composition
metadata:
  name: spoketenantenvironment-default
spec:
  compositeTypeRef:
    apiVersion: nutgraf.in/v1alpha1
    kind: SpokeTenantEnvironment
  
  resources:
    # Resource 1: Namespace
    - name: namespace
      base:
        apiVersion: v1
        kind: Namespace
        metadata:
          name: ""  # Patched from tenantId
          labels:
            tenant-id: ""
            tier: ""
            managed-by: crossplane
      patches:
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: metadata.name
          transforms:
            - type: string
              string:
                fmt: "tenant-%s"
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: metadata.labels['tenant-id']
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tier
          toFieldPath: metadata.labels['tier']
    
    # Resource 2: ServiceAccount
    - name: service-account
      base:
        apiVersion: v1
        kind: ServiceAccount
        metadata:
          name: ""
          namespace: ""
      patches:
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: metadata.name
          transforms:
            - type: string
              string:
                fmt: "tenant-%s"
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: metadata.namespace
          transforms:
            - type: string
              string:
                fmt: "tenant-%s"
    
    # Resource 3: Role
    - name: role
      base:
        apiVersion: rbac.authorization.k8s.io/v1
        kind: Role
        metadata:
          name: ""
          namespace: ""
        rules:
          - apiGroups: [""]
            resources: ["pods", "services", "configmaps", "secrets"]
            verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
          - apiGroups: ["apps"]
            resources: ["deployments", "statefulsets"]
            verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
      patches:
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: metadata.name
          transforms:
            - type: string
              string:
                fmt: "tenant-%s-role"
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: metadata.namespace
          transforms:
            - type: string
              string:
                fmt: "tenant-%s"
    
    # Resource 4: RoleBinding
    - name: role-binding
      base:
        apiVersion: rbac.authorization.k8s.io/v1
        kind: RoleBinding
        metadata:
          name: ""
          namespace: ""
        roleRef:
          apiGroup: rbac.authorization.k8s.io
          kind: Role
          name: ""
        subjects:
          - kind: ServiceAccount
            name: ""
            namespace: ""
      patches:
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: metadata.name
          transforms:
            - type: string
              string:
                fmt: "tenant-%s-binding"
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: metadata.namespace
          transforms:
            - type: string
              string:
                fmt: "tenant-%s"
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: roleRef.name
          transforms:
            - type: string
              string:
                fmt: "tenant-%s-role"
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: subjects[0].name
          transforms:
            - type: string
              string:
                fmt: "tenant-%s"
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: subjects[0].namespace
          transforms:
            - type: string
              string:
                fmt: "tenant-%s"
    
    # Resource 5: ResourceQuota
    - name: resource-quota
      base:
        apiVersion: v1
        kind: ResourceQuota
        metadata:
          name: ""
          namespace: ""
        spec:
          hard:
            requests.cpu: ""
            requests.memory: ""
            requests.storage: ""
            pods: ""
      patches:
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: metadata.name
          transforms:
            - type: string
              string:
                fmt: "tenant-%s-quota"
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: metadata.namespace
          transforms:
            - type: string
              string:
                fmt: "tenant-%s"
        - type: FromCompositeFieldPath
          fromFieldPath: spec.resourceQuota.cpu
          toFieldPath: spec.hard['requests.cpu']
        - type: FromCompositeFieldPath
          fromFieldPath: spec.resourceQuota.memory
          toFieldPath: spec.hard['requests.memory']
        - type: FromCompositeFieldPath
          fromFieldPath: spec.resourceQuota.storage
          toFieldPath: spec.hard['requests.storage']
        - type: FromCompositeFieldPath
          fromFieldPath: spec.resourceQuota.pods
          toFieldPath: spec.hard['pods']
    
    # Resource 6: CNPG Pooler
    - name: pooler
      base:
        apiVersion: postgresql.cnpg.io/v1
        kind: Pooler
        metadata:
          name: ""
          namespace: spoke-platform-data
        spec:
          cluster:
            name: shared-cnpg
          type: rw
          instances: 1
          pgbouncer:
            poolMode: transaction
            parameters:
              max_client_conn: "100"
              default_pool_size: "5"
            authQueryUser:
              secretRef:
                name: ""  # References TenantDatabase secret
          template:
            spec:
              containers:
                - name: pgbouncer
                  env:
                    - name: PGDATABASE
                      value: ""
      patches:
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: metadata.name
          transforms:
            - type: string
              string:
                fmt: "%s-pooler"
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: metadata.labels['tenant-id']
        - type: FromCompositeFieldPath
          fromFieldPath: spec.databaseName
          toFieldPath: spec.template.spec.containers[0].env[0].value
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: spec.pgbouncer.authQueryUser.secretRef.name
          transforms:
            - type: string
              string:
                fmt: "%s-db-credentials"
    
    # Resource 7: PostgREST Deployment
    - name: postgrest-deployment
      base:
        apiVersion: apps/v1
        kind: Deployment
        metadata:
          name: ""
          namespace: ""
        spec:
          replicas: 1
          selector:
            matchLabels:
              app: postgrest
              tenant-id: ""
          template:
            metadata:
              labels:
                app: postgrest
                tenant-id: ""
            spec:
              containers:
                - name: postgrest
                  image: ""  # Patched from spec.postgrestImage
                  ports:
                    - containerPort: 3000
                      name: http
                  env:
                    - name: PGRST_DB_URI
                      valueFrom:
                        secretKeyRef:
                          name: ""  # References TenantDatabase pooler-app secret
                          key: url
                    - name: PGRST_DB_SCHEMA
                      value: "public"
                    - name: PGRST_DB_ANON_ROLE
                      value: ""  # Patched from tenantId
                    - name: PGRST_JWT_SECRET
                      valueFrom:
                        secretKeyRef:
                          name: hub-ory-jwt-secret
                          key: secret
                  resources:
                    requests:
                      cpu: 100m
                      memory: 128Mi
                    limits:
                      cpu: 500m
                      memory: 512Mi
      patches:
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: metadata.name
          transforms:
            - type: string
              string:
                fmt: "postgrest-%s"
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: metadata.namespace
          transforms:
            - type: string
              string:
                fmt: "tenant-%s"
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: spec.selector.matchLabels['tenant-id']
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: spec.template.metadata.labels['tenant-id']
        - type: FromCompositeFieldPath
          fromFieldPath: spec.postgrestImage
          toFieldPath: spec.template.spec.containers[0].image
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: spec.template.spec.containers[0].env[0].valueFrom.secretKeyRef.name
          transforms:
            - type: string
              string:
                fmt: "%s-pooler-app"
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: spec.template.spec.containers[0].env[2].value
          transforms:
            - type: string
              string:
                fmt: "tenant-%s-user"
    
    # Resource 8: PostgREST Service
    - name: postgrest-service
      base:
        apiVersion: v1
        kind: Service
        metadata:
          name: ""
          namespace: ""
        spec:
          type: ClusterIP
          ports:
            - port: 3000
              targetPort: 3000
              protocol: TCP
              name: http
          selector:
            app: postgrest
            tenant-id: ""
      patches:
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: metadata.name
          transforms:
            - type: string
              string:
                fmt: "postgrest-%s"
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: metadata.namespace
          transforms:
            - type: string
              string:
                fmt: "tenant-%s"
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: spec.selector['tenant-id']
    
    # Resource 9: AtlasMigration
    - name: atlasmigration
      base:
        apiVersion: db.atlasgo.io/v1alpha1
        kind: AtlasMigration
        metadata:
          name: ""
          namespace: ""
        spec:
          urlFrom:
            secretKeyRef:
              name: ""  # References TenantDatabase pooler-app secret
              key: url
          dir:
            configMapRef:
              name: ""  # References migrations ConfigMap
      patches:
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: metadata.name
          transforms:
            - type: string
              string:
                fmt: "%s-migrations"
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: metadata.namespace
          transforms:
            - type: string
              string:
                fmt: "tenant-%s"
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: spec.urlFrom.secretKeyRef.name
          transforms:
            - type: string
              string:
                fmt: "%s-pooler-app"
        - type: FromCompositeFieldPath
          fromFieldPath: spec.tenantId
          toFieldPath: spec.dir.configMapRef.name
          transforms:
            - type: string
              string:
                fmt: "tenant-%s-migrations"
```

### 3. Hub Composition Refactoring

**File:** `zero-ops/manifests/hub-core-services/crossplane/tenant-platform/compositions/ainativesaas-starter-hetzner.yaml`

**Changes:**

1. **Remove 9 Object MRs:**
   - namespace
   - service-account
   - role
   - role-binding
   - resource-quota
   - pooler
   - postgrest-deployment
   - postgrest-service
   - atlasmigration

2. **Add SpokeTenantEnvironment Object MR:**

```yaml
# Resource 2: SpokeTenantEnvironment XR - Remote Provider Pattern
- name: spoke-tenant-environment
  base:
    apiVersion: kubernetes.crossplane.io/v1alpha2
    kind: Object
    spec:
      managementPolicies: ["*"]
      forProvider:
        manifest:
          apiVersion: nutgraf.in/v1alpha1
          kind: SpokeTenantEnvironment
          metadata:
            name: ""  # Patched from tenantId
          spec:
            tenantId: ""
            tier: ""
            databaseName: ""
            postgrestImage: ""
            resourceQuota:
              cpu: ""
              memory: ""
              storage: ""
              pods: ""
      readinessChecks:
        - type: MatchCondition
          matchCondition:
            type: Ready
            status: "True"
      providerConfigRef:
        name: ""  # Patched from cellId
  patches:
    - type: FromCompositeFieldPath
      fromFieldPath: spec.cellId
      toFieldPath: spec.providerConfigRef.name
    - type: FromCompositeFieldPath
      fromFieldPath: spec.tenantId
      toFieldPath: metadata.name
      transforms:
        - type: string
          string:
            fmt: "%s-spoke-tenant-environment"
    - type: FromCompositeFieldPath
      fromFieldPath: spec.tenantId
      toFieldPath: spec.forProvider.manifest.metadata.name
    - type: FromCompositeFieldPath
      fromFieldPath: spec.tenantId
      toFieldPath: spec.forProvider.manifest.spec.tenantId
    - type: FromCompositeFieldPath
      fromFieldPath: spec.tier
      toFieldPath: spec.forProvider.manifest.spec.tier
    - type: FromCompositeFieldPath
      fromFieldPath: spec.database.name
      toFieldPath: spec.forProvider.manifest.spec.databaseName
    - type: FromCompositeFieldPath
      fromFieldPath: spec.postgrest.image
      toFieldPath: spec.forProvider.manifest.spec.postgrestImage
    - type: FromCompositeFieldPath
      fromFieldPath: spec.resourceQuota.cpu
      toFieldPath: spec.forProvider.manifest.spec.resourceQuota.cpu
    - type: FromCompositeFieldPath
      fromFieldPath: spec.resourceQuota.memory
      toFieldPath: spec.forProvider.manifest.spec.resourceQuota.memory
    - type: FromCompositeFieldPath
      fromFieldPath: spec.resourceQuota.storage
      toFieldPath: spec.forProvider.manifest.spec.resourceQuota.storage
    - type: FromCompositeFieldPath
      fromFieldPath: spec.resourceQuota.pods
      toFieldPath: spec.forProvider.manifest.spec.resourceQuota.pods
```

### 4. ArgoCD Distribution

**File:** `zero-ops/manifests/argocd/apps/platform-spoke-catalog-appsets.yaml`

Add new ApplicationSet for `SpokeTenantEnvironment` XRD and Composition:

```yaml
---
# SpokeTenantEnvironment XRD and Composition (Spoke)
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: platform-spoketenantenvironment-xrds-compositions
  namespace: argocd
spec:
  generators:
    - list:
        elements:
          - cluster: spoke-pool-eu-prod-01
            url: https://spoke-pool-eu-prod-01.example.com
  template:
    metadata:
      name: '{{cluster}}-spoketenantenvironment-xrds'
    spec:
      project: platform
      source:
        repoURL: https://github.com/soloz-io/zero-ops.git
        targetRevision: main
        path: manifests/spoke/xrds
        directory:
          include: 'spoketenantenvironment.yaml'
      destination:
        server: '{{url}}'
        namespace: crossplane-system
      syncPolicy:
        automated:
          prune: true
          selfHeal: true
        syncOptions:
          - CreateNamespace=true
---
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: platform-spoketenantenvironment-compositions
  namespace: argocd
spec:
  generators:
    - list:
        elements:
          - cluster: spoke-pool-eu-prod-01
            url: https://spoke-pool-eu-prod-01.example.com
  template:
    metadata:
      name: '{{cluster}}-spoketenantenvironment-compositions'
    spec:
      project: platform
      source:
        repoURL: https://github.com/soloz-io/zero-ops.git
        targetRevision: main
        path: manifests/spoke/compositions
        directory:
          include: 'spoketenantenvironment-*.yaml'
      destination:
        server: '{{url}}'
        namespace: crossplane-system
      syncPolicy:
        automated:
          prune: true
          selfHeal: true
```

### 5. RBAC Configuration

**File:** `zero-ops/manifests/hub-core-services/crossplane/provider-kubernetes-rbac.yaml`

Update Hub's `provider-kubernetes` RBAC to only allow 2 XRD types:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: crossplane-provider-kubernetes-spoke
rules:
  # TenantDatabase XR (existing)
  - apiGroups: ["nutgraf.in"]
    resources: ["tenantdatabases"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  
  # SpokeTenantEnvironment XR (new)
  - apiGroups: ["nutgraf.in"]
    resources: ["spoketenantenvironments"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  
  # Status subresource for both XRs
  - apiGroups: ["nutgraf.in"]
    resources: ["tenantdatabases/status", "spoketenantenvironments/status"]
    verbs: ["get", "update", "patch"]
```

## Progressive Rollout Strategy

**Scenario:** Update PostgREST image version from v12.0.2 to v12.0.3

**Before (Current):**
1. Update Hub Composition `postgrest-deployment` resource
2. Commit to Git
3. ArgoCD syncs to Hub
4. Crossplane reconciles ALL tenants globally
5. ALL tenants across ALL Spokes get new image simultaneously

**After (Federated API Boundary):**
1. Update Spoke Composition `postgrest-deployment` resource
2. Commit to Git
3. Update ArgoCD ApplicationSet to target only `spoke-pool-eu-prod-01`
4. ArgoCD syncs Composition to Cell A only
5. Crossplane on Cell A reconciles tenants on Cell A only
6. Validate Cell A tenants
7. Update ApplicationSet to include Cell B
8. Repeat for remaining cells

**Rollback:** Revert Composition on affected cell only, other cells unaffected

## Status Aggregation

**Spoke XR Status:**
```yaml
status:
  ready: true
  message: "All resources provisioned successfully"
  conditions:
    - type: Ready
      status: "True"
      reason: "AllResourcesReady"
      lastTransitionTime: "2026-04-27T10:00:00Z"
```

**Hub XR Status:**
```yaml
status:
  databaseReady: true  # From TenantDatabase XR
  environmentReady: true  # From SpokeTenantEnvironment XR
  conditions:
    - type: Ready
      status: "True"
      reason: "AllXRsReady"
      lastTransitionTime: "2026-04-27T10:00:30Z"
```

## Migration Path

**Phase 1:** Deploy `SpokeTenantEnvironment` XRD and Composition to Spokes (no impact on existing tenants)

**Phase 2:** Update Hub Composition to add `SpokeTenantEnvironment` Object MR (existing resources still provisioned)

**Phase 3:** Test new tenant with both patterns active (validate Spoke Composition works)

**Phase 4:** Remove 9 Object MRs from Hub Composition (new tenants use Spoke Composition only)

**Phase 5:** Existing tenants continue with old pattern until next update (gradual migration)

## Validation Strategy

**Unit Testing:** Manual validation at each phase (no automated tests per requirements)

**Integration Testing:** Deploy test tenant, verify all resources created, verify status propagation

**Rollout Testing:** Update Composition on one Spoke, verify isolation from other Spokes

**Rollback Testing:** Revert Composition, verify tenants recover

**Performance Testing:** Measure Hub ETCD size before/after, verify >80% reduction
