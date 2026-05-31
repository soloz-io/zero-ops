### 1. ADR Documentation Update

**File: `docs/adr/035-enterprise-pki-and-delegated-trust-via-infisical-oss.md`**
# Decision

## Trust Hierarchy
The platform implements a three-tier trust hierarchy:
1. Offline Root CA
2. Fleet Intermediate CA
3. Leaf Certificates

### Offline Root CA
The Offline Root CA is generated outside Infisical, stored offline, never connected to production systems, and signs the Fleet Intermediate CA. The Offline Root private key is never stored in Kubernetes.

### Fleet Intermediate CA
The Fleet Intermediate CA is hosted within Infisical PKI, is the sole issuing authority for platform certificates, and signs all Spoke-issued certificates. The Fleet Intermediate private key never leaves Infisical.

### Leaf Certificates
Leaf certificates are issued through cert-manager, infisical-issuer, and Infisical PKI. All certificates chain through: Offline Root CA -> Fleet Intermediate CA -> Leaf Certificate.

# Certificate Issuance Model
Each Spoke cluster deploys cert-manager and infisical-issuer. Leaf certificates are requested locally. The Fleet Intermediate CA signs the certificate. The private key is generated and stored locally in the Spoke cluster. Crossplane is never involved.

# Bootstrap Exception
ADR-025 requires deterministic cluster bootstrap. The ArgoCD Agent must establish an mTLS connection before GitOps becomes available.

## Bootstrap Certificate
The Hub Operator SHALL issue a temporary bootstrap certificate.
Characteristics:
* TTL: 72 hours
* Single purpose
* ArgoCD Agent only
* Delivered through ClusterResourceSet
* Replaced after GitOps initialization

# Trust Distribution
The Offline Root CA public certificate is injected during Hub bootstrap. No per-Spoke trust bundles exist. No Intermediate CA bundles are distributed.

# Hub Operator Responsibilities
The Hub Operator participates only in Day-0 bootstrap to create the Machine Identity, generate the bootstrap certificate, and store bootstrap artifacts in ClusterResourceSet payloads. The Hub Operator SHALL NOT rotate, renew, reconcile, or distribute leaf certificates.

# Crossplane Responsibilities
Crossplane SHALL NOT generate, copy, read, or traverse certificate or private key material.

# Identity Separation
Infrastructure identity (cert-manager, infisical-issuer) and workload identity (SPIRE) remain separated. SPIRE SHALL NOT be used for infrastructure certificates.

# PKI Profiles
Certificate validity is governed centrally.
| Profile | TTL |
|---|---|
| Infrastructure Services | 24h |
| Database Clients | 4h |
| Service Mesh Components | 1h |
| Human Access | 15m |
| Bootstrap Certificate | 72h |

# Revocation Strategy
The platform does not implement CRLs or OCSP. Compromise mitigation relies on short certificate TTLs, Machine Identity rotation, Fleet Intermediate replacement, and Trust anchor replacement when required.

# Infisical OSS Authorization Limitation
Infisical OSS does not provide Certificate Authority–scoped authorization controls for Machine Identities. PKI permissions are effectively project-scoped.
Accordingly, the platform adopts a single Fleet Intermediate CA model and does not rely on Intermediate CA separation as a security boundary.
Security isolation is enforced through:
- Machine Identity protection
- Short-lived certificates
- Audit logging
- SPIRE workload identities
- Kubernetes namespace and cluster boundaries
rather than delegated Intermediate CA ownership.
```

### 2. Delete Crossplane Secret Traversal Anti-Pattern

Execute the following commands to eradicate the Crossplane function:
```bash
rm -rf operators/function-cert-distribution/
rm manifests/hub-core-services/crossplane/providers/function/function-cert-distribution.yaml
```

**File: `manifests/hub-core-services/crossplane/providers/kustomization.yaml`**
```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - provider-kubernetes/provider.yaml
  - function/function.yaml
```

**File: `manifests/hub-core-services/crossplane/tenant-platform/compositions/spokepool-hetzner.yaml`**
```yaml
# DELETE the entire `- step: distribute-certificates` pipeline block.
# DELETE the following base objects from the `- step: patch-and-transform` block:
# - name: argocd-agent-client-cert
# - name: alloy-client-cert
# - name: nats-leafnode-client-cert
# - name: alloy-client-cert-distribution
# - name: nats-leafnode-cert-distribution
# - name: argocd-agent-cert-distribution
# - name: infisical-auth-distribution

