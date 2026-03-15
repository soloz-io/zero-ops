#!/bin/bash
# Bootstrap secrets for Demo 1 — run once before applying app-of-apps
# These are the ONLY imperative operations allowed (GitOps bootstrap exception)
set -e

export KUBECONFIG="$(pwd)/secrets/mothership.kubeconfig"

# Namespaces
kubectl create namespace cert-manager --dry-run=client -o yaml | kubectl apply -f -
kubectl create namespace kube-system --dry-run=client -o yaml | kubectl apply -f - 2>/dev/null || true
kubectl create namespace identity-services --dry-run=client -o yaml | kubectl apply -f -
kubectl create namespace zero-ops-system --dry-run=client -o yaml | kubectl apply -f -

# 1. Hetzner DNS token — used by cert-manager (DNS-01) and ExternalDNS
kubectl create secret generic hetzner-dns \
  --from-literal=api-key="${HETZNER_DNS_TOKEN}" \
  --namespace=cert-manager \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic hetzner-dns \
  --from-literal=api-key="${HETZNER_DNS_TOKEN}" \
  --namespace=kube-system \
  --dry-run=client -o yaml | kubectl apply -f -

# 2. Database passwords — used by Ory stack (Hydra, Kratos, Keto)
kubectl create secret generic identity-postgres-passwords \
  --from-literal=hydra-password="${HYDRA_DB_PASSWORD:-$(openssl rand -hex 32)}" \
  --from-literal=kratos-password="${KRATOS_DB_PASSWORD:-$(openssl rand -hex 32)}" \
  --from-literal=keto-password="${KETO_DB_PASSWORD:-$(openssl rand -hex 32)}" \
  --namespace=zero-ops-system \
  --dry-run=client -o yaml | kubectl apply -f -

# 3. GHCR pull secret — used by auth-proxy deployment
kubectl create secret docker-registry ghcr-pull-secret \
  --docker-server=ghcr.io \
  --docker-username="${GHCR_USERNAME}" \
  --docker-password="${GHCR_TOKEN}" \
  --namespace=identity-services \
  --dry-run=client -o yaml | kubectl apply -f -

# 4. ArgoCD repo credentials — used by ArgoCD to pull from private repo
kubectl create secret generic repo-soloz-io-zero-ops \
  --from-literal=type=git \
  --from-literal=url=https://github.com/soloz-io/zero-ops \
  --from-literal=username="${GHCR_USERNAME}" \
  --from-literal=password="${GHCR_TOKEN}" \
  --namespace=argocd \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl label secret repo-soloz-io-zero-ops -n argocd \
  argocd.argoproj.io/secret-type=repository --overwrite

echo ""
echo "✅ Bootstrap secrets created. Now apply the app-of-apps:"
echo "   kubectl apply -f manifests/argocd/app-of-apps.yaml"
