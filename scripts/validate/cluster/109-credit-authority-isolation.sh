#!/usr/bin/env bash
# ADR-109: the Credit Authority's isolation and guarantees, LIVE. The rendered
# half is preflight/109-credit-authority-isolation.sh; both gate the capability.
#
# Read-only, but for one local port-forward to the gateway for the HTTP suites.
#
#   X1  each Credit Authority (namespaces labelled app-id=credits) is Ready, and
#       its provisioner exited 0
#   X2  the ledger is bound to 127.0.0.1, 3000 is the pod's only port and the
#       Service's only port
#   X3  its ingress is the platform's: its own namespace, the EphemeralJob
#       operator, and its declared consumers' SDKs, on 3000 only
#   X4  its caller map holds the coordinator's `service` entry
#   X5  nothing in its namespace grants pods/exec or pods/portforward: either
#       reaches the ledger's loopback and bypasses the gateway
#   X6  with CREDITS_TOKEN (the coordinator's machine token) set: the guarantees
#       and isolation suites (test/e2e/credit-authority) pass through the gateway
validate_adr109_credit_authority() {
    section "ADR-109 Credit Authority isolation and guarantees (live)"
    ensure_spoke_kubeconfig || { soft_fail "no spoke kubeconfig; the Credit Authority runs on the spoke"; return 0; }

    local namespaces ns
    namespaces=$(kc_spoke get ns -l app-id=credits -o jsonpath='{.items[*].metadata.name}')
    if [[ -z "$namespaces" ]]; then
        note "no Credit Authority is deployed on this spoke"
        return 0
    fi

    for ns in $namespaces; do
        local out line
        out=$(kc_spoke -n "$ns" get pods,svc,ciliumnetworkpolicies,roles,rolebindings,secrets -o json | python3 -c '
import base64, json, sys
ns = sys.argv[1]
items = json.load(sys.stdin)["items"]
kind = lambda k: [i for i in items if i["kind"] == k]
def check(ok, good, bad): print(("PASS " + good) if ok else ("FAIL " + bad))

pods = [p for p in kind("Pod") if p["metadata"].get("labels", {}).get("app") == "sdk"]
ready = [p for p in pods if any(c["type"] == "Ready" and c["status"] == "True" for c in p["status"].get("conditions", []))]
prov = [s for p in pods for s in p["status"].get("initContainerStatuses", []) if s["name"] == "provision"]
done = [s for s in prov if (s.get("state", {}).get("terminated") or {}).get("exitCode") == 0]
check(ready and len(done) == len(pods), f"X1 {ns}: Ready, the ledger provisioned",
      f"X1 {ns}: {len(ready)}/{len(pods)} pods Ready, {len(done)} provisioned -- the Credit Authority is unavailable")

shape_ok = bool(pods)
for p in pods:
    spec = p["spec"]
    inits = spec.get("initContainers", [])
    ledger = next((c for c in inits if c["name"] == "ledger"), {})
    args = ledger.get("args", [])
    bind = args[args.index("--bind") + 1] if "--bind" in args else ""
    ports = [pt["containerPort"] for c in inits + spec["containers"] for pt in c.get("ports", [])]
    shape_ok = shape_ok and bind == "127.0.0.1:3068" and ports == [3000] and not spec.get("hostNetwork")
svc_ports = [pt["port"] for s in kind("Service") for pt in s["spec"].get("ports", [])]
check(shape_ok and svc_ports == [3000], f"X2 {ns}: the ledger is on loopback, 3000 is the only port",
      f"X2 {ns}: pod or Service exposes more than the gateway (service ports {svc_ports})")

pols = [c for c in kind("CiliumNetworkPolicy") if c["metadata"]["name"].endswith("-cross-app-ingress")]
peers = [(f.get("matchLabels", {}).get("app"), f.get("matchLabels", {}).get("k8s:io.kubernetes.pod.namespace"))
         for c in pols for r in c["spec"].get("ingress", []) for f in r.get("fromEndpoints", [])]
ports = {pt["port"] for c in pols for r in c["spec"].get("ingress", []) for tp in r.get("toPorts", []) for pt in tp.get("ports", [])}
foreign = [pr for pr in peers if pr[1] not in (ns, "platform-ops") and pr[0] != "sdk"]
check(pols and ports == {"3000"} and not foreign, f"X3 {ns}: ingress is the platform policy, on 3000, consumers by their SDK only",
      f"X3 {ns}: ingress wrong: peers={peers} ports={sorted(ports)}")

callers = next((s for s in kind("Secret") if s["metadata"]["name"].endswith("-consumer-callers")), None)
azp = base64.b64decode(callers["data"].get("OIDC_ALLOWED_AZP", "")).decode() if callers else ""
check(any(e.endswith(":service") for e in azp.split()), f"X4 {ns}: the caller map holds a service entry",
      f"X4 {ns}: no service entry in the caller map ({azp!r}): the coordinator cannot be admitted")

def grants(rule):
    return any(r in ("pods/exec", "pods/portforward", "*", "pods/*") for r in rule.get("resources", []))
risky = [r["metadata"]["name"] for r in kind("Role") if any(grants(x) for x in r.get("rules", []))]
bound = [b["metadata"]["name"] for b in kind("RoleBinding") if b["roleRef"]["name"] in risky]
check(not bound, f"X5 {ns}: nothing in the namespace grants pods/exec or pods/portforward",
      f"X5 {ns}: bindings granting exec or port-forward, which reach the ledger past the gateway: {bound}")
' "$ns")
        while IFS= read -r line; do
            case "$line" in
                PASS\ *) pass "${line#PASS }" ;;
                FAIL\ *) hard_fail "${line#FAIL }" ;;
            esac
        done <<< "$out"

        if [[ -z "${CREDITS_TOKEN:-}" ]]; then
            soft_fail "X6 $ns: CREDITS_TOKEN is not set, so the guarantees and isolation suites were NOT run; the capability is not proven"
            continue
        fi
        local port=13068 pf
        kubectl --kubeconfig="$SPOKE_KUBECONFIG" -n "$ns" port-forward svc/sdk-workload "$port:3000" >/dev/null 2>&1 &
        pf=$!
        sleep 3
        if (cd "$VALIDATE_ROOT/test/e2e" && CREDITS_URL="http://127.0.0.1:$port" CREDITS_LEDGER="${CREDITS_LEDGER:-credits}" \
                go test -count=1 ./credit-authority >/tmp/credit-authority-suite.log 2>&1); then
            pass "X6 $ns: the guarantees and isolation suites pass through the gateway"
        else
            hard_fail "X6 $ns: the guarantees or isolation suite failed -- see /tmp/credit-authority-suite.log"
        fi
        kill "$pf" 2>/dev/null || true
    done
}