# UPDATE the `cluster-resource-set` resource to dynamically map the separate CRS components:
          - name: cluster-resource-set
            base:
              apiVersion: kubernetes.crossplane.io/v1alpha2
              kind: Object
              spec:
                managementPolicies: ["*"]
                forProvider:
                  manifest:
                    apiVersion: addons.cluster.x-k8s.io/v1beta1
                    kind: ClusterResourceSet
                    metadata:
                      namespace: platform-capi
                    spec:
                      clusterSelector:
                        matchLabels:
                          spoke-type: pool
                      resources:
                        - name: offline-root-ca-crs
                          kind: Secret
                        - name: "" # Patched to <spokeName>-machine-identity-crs
                          kind: Secret
                        - name: "" # Patched to <spokeName>-bootstrap-cert-crs
                          kind: Secret
                        - name: cilium-addon-template
                          kind: Secret
                        - name: ccm-addon-template
                          kind: Secret
                        - name: csi-addon-template
                          kind: Secret
                        - name: argocd-crds-template
                          kind: Secret
                        - name: argocd-redis
                          kind: ConfigMap
                        - name: argocd-core-components
                          kind: ConfigMap
                        - name: argocd-agent-deployment
                          kind: ConfigMap
                        - name: "" # Patched to <spokeName>-argocd-agent-params
                          kind: ConfigMap
                        - name: argocd-agent-rbac
                          kind: ConfigMap
                      strategy: Reconcile
                providerConfigRef:
                  name: kubernetes-provider
            patches:
              - type: FromCompositeFieldPath
                fromFieldPath: metadata.name
                toFieldPath: metadata.name
                transforms:
                  - type: string
                    string:
                      type: Format
                      fmt: "%s-bootstrap"
              - type: FromCompositeFieldPath
                fromFieldPath: metadata.name
                toFieldPath: spec.forProvider.manifest.metadata.name
                transforms:
                  - type: string
                    string:
                      type: Format
                      fmt: "%s-bootstrap"
              - type: FromCompositeFieldPath
                fromFieldPath: metadata.name
                toFieldPath: spec.forProvider.manifest.spec.clusterSelector.matchLabels.cell-id
              - type: FromCompositeFieldPath
                fromFieldPath: metadata.name
                toFieldPath: spec.forProvider.manifest.spec.resources[1].name
                transforms:
                  - type: string
                    string:
                      type: Format
                      fmt: "%s-machine-identity-crs"
              - type: FromCompositeFieldPath
                fromFieldPath: metadata.name
                toFieldPath: spec.forProvider.manifest.spec.resources[2].name
                transforms:
                  - type: string
                    string:
                      type: Format
                      fmt: "%s-bootstrap-cert-crs"
              - type: FromCompositeFieldPath
                fromFieldPath: metadata.name
                toFieldPath: spec.forProvider.manifest.spec.resources[10].name
                transforms:
                  - type: string
                    string:
                      type: Format
                      fmt: "%s-argocd-agent-params"
```

**Files to DELETE:**
*   `manifests/hub-core-services/security/infisical-auth-crs-transform.yaml`
*   `manifests/hub-core-services/security/argocd-agent-cert.yaml`
*   `manifests/hub-core-services/security/nats-leafnode-cert.yaml`
*   `manifests/hub-core-services/security/observability-cert.yaml`

### 3. Static Root CA Distribution (Day-0)

**File: `manifests/platform-capi/offline-root-ca-crs.yaml`**
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: offline-root-ca-crs
  namespace: platform-capi
  labels:
    addons.cluster.x-k8s.io/resource-set: "true"
type: addons.cluster.x-k8s.io/resource-set
stringData:
  root-ca.yaml: |
    apiVersion: v1
    kind: Secret
    metadata:
      name: offline-root-ca
      namespace: argocd
      labels:
        platform.nutgraf.in/trust-anchor: "true"
    type: Opaque
    stringData:
      ca.crt: "PLACEHOLDER_REPLACED_DURING_HUB_BOOTSTRAP"
```

### 4. Configuration Updates

**File: `manifests/hub-core-services/hub-environment/hub-bootstrap-config.yaml`**
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: hub-bootstrap-config
  namespace: platform-ops
  annotations:
    argocd.argoproj.io/sync-wave: "0"
