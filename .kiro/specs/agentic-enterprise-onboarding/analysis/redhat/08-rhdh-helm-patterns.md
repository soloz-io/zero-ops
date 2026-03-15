# RHDH Helm Chart - Applicability Analysis

## Project Overview
**Source:** `.kiro/specs/agentic-enterprise-onboarding/references/redhat/rhdh-chart/`

Red Hat Developer Hub (Backstage) Helm chart with production-grade patterns: external database support, monitoring integration, secret management.

## Applicability to Zero-Ops Platform

### ✅ HIGHLY APPLICABLE (Helm Patterns)

**Use Case:** Helm Chart Best Practices for Zero-Ops Components

**Justification:**
1. **Production-Grade Patterns**: RHDH chart demonstrates enterprise Helm patterns (external DB, monitoring, secrets).
2. **Zero-Ops Uses Helm**: Ory stack (Hydra, Kratos, Keto), AgentGateway, and future components deployed via Helm.
3. **Reusable Patterns**: External database configuration, monitoring integration, secret injection - all applicable to Zero-Ops.

## Recommended Adoption

### ✅ Adopt: External Database Configuration Pattern

**RHDH Pattern:**
```yaml
# charts/backstage/values.yaml
postgresql:
  enabled: false  # Disable embedded PostgreSQL

externalDatabase:
  host: postgres.example.com
  port: 5432
  user: backstage
  database: backstage
  existingSecret: backstage-postgres-secret
  existingSecretKey: password
```

**Zero-Ops Application:**
```yaml
# manifests/platform-identity/ory-hydra/values.yaml
hydra:
  config:
    dsn: postgres://hydra:$(HYDRA_DB_PASSWORD)@identity-postgres-rw.ory-system.svc.cluster.local:5432/hydra_db
  
  extraEnv:
    - name: HYDRA_DB_PASSWORD
      valueFrom:
        secretKeyRef:
          name: identity-postgres-passwords
          key: hydra-password
```

**Benefit:** Consistent external database pattern across all Zero-Ops Helm charts.

**Source Reference:**
```
.kiro/specs/agentic-enterprise-onboarding/references/redhat/rhdh-chart/docs/external-db.md
```

### ✅ Adopt: Monitoring Integration Pattern

**RHDH Pattern:**
```yaml
# charts/backstage/templates/servicemonitor.yaml
{{- if .Values.metrics.serviceMonitor.enabled }}
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: {{ include "backstage.fullname" . }}
  labels:
    {{- include "backstage.labels" . | nindent 4 }}
spec:
  selector:
    matchLabels:
      {{- include "backstage.selectorLabels" . | nindent 6 }}
  endpoints:
    - port: http
      path: /metrics
      interval: {{ .Values.metrics.serviceMonitor.interval }}
{{- end }}
```

**Zero-Ops Application:**
```yaml
# manifests/platform-identity/auth-proxy/templates/servicemonitor.yaml
{{- if .Values.metrics.enabled }}
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: {{ include "auth-proxy.fullname" . }}
  labels:
    nutgraf.in/component: auth-proxy
spec:
  selector:
    matchLabels:
      app: auth-proxy
  endpoints:
    - port: metrics
      path: /metrics
      interval: 30s
{{- end }}
```

**Benefit:** Standardized Prometheus monitoring across all Zero-Ops components.

**Source Reference:**
```
.kiro/specs/agentic-enterprise-onboarding/references/redhat/rhdh-chart/docs/monitoring.md
```

### ✅ Adopt: Secret Management Pattern

**RHDH Pattern:**
```yaml
# charts/backstage/templates/deployment.yaml
env:
  - name: POSTGRES_PASSWORD
    valueFrom:
      secretKeyRef:
        name: {{ .Values.externalDatabase.existingSecret }}
        key: {{ .Values.externalDatabase.existingSecretKey }}
```

**Zero-Ops Application:**
```yaml
# manifests/platform-identity/auth-proxy/templates/deployment.yaml
env:
  - name: HYDRA_ADMIN_URL
    value: {{ .Values.hydra.adminUrl }}
  - name: KRATOS_ADMIN_URL
    value: {{ .Values.kratos.adminUrl }}
  - name: JWKS_CACHE_TTL
    value: {{ .Values.jwt.cacheT TL }}
```

**Benefit:** Consistent secret injection pattern (no hardcoded credentials).

### ✅ Adopt: Helm Chart Testing Pattern

**RHDH Pattern:**
```yaml
# ct-lint.yaml (Chart Testing config)
chart-dirs:
  - charts
chart-repos:
  - bitnami=https://charts.bitnami.com/bitnami
helm-extra-args: --timeout 600s
```

**Zero-Ops Application:**
```yaml
# zero-ops/.github/workflows/helm-lint.yaml
- name: Lint Helm Charts
  run: |
    ct lint --config ct-lint.yaml \
      --charts manifests/platform-identity/auth-proxy \
      --charts manifests/platform-identity/ory-hydra
```

**Benefit:** Automated Helm chart validation in CI.

**Source Reference:**
```
.kiro/specs/agentic-enterprise-onboarding/references/redhat/rhdh-chart/ct-lint.yaml
```

### ✅ Adopt: Values Schema Validation

