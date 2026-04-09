# cert-manager + Hetzner Webhook Integration

**Integration Document for**: spoke-pool-provisioner  
**Component**: cert-manager with Hetzner DNS webhook  
**Purpose**: Pre-generate mTLS certificates for ArgoCD Agent and NATS Leaf Node before Spoke Pool cluster bootstrap  
**Status**: Analysis Complete  
**Created**: 2026-04-09

---

## 1. Overview

### 1.1 Role in Spoke Pool Provisioning

cert-manager runs in the **Hub cluster** and pre-generates mTLS certificates **before** Spoke Pool clusters are provisioned. These certificates are injected into ClusterResourceSet as "Secret Zero" - the minimal bootstrap credentials needed for ArgoCD Agent and NATS Leaf Node to connect to Hub services.

**Critical Constraint**: Certificates must exist in Hub **before** CAPI cluster provisioning begins, as ClusterResourceSet references are resolved at cluster creation time.

### 1.2 Spec Requirements Mapping

| Requirement | Description | cert-manager Role |
|-------------|-------------|-------------------|
| **FR-1.2** | Automated Cluster Bootstrap | Pre-generate ArgoCD Agent mTLS certificate |
| **FR-2.3** | NATS Leaf Node mTLS | Pre-generate NATS client certificate |
| **NFR-4.1** | All Hub-Spoke communication uses mTLS | Certificate authority for Hub-Spoke trust |
| **NFR-4.2** | Certificates auto-rotate 7 days before expiration | cert-manager automatic renewal |
| **AC-1** | Composition patches pre-generated mTLS certificate | Certificate Secret exists before XR apply |
| **AC-3** | ClusterResourceSet contains mTLS cert Secret | Certificate injected via Crossplane patching |

---

## 2. Architecture

### 2.1 Certificate Lifecycle in Spoke Pool Provisioning

```
┌─────────────────────────────────────────────────────────────────────┐
│                         Hub Cluster (Pre-Provisioning)              │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  1. Platform Admin prepares SpokePool XR                           │
│     └─> cell-id: spokepool-01                                      │
│                                                                     │
│  2. cert-manager generates certificates (BEFORE Crossplane apply)  │
│     ├─> Certificate: argocd-agent-spokepool-01                     │
│     │   └─> Secret: argocd-agent-spokepool-01-tls                  │
│     │       ├─> tls.crt (client certificate)                       │
│     │       ├─> tls.key (private key)                              │
│     │       └─> ca.crt (CA certificate)                            │
│     │                                                               │
│     └─> Certificate: nats-leaf-spokepool-01                        │
│         └─> Secret: nats-leaf-spokepool-01-tls                     │
│             ├─> tls.crt (client certificate)                       │
│             ├─> tls.key (private key)                              │
│             └─> ca.crt (CA certificate)                            │
│                                                                     │
│  3. Crossplane SpokePool Composition patches certificates          │
│     └─> ClusterResourceSet references:                             │
│         ├─> argocd-agent-spokepool-01-tls (from Hub)               │
│         └─> nats-leaf-spokepool-01-tls (from Hub)                  │
│                                                                     │
│  4. CAPI provisions cluster, injects ClusterResourceSet            │
│     └─> Spoke Pool cluster receives certificates at bootstrap      │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│                    Spoke Pool Cluster (Post-Bootstrap)              │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  5. ArgoCD Agent uses mTLS certificate                             │
│     └─> Connects to Hub ArgoCD Principal (port 8443)               │
│                                                                     │
│  6. NATS Leaf Node uses mTLS certificate                           │
│     └─> Connects to Hub NATS JetStream (port 7422)                 │
│                                                                     │
│  7. cert-manager NOT deployed in Spoke (Hub manages rotation)      │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### 2.2 Certificate Authorities

**Hub CA Structure**:
- **ArgoCD CA**: Self-signed CA for ArgoCD Agent mTLS (managed by cert-manager Issuer)
- **NATS CA**: Self-signed CA for NATS Leaf Node mTLS (managed by cert-manager Issuer)
- **Public CA**: Let's Encrypt for external HTTPS endpoints (optional, not Phase 1)

**Rationale**: Separate CAs for ArgoCD and NATS prevent cross-service certificate misuse (defense in depth).

---

## 3. cert-manager Configuration

### 3.1 Hub Deployment (Existing)

cert-manager is already deployed in Hub cluster with Hetzner DNS webhook for DNS-01 challenges.

**Deployment Location**: `hub-platform` namespace  
**Version**: v1.13+  
**Components**:
- cert-manager controller (certificate lifecycle)
- cert-manager webhook (admission control)
- cert-manager-webhook-hetzner (DNS-01 solver for Hetzner DNS)

### 3.2 Self-Signed Issuers for mTLS

**ArgoCD CA Issuer**:
```yaml
# Hub cluster: hub-platform namespace
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: argocd-ca-issuer
  namespace: hub-platform
