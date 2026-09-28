#!/usr/bin/env bash
# Verify oranger's own logical database, end to end (commit 98e0c24).
#
# oranger's fleet declared `database: enabled: false`, which suppressed TWO
# things through one guard: the AINativeSaaS XR on the hub that provisions the
# database, and the tenant baseline migration on the spoke that creates
# users/identities/sessions in it. This walks the five steps that guard gates,
# in order, so a failure names the step rather than the symptom.
#
#   HUB_KUBECONFIG=k8-secrets/kubeconfig/hub.kubeconfig \
#   SPOKE_KUBECONFIG=k8-secrets/kubeconfig/nutgraf-01.kubeconfig \
#     scripts/validate/verify-oranger-database.sh
set -uo pipefail

TENANT=nutgraf
APP=oranger
CELL=nutgraf-01
SCOPE="${TENANT}-${APP}"
NS="tenant-${SCOPE}"
DB="${TENANT}-${APP}-db"

HUB_KUBECONFIG="${HUB_KUBECONFIG:?set HUB_KUBECONFIG}"
SPOKE_KUBECONFIG="${SPOKE_KUBECONFIG:?set SPOKE_KUBECONFIG}"
kc_hub()   { kubectl --kubeconfig="$HUB_KUBECONFIG"   "$@"; }
kc_spoke() { kubectl --kubeconfig="$SPOKE_KUBECONFIG" "$@"; }

fails=0
step() { printf '\n\033[1m── %s\033[0m\n' "$1"; }
ok()   { printf '   \033[32mOK\033[0m   %s\n' "$1"; }
bad()  { printf '   \033[31mFAIL\033[0m %s\n' "$1"; fails=$((fails+1)); }

step "1/6  ArgoCD has the commit"
rev=$(kc_hub get application -n argocd -o jsonpath="{range .items[?(@.spec.source.path=='environments/dev/${APP}')]}{.metadata.name}={.status.sync.revision}{'\n'}{end}" 2>/dev/null)
[ -n "$rev" ] && echo "$rev" || echo "   (no Application matched path environments/dev/${APP})"
# The tenant Application is what renders the XR; name it explicitly if the above is empty.
kc_hub get application -n argocd 2>/dev/null | grep -E "NAME|${APP}|${TENANT}" || true

step "2/6  Hub: the AINativeSaaS XR exists and names the database"
if kc_hub get ainativesaas "${SCOPE}" -n "${NS}" >/dev/null 2>&1; then
  got=$(kc_hub get ainativesaas "${SCOPE}" -n "${NS}" -o jsonpath='{.spec.database.name}')
  [ "$got" = "$DB" ] && ok "spec.database.name = ${got}" || bad "spec.database.name = '${got}', expected ${DB}"
  kc_hub get ainativesaas "${SCOPE}" -n "${NS}" \
    -o custom-columns=SYNCED:.status.conditions[?\(@.type==\"Synced\"\)].status,READY:.status.conditions[?\(@.type==\"Ready\"\)].status
else
  bad "no AINativeSaaS/${SCOPE} in ${NS} — the hub has not rendered the XR yet"
fi

step "3/6  Hub: the operator seeded db-credentials in Infisical"
# /spoke-pool/${CELL}/tenants/${APP}/db-credentials, recorded as a condition on the XR.
kc_hub get ainativesaas "${SCOPE}" -n "${NS}" -o jsonpath='{range .status.conditions[*]}{.type}={.status}  {.reason}{"\n"}{end}' 2>/dev/null \
  | grep -i -E "seed|db" || echo "   (no seeding condition yet)"
echo "   expected Infisical path: /spoke-pool/${CELL}/tenants/${APP}/db-credentials"

step "4/6  Spoke: the TenantDatabase claim reconciled"
if kc_spoke get tenantdatabase -n "${NS}" >/dev/null 2>&1; then
  kc_spoke get tenantdatabase -n "${NS}"
  r=$(kc_spoke get tenantdatabase -n "${NS}" -o jsonpath='{.items[0].status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)
  [ "$r" = "True" ] && ok "Ready=True" || bad "Ready=${r:-<none>} — check the composed resources below"
  kc_spoke get database.postgresql.cnpg.io,role.postgresql.sql.crossplane.io -n "${NS}" 2>/dev/null | head
else
  bad "no TenantDatabase in ${NS} — the hub's provider-kubernetes Object has not landed"
fi

step "5/6  Spoke: the credentials Secret the migration Job reads"
if kc_spoke get secret "tenant-${APP}-db-credentials" -n "${NS}" >/dev/null 2>&1; then
  ok "Secret tenant-${APP}-db-credentials exists"
  echo -n "   database key: "; kc_spoke get secret "tenant-${APP}-db-credentials" -n "${NS}" -o jsonpath='{.data.database}' | base64 -d; echo
else
  bad "Secret tenant-${APP}-db-credentials missing — the migration Job will stay Pending"
fi

step "6/6  Spoke: the tenant baseline migration ran"
job=$(kc_spoke get job -n "${NS}" -o name 2>/dev/null | grep tenant-baseline-migration | head -1)
if [ -n "$job" ]; then
  kc_spoke get "$job" -n "${NS}"
  s=$(kc_spoke get "$job" -n "${NS}" -o jsonpath='{.status.succeeded}')
  [ "${s:-0}" -ge 1 ] && ok "migration succeeded" || bad "migration has not succeeded"
  echo "   --- logs ---"
  kc_spoke logs -n "${NS}" "$job" --tail=20 2>/dev/null | sed 's/^/   /'
else
  bad "no tenant-baseline-migration Job in ${NS}"
fi

step "tables (requires the Job to have run)"
echo "   kubectl --kubeconfig=\$SPOKE_KUBECONFIG exec -n platform-data shared-cnpg-1 -- \\"
echo "     psql -d ${DB} -c '\\dt public.*'"

printf '\n'
[ "$fails" -eq 0 ] && { printf '\033[32mall checks passed\033[0m\n'; exit 0; }
printf '\033[31m%d check(s) failed\033[0m\n' "$fails"; exit 1
