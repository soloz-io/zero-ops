#!/usr/bin/env bash
# Hydra rejects any authorization request whose redirect_uri is not registered.
# The rejection surfaces in the browser; no cluster-side condition anywhere goes
# unhealthy. One unregistered URI breaks login completely while the platform
# reports itself fully healthy — so the closure is checked statically instead.
validate_oauth_redirect_uris() {
    section "Gateway redirectURIs are registered on an OAuth2Client"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 - <<'PY'
import glob, re, yaml

registered = set()
for f in glob.glob("manifests/hub-core-services/identity/hydra-maester/*.yaml"):
    for doc in yaml.safe_load_all(open(f)):
        if doc and doc.get("kind") == "OAuth2Client":
            registered.update(doc["spec"].get("redirectUris", []) or [])

used = set()
for f in ["manifests/spoke/spoke-catalog/infra/agentgateway-config.yaml",
          "manifests/hub-core-services/api-gateway/agentgateway-config.yaml"]:
    try:
        text = open(f).read()
    except FileNotFoundError:
        continue
    for m in re.finditer(r"redirectURI:\s*(\S+)", text):
        used.add(m.group(1).strip(chr(34) + chr(39)))

missing = sorted(u for u in used if u not in registered)
if missing:
    print("UNREGISTERED " + " ".join(missing))
print("USED %d REGISTERED %d" % (len(used), len(registered)))
PY
)
    if grep -q '^UNREGISTERED' <<< "$out"; then
        hard_fail "redirectURI used by a gateway but registered on no OAuth2Client: $(grep '^UNREGISTERED' <<< "$out" | cut -d' ' -f2-) — login fails in the browser with nothing unhealthy in-cluster"
    else
        pass "redirect URIs closed over hydra-maester clients ($(grep -o 'USED [0-9]*' <<< "$out" | cut -d' ' -f2) used, all registered)"
    fi
}