spec:
  ca:
    secretName: argocd-ca-secret  # Self-signed root CA
```

**NATS CA Issuer**:
```yaml
# Hub cluster: hub-platform namespace
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: nats-ca-issuer
  namespace: hub-platform
spec:
  ca:
    secretName: nats-ca-secret  # Self-signed root CA
```

**Root CA Generation** (one-time bootstrap):
```yaml
# Generate self-signed root CA for ArgoCD
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: argocd-ca
  namespace: hub-platform
spec:
  isCA: true
  commonName: argocd-ca
  secretName: argocd-ca-secret
  privateKey:
    algorithm: RSA
    size: 4096
  issuerRef:
    name: selfsigned-issuer
    kind: ClusterIssuer
  duration: 87600h  # 10 years
  renewBefore: 720h  # 30 days
```

### 3.3 ArgoCD Agent Certificate Template

**Certificate CR** (created per Spoke Pool):
```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: argocd-agent-spokepool-01
  namespace: hub-platform
spec:
  secretName: argocd-agent-spokepool-01-tls
  duration: 2160h  # 90 days
  renewBefore: 168h  # 7 days (NFR-4.2)
  subject:
    organizations:
      - zero-ops-platform
  commonName: argocd-agent-spokepool-01
  dnsNames:
    - argocd-agent-spokepool-01
    - argocd-agent.spoke-pool.svc
    - argocd-agent.spoke-pool.svc.cluster.local
  usages:
    - client auth
    - server auth
  issuerRef:
    name: argocd-ca-issuer
    kind: Issuer
```

**Key Fields**:
- `commonName`: Matches agent identity (validated by Hub Principal)
- `dnsNames`: Spoke-side service DNS names (for server auth)
- `usages`: Both client auth (agent → Hub) and server auth (Hub → agent health checks)
- `renewBefore: 168h`: Auto-renew 7 days before expiry (NFR-4.2)

### 3.4 NATS Leaf Node Certificate Template

**Certificate CR** (created per Spoke Pool):
```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: nats-leaf-spokepool-01
  namespace: hub-platform
spec:
  secretName: nats-leaf-spokepool-01-tls
  duration: 2160h  # 90 days
  renewBefore: 168h  # 7 days (NFR-4.2)
  subject:
    organizations:
      - zero-ops-platform
  commonName: nats-leaf-spokepool-01
  dnsNames:
    - nats-leaf-spokepool-01
    - nats.spoke-pool.svc
    - nats.spoke-pool.svc.cluster.local
  usages:
    - client auth
    - server auth
  issuerRef:
    name: nats-ca-issuer
    kind: Issuer
```

---

## 4. Integration with Crossplane SpokePool Composition

### 4.1 Certificate Pre-Generation Workflow

**Crossplane Composition MUST**:
1. Generate Certificate CRs **before** CAPI Cluster CR
2. Wait for Certificate Ready status (cert-manager populates Secret)
3. Patch Secret references into ClusterResourceSet
4. Create CAPI Cluster CR (triggers ClusterResourceSet injection)

**Implementation Pattern** (function-go-templating):
```yaml
# SpokePool Composition (Pipeline mode)
apiVersion: apiextensions.crossplane.io/v1
kind: Composition
metadata:
  name: spokepool-hetzner-v1
