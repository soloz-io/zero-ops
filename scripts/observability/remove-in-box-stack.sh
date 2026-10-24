#!/usr/bin/env bash
# Remove the in-box observability stack a box ran before ADR-078 addendum 3.
#
#   KUBECONFIG=<cluster> scripts/observability/remove-in-box-stack.sh [--dry-run]
#
# Run once per cluster (hub and every spoke) after the release that removes it.
# Idempotent: a second run finds nothing and says so.
#
# WHY A SCRIPT. The hub's ApplicationSets run with preserveResourcesOnDeletion
# (the ADR-046 incident), so removing a component's Application leaves its
# resources running -- the hub kept VMSingle, VLogs, VMAlert, the operator and
# Grafana after rc.163. The spoke catalogue prunes, but it pruned the operator in
# the same sync as its resources, and those carry the operator's finalizer: they
# sat in "Pending deletion" with nothing left to release them.
#
# ORDER. While the operator runs, its resources are deleted first so the operator
# removes its own finalizers and owned objects. Where the operator is already
# gone, the finalizer it would have removed is removed here, and Kubernetes
# garbage-collects the owned objects. Only then the operator, its admission
# webhook (which would otherwise refuse calls for kinds nothing serves) and the
# CRDs. Grafana is removed on the hub, where it ran.
set -euo pipefail

DRY=false; [[ "${1:-}" == "--dry-run" ]] && DRY=true
NS=platform-observability
OPERATOR=victoriametrics-operator-victoria-metrics-operator
FINALIZER=apps.victoriametrics.com/finalizer
KINDS=(vmalerts vlogs vmsingles vmrules)

run() { if $DRY; then echo "  WOULD kubectl $*"; else echo "  kubectl $*"; kubectl "$@"; fi; }
have() { kubectl get "$@" >/dev/null 2>&1; }

echo "=== Removing the in-box observability stack from $(kubectl config current-context 2>/dev/null || echo "$KUBECONFIG") ==="
$DRY && echo "    dry run -- nothing will be changed"

# 1. The operator's resources.
if have crd vmsingles.operator.victoriametrics.com; then
    operator_up=false
    have -n "$NS" deploy "$OPERATOR" && operator_up=true
    for k in "${KINDS[@]}"; do
        for obj in $(kubectl -n "$NS" get "$k.operator.victoriametrics.com" -o name 2>/dev/null); do
            if ! $operator_up; then
                # Nothing will release this finalizer; release it here.
                fins=$(kubectl -n "$NS" get "$obj" -o jsonpath='{.metadata.finalizers}' 2>/dev/null)
                if [[ "$fins" == *"$FINALIZER"* ]]; then
                    keep=$(python3 -c 'import json,sys; print(json.dumps([f for f in json.loads(sys.argv[1]) if f != sys.argv[2]]))' "$fins" "$FINALIZER")
                    run -n "$NS" patch "$obj" --type=merge -p "{\"metadata\":{\"finalizers\":$keep}}"
                fi
            fi
            run -n "$NS" delete "$obj" --wait=true --timeout=300s --ignore-not-found
        done
    done
fi

# 1b. The operator puts its finalizer on what it creates, not only on its own
#     resources: Services, PVCs, ServiceAccounts, Secrets, ConfigMaps. With the
#     operator gone those sit in Terminating forever (nutgraf-01, rc.167 cleanup).
#     Release it from every object in the namespace that still carries it.
if ! have -n "$NS" deploy "$OPERATOR"; then
    while IFS=' ' read -r ref fins; do
        [[ -z "$ref" ]] && continue
        keep=$(python3 -c 'import json,sys; print(json.dumps([f for f in json.loads(sys.argv[1]) if f != sys.argv[2]]))' "$fins" "$FINALIZER")
        run -n "$NS" patch "$ref" --type=merge -p "{\"metadata\":{\"finalizers\":$keep}}"
    done < <(kubectl -n "$NS" get svc,pvc,deploy,sts,sa,secret,cm,role,rolebinding -o json 2>/dev/null | python3 -c '
import json, sys
for i in json.load(sys.stdin)["items"]:
    f = i["metadata"].get("finalizers") or []
    if sys.argv[1] in f:
        print(i["kind"].lower() + "/" + i["metadata"]["name"], json.dumps(f))
' "$FINALIZER")
fi

# 2. The operator: its release, its webhook, its cluster RBAC.
for obj in deploy/"$OPERATOR" svc/"$OPERATOR" sa/"$OPERATOR" \
           secret/"$OPERATOR"-root-ca secret/"$OPERATOR"-validation; do
    have -n "$NS" "$obj" && run -n "$NS" delete "$obj" --ignore-not-found
done
for obj in validatingwebhookconfiguration/"$OPERATOR"-admission \
           clusterrole/"$OPERATOR" clusterrolebinding/"$OPERATOR" \
           clusterrole/victoriametrics:admin clusterrole/victoriametrics:view; do
    have "$obj" && run delete "$obj" --ignore-not-found
done

# 3. The CRDs, once nothing of their kinds remains.
for crd in $(kubectl get crd -o name 2>/dev/null | grep 'operator\.victoriametrics\.com$' || true); do
    run delete "$crd" --wait=true --timeout=120s --ignore-not-found
done

# 4. Grafana (hub only): the chart's release and the ConfigMaps it served.
for obj in deploy/grafana svc/grafana sa/grafana secret/grafana pvc/grafana \
           cm/grafana cm/grafana-config-dashboards \
           cm/grafana-dashboard-platform-health cm/grafana-datasources-fleet; do
    have -n "$NS" "$obj" && run -n "$NS" delete "$obj" --ignore-not-found
done
for obj in clusterrole/grafana-clusterrole clusterrolebinding/grafana-clusterrolebinding; do
    have "$obj" && run delete "$obj" --ignore-not-found
done

# What is left is a finding, not a pass.
$DRY && exit 0
left=$(kubectl -n "$NS" get deploy,svc,pvc,sa,secret,cm -o name 2>/dev/null \
    | grep -E 'vmsingle|vlogs|vmalert|victoria|vm-platform-rulefiles|/grafana$' || true)
crds=$(kubectl get crd -o name 2>/dev/null | grep -c 'operator\.victoriametrics\.com$' || true)
if [[ -n "$left" || "$crds" != "0" ]]; then
    echo "NOT CLEAN: still present:"; echo "$left" | sed 's/^/  /'; echo "  victoriametrics CRDs: $crds"
    exit 1
fi
echo "clean: no in-box store, operator, CRD or Grafana remains"
