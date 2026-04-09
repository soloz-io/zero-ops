# Messaging Catalog

This directory contains messaging-related manifests for the Zero-Ops platform.

## NATS Leaf Node mTLS Certificates

**File**: `nats-leaf-cert.yaml`

Provides mTLS certificates for NATS Leaf Node communication between Spoke Pool clusters and Hub JetStream.

### Components

1. **ClusterIssuer** (`nats-leaf-ca-issuer`) - Self-signed CA issuer (separate from ArgoCD CA)
2. **CA Certificate** (`nats-leaf-ca`) - Root CA for NATS Leaf Nodes (10-year validity)
3. **Issuer** (`nats-leaf-issuer`) - Issues client certificates using the CA
4. **Client Certificate Template** (`nats-leaf-client-template`) - Template for per-cluster certificates

### Certificate Lifecycle

- **Duration**: 90 days
- **Renewal**: Auto-renew 7 days before expiration (NFR-4.2)
- **Usage**: Client auth, server auth, digital signature, key encipherment
- **DNS Names**: nats-leaf-node, nats-leaf-node.spoke-pool-system.svc.cluster.local

### Defense in Depth

NATS uses a separate CA from ArgoCD to ensure that compromise of one system doesn't affect the other.

### Integration

The Composition will reference this template to generate unique certificates for each Spoke Pool cluster's NATS Leaf Node.

### Requirements

- FR-2.3: NATS Leaf Node
- NFR-4.1: mTLS authentication
- NFR-4.2: Auto-rotation 7 days before expiration
- NFR-3.3: Event buffering during Hub unavailability

## Related Files

- ArgoCD certificates: `catalog/security/argocd-agent-cert.yaml`
- NATS Leaf Node deployment: `edge-catalog/nats-leaf-node.yaml` (to be created)
