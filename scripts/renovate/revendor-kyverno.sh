#!/usr/bin/env bash
set -euo pipefail

# Re-vendor Kyverno Helm chart into Git-spoke manifests.
#
# Called by Renovate postUpgradeTasks after bumping targetRevision.
# Reads the new version from RENOVATE_POST_UPGRADE_COMMAND_DATA_FILE
# (Renovate's upgrade context JSON) or falls back to the AppSet file.
#
# Requires: helm, kustomize, python3 (with pyyaml), sha256sum
#
# Usage:
#   bash scripts/renovate/revendor-kyverno.sh          # auto-detect version
#   bash scripts/renovate/revendor-kyverno.sh 3.3.9    # explicit version

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

if [ $# -ge 1 ]; then
  NEW_VERSION="$1"
else
  DATA_FILE="${RENOVATE_POST_UPGRADE_COMMAND_DATA_FILE:-}"

  if [ -n "$DATA_FILE" ] && [ -f "$DATA_FILE" ]; then
    NEW_VERSION=$(python3 -c "
import json, sys
data = json.load(open('$DATA_FILE'))
for item in data:
    if item.get('depName') == 'kyverno':
        print(item['newValue'])
        sys.exit(0)
print('ERROR: kyverno not found in data file', file=sys.stderr)
sys.exit(1)
")
  else
    APPSET="${PROJECT_ROOT}/manifests/argocd/environment-manager/templates/01-platform-infra-appset.yaml"
    NEW_VERSION=$(grep -A1 "repoURL: 'https://kyverno.github.io/kyverno'" "$APPSET" \
      | grep targetRevision | sed "s/.*targetRevision: '//;s/'//")
  fi
fi

if [ -z "$NEW_VERSION" ]; then
  echo "ERROR: Could not determine new Kyverno version" >&2
  exit 1
fi

echo "==> Re-vendoring Kyverno v${NEW_VERSION}..."

CHART_REPO="https://kyverno.github.io/kyverno"
SPOKE_DIR="${PROJECT_ROOT}/manifests/spoke/spoke-catalog/infra/kyverno"
WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT

# --- Render ---
echo "    Rendering Helm chart..."
# Use curl + local helm template for reliability (helm pull/template --repo
# has intermittent resolution issues with some Helm repos).
CHART_URL="${CHART_REPO}/kyverno-${NEW_VERSION}.tgz"
curl -fsSL "$CHART_URL" -o "$WORK_DIR/kyverno.tgz"
tar -xzf "$WORK_DIR/kyverno.tgz" -C "$WORK_DIR"
helm template kyverno "$WORK_DIR/kyverno" \
  --namespace kyverno \
  --kube-version 1.28.0 \
  --set crds.install=true \
  --set backgroundController.enabled=true \
  --set cleanupController.enabled=true \
  --set reportsController.enabled=true \
  --set webhooksCleanup.enabled=true \
  > "$WORK_DIR/rendered.yaml"

# --- Split CRDs and controller resources (Python, not awk) ---
echo "    Splitting CRDs and controller resources..."
python3 -c "
import yaml, sys

with open('${WORK_DIR}/rendered.yaml') as f:
    docs = list(yaml.safe_load_all(f))

crds = [d for d in docs if d and d.get('kind') == 'CustomResourceDefinition']
others = [d for d in docs if d and d.get('kind') != 'CustomResourceDefinition']

with open('${WORK_DIR}/crds.yaml', 'w') as f:
    yaml.dump_all(crds, f, default_flow_style=False, sort_keys=False)

with open('${WORK_DIR}/controller.yaml', 'w') as f:
    yaml.dump_all(others, f, default_flow_style=False, sort_keys=False)
"

# --- Apply Kustomize patch (sync-wave on CRDs) ---
# Control-plane tolerations are already applied by the root kustomization
# (spoke-catalog/infra/kustomization.yaml lines 98-125), so we only
# add the sync-wave annotation here.
echo "    Applying Kustomize patches..."
cat > "$WORK_DIR/kustomization.yaml" <<EOF
resources:
  - controller.yaml
  - crds.yaml
patches:
  - target:
      kind: CustomResourceDefinition
    patch: |-
      - op: add
        path: /metadata/annotations/argocd.argoproj.io~1sync-wave
        value: "-5"
EOF

cd "$WORK_DIR"
kustomize build . > "$WORK_DIR/patched.yaml"

# --- Re-split after patching ---
echo "    Re-splitting patched output..."
python3 -c "
import yaml

with open('${WORK_DIR}/patched.yaml') as f:
    docs = list(yaml.safe_load_all(f))

crds = [d for d in docs if d and d.get('kind') == 'CustomResourceDefinition']
others = [d for d in docs if d and d.get('kind') != 'CustomResourceDefinition']

with open('${WORK_DIR}/final-crds.yaml', 'w') as f:
    yaml.dump_all(crds, f, default_flow_style=False, sort_keys=False)

with open('${WORK_DIR}/final-controller.yaml', 'w') as f:
    yaml.dump_all(others, f, default_flow_style=False, sort_keys=False)
"

# --- Provenance headers ---
echo "    Updating provenance headers..."
HEADER="# Vendored from helm chart kyverno ${NEW_VERSION} (https://kyverno.github.io/kyverno)"

for f in "$WORK_DIR/final-controller.yaml" "$WORK_DIR/final-crds.yaml"; do
  # Compute hash of the YAML content (before header is added)
  CONTENT_HASH=$(cat "$f" | sha256sum | cut -d' ' -f1)
  DIGEST_LINE="# sha256: ${CONTENT_HASH}"
  YAML_CONTENT=$(cat "$f")
  printf '%s\n%s\n%s\n' "$HEADER" "$DIGEST_LINE" "$YAML_CONTENT" > "$f"
done

# --- Version labels ---
echo "    Updating version labels..."
perl -pi -e "s|app\.kubernetes\.io/version: .*|app.kubernetes.io/version: ${NEW_VERSION}|g" \
  "$WORK_DIR/final-controller.yaml"
perl -pi -e "s|helm\.sh/chart: kyverno-.*|helm.sh/chart: kyverno-${NEW_VERSION}|g" \
  "$WORK_DIR/final-controller.yaml"

# --- Write ---
echo "    Writing vendored files..."
cp "$WORK_DIR/final-controller.yaml" "${SPOKE_DIR}/controller.yaml"
cp "$WORK_DIR/final-crds.yaml" "${SPOKE_DIR}/crd.yaml"

echo "==> Done. Vendored Kyverno v${NEW_VERSION} (sha256: ${DIGEST:0:16}...)"
