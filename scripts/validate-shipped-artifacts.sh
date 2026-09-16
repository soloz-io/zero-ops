#!/usr/bin/env bash
# A per-box fact must not travel in the bundle.
#
# manifests/environments/<env>/ is packaged whole as the hub-environment-<env>
# chart, so every file committed under it is published and applied to every
# tenant's cluster. generated/ inside it is a per-BOX artifact path: Day-0 writes
# this box's own Infisical organisation and project ids there (ADR-045).
#
# Those two facts together shipped the platform's own identity to every tenant.
# Each box ran with Infisical project ids belonging to the platform, which its
# machine identity cannot read, so hub-operator answered every SpokePool
# reconcile with "Project <id> not found" and no spoke PKI was ever created. No
# check failed: a ConfigMap holding the wrong id is as healthy as one holding the
# right one, and the box reported green for as long as it ran.
#
# This is the third per-box fact found baked into a shipped artifact, after the
# DNS zone and the platform-admin OIDC binding. The first two were fixed where
# they were found. This refuses the class.
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

fail=0

# A UUID in a shipped artifact is an identity: organisations, projects and
# machine identities are named by them, and nothing generic is. Slugs are not
# flagged -- "hub-platform" is a convention every box shares, and is the same
# string on all of them.
uuid='[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}'

while IFS= read -r f; do
    [[ -f "$f" ]] || continue
    if grep -qEi "$uuid" "$f"; then
        echo "❌ $f carries an identity and is packaged into the published bundle."
        grep -nEi "$uuid" "$f" | head -3 | sed 's/^/     /'
        fail=1
    fi
done < <(git ls-files 'manifests/environments/*/generated/*.yaml')

if [[ "$fail" -eq 0 ]]; then
    echo "✅ no per-box identity in the shipped environment artifacts"
    exit 0
fi

cat >&2 <<'EOF'

These files are published as part of hub-environment-<env> and applied to every
tenant. A box's own ids come from its own generated artifact, reconciled by its
own Application; this path exists so the kustomization has a patch to apply.

Day-0 fills it in locally. Do not commit what it writes.
EOF
exit 1
