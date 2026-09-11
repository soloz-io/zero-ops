{{/*
Resolve the spoke pool this box's burst-capacity autoscaler watches.

The autoscaler is hub-side infrastructure ABOUT a spoke: the clusterapi provider
needs a client for the workload cluster to see which pods are pending, so the
cluster is named explicitly rather than discovered. That name was a literal in
the Deployment -- spoke-pool-hybrid-dev-01 -- and travelled to every box the
published bundle reached, so a hetzner box ran an autoscaler mounting a
kubeconfig secret for a hybrid spoke it never provisioned. It sat in
ContainerCreating indefinitely, and the pool it did provision had no autoscaler.

Refusing beats defaulting for the same reason as hubDomain: a default would be
some other box's spoke, and the failure it produces is silent -- an autoscaler
that never scales looks like a cluster with nothing to scale.
*/}}
{{- define "environment-manager.burstSpokePool" -}}
{{- $env := .Values.environmentSlug -}}
{{- $provider := .Values.provider -}}
{{- $byEnv := index (.Values.burstSpokePool | default dict) $env | default dict -}}
{{- $pool := index $byEnv $provider | default "" -}}
{{- if not $pool -}}
{{- fail (printf "burstSpokePool has no entry for %s+%s: the burst-capacity autoscaler would be rendered for a spoke this combination never provisions. Add it beside supportedMatrix in the environment-manager values." $env $provider) -}}
{{- end -}}
{{- $pool -}}
{{- end -}}
