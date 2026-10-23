#!/usr/bin/env bash
# ADR-030 #3: a rotated credential must trigger a rolling update. On this
# platform that is a `*.reloader.stakater.com/reload` annotation, and an
# annotation does nothing unless Reloader runs on the cluster that carries it.
#
# The hub installs Reloader as an Argo component; the spoke had none, so every
# reload annotation there was inert and nothing said so -- rotated OIDC and DNS
# credentials stayed in memory, and a release's ConfigMap change was never
# loaded (2026-10-23, rc.159).
#
#   030-1  when the spoke catalogue, or a tenant chart (whose workloads run on
#          the spoke), carries a reload annotation, the spoke catalogue's
#          kustomization lists a resource that runs the Reloader image.

validate_reload_annotations_have_a_reloader() {
    section "ADR-030: reload annotations on the spoke have a Reloader to act on them"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 - <<'PY'
import pathlib, re, yaml

CATALOGUE = pathlib.Path("manifests/spoke/spoke-catalog/infra")
SCOPES = [CATALOGUE, pathlib.Path("manifests/tenants/charts")]
ANNOTATION = re.compile(r'^\s*(?:configmap|secret)\.reloader\.stakater\.com/(?:reload|auto)\s*:', re.M)
IMAGE = re.compile(r'image:\s*"?ghcr\.io/stakater/reloader[:@]')

users = sorted({str(p) for s in SCOPES for p in s.rglob("*") if p.suffix in (".yaml", ".tpl")
                and ANNOTATION.search(p.read_text())})
if not users:
    print("PASS no reload annotation on the spoke")
    raise SystemExit

kustomization = yaml.safe_load((CATALOGUE / "kustomization.yaml").read_text())
runs = [r for r in kustomization.get("resources", [])
        if (CATALOGUE / r).is_file() and IMAGE.search((CATALOGUE / r).read_text())]
if runs:
    print(f"PASS {len(users)} file(s) carry reload annotations; the spoke catalogue runs Reloader ({runs[0]})")
else:
    print(f"FAIL {len(users)} file(s) carry reload annotations ({', '.join(users[:4])}) but the spoke "
          f"catalogue runs no Reloader; every one of them is inert")
PY
    )

    local line
    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        case "$line" in
            PASS\ *) pass "${line#PASS }" ;;
            FAIL\ *) hard_fail "${line#FAIL }" ;;
            *)       note "$line" ;;
        esac
    done <<< "$out"
}
