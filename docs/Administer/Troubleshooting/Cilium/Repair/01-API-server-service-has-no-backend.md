# Repair: Cilium's API server Service has no backend

Pods on a cluster cannot reach the Kubernetes API server through its Service
(`10.96.0.1:443`), although the API server itself is healthy. Controllers
crash-loop one after another, and the cluster does not recover by itself.

First seen on the hub, 2026-10-28, during the 0.1.16-rc.177 sync.

## Symptoms

The symptom appears in the controllers, not in Cilium:

| You see | It means |
|---|---|
| `dial tcp 10.96.0.1:443: connect: operation not permitted` in a pod's log | Cilium's socket load-balancer has **no backend** for the API server Service. It is not a NetworkPolicy denial: a denial is a silent drop, a hang. |
| `cilium-operator`, `ccm-*`, `cloudnative-pg`, `capi-*`, `kyverno-*` and other operators in `CrashLoopBackOff` | they all fail their first API call at start |
| Argo CD `ComparisonError: … failed calling webhook …` on unrelated Applications | the admission webhooks (CNPG, CAPI, Kyverno) are down, so server-side dry-runs fail |
| `kubectl get --raw /readyz` from your machine returns `ok` | the API server is fine; only the in-cluster Service path is broken |

## Cause

The cluster runs Cilium with `kube-proxy-replacement: "true"`, so Cilium itself
serves `10.96.0.1`. When `cilium-config` has no `k8s-service-host`, each Cilium
agent reaches the API server **through that same Service**.

If the API server's endpoint is withdrawn even briefly (the API server fails or
is restarted), Cilium syncs the empty backend set into its map. From then on the
agent cannot reach the API server to learn the endpoint is back: the Kubernetes
`EndpointSlice` lists a ready API server, Cilium's map stays empty, and every pod
using `10.96.0.1` gets `operation not permitted`. This is a deadlock, not a
transient.

Cilium's documentation requires `k8sServiceHost` and `k8sServicePort` when kube-proxy
replacement is on (docs.cilium.io, "Kubernetes Without kube-proxy"). Spokes get
them: hub-operator renders each spoke's `cilium-config` with its control-plane
endpoint. The hub did not: its `cilium-config` is written by the CLI at bootstrap,
which assumed the hub keeps kube-proxy. It does not, and ADR-046 §24.6 withdrew that
assumption — the CLI now injects the hub's endpoint at Day-0 as well. A hub
bootstrapped before that change still needs the repair below.

### The 2026-10-28 trigger

| UTC | Evidence |
|---|---|
| 16:32:59 | etcd: `apply request took too long` (~500ms, expected 100ms) |
| 16:38:19 | Cilium agent: the API server reports `[-]etcd failed` |
| 16:41–16:46 | Cilium's Service watch drops: `http2: client connection lost` |
| 16:47:39 | first `connect: operation not permitted` to `10.96.0.1:443`, on both nodes |
| 16:48:05 | the API server is killed (exit 137); the node records `SystemOOM`, victim Argo CD's application controller |

The rc.177 sync applied 26 large CRDs (~2 MB, server-side) and a new stack at once.
etcd, the API server and Argo CD's application controller (no memory limit) were
overloaded together on the 7.6 GiB control-plane node. The overload withdrew the
endpoint; the missing `k8s-service-host` made it permanent.

## Diagnose (read-only)

Point `KUBECONFIG` at the affected cluster.

1. The API server is healthy from outside:
   ```
   kubectl get --raw /readyz
   ```
2. Cilium has no backend for it, on every node:
   ```
   for p in $(kubectl -n kube-system get pods -l k8s-app=cilium -o name); do
     kubectl -n kube-system exec $p -c cilium-agent -- cilium-dbg service list | grep 10.96.0.1
   done
   ```
   The broken state shows the frontend `10.96.0.1:443/TCP ClusterIP` with an empty
   backend column.
3. Kubernetes does have one:
   ```
   kubectl -n default get endpointslices -l kubernetes.io/service-name=kubernetes
   ```
4. Cilium is configured to reach the API server only through the Service:
   ```
   kubectl -n kube-system get cm cilium-config -o jsonpath='{.data.kube-proxy-replacement} {.data.k8s-service-host}{"\n"}'
   ```
   `true` with no `k8s-service-host` is this fault.
5. The control-plane node is not still short of memory (repair restarts agents):
   ```
   kubectl get --raw /api/v1/nodes/<control-plane-node>/proxy/stats/summary
   ```
   `node.memory.availableBytes` should be at least ~1.5 GiB. If it is lower, find
   what is consuming it first.

## Repair

A few seconds of pod-network disruption per node while its agent restarts. Nothing
is deleted.

1. Give the agents the API server's address directly: the control-plane endpoint,
   the same `server` your kubeconfig uses (the hub's is `65.109.41.89:6443`).
   ```
   kubectl -n kube-system patch cm cilium-config --type merge \
     -p '{"data":{"k8s-service-host":"<host>","k8s-service-port":"<port>"}}'
   ```
2. Restart the agents and the operator:
   ```
   kubectl -n kube-system rollout restart ds/cilium deploy/cilium-operator
   kubectl -n kube-system rollout status ds/cilium --timeout=5m
   kubectl -n kube-system rollout status deploy/cilium-operator --timeout=5m
   ```

## Verify

1. Every agent is healthy and has the backend:
   ```
   kubectl -n kube-system exec <agent> -c cilium-agent -- cilium-dbg status --brief
   kubectl -n kube-system exec <agent> -c cilium-agent -- cilium-dbg service list | grep -A2 10.96.0.1
   ```
   Expected: `OK`, and `10.96.0.1:443/TCP ClusterIP 1 => <api-ip>:6443/TCP (active)`.
2. The crash-looping controllers become Ready within about 10 minutes. One that
   stays in `CrashLoopBackOff` can be deleted once to start on a working connection;
   if it still fails, read its last error. It is a different fault.
3. Re-run the Argo CD sync that failed (`scripts/argocd-hard-sync.sh --promote
   <version>`). Do not re-publish.

## Prevention

- **Every cluster running kube-proxy replacement has `k8s-service-host`.** Spokes get
  it from hub-operator; the hub gets it from the CLI at Day-0 (ADR-046 §24.6). The CLI
  reads `Cluster.spec.controlPlaneEndpoint` after the Cluster exists and stops the
  bootstrap if it never appears. Two checks hold the rule: `validate/preflight/046-…`
  asserts the render, and `validate/cluster/046-…`
  (`validate_hub_cilium_apiserver_endpoint`) asserts the hub's live ConfigMap against
  its own endpoint. A hub bootstrapped before that change keeps the patched value
  until it is rebuilt.
- **Argo CD's application controller has a memory limit**, so a sync-time spike kills
  that container and not the API server beside it
  (`manifests/argocd/components/01/platform-argocd.yaml`).
- **Large CRD sets should not land in one sync** with a whole new stack (pending:
  an earlier wave for CRDs, or only the CRDs in use).

## Related

- `docs/runbooks/redis-aof-corruption.md` and
  `docs/runbooks/spoke-database-loss-and-recovery.md`: the same `EPERM` reading
  (Cilium socket LB, no backend), with a backend that is really gone.
- ADR-046 §24: why a spoke's `cilium-config` is rendered by hub-operator.
