#!/usr/bin/env bash
# Hydra rejects any authorization request whose redirect_uri is not registered.
# The rejection surfaces in the browser; no cluster-side condition anywhere goes
# unhealthy. One unregistered URI breaks login completely while the platform
# reports itself fully healthy — so the closure is checked statically instead.
validate_oauth_redirect_uris() {
    section "Gateway redirectURIs are registered on an OAuth2Client"

    local out
    out=$(cd "$VALIDATE_ROOT" && ENV_ZONE="$ENV_ZONE" python3 - <<'PY'
import glob, os, re, yaml

registered = set()
for f in glob.glob("manifests/hub-core-services/identity/hydra-maester/*.yaml"):
    for doc in yaml.safe_load_all(open(f)):
        if doc and doc.get("kind") == "OAuth2Client":
            registered.update(doc["spec"].get("redirectUris", []) or [])

# Fleet-owned clients, rendered per tenant at onboarding (ADR-047 addendum). They are
# templated, so the host is a placeholder here: collect the URI PATHS and treat a
# gateway URI as registered when a fleet template produces its path. Without this the
# check fails every tenant hostname the moment tenant clients stop being declared in
# the platform — which is the intended end state, not a regression.
tenant_paths = set()
for f in glob.glob("manifests/tenants/charts/*/templates/oauth2clients.yaml"):
    for m in re.finditer(r"https://\{\{[^}]*\}\}(/\S*)", open(f).read()):
        tenant_paths.add(m.group(1))

used = set()
for f in ["manifests/spoke/spoke-catalog/infra/agentgateway-config.yaml",
          "manifests/hub-core-services/api-gateway/agentgateway-config.yaml"]:
    try:
        text = open(f).read()
    except FileNotFoundError:
        continue
    for m in re.finditer(r"redirectURI:\s*(\S+)", text):
        used.add(m.group(1).strip(chr(34) + chr(39)))

zone = os.environ.get("ENV_ZONE", "")

def is_registered(u):
    if u in registered:
        return True
    # A fleet template that renders this path counts as registration: the concrete
    # host is supplied at onboarding from fleet-registry (ADR-047 addendum), so it
    # cannot be checked literally here.
    #
    # Path alone is far too weak — every OIDC client on earth uses /oauth/callback,
    # so it would accept https://evil.example.com/oauth/callback. Require the host to
    # sit inside this environment's DNS zone as well, which a fleet host always does
    # and an arbitrary one does not.
    if not zone:
        return False
    m = re.match(r"https://([^/]+)(/\S*)$", u)
    if not m:
        return False
    host, path = m.group(1), m.group(2)
    if not host.endswith("." + zone):
        return False
    return any(path == p for p in tenant_paths)

missing = sorted(u for u in used if not is_registered(u))
if missing:
    print("UNREGISTERED " + " ".join(missing))
print("USED %d REGISTERED %d" % (len(used), len(registered)))
PY
)
    # A Python traceback yields no UNREGISTERED line, which would otherwise read as
    # success. A validation that passes because it crashed is worse than none.
    if ! grep -q '^USED ' <<< "$out"; then
        hard_fail "redirect-URI analysis did not complete — treating as failure. Output: $(tr '\n' ' ' <<< "$out" | tail -c 300)"
        return 0
    fi

    if grep -q '^UNREGISTERED' <<< "$out"; then
        hard_fail "redirectURI used by a gateway but registered on no OAuth2Client: $(grep '^UNREGISTERED' <<< "$out" | cut -d' ' -f2-) — login fails in the browser with nothing unhealthy in-cluster"
    else
        pass "redirect URIs closed over hydra-maester clients ($(grep -o 'USED [0-9]*' <<< "$out" | cut -d' ' -f2) used, all registered)"
    fi
}