data:
  CLUSTER_ID: "hub-production"
  CLUSTER_REGION: "ap-south-1"
  INFISICAL_PROJECT_SLUG: "hub-platform"
  INFISICAL_ENVIRONMENT_SLUG: "dev"
  DOMAIN: "nutgraf.in"
  INFISICAL_PKI_PROFILE_ID: "PLACEHOLDER_REPLACED_DURING_HUB_BOOTSTRAP"
```

**File: `operators/hub-operator/config/manager/manager.yaml`**
```yaml
# Under the container env section:
        - name: INFISICAL_PKI_PROFILE_ID
          valueFrom:
            configMapKeyRef:
              name: hub-bootstrap-config
              key: INFISICAL_PKI_PROFILE_ID
```

### 5. Hub Operator Implementation

**File: `operators/hub-operator/internal/secrets/infisical.go`**
```go
package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type IssueCertRequest struct {
	ProfileId  string `json:"profileId"`
	CommonName string `json:"commonName"`
	TTL        string `json:"ttl"`
}

type IssueCertResponseWrapper struct {
	Certificate          IssueCertData `json:"certificate"`
	CertificateRequestId string        `json:"certificateRequestId"`
	Status               string        `json:"status"`
}

type IssueCertData struct {
	Certificate          string `json:"certificate"`
	IssuingCaCertificate string `json:"issuingCaCertificate"`
	CertificateChain     string `json:"certificateChain"`
	PrivateKey           string `json:"privateKey"`
	SerialNumber         string `json:"serialNumber"`
	CertificateId        string `json:"certificateId"`
}

// IssueBootstrapCertificate mints a 72-hour cert for ArgoCD Agent bootstrap.
func (c *InfisicalClient) IssueBootstrapCertificate(ctx context.Context, profileId, commonName string) (*IssueCertData, error) {
	reqBody := IssueCertRequest{
		ProfileId:  profileId,
		CommonName: commonName,
		TTL:        "72h",
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/api/v1/cert-manager/certificates", c.BaseURL),
		bytes.NewBuffer(payload))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to issue bootstrap cert: status %d", resp.StatusCode)
	}

	var result IssueCertResponseWrapper
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result.Certificate, nil
}
```

**File: `operators/hub-operator/internal/controller/spokepool_controller.go`**
```go
package controller

import (
	// ... standard imports ...
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Reconcile handles the creation of the Spoke's bootstrap payloads
func (r *SpokePoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// ... fetch SpokePool CR ...

	pkiProfileId := os.Getenv("INFISICAL_PKI_PROFILE_ID")
	if pkiProfileId == "" {
		return ctrl.Result{}, fmt.Errorf("INFISICAL_PKI_PROFILE_ID is not set")
	}

	// 1. Generate Machine Identity
	machineIdentity, err := r.Infisical.GetOrCreateSpokeMachineIdentity(ctx, spoke.Name)
	if err != nil {
		return ctrl.Result{}, err
	}

	// 2. Mint 72h Bootstrap Certificate (ADR-035 Exception)
	cert, err := r.Infisical.IssueBootstrapCertificate(ctx, pkiProfileId, spoke.Name)
	if err != nil {
		return ctrl.Result{}, err
	}

	// 3. Construct Separate CRS Secrets
	identityYAML := fmt.Sprintf(`
apiVersion: v1
kind: Secret
metadata:
  name: infisical-auth
  namespace: platform-ops
type: Opaque
stringData:
  client-id: %s
  client-secret: %s
`, machineIdentity.ClientID, machineIdentity.ClientSecret)

	certYAML := fmt.Sprintf(`
apiVersion: v1
kind: Secret
metadata:
  name: argocd-agent-client-cert
  namespace: argocd
  labels:
    platform.nutgraf.in/bootstrap: "true"
type: kubernetes.io/tls
stringData:
  tls.crt: |
%s
  tls.key: |
%s
`, indent(cert.Certificate), indent(cert.PrivateKey))

	identityCRS := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-machine-identity-crs", spoke.Name),
			Namespace: "platform-capi",
			Labels: map[string]string{
				"addons.cluster.x-k8s.io/resource-set": "true",
			},
		},
		Type: "addons.cluster.x-k8s.io/resource-set",
		StringData: map[string]string{
			"identity.yaml": identityYAML,
		},
	}

	certCRS := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-bootstrap-cert-crs", spoke.Name),
			Namespace: "platform-capi",
			Labels: map[string]string{
				"addons.cluster.x-k8s.io/resource-set": "true",
			},
		},
		Type: "addons.cluster.x-k8s.io/resource-set",
		StringData: map[string]string{
			"bootstrap-cert.yaml": certYAML,
		},
	}

	// Apply CRS Secrets to API Server...
	if err := r.Client.Create(ctx, identityCRS); client.IgnoreAlreadyExists(err) != nil {
		return ctrl.Result{}, err
	}
	if err := r.Client.Create(ctx, certCRS); client.IgnoreAlreadyExists(err) != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func indent(text string) string {
	// standard multiline indention logic
}
```

### 6. ArgoCD Agent Day-0 Configuration

**File: `manifests/spoke/spoke-bootstrap/argocd-agent-templates.yaml`**
```yaml
# Update the deployment arguments to map the root CA correctly
# ... inside deployment.yaml container spec ...
                - name: ARGOCD_AGENT_TLS_SECRET_NAME
                  value: "argocd-agent-client-cert"
                - name: ARGOCD_AGENT_TLS_ROOT_CA_PATH
                  value: "/etc/argocd/ca/ca.crt"
              volumeMounts:
                - name: tls-certs
                  mountPath: /etc/argocd/tls
                  readOnly: true
                - name: root-ca
                  mountPath: /etc/argocd/ca
                  readOnly: true
          volumes:
            - name: tls-certs
              secret:
                secretName: argocd-agent-client-cert
            - name: root-ca
              secret:
                secretName: offline-root-ca
