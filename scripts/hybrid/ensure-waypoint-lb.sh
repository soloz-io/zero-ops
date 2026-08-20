#!/usr/bin/env bash
# Idempotently provision the Waypoint public Hetzner Load Balancer (ADR-046
# addendum: hostNetwork gateway topology).
#
# Background: with gatewayAPI.hostNetwork.enabled=true, Cilium exposes the
# Gateway listeners directly on the control-plane node's host network
# (0.0.0.0:80/443 served by the embedded Envoy). The Gateway's generated
# Service becomes ClusterIP, so the Hetzner CCM can no longer manage a
# LoadBalancer for it. This script therefore provisions the LB directly via
# the hcloud API:
#
#   LB (lb11, hel1) 80/443, proxyprotocol enabled
#     -> target: spoke control-plane server, PRIVATE network (10.0.0.0/24)
#        port 80/443, HTTP health checks with relaxed timeouts
#
# The private-network path is used because the embedded Envoy binds IPv4-only;
# Hetzner probes the public IPv6 address otherwise and marks targets unhealthy.
#
# Requires:
#   - hcloud CLI with a token for the spoke's project (HCLOUD_TOKEN from the
#     spoke's kube-system/hetzner secret)
#   - kubectl access to the spoke (KUBECONFIG) to discover the CP node IP
#     (script falls back to $CP_PUBLIC_IP if kubectl is unavailable)
#
# Usage:
#   ensure-waypoint-lb.sh [lb-name] [network-id] [private-lb-ip]
set -euo pipefail

LB_NAME="${1:-waypoint-gateway-lb}"
NETWORK_ID="${2:-}"
if [ -z "$NETWORK_ID" ]; then
  NETWORK_ID="$(hcloud network list -o noheader -o columns=id,name 2>/dev/null | grep "spoke-pool-" | head -1 | awk '{print $1}')"
fi
LB_PRIVATE_IP="${3:-10.0.0.5}"
HC_PATH="/"
HC_DOMAIN="waypoint.nutgraf.in"
HC_CODES="2xx,3xx,404"
HC_TIMEOUT="10s"
HC_INTERVAL="15s"
HC_RETRIES="3"
# Health checks MUST be TCP: the embedded Envoy's hostNetwork listeners carry
# the proxy_protocol listener filter (gateway-api proxy protocol is required
# for the LB's PROXY-protocol traffic), so plain HTTP probes are reset
# ("connection reset by peer") and every HTTP health check fails. TCP checks
# only need the handshake, which succeeds through the filter.

# Discover the control-plane server by node name or ExternalIP.
CP_NODE_NAME="${CP_NODE_NAME:-$(kubectl get nodes -l node-role.kubernetes.io/control-plane -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)}"
if [ -n "$CP_NODE_NAME" ]; then
  SERVER_ID="$(hcloud server list -o noheader -o columns=id,name 2>/dev/null | awk -v name="$CP_NODE_NAME" '$2==name {print $1}' | head -1)"
fi

if [ -z "${SERVER_ID:-}" ]; then
  CP_PUBLIC_IP="${CP_PUBLIC_IP:-$(kubectl get nodes -l node-role.kubernetes.io/control-plane -o jsonpath='{.items[0].status.addresses[?(@.type=="ExternalIP")].address}' 2>/dev/null || true)}"
  if [ -n "$CP_PUBLIC_IP" ]; then
    SERVER_ID="$(hcloud server list -o noheader -o columns=id,ipv4 2>/dev/null | awk -v ip="$CP_PUBLIC_IP" '$2==ip {print $1}' | head -1)"
  fi
fi

if [ -z "${SERVER_ID:-}" ]; then
  echo "ERROR: cannot determine control-plane server in hcloud" >&2
  exit 1
fi

echo "Using server $SERVER_ID as LB target"

# Create the LB if missing.
if ! hcloud load-balancer describe "$LB_NAME" >/dev/null 2>&1; then
  hcloud load-balancer create --name "$LB_NAME" --type lb11 --location hel1 >/dev/null
  echo "Created load balancer $LB_NAME"
fi

# Attach to the spoke private network (idempotent).
if ! hcloud load-balancer describe "$LB_NAME" -o json | grep -q "\"network\": $NETWORK_ID"; then
  hcloud load-balancer attach-to-network "$LB_NAME" --network "$NETWORK_ID" --ip "$LB_PRIVATE_IP" >/dev/null || true
  echo "Attached $LB_NAME to network $NETWORK_ID at $LB_PRIVATE_IP"
fi

# Ensure the 80/443 TCP-proxy services with PROXY protocol and TCP health
# checks (idempotent: update-service overwrites the health check settings).
for PORT in 80 443; do
  if ! hcloud load-balancer describe "$LB_NAME" -o json | grep -q "\"listen_port\": $PORT"; then
    hcloud load-balancer add-service "$LB_NAME" \
      --protocol tcp --listen-port "$PORT" --destination-port "$PORT" \
      --proxy-protocol=true >/dev/null
  fi
  hcloud load-balancer update-service "$LB_NAME" --listen-port "$PORT" \
    --health-check-protocol tcp --health-check-port "$PORT" \
    --health-check-interval "$HC_INTERVAL" --health-check-retries "$HC_RETRIES" \
    --health-check-timeout "$HC_TIMEOUT" >/dev/null
  echo "Service $PORT -> $PORT (proxyprotocol, TCP health check) ensured"
done

# Ensure the private-IP server target (re-add to flip use_private_ip if it
# changed; remove-then-add keeps the CLI simple and idempotent).
CUR="$(hcloud load-balancer describe "$LB_NAME" -o json | python3 -c "
import json,sys
d=json.load(sys.stdin)
for t in d['targets']:
    if t['server']['id']==$SERVER_ID:
        print('private' if t['use_private_ip'] else 'public')
" 2>/dev/null || true)"
if [ "$CUR" != "private" ]; then
  hcloud load-balancer remove-target "$LB_NAME" --server "$SERVER_ID" >/dev/null 2>&1 || true
  hcloud load-balancer add-target "$LB_NAME" --server "$SERVER_ID" --use-private-ip=true >/dev/null
  echo "Target server $SERVER_ID ensured (use_private_ip=true)"
fi

echo "LB $LB_NAME: public IPv4 $(hcloud load-balancer describe "$LB_NAME" -o json | python3 -c "import json,sys;print(json.load(sys.stdin)['public_net']['ipv4']['ip'])")"
echo "Note: A record waypoint.nutgraf.in / api.waypoint.nutgraf.in must point at this IP (Hetzner Cloud DNS)."