spec:
  mode: Pipeline
  pipeline:
    - step: generate-certificates
      functionRef:
        name: function-go-templating
      input:
        apiVersion: gotemplating.fn.crossplane.io/v1beta1
        kind: GoTemplate
        source: Inline
        inline:
          template: |
            # 1. ArgoCD Agent Certificate
            ---
            apiVersion: cert-manager.io/v1
            kind: Certificate
            metadata:
              name: argocd-agent-{{ .observed.composite.resource.metadata.name }}
              namespace: hub-platform
              annotations:
                crossplane.io/external-name: argocd-agent-{{ .observed.composite.resource.metadata.name }}
            spec:
              secretName: argocd-agent-{{ .observed.composite.resource.metadata.name }}-tls
              duration: 2160h
              renewBefore: 168h
              commonName: argocd-agent-{{ .observed.composite.resource.metadata.name }}
              dnsNames:
                - argocd-agent-{{ .observed.composite.resource.metadata.name }}
                - argocd-agent.spoke-pool.svc
                - argocd-agent.spoke-pool.svc.cluster.local
              usages:
                - client auth
                - server auth
              issuerRef:
                name: argocd-ca-issuer
                kind: Issuer
            
            # 2. NATS Leaf Node Certificate
            ---
            apiVersion: cert-manager.io/v1
            kind: Certificate
            metadata:
              name: nats-leaf-{{ .observed.composite.resource.metadata.name }}
              namespace: hub-platform
            spec:
              secretName: nats-leaf-{{ .observed.composite.resource.metadata.name }}-tls
              duration: 2160h
              renewBefore: 168h
              commonName: nats-leaf-{{ .observed.composite.resource.metadata.name }}
              dnsNames:
                - nats-leaf-{{ .observed.composite.resource.metadata.name }}
                - nats.spoke-pool.svc
                - nats.spoke-pool.svc.cluster.local
              usages:
                - client auth
                - server auth
              issuerRef:
                name: nats-ca-issuer
                kind: Issuer
    
    - step: wait-for-certificates
      functionRef:
        name: function-auto-ready
      # Blocks until Certificate status.conditions[Ready=True]
    
    - step: generate-capi-resources
      functionRef:
        name: function-go-templating
      input:
        apiVersion: gotemplating.fn.crossplane.io/v1beta1
        kind: GoTemplate
        source: Inline
        inline:
          template: |
            # ClusterResourceSet with certificate references
            ---
            apiVersion: addons.cluster.x-k8s.io/v1beta1
            kind: ClusterResourceSet
            metadata:
              name: {{ .observed.composite.resource.metadata.name }}-addons
              namespace: hub-platform-capi
            spec:
              clusterSelector:
                matchLabels:
                  cell-id: {{ .observed.composite.resource.metadata.name }}
              resources:
                # ArgoCD Agent mTLS certificate
                - name: argocd-agent-{{ .observed.composite.resource.metadata.name }}-tls
                  kind: Secret
                # NATS Leaf Node mTLS certificate
                - name: nats-leaf-{{ .observed.composite.resource.metadata.name }}-tls
                  kind: Secret
                # ArgoCD Agent CA certificate
                - name: argocd-ca-secret
                  kind: Secret
                # NATS CA certificate
                - name: nats-ca-secret
                  kind: Secret
                # ArgoCD Agent Deployment
                - name: argocd-agent-deployment
                  kind: ConfigMap
```

### 4.2 Certificate Readiness Dependency

**Critical**: CAPI Cluster CR MUST NOT be created until Certificate CRs reach `Ready=True` status.

**Validation**:
```bash
# Verify certificates exist before applying SpokePool XR
kubectl get certificate -n hub-platform argocd-agent-spokepool-01 -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
# Expected: True

kubectl get secret -n hub-platform argocd-agent-spokepool-01-tls -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -noout -text
# Expected: CN=argocd-agent-spokepool-01, valid for 90 days
```

---

## 5. Certificate Rotation Strategy

### 5.1 Automatic Renewal (Hub-Managed)

cert-manager automatically renews certificates 7 days before expiration (NFR-4.2).

**Renewal Workflow**:
1. cert-manager detects certificate expires in < 7 days
2. Generates new private key and CSR
3. Issues new certificate from CA Issuer
4. Updates Secret in Hub cluster (`argocd-agent-spokepool-01-tls`)
5. **Problem**: Spoke Pool cluster still has old certificate (ClusterResourceSet is immutable)

### 5.2 Certificate Distribution to Spokes

**Phase 1 Approach** (Manual Rotation):
- Certificates valid for 90 days, renewed at 83 days
- Platform Admin manually updates Spoke Pool clusters when certificates rotate
- Acceptable for Phase 1 (< 50 cells, quarterly rotation)

**Phase 2 Approach** (Automated Rotation via External Secrets Operator):
- Deploy External Secrets Operator to Spoke Pool clusters
- Configure SecretStore pointing to Hub cluster (via kubeconfig)
- ExternalSecret syncs certificate from Hub Secret every 5 minutes
- ArgoCD Agent and NATS Leaf Node reload certificates on Secret update

**Phase 2 Configuration** (deferred):
```yaml
# Spoke Pool cluster
apiVersion: external-secrets.io/v1beta1
kind: SecretStore
metadata:
  name: hub-cert-store
  namespace: spoke-pool
