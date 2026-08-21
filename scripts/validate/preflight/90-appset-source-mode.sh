#!/usr/bin/env bash
# ArgoCD rejects an Application that sets both `directory` and `kustomize` on one
# source. The rejection is a sync-time ComparisonError on the GENERATED child
# app, not a template error on the ApplicationSet — so it does not surface until
# the AppSet has already been applied to a live cluster.
validate_appset_source_mode() {
    section "ApplicationSet source-mode exclusivity"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 - <<'PY'
import glob, re, yaml

for f in sorted(glob.glob("manifests/argocd/**/*.yaml", recursive=True)):
    raw = open(f).read()
    if "ApplicationSet" not in raw:
        continue
    # Strip Helm actions so the ApplicationSet body parses as YAML.
    cleaned = re.sub(r"\{\{-?.*?-?\}\}", "PLACEHOLDER", raw, flags=re.S)
    try:
        docs = list(yaml.safe_load_all(cleaned))
    except Exception:
        continue
    for doc in docs:
        if not isinstance(doc, dict) or doc.get("kind") != "ApplicationSet":
            continue
        tmpl = doc.get("spec", {}).get("template", {}).get("spec", {})
        if not isinstance(tmpl, dict):
            continue
        srcs = tmpl.get("sources") or ([tmpl["source"]] if "source" in tmpl else [])
        for s in srcs:
            if isinstance(s, dict) and s.get("directory") and s.get("kustomize"):
                print("%s: a source sets both directory and kustomize" % f)
PY
)
    if [[ -z "$out" ]]; then
        pass "no ApplicationSet source sets both directory and kustomize"
    else
        while IFS= read -r line; do [[ -n "$line" ]] && hard_fail "$line"; done <<< "$out"
    fi
}
