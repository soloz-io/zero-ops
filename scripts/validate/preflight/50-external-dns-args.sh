#!/usr/bin/env bash
# 03-platform-services-appset.yaml patches /containers/0/args/0 and /args/1 by
# INDEX. Reordering the args does not fail the patch — it rewrites the wrong
# flag, and external-dns then runs with a valid-looking but wrong domain filter.
# The arg order is therefore a contract, and this is where it is enforced.
validate_external_dns_args() {
    section "external-dns args[0]/args[1] index contract"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 - <<'PY'
import yaml
f = "manifests/spoke/spoke-catalog/infra/external-dns.yaml"
for doc in yaml.safe_load_all(open(f)):
    if not doc or doc.get("kind") != "Deployment":
        continue
    args = doc["spec"]["template"]["spec"]["containers"][0].get("args", [])
    if len(args) < 2:
        print("BAD fewer than two args"); break
    if not args[0].startswith("--domain-filter="):
        print("BAD args[0] is %r, expected --domain-filter=" % args[0]); break
    if not args[1].startswith("--txt-owner-id="):
        print("BAD args[1] is %r, expected --txt-owner-id=" % args[1]); break
    print("OK")
    break
PY
)
    if [[ "$out" == OK* ]]; then
        pass "external-dns args[0]=--domain-filter args[1]=--txt-owner-id (AppSet patches by index)"
    else
        hard_fail "external-dns arg order violates the AppSet index patch: ${out#BAD }"
    fi
}
