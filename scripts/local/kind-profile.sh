#!/usr/bin/env bash
# Local Kind profile for ADR-107: the sandboxed runtime on every Kind node, so
# Isolation: Sandboxed jobs and session peers (renderers) run isolated locally,
# exactly as in dev.
#
#   scripts/local/kind-profile.sh            uses the current kubectl context
#   KUBECONFIG=<file> scripts/local/kind-profile.sh
#
# Idempotent. Fails, naming the step, if the runtime cannot be installed or a
# sandboxed pod cannot run -- it never leaves a cluster that claims the runtime
# and cannot provide it.
#
# The operator for this profile is manifests/local/kind/operator (config/default
# with --network-policy=none); apply that instead of config/default.
set -euo pipefail

ZERO_OPS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LABEL="sandbox.zero-ops.io/runtime=sandboxed"
log() { echo "[kind-profile] $*"; }
fail() { echo "[kind-profile] FAILED: $*" >&2; exit 1; }

ctx="$(kubectl config current-context)"
[[ "$ctx" == kind-* ]] || fail "current context '$ctx' is not a Kind cluster; this profile is for local Kind only"

log "1/4 sandboxed runtime component"
kubectl kustomize "$ZERO_OPS_DIR/manifests/local/kind/runtime" | kubectl apply -f -

log "2/4 offering the runtime on every Kind node ($LABEL)"
kubectl label nodes --all "$LABEL" --overwrite >/dev/null

log "3/4 waiting for the installer on every node"
if ! kubectl -n kube-system rollout status ds/sandboxed-runtime-installer --timeout=300s; then
    kubectl -n kube-system logs -l app.kubernetes.io/name=sandboxed-runtime-installer -c install --tail=40 >&2 || true
    fail "the runtime installer did not become Ready (its log is above)"
fi

log "4/4 a sandboxed pod runs under the user-space kernel"
probe=adr107-runtime-check
kubectl -n kube-system delete pod "$probe" --ignore-not-found --wait >/dev/null
kubectl -n kube-system apply -f - >/dev/null <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: $probe
spec:
  runtimeClassName: sandboxed
  restartPolicy: Never
  containers:
    - name: check
      image: busybox:1.37.0
      command: ["dmesg"]
      resources:
        limits: {cpu: 100m, memory: 64Mi}
YAML
if ! kubectl -n kube-system wait pod/"$probe" --for=jsonpath='{.status.phase}'=Succeeded --timeout=120s >/dev/null; then
    kubectl -n kube-system describe pod "$probe" | tail -20 >&2 || true
    kubectl -n kube-system delete pod "$probe" --ignore-not-found >/dev/null
    fail "a pod under RuntimeClass sandboxed did not complete (events above)"
fi
out="$(kubectl -n kube-system logs "$probe")"
kubectl -n kube-system delete pod "$probe" --ignore-not-found >/dev/null
grep -qi "gvisor" <<< "$out" || fail "the sandboxed pod ran, but not under gVisor: dmesg shows no gVisor banner"
log "ready: RuntimeClass sandboxed is served by gVisor on: $(kubectl get nodes -l "$LABEL" -o jsonpath='{.items[*].metadata.name}')"