spec:
  provider:
    kubernetes:
      remoteNamespace: hub-platform-ops
      server:
        url: https://api.nutgraf.in:6443  # Hub cluster API server
        caProvider:
          type: ConfigMap
          name: hub-ca
          key: ca.crt
      auth:
        serviceAccount:
          name: spoke-cert-sync

---
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: argocd-agent-tls
  namespace: spoke-pool
spec:
  refreshInterval: 5m
  secretStoreRef:
    name: hub-cert-store
    kind: SecretStore
  target:
    name: argocd-agent-tls
    creationPolicy: Owner
  data:
    - secretKey: tls.crt
      remoteRef:
        key: argocd-agent-spokepool-01-tls
        property: tls.crt
    - secretKey: tls.key
      remoteRef:
        key: argocd-agent-spokepool-01-tls
        property: tls.key
    - secretKey: ca.crt
      remoteRef:
        key: argocd-agent-spokepool-01-tls
        property: ca.crt
```

---

## 6. Hetzner DNS Webhook (Not Used for mTLS)

### 6.1 Purpose

The Hetzner DNS webhook enables DNS-01 challenges for **public HTTPS certificates** (e.g., Let's Encrypt for external ingress).

**Not Used in Phase 1**: Spoke Pool clusters use mTLS with self-signed CAs, not public certificates.

### 6.2 Future Use Case (Phase 2+)

When Spoke Pool clusters expose public HTTPS endpoints (e.g., tenant APIs), cert-manager can issue Let's Encrypt certificates using Hetzner DNS-01 challenges.

**Example** (deferred):
```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: tenant-api-tls
  namespace: tenant-acme
spec:
  secretName: tenant-api-tls
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
  dnsNames:
    - api.nutgraf.in  # Hub domain
  # Uses Hetzner DNS webhook for DNS-01 challenge
```

---

## 7. Observability

### 7.1 Certificate Metrics

cert-manager exposes Prometheus metrics for certificate lifecycle:

**Key Metrics**:
- `certmanager_certificate_expiration_timestamp_seconds`: Certificate expiry time
- `certmanager_certificate_ready_status`: Certificate Ready status (0=False, 1=True)
- `certmanager_certificate_renewal_timestamp_seconds`: Last renewal time

**Grafana Alerts**:
```yaml
# Alert when certificate expires in < 14 days (double the renewal window)
- alert: CertificateExpiringSoon
  expr: (certmanager_certificate_expiration_timestamp_seconds - time()) < (14 * 24 * 3600)
  labels:
    severity: warning
  annotations:
    summary: "Certificate {{ $labels.name }} expires in < 14 days"

# Alert when certificate renewal fails
- alert: CertificateRenewalFailed
  expr: certmanager_certificate_ready_status == 0
  for: 1h
  labels:
    severity: critical
  annotations:
    summary: "Certificate {{ $labels.name }} renewal failed"
```

### 7.2 Certificate Validation

**Pre-Provisioning Checks**:
```bash
# Verify certificate exists and is valid
kubectl get certificate -n hub-platform argocd-agent-spokepool-01 \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'

# Verify Secret contains all required keys
kubectl get secret -n hub-platform argocd-agent-spokepool-01-tls \
  -o jsonpath='{.data}' | jq 'keys'
# Expected: ["ca.crt", "tls.crt", "tls.key"]

# Verify certificate CN matches agent name
kubectl get secret -n hub-platform argocd-agent-spokepool-01-tls \
  -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -noout -subject
