# Security Catalog

This directory contains security-related manifests for the Zero-Ops platform.

## ArgoCD Agent mTLS Certificates

**File**: `argocd-agent-cert.yaml`

Provides mTLS certificates for ArgoCD Agent communication between Hub and Spoke Pool clusters.

### Components

1. **ClusterIssuer** (`argocd-agent-ca-issuer`) - Self-signed CA issuer
2. **CA Certificate** (`argocd-agent-ca`) - Root CA for ArgoCD Agent (10-year validity)
3. **Issuer** (`argocd-agent-issuer`) - Issues client certificates using the CA
4. **Client Certificate Template** (`argocd-agent-client-template`) - Template for per-cluster certificates

### Certificate Lifecycle

- **Duration**: 90 days
- **Renewal**: Auto-renew 7 days before expiration (NFR-4.2)
- **Usage**: Client authentication, digital signature, key encipherment

### Integration

The Composition will reference this template to generate unique certificates for each Spoke Pool cluster during provisioning.

### Requirements

- FR-1.2: Automated Cluster Bootstrap
- NFR-4.1: mTLS authentication
- NFR-4.2: Auto-rotation 7 days before expiration
- AC-3: ArgoCD Agent Bootstrap

## Related Files

- NATS certificates: `catalog/messaging/nats-leaf-cert.yaml`
- ClusterResourceSet: `xrds/compositions/spokepool-clusterresourceset.yaml`
