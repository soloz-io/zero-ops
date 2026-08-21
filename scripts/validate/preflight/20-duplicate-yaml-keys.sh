#!/usr/bin/env bash
# YAML is last-wins on duplicate keys, silently. A second `annotations:` block in
# the same mapping discards the first with no parser warning and no admission
# error: the object applies cleanly, just without the keys that were written.
# This class of defect is invisible in every downstream signal.
validate_duplicate_yaml_keys() {
    section "Duplicate YAML keys (silent last-wins)"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 - <<'PY'
import glob, yaml

class DupCheck(yaml.SafeLoader):
    pass

def no_dupes(loader, node, deep=False):
    seen, bad = set(), []
    for k, _ in node.value:
        key = loader.construct_object(k, deep=deep)
        if key in seen:
            bad.append(key)
        seen.add(key)
    if bad:
        raise yaml.constructor.ConstructorError(
            None, None, f"duplicate key(s) {bad} at line {node.start_mark.line + 1}")
    return yaml.SafeLoader.construct_mapping(loader, node, deep)

DupCheck.add_constructor(yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG, no_dupes)

roots = ["manifests/hub-core-services", "manifests/spoke", "manifests/environments",
         "manifests/providers", "manifests/argocd"]
for r in roots:
    for f in sorted(glob.glob(r + "/**/*.yaml", recursive=True)):
        try:
            with open(f) as fh:
                list(yaml.load_all(fh, DupCheck))
        except yaml.constructor.ConstructorError as e:
            print(f"{f}: {e.problem}")
        except Exception:
            # Helm templates and other non-parseable files are out of scope here;
            # the render module already proves they produce valid output.
            pass
PY
)
    if [[ -z "$out" ]]; then
        pass "no duplicate keys in tracked manifests"
    else
        while IFS= read -r line; do
            [[ -n "$line" ]] && hard_fail "duplicate YAML key — $line"
        done <<< "$out"
    fi
}