**RHDH Pattern:**
```yaml
# charts/backstage/values.schema.json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "postgresql": {
      "type": "object",
      "properties": {
        "enabled": { "type": "boolean" }
      }
    },
    "externalDatabase": {
      "type": "object",
      "required": ["host", "port", "user", "database"]
    }
  }
}
```

**Zero-Ops Application:**
```yaml
# manifests/platform-identity/auth-proxy/values.schema.json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "required": ["hydra", "kratos"],
  "properties": {
    "hydra": {
      "type": "object",
      "required": ["publicUrl", "adminUrl"]
    },
    "kratos": {
      "type": "object",
      "required": ["publicUrl", "adminUrl"]
    }
  }
}
```

**Benefit:** Helm validates values.yaml at install time (prevents misconfigurations).

## Alignment with Zero-Ops Architecture

| Zero-Ops Component | RHDH Helm Pattern | Adoption Decision |
|---|---|---|
| Ory Hydra/Kratos/Keto | External DB config | ✅ Adopt |
| auth-proxy | ServiceMonitor | ✅ Adopt |
| AgentGateway | Secret injection | ✅ Adopt |
| All Helm charts | Values schema validation | ✅ Adopt |
| CI/CD | Chart testing (ct lint) | ✅ Adopt |
| Platform Console | Backstage deployment | ⚠️ Defer (post-MVP) |

## Implementation Priority
**Priority:** MEDIUM (Sprint 1 - Day 6)

**Rationale:** Day 6 deploys ArgoCD and infrastructure. Helm patterns should be applied to all charts before deployment.

## Specific Patterns to Extract

### 1. External Database Helper Template
**File:** `charts/backstage/templates/_helpers.tpl`
**Pattern:**
```yaml
{{- define "backstage.postgresql.host" -}}
{{- if .Values.postgresql.enabled -}}
{{ include "backstage.fullname" . }}-postgresql
{{- else -}}
{{ .Values.externalDatabase.host }}
{{- end -}}
{{- end -}}
```

**Zero-Ops Application:**
```yaml
# manifests/platform-identity/ory-hydra/templates/_helpers.tpl
{{- define "hydra.database.dsn" -}}
postgres://hydra:$(HYDRA_DB_PASSWORD)@{{ .Values.externalDatabase.host }}:{{ .Values.externalDatabase.port }}/{{ .Values.externalDatabase.database }}
{{- end -}}
```

### 2. ServiceMonitor Conditional
**File:** `charts/backstage/templates/servicemonitor.yaml`
**Pattern:** Only create if `.Values.metrics.serviceMonitor.enabled`

**Zero-Ops Application:** Apply to auth-proxy, zero-ops-api, all custom services.

### 3. Secret Existence Check
**File:** `charts/backstage/templates/NOTES.txt`
**Pattern:** Warn if required secrets don't exist

**Zero-Ops Application:**
```yaml
# manifests/platform-identity/ory-hydra/templates/NOTES.txt
{{- if not (lookup "v1" "Secret" .Release.Namespace "identity-postgres-passwords") }}
WARNING: Secret 'identity-postgres-passwords' not found.
Create it before Hydra can start:
  kubectl create secret generic identity-postgres-passwords \
    --namespace={{ .Release.Namespace }} \
    --from-literal=hydra-password=$(openssl rand -base64 32)
{{- end }}
```

## Risks if NOT Adopted
- Inconsistent Helm chart structure across Zero-Ops components
- No values.yaml validation (runtime errors instead of install-time errors)
- Manual monitoring setup (no ServiceMonitor templates)
- Hardcoded database credentials (security risk)

## Risks if Fully Adopted (Backstage Deployment)
- Heavy dependency (PostgreSQL, complex plugin ecosystem)
- Maintenance burden (Backstage upgrades every 6 weeks)
- Scope overlap with Platform Console

**Recommendation:** Adopt Helm patterns, NOT full Backstage deployment.

## Next Steps

### Sprint 1 - Day 6 (Task 40a)
1. Create Helm chart templates for auth-proxy
2. Apply external database pattern to Ory charts
3. Add ServiceMonitor templates to all charts
4. Create values.schema.json for validation
5. Add ct-lint.yaml for CI validation

### Implementation Code Path
```
zero-ops/manifests/platform-identity/
├── auth-proxy/
│   ├── Chart.yaml
│   ├── values.yaml
│   ├── values.schema.json (NEW - from RHDH pattern)
│   └── templates/
│       ├── deployment.yaml
│       ├── service.yaml
│       ├── servicemonitor.yaml (NEW - from RHDH pattern)
│       └── _helpers.tpl (NEW - from RHDH pattern)
├── ory-hydra/
│   └── values.yaml (UPDATED - external DB pattern)
└── ory-kratos/
    └── values.yaml (UPDATED - external DB pattern)
```

## Conclusion
RHDH Helm Chart is HIGHLY applicable for Helm patterns. Adopt external database config, monitoring integration, secret management, and values schema validation. Do NOT adopt full Backstage deployment (use patterns only).

## Source Code References
```
.kiro/specs/agentic-enterprise-onboarding/references/redhat/rhdh-chart/
├── charts/backstage/values.yaml (external DB pattern)
├── charts/backstage/templates/servicemonitor.yaml (monitoring pattern)
├── charts/backstage/values.schema.json (validation pattern)
├── docs/external-db.md (database configuration guide)
├── docs/monitoring.md (Prometheus integration guide)
└── ct-lint.yaml (chart testing config)
```
