#!/usr/bin/env bash
# Assemble the environment-manager chart the way a released bundle carries it.
#
# The component descriptors under manifests/argocd/components/ are NOT committed
# inside the chart (see bundle-chart.sh: a committed second copy is the drift
# ADR-063 exists to refuse). The chart reads them from `descriptors/<boundary>/`,
# so `helm template` run against the source tree finds none of them.
#
# That mattered once the validators started checking descriptors. Rendering the
# source chart shows a validator zero descriptors, so both of them fell back to
# reading the descriptor FILE and parsing its helmValues as plain YAML -- which
# it is not. _distribution.tpl runs every descriptor's helmValues through `tpl`,
# so a descriptor may carry Helm actions, and one that does (platform-argocd
# includes the cloud-control placement) parses as YAML in neither position:
# a `{{- include }}` on its own line is a syntax error, and the checks that
# raw-parse it report the value as absent rather than as unreadable.
#
# Staging here means every validator renders what the tenant actually installs.

# stage_bundle_chart <src-chart-dir> <dest-dir>
stage_bundle_chart() {
  local src="$1" dest="$2"
  rm -rf "$dest"
  mkdir -p "$(dirname "$dest")"
  cp -R "$src" "$dest"
  rm -rf "$dest/descriptors"

  local boundary_dir boundary
  for boundary_dir in manifests/argocd/components/*/; do
    [ -d "$boundary_dir" ] || continue
    boundary=$(basename "$boundary_dir")
    mkdir -p "$dest/descriptors/$boundary"
    cp "$boundary_dir"*.yaml "$dest/descriptors/$boundary/" 2>/dev/null || true
  done

  mkdir -p "$dest/values"
  cp manifests/hub-core-services/identity/zitadel/values.yaml "$dest/values/zitadel.yaml"
}