# Expected: subject=CN=argocd-agent-spokepool-01
```

---

## 8. Spec Alignment

### 8.1 Functional Requirements

| Requirement | Implementation | Validation |
|-------------|----------------|------------|
| FR-1.2 | ClusterResourceSet contains mTLS cert Secret | kubectl get clusterresourceset -o yaml |
| FR-2.3 | NATS Leaf Node mTLS certificate | kubectl get certificate nats-leaf-spokepool-01 |
| AC-1 | Composition patches pre-generated certificate | Crossplane function-go-templating |
| AC-3 | ClusterResourceSet contains 5 resources | 2 cert Secrets + 2 CA Secrets + 1 ConfigMap |

### 8.2 Non-Functional Requirements

| Requirement | Implementation | Validation |
|-------------|----------------|------------|
| NFR-4.1 | All Hub-Spoke communication uses mTLS | ArgoCD Agent + NATS use client certificates |
| NFR-4.2 | Certificates auto-rotate 7 days before expiration | cert-manager renewBefore: 168h |
| NFR-1.1 | Cell provisioning < 15 minutes | Certificate generation < 30 seconds |

---

## 9. Testing Strategy

### 9.1 Unit Tests (cert-manager webhook)

**Existing Tests**: `archived/hetzner/cert-manager-webhook-hetzner/internal/hetzner/solver_test.go`

**Coverage**:
- DNS-01 challenge Present/CleanUp
- Hetzner API error handling
- Zone RRSet record creation

**Not Needed for Phase 1**: Hetzner webhook only used for public certificates (deferred).

### 9.2 Integration Tests (Certificate Generation)

**Test Scenario 1**: Certificate CR → Secret Creation
```bash
# Apply Certificate CR
kubectl apply -f - <<EOF
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: test-argocd-agent
  namespace: hub-platform
spec:
  secretName: test-argocd-agent-tls
  duration: 2160h
  renewBefore: 168h
  commonName: test-argocd-agent
  usages:
    - client auth
  issuerRef:
    name: argocd-ca-issuer
    kind: Issuer
EOF

# Wait for Ready status
kubectl wait --for=condition=Ready certificate/test-argocd-agent -n hub-platform --timeout=60s

# Verify Secret exists
kubectl get secret -n hub-platform test-argocd-agent-tls -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -noout -text
```

**Test Scenario 2**: Certificate Renewal
```bash
# Force renewal by deleting Secret
kubectl delete secret -n hub-platform test-argocd-agent-tls

# cert-manager should recreate Secret within 30 seconds
kubectl wait --for=condition=Ready certificate/test-argocd-agent -n hub-platform --timeout=60s
```

### 9.3 E2E Tests (SpokePool Provisioning)

**Test Scenario**: End-to-End Certificate Injection
```bash
# 1. Apply SpokePool XR
kubectl apply -f spokepool-01.yaml

# 2. Verify certificates generated
kubectl get certificate -n hub-platform argocd-agent-spokepool-01 -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
# Expected: True

# 3. Verify ClusterResourceSet references certificates
kubectl get clusterresourceset spokepool-01-addons -n hub-platform-capi -o yaml | grep -A5 resources
# Expected: argocd-agent-spokepool-01-tls, nats-leaf-spokepool-01-tls

# 4. Wait for CAPI Cluster Ready
kubectl wait --for=condition=Ready cluster/spokepool-01 -n hub-platform-capi --timeout=20m

# 5. Verify certificates injected into Spoke Pool
kubectl --context spokepool-01 get secret -n spoke-pool argocd-agent-tls -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -noout -subject
# Expected: subject=CN=argocd-agent-spokepool-01
```

---

## 10. Troubleshooting

### 10.1 Certificate Not Generated

**Symptom**: Certificate CR stuck in `Pending` state

**Diagnosis**:
```bash
kubectl describe certificate -n hub-platform argocd-agent-spokepool-01
# Check Events for errors
```

**Common Causes**:
- Issuer not found (`argocd-ca-issuer` missing)
- CA Secret not found (`argocd-ca-secret` missing)
- cert-manager controller not running

**Resolution**:
```bash
# Verify Issuer exists
kubectl get issuer -n hub-platform argocd-ca-issuer

# Verify CA Secret exists
kubectl get secret -n hub-platform argocd-ca-secret