```

### 7. Spoke-Local PKI Issuance (Day-1+)

**File: `manifests/spoke/spoke-catalog/infra/infisical-issuer.yaml`**
```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: infisical-issuer
  namespace: platform-ops
  annotations:
    argocd.argoproj.io/sync-wave: "2"
spec:
  project: platform-infrastructure
  source:
    repoURL: 'https://dl.cloudsmith.io/public/infisical/helm-charts/helm/charts/'
    chart: infisical-issuer
    targetRevision: 0.1.0
  destination:
    server: https://kubernetes.default.svc
    namespace: platform-ops
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
```

**File: `manifests/spoke/spoke-catalog/infra/cluster-issuer.yaml`**
```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: infisical-fleet-issuer
  annotations:
    argocd.argoproj.io/sync-wave: "3"
spec:
  infisical:
    url: https://infisical.nutgraf.in
    auth:
      universalAuth:
        credentialsRef:
          clientId:
            name: infisical-auth
            key: client-id
            namespace: platform-ops
          clientSecret:
            name: infisical-auth
            key: client-secret
            namespace: platform-ops
```

**File: `manifests/spoke/spoke-catalog/infra/certificates.yaml`**
```yaml
# Adopts the Day-0 bootstrap secret and handles continuous rotation
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: argocd-agent-client-cert
  namespace: argocd
  annotations:
    argocd.argoproj.io/sync-wave: "4"
spec:
  secretName: argocd-agent-client-cert
  duration: 24h
  renewBefore: 8h
  commonName: argocd-agent-client
  issuerRef:
    name: infisical-fleet-issuer
    kind: ClusterIssuer
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: alloy-client-cert
  namespace: platform-observability
  annotations:
    argocd.argoproj.io/sync-wave: "4"
spec:
  secretName: alloy-client-cert
  duration: 24h
  renewBefore: 8h
  commonName: alloy-client
  issuerRef:
    name: infisical-fleet-issuer
    kind: ClusterIssuer
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: nats-leafnode-client-cert
  namespace: platform-messaging
  annotations:
    argocd.argoproj.io/sync-wave: "4"
spec:
  secretName: nats-leafnode-client-cert
  duration: 24h
  renewBefore: 8h
  commonName: nats-leafnode-client
  issuerRef:
    name: infisical-fleet-issuer
    kind: ClusterIssuer
```

**File: `manifests/spoke/spoke-catalog/infra/kustomization.yaml`**
```yaml
resources:
  # ... existing ...
  - infisical-issuer.yaml
  - cluster-issuer.yaml
  - certificates.yaml
```