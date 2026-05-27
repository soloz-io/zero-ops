# ADR 0022: Dynamic Permission Escalation via RBAC Aggregation for Platform Controllers

**Status**: Accepted  
**Date**: 2026-05-11  
**Context**: Platform controllers (like Kyverno) occasionally need to manage Custom Resources (like Crossplane `ProviderConfig` objects) that are introduced to the cluster *after* the controller was initially deployed. Manually editing Helm charts or maintaining floating `ClusterRoleBindings` to grant these permissions breaks declarative encapsulation and creates GitOps drift.

---

## Problem Statement

Platform controllers require dynamic permission management to handle new Custom Resources and API extensions. Traditional approaches include:

1. **Manual Helm Chart Updates**: Editing values.yaml files and re-deploying controllers
2. **Floating ClusterRoleBindings**: Creating separate RBAC resources outside of Git version control
3. **Static cluster-admin Grants**: Overly privileged permissions that violate least privilege principle

These approaches break our GitOps principles and create operational drift between declared and actual permissions.

---

## Decision

We will exclusively use **Kubernetes ClusterRole Aggregation** to extend permissions for foundational platform controllers.

When a Kyverno policy requires new permissions (e.g., generating a Crossplane `ProviderConfig`), we will declare a specific `ClusterRole` alongside the policy manifest, tagged with `rbac.kyverno.io/aggregate-to-background-controller: "true"`.

---

## Architecture

```yaml
# Kyverno Policy with Dynamic RBAC
apiVersion: kyverno.io/v1
kind: ClusterPolicy
metadata:
  name: crossplane-providerconfig-generator
  annotations:
    policies.kyverno.io/title: "Generate Crossplane ProviderConfig"
    policies.kyverno.io/category: "Crossplane"
spec:
  rules:
    - name: generate-providerconfig
      context:
        - name: request
          variable:
            name: providerName
      generate:
        apiVersion: platform.crossplane.io/v1alpha1
        kind: ProviderConfig
        name: "{{request.object.metadata.name}}-providerconfig"
        namespace: platform-ops
        data:
          spec:
            providerConfigRef:
              name: "{{request.object.metadata.name}}"

# Aggregated ClusterRole for Dynamic Permissions
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kyverno-policy-controller-aggregated
  labels:
    rbac.kyverno.io/aggregate-to-background-controller: "true"
rules:
  - apiGroups: ["platform.crossplane.io"]
    resources: ["providerconfigs"]
    verbs: ["create", "update", "patch", "get", "list", "watch"]
```

---

## Consequences

### Positive

- **Tight Coupling**: Policies and their required permissions are tightly coupled in Git
- **Least Privilege**: We adhere to Principle of Least Privilege by not granting `cluster-admin` to background controllers, while still retaining dynamic extensibility
- **Declarative**: All permissions are declared in version control alongside the policies that require them
- **No Manual RBAC**: Eliminates manual RBAC patching; Kubernetes API server handles rule merging automatically
- **GitOps Compliant**: Permission changes follow the same Git workflow as policy changes

### Negative/Mitigation

- **Complexity**: Requires understanding of Kubernetes RBAC aggregation rules
- **Debugging**: Permission issues may be less obvious than with explicit ClusterRoleBindings
- **Migration**: Existing manual ClusterRoleBindings need to be migrated to aggregation pattern

---

## Implementation Guidelines

### When Adding New Policy Permissions

1. **Identify Required Permissions**: Determine what API groups/resources the policy needs
2. **Create Aggregated ClusterRole**: Add new ClusterRole with aggregation label
3. **Document in Policy**: Include permission requirements in policy documentation
4. **Test in Isolated Environment**: Verify permissions work before production deployment

### Example Pattern

```yaml
# For any new controller that needs to manage CustomResourceDefinition X
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: x-resource-manager-aggregated
  labels:
    rbac.kyverno.io/aggregate-to-background-controller: "true"
rules:
  - apiGroups: ["apiextensions.k8s.io"]
    resources: ["customresourcedefinitions"]
    verbs: ["create", "update", "patch", "get", "list", "watch"]
```

---

## Migration Path

### Phase 1: Audit Existing Permissions
- Identify all manually created ClusterRoleBindings
- Map them to responsible controllers/policies

### Phase 2: Create Aggregated Roles
- Convert manual bindings to aggregated ClusterRoles
- Add appropriate aggregation labels

### Phase 3: Update Policies
- Ensure policies reference aggregated roles
- Remove manual ClusterRoleBindings

### Phase 4: Validate
- Test all policy functionality
- Verify no permission regressions

---

## Related ADRs

- **ADR 015**: Namespace Alignment - Defines where these aggregated roles should be deployed
- **ADR 0001**: ClusterResourceSet Management - Works with this pattern for bootstrap resource delivery
