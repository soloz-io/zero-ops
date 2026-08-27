#!/usr/bin/env bash
# Every ExternalSecret backed by Infisical names a key that something must put
# there. When nothing does, the failure is mute in a very specific way: the
# ExternalSecret reports "could not get secret data from provider" — which never
# says WHERE it looked or WHO was supposed to write it — and the consuming pod
# reports only CreateContainerConfigError. Neither names the missing key's owner.
#
# It has cost a full bootstrap four times: agentgateway-oidc-cookie-secret,
# waypoint-bff-client-secret, the Grafana Cloud set and the S3 backup pair all
# reached a rebuilt Infisical with no producer. The first was a genuine omission —
# its siblings hub-kratos-ui-cookie-secret and hub-hydra-system-secret were in the
# operator's registry and it simply was not.
#
# So: cross-check the keys the manifests CONSUME against the keys the hub-operator
# PRODUCES, and require that anything left over be declared here deliberately, with
# the reason it cannot be generated. Producers are read from the operator source, so
# adding a registry entry is enough — this check needs no edit to stay correct.
validate_infisical_key_producers() {
    section "Every Infisical-backed ExternalSecret key has a producer"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 - <<'PY'
import glob, os, re, yaml

# Credentials issued by systems OUTSIDE the platform, so nothing here can generate
# them. They are no longer hand-seeded: hub-bootstrap.sh step 6b builds a Secret
# from k8-secrets/<dir>/ and the operator's CLISecretMappings upload it. They stay
# listed so every run prints the set that needs real values on disk — an empty file
# is skipped rather than uploaded blank, and that skip is easy to miss.
EXTERNAL = {
    "S3_ACCESS_KEY_ID":              "k8-secrets/s3/access-key-id — Hetzner Object Storage key (starts 0A…, NOT the hcloud-token)",
    "S3_SECRET_ACCESS_KEY":          "k8-secrets/s3/secret-access-key — Hetzner Object Storage secret (CNPG Barman backups)",
    "GRAFANA_CLOUD_API_KEY":         "k8-secrets/grafana-cloud/api-key",
    "GRAFANA_CLOUD_LOKI_URL":        "k8-secrets/grafana-cloud/loki-url",
    "GRAFANA_CLOUD_LOKI_USER":       "k8-secrets/grafana-cloud/loki-user",
    "GRAFANA_CLOUD_PROMETHEUS_URL":  "k8-secrets/grafana-cloud/prometheus-url",
    "GRAFANA_CLOUD_PROMETHEUS_USER": "k8-secrets/grafana-cloud/prometheus-user",
}

# Known-missing producers awaiting a decision, reported as a WARNING rather than a
# failure so an open design question does not block a bootstrap. Anything not listed
# here and not produced is a hard failure — that is the point of the check.
# Removing an entry here is the last step of closing its gap.
PENDING = {
    "waypoint-bff-client-secret":
        "hydra-maester mints this when it registers waypoint-bff-client, but nothing "
        "uploads it to Infisical, so no consumer can resolve it. The consumer is the "
        "tenant BFF workload on the spoke, which is declared in the fleet-registry but "
        "not yet deployed in dev - so nothing is broken by its absence today, and "
        "nothing proves the path works either. (This entry previously named "
        "auth-proxy's ExternalSecret; auth-proxy consumes the client REGISTRATION, not "
        "this credential, and has no ExternalSecret for it.) "
        "Closing it is ADR-053: the hub-operator generates the credential once, writes "
        "it to Infisical, registers the confidential client against Hydra directly, and "
        "ESO delivers it to the spoke. Removing this entry is that ADR's acceptance "
        "gate, and the check then enforces the producer.",
}

SRC = glob.glob("operators/hub-operator/internal/infisical/*.go") + \
      glob.glob("operators/hub-operator/internal/database/*.go")

# Keys produced INTO a cell path (/spoke-pool/<cellId>/...), as opposed to the
# Infisical root. This distinction is the whole point of the check: a spoke's
# SecretStore is authorised for its own prefix ONLY (ADR-031), so a producer that
# writes S3_ACCESS_KEY_ID to the root does NOT satisfy a spoke consumer reading
# /spoke-pool/<cell>/shared/S3_ACCESS_KEY_ID. Matching on key NAME alone treated
# those as satisfied and reported OK while every spoke ExternalSecret failed.
#
# Derived from the operator source rather than restated here, so deleting a
# producer breaks this check instead of silently narrowing it.
CELL_SRC = "operators/hub-operator/internal/secrets/cell_credentials.go"
cell_produced = {"infisical-credentials"}  # EnsureInfisicalCredentials, shared path
if os.path.exists(CELL_SRC):
    csrc = open(CELL_SRC).read()
    # Copied from the root into each cell by EnsureFleetCredentialsMaterialised.
    block = re.search(r'FleetCredentialKeys\s*=\s*\[\]string\{(.*?)\}', csrc, re.S)
    if block:
        cell_produced |= set(re.findall(r'"([^"]+)"', block.group(1)))

# GENERATED into each cell by the application-secret uploader. These are produced,
# not copied, so they are declared alongside the other generated secrets: any
# mapping carrying CellScopedKey is written once per SpokePool into that cell's
# shared path. Derived from the source so deleting the field breaks this check.
MAP_SRC = "operators/hub-operator/internal/infisical/secret_mappings.go"
if os.path.exists(MAP_SRC):
    cell_produced |= set(re.findall(r'CellScopedKey:\s*"([^"]+)"', open(MAP_SRC).read()))
if not SRC:
    print("SKIP\thub-operator source not found")
    raise SystemExit

produced = set()
for f in SRC:
    src = open(f).read()
    produced |= set(re.findall(r'InfisicalKey:\s*"([^"]+)"', src))
    produced |= set(re.findall(r'PasswordKey:\s*"([^"]+)"', src))
    produced |= {k for k in re.findall(r'UsernameKey:\s*"([^"]+)"', src) if k}
    # DKIM ed25519 pair: PrivateKeyKey / PublicKeyKey (KeyType != ""), e.g. hub-stalwart-dkim-*
    produced |= set(re.findall(r'PrivateKeyKey:\s*"([^"]+)"', src))
    produced |= set(re.findall(r'PublicKeyKey:\s*"([^"]+)"', src))
    # Key constants, consumed by the DB-role and uploader paths.
    produced |= set(re.findall(r'\bKey[A-Za-z0-9_]*\s*=\s*"([^"]+)"', src))
    # Values written inline, e.g. the Svix JWT derived from its signing secret.
    produced |= set(re.findall(r'CreateOrUpdateSecretRaw\([^)]*?"([A-Za-z0-9_.\-]+)"', src, re.S))

wanted = {}
wanted_cell = {}
for f in glob.glob("manifests/**/*.yaml", recursive=True):
    try:
        docs = [d for d in yaml.safe_load_all(open(f)) if d]
    except Exception:
        continue
    for d in docs:
        if d.get("kind") != "ExternalSecret":
            continue
        store = (d.get("spec", {}).get("secretStoreRef") or {}).get("name", "")
        if "infisical" not in store:
            continue
        for item in d["spec"].get("data") or []:
            k = (item.get("remoteRef") or {}).get("key")
            if not k:
                continue
            # A cell-scoped key names its path; PLACEHOLDER is substituted per
            # spoke at render time. Previously any key containing PLACEHOLDER was
            # skipped outright, which excluded EVERY spoke ExternalSecret from the
            # check — the exact set most likely to reference an unproduced path.
            # Evaluate the leaf against the cell producers instead of skipping.
            if k.startswith("/spoke-pool/"):
                wanted_cell.setdefault(k.rsplit("/", 1)[-1], (k, f))
            elif "PLACEHOLDER" not in k:
                wanted.setdefault(k, f)

for k in sorted(wanted):
    if k in produced or k in EXTERNAL:
        continue
    if k in PENDING:
        print("\t".join(["PENDING", k, PENDING[k]]))
        continue
    print("\t".join(["BAD", k, os.path.relpath(wanted[k])]))

for leaf in sorted(wanted_cell):
    full, f = wanted_cell[leaf]
    if leaf in cell_produced:
        continue
    where = "produced at the ROOT only" if (leaf in produced or leaf in EXTERNAL) else "not produced anywhere"
    print("\t".join(["BAD", full,
                     "%s — consumed from a cell path but %s; a cell SecretStore cannot read the root (ADR-031)"
                     % (os.path.relpath(f), where)]))

for k in sorted(EXTERNAL):
    if k in wanted or k in wanted_cell:
        print("\t".join(["SEED", k, EXTERNAL[k]]))
PY
)

    if [[ -z "$out" ]]; then
        pass "no Infisical-backed ExternalSecrets found to check"
        return 0
    fi

    if grep -q $'^SKIP\t' <<< "$out"; then
        warn "hub-operator source not available — cannot verify key producers"
        return 0
    fi

    local tag a b bad=0
    while IFS=$'\t' read -r tag a b; do
        [[ "$tag" == "BAD" ]] || continue
        bad=1
        hard_fail "Infisical key '$a' has no producer — nothing generates or uploads it ($b)"
    done <<< "$out"

    (( bad )) || pass "every consumed Infisical key is produced by the hub-operator, or declared external"

    while IFS=$'\t' read -r tag a b; do
        [[ "$tag" == "PENDING" ]] || continue
        warn "Infisical key '$a' has no producer (tracked gap, not yet closed)"
        note "$b"
    done <<< "$out"

    note "externally issued — seeded by hub-bootstrap step 6b from k8-secrets/:"
    while IFS=$'\t' read -r tag a b; do
        [[ "$tag" == "SEED" ]] || continue
        note "  $a — $b"
    done <<< "$out"
}
