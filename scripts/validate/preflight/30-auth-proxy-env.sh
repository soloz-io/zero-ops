#!/usr/bin/env bash
# auth-proxy's config.go has no fallbacks by design, so every variable it reads
# must be present in the Deployment or the pod CrashLoops on first start.
#
# This is the exact defect that left AUTH_PUBLIC_BASE_URL and MCP_GATEWAY_BASE_URL
# absent from the Deployment while the process still ran happily — against
# compiled-in production URLs. Removing the fallbacks turned a silent
# misconfiguration into a crash; this check turns the crash into a pre-flight
# error, before anything is provisioned.
validate_auth_proxy_env() {
    section "auth-proxy required env ⊆ Deployment env"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 - <<'PY'
import re, yaml

src = open("internal/auth-proxy/config.go").read()
required = set(re.findall(r'm\.(?:get|duration)\("([A-Z0-9_]+)"\)', src))

supplied = set()
for doc in yaml.safe_load_all(open("manifests/hub-core-services/identity/auth-proxy/deployment.yaml")):
    if not doc or doc.get("kind") != "Deployment":
        continue
    for c in doc["spec"]["template"]["spec"]["containers"]:
        for e in c.get("env", []) or []:
            supplied.add(e["name"])

missing = sorted(required - supplied)
unused = sorted(supplied - required)
if missing:
    print("MISSING " + ",".join(missing))
if unused:
    print("UNUSED " + ",".join(unused))
print("COUNT %d" % len(required))
PY
)
    if grep -q '^MISSING' <<< "$out"; then
        hard_fail "auth-proxy Deployment is missing env config.go requires: $(grep '^MISSING' <<< "$out" | cut -d' ' -f2-) — the pod will CrashLoop on start"
    else
        pass "auth-proxy: all $(grep '^COUNT' <<< "$out" | cut -d' ' -f2) required vars present in the Deployment"
    fi

    # An unread variable is dead config, not a broken deployment — worth saying,
    # not worth blocking a bootstrap over.
    if grep -q '^UNUSED' <<< "$out"; then
        note "Deployment sets vars config.go never reads: $(grep '^UNUSED' <<< "$out" | cut -d' ' -f2-)"
    fi
}