# Check cert-manager logs
kubectl logs -n cert-manager -l app=cert-manager
```

### 10.2 ClusterResourceSet Missing Certificate

**Symptom**: ClusterResourceSet does not reference certificate Secret

**Diagnosis**:
```bash
kubectl get clusterresourceset spokepool-01-addons -n hub-platform-capi -o yaml
# Check resources[] array
```

**Common Causes**:
- Crossplane Composition did not wait for Certificate Ready
- function-auto-ready not configured
- Certificate Secret name mismatch

**Resolution**:
- Verify Composition pipeline includes `function-auto-ready` step
- Verify Secret name matches ClusterResourceSet reference

### 10.3 Certificate Expired in Spoke Pool

**Symptom**: ArgoCD Agent or NATS Leaf Node fails with "certificate expired" error

**Diagnosis**:
```bash
kubectl --context spokepool-01 get secret -n spoke-pool argocd-agent-tls -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -noout -dates
# Check notAfter date
```

**Common Causes**:
- Certificate rotated in Hub but not synced to Spoke
- External Secrets Operator not deployed (Phase 2)

**Resolution** (Phase 1):
- Manually update Spoke Pool cluster with new certificate from Hub
- Restart ArgoCD Agent and NATS Leaf Node pods

---

## 11. Design Patterns (sbt-patterns Alignment)

### 11.1 GitOps-First Principle

**Pattern**: Infrastructure as Code with declarative certificate management

**Implementation**:
- Certificate CRs stored in Git (`xrds/certificates/`)
- Crossplane Composition generates Certificate CRs from SpokePool XR
- No manual `kubectl apply` for certificates

### 11.2 Secret Zero Pattern

**Pattern**: Minimal bootstrap credentials injected at cluster creation

**Implementation**:
- mTLS certificates are the ONLY secrets needed for bootstrap
- Everything else (CNPG, PostgREST, etc.) deployed via ArgoCD after agent connects
- Reduces attack surface (no long-lived tokens in ClusterResourceSet)

### 11.3 Defense in Depth

**Pattern**: Separate CAs for different services

**Implementation**:
- ArgoCD CA: Only for ArgoCD Agent mTLS
- NATS CA: Only for NATS Leaf Node mTLS
- Prevents certificate misuse across services

---

## 12. Dependencies

### 12.1 Upstream Dependencies

- **cert-manager**: v1.13+ (already deployed in Hub)
- **cert-manager-webhook-hetzner**: v0.6.7+ (for DNS-01, Phase 2+)
- **Crossplane**: v1.14+ (for Composition orchestration)
- **function-auto-ready**: Crossplane function for Certificate readiness

### 12.2 Downstream Dependencies

- **ArgoCD Agent**: Consumes mTLS certificate for Hub connection
- **NATS Leaf Node**: Consumes mTLS certificate for Hub connection
- **ClusterResourceSet**: Injects certificates into Spoke Pool at bootstrap

### 12.3 Integration Sequence

1. ✅ **CAPI + CAPH Integration** (cluster provisioning)
2. ✅ **Kyverno Integration** (cluster discovery)
3. ✅ **ArgoCD Agent Integration** (edge catalog deployment)
4. ✅ **Atlas Integration** (schema provisioning)
5. ✅ **PostgREST + AgentGateway Integration** (tenant API)
6. ✅ **Crossplane Integration** (SpokePool XR orchestration)
7. ✅ **CNPG Integration** (shared database)
8. ✅ **NATS Leaf Node Integration** (event forwarding)
9. ✅ **Grafana Alloy Integration** (metrics collection)
10. 🆕 **cert-manager Integration** (this document)

---

## 13. Implementation Checklist

### 13.1 Hub Cluster Setup

- [ ] cert-manager deployed in `cert-manager` namespace
- [ ] cert-manager-webhook-hetzner deployed (for Phase 2+)
- [ ] Self-signed root CA generated: `argocd-ca-secret`
- [ ] Self-signed root CA generated: `nats-ca-secret`
- [ ] Issuer created: `argocd-ca-issuer`
- [ ] Issuer created: `nats-ca-issuer`
- [ ] Prometheus ServiceMonitor configured for cert-manager metrics
- [ ] Grafana alerts configured for certificate expiration

### 13.2 Crossplane Composition

- [ ] SpokePool Composition includes certificate generation step
- [ ] function-auto-ready configured to wait for Certificate Ready
- [ ] ClusterResourceSet references certificate Secrets
- [ ] Certificate naming convention: `argocd-agent-<cell-id>`, `nats-leaf-<cell-id>`

### 13.3 Testing

- [ ] Unit tests: Certificate CR → Secret creation
- [ ] Integration test: Certificate renewal
- [ ] E2E test: SpokePool XR → Certificate → ClusterResourceSet → Spoke injection
- [ ] Validation: ArgoCD Agent connects using mTLS certificate
- [ ] Validation: NATS Leaf Node connects using mTLS certificate

---

**Document Version**: 1.0  
**Last Updated**: 2026-04-09  
**Next Review**: After Phase 1 implementation

