#!/usr/bin/env bash
# ADR-109: the Credit Authority's isolation, as rendered. The live half is
# cluster/109-credit-authority-isolation.sh; both gate the capability.
#
#   I1  the pod: the ledger is a native sidecar bound to 127.0.0.1; the
#       provisioner is a one-shot init container after it; the gateway is the
#       only container with a port, and the pod's only port is 3000
#   I2  the gateway's ledger route: strict JWT on the issuer and this
#       application's project; every rule is `require` or `allow`, never `deny`
#   I3  the caller binding: no ID token, this tenant, and the coordinator's
#       `service` entry in the typed caller map
#   I4  no opt-out of the balance check: creating a transaction requires no query
#       string and a body whose fields are exactly postings, metadata, reference
#   I5  every `allow` is anchored at both ends on the tenant's ledger, with the
#       path characters ADR-109 §4 states, and nothing else is allowed
#   I6  the config holds no dollar sign but its environment references (it is
#       shell-expanded before it is parsed)
#   I7  the chart refuses to render without a coordinator, without pinned image
#       digests, or with a ledger name its rules cannot be built from
#   I8  a backend-only application: no gateway, and the platform's ingress admits
#       only its declared consumer's SDK on 3000
validate_credit_authority_isolation() {
    section "ADR-109 Credit Authority isolation (rendered)"

    if ! command -v helm >/dev/null 2>&1; then
        soft_fail "helm not installed; cannot render the Credit Authority chart"
        return 0
    fi

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 - <<'PY'
import json, re, shutil, subprocess, tempfile, yaml, os

SRC = "manifests/tenants/charts"
UT = f"{SRC}/universal-tenant"
DIGEST = "sha256:" + "0" * 64
FIXTURE = {
    "tenantId": "acme", "coordinator": "waypoint",
    "images": {"ledger": {"digest": DIGEST}, "provisioner": {"digest": DIGEST}},
}

fails, passes = [], []
def check(ok, good, bad): (passes if ok else fails).append(good if ok else bad)
def docs(text): return [d for d in yaml.safe_load_all(text) if d]

work = tempfile.mkdtemp()
for name in ("credit-authority", "platform-workload"):
    shutil.copytree(f"{SRC}/{name}", f"{work}/{name}")
dep = subprocess.run(["helm", "dependency", "build", f"{work}/credit-authority"], capture_output=True, text=True)
CA = f"{work}/credit-authority"

def render(chart, values):
    with tempfile.NamedTemporaryFile("w", suffix=".yaml") as f:
        yaml.safe_dump(values, f); f.flush()
        r = subprocess.run(["helm", "template", "t", chart, "-f", f.name], capture_output=True, text=True)
    err = next((l for l in r.stderr.splitlines() if l.startswith("Error:")), None)
    return r.stdout, err

if dep.returncode != 0:
    fails.append(f"I0 the chart's dependency did not build: {dep.stderr.strip()}")
    out, err = "", "not rendered"
else:
    out, err = render(CA, FIXTURE)
if err:
    fails.append(f"I0 the fixture did not render: {err}")
else:
    ds = docs(out)
    rollout = next(d for d in ds if d["kind"] == "Rollout")
    pod = rollout["spec"]["template"]["spec"]
    inits = pod.get("initContainers", [])
    names = [c["name"] for c in inits]
    ledger = inits[0] if inits else {}
    args = ledger.get("args", [])
    bind = args[args.index("--bind") + 1] if "--bind" in args else ""
    ported = [c["name"] for c in inits + pod["containers"] if c.get("ports")]
    ports = [p["containerPort"] for c in inits + pod["containers"] for p in c.get("ports", [])]
    check(names == ["ledger", "provision"] and ledger.get("restartPolicy") == "Always"
          and bind == "127.0.0.1:3068" and "restartPolicy" not in inits[1]
          and len(pod["containers"]) == 1 and ported == [pod["containers"][0]["name"]] and ports == [3000]
          and not pod.get("hostNetwork"),
          "I1 ledger is a loopback native sidecar, provisioning is one-shot before the gateway, 3000 is the only port",
          f"I1 pod shape wrong: init={names} restartPolicy={ledger.get('restartPolicy')} bind={bind!r} ported={ported} ports={ports}")

    cm = next(d for d in ds if d["kind"] == "ConfigMap" and "config.yaml" in d.get("data", {}))
    raw = cm["data"]["config.yaml"]
    cfg = yaml.safe_load(raw)
    routes = {r["name"]: r for r in cfg["binds"][0]["listeners"][0]["routes"]}
    route = routes.get("ledger", {})
    pol = route.get("policies", {})
    jwt = pol.get("jwtAuth", {})
    rules = pol.get("authorization", {}).get("rules", [])
    modes = [next(iter(r)) if isinstance(r, dict) else "allow" for r in rules]
    check(jwt.get("mode") == "strict" and jwt.get("issuer") == "${OIDC_ISSUER_URL}"
          and jwt.get("audiences") == ["${OIDC_PROJECT_ID}"] and jwt.get("jwks", {}).get("url") == "${OIDC_JWKS_URL}"
          and rules and "deny" not in modes and set(modes) == {"require", "allow"},
          "I2 the ledger route requires a strict JWT for this project, and uses only require and allow",
          f"I2 ledger route policy wrong: jwtAuth={jwt} modes={modes}")

    requires = [r["require"] for r in rules if isinstance(r, dict) and "require" in r]
    check("!has(jwt.at_hash)" in requires
          and 'jwt["urn:zitadel:iam:user:resourceowner:id"] == "${OIDC_ORG_ID}"' in requires
          and any('"${OIDC_ALLOWED_AZP}"' in r and '"=waypoint:service "' in r and "jwt.client_id" in r for r in requires),
          "I3 the caller is the coordinator's machine client, of this tenant, with an access token",
          f"I3 caller binding incomplete: {requires}")

    body = [r for r in requires if "json(request.body)" in r]
    check(len(body) == 1 and "request.pathAndQuery == request.path" in body[0]
          and 'k in ["postings", "metadata", "reference"]' in body[0] and body[0].startswith('request.method != "POST" ||'),
          "I4 creating a transaction admits no query string and only postings, metadata and reference",
          f"I4 transaction body rule missing or wrong: {body}")

    allows = [r["allow"] if isinstance(r, dict) else r for r in rules if not (isinstance(r, dict) and "require" in r)]
    pat = re.compile(r'^request\.method == "(GET|POST)" && request\.path\.matches\(r"\^/v2/credits/([^"]*)\\z"\)$')
    ok = len(allows) == 4 and all(pat.match(a) for a in allows)
    tails = sorted(pat.match(a).group(2) for a in allows) if ok else allows
    check(ok and tails == sorted(["transactions", "transactions(/[0-9]+)?", "accounts(/[A-Za-z0-9_:-]+)?",
                                  "(aggregate/balances|volumes)"]),
          "I5 only the four operations are allowed, each anchored on the tenant's ledger",
          f"I5 allow rules wrong: {tails}")

    stray = re.sub(r"\$\{[A-Z_]+\}", "", raw)
    check("$" not in stray, "I6 the config's only dollar signs are its environment references",
          "I6 a bare dollar sign in the gateway config would be shell-expanded before it is parsed")

    refused = {}
    for what, v in {
        "no coordinator": {**FIXTURE, "coordinator": ""},
        "no ledger digest": {**FIXTURE, "images": {"ledger": {"digest": ""}, "provisioner": {"digest": DIGEST}}},
        "no provisioner digest": {**FIXTURE, "images": {"ledger": {"digest": DIGEST}, "provisioner": {"digest": ""}}},
        "a ledger name with a dot": {**FIXTURE, "ledger": {"name": "a.b", "bucket": "a"}},
    }.items():
        refused[what] = bool(render(CA, v)[1])
    check(all(refused.values()), "I7 the chart refuses a missing coordinator, unpinned images and an unsafe ledger name",
          f"I7 rendered what it must refuse: {[k for k, v in refused.items() if not v]}")

ut, e8 = render(UT, {"tenantId": "acme", "appId": "credits", "cellId": "cell-1",
                     "oidcIssuer": "https://id.example.test",
                     "identity": {"backendOnly": True, "allowedConsumers": ["waypoint"]}})
if e8:
    fails.append(f"I8 the backend-only fixture did not render: {e8}")
else:
    ds = docs(ut)
    gw = [d for d in ds if d["kind"] == "Deployment" and "agentgateway" in d["metadata"]["name"]]
    ing = next((d for d in ds if d["kind"] == "CiliumNetworkPolicy" and d["metadata"]["name"] == "credits-cross-app-ingress"), None)
    froms = [(f["matchLabels"].get("app"), f["matchLabels"].get("k8s:io.kubernetes.pod.namespace"))
             for r in (ing or {}).get("spec", {}).get("ingress", []) for f in r.get("fromEndpoints", [])]
    check(not gw and ing and ("sdk", "tenant-acme-waypoint") in froms
          and not any(ns and ns.startswith("tenant-acme-") and ns not in ("tenant-acme-waypoint", "tenant-acme-credits") for _, ns in froms),
          "I8 a backend-only application has no gateway, and admits only its declared consumer's SDK",
          f"I8 backend-only rendering wrong: gateways={[d['metadata']['name'] for d in gw]} ingress={froms}")

shutil.rmtree(work, ignore_errors=True)
for p in passes: print("PASS " + p)
for f in fails: print("FAIL " + f)
PY
    )
    local line
    while IFS= read -r line; do
        case "$line" in
            PASS\ *) pass "${line#PASS }" ;;
            FAIL\ *) hard_fail "${line#FAIL }" ;;
        esac
    done <<< "$out"
}
