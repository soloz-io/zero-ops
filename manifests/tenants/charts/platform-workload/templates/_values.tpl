{{/*
The workload's values, defaults applied and required fields checked, as JSON.

A library chart's own values.yaml never reaches the chart that depends on it
(its values would nest under `platform-workload:`), so the defaults live here and
are merged under the app's `workload:` block. Everything the templates read goes
through this one place, so a default is written once.

Root keys, unchanged from the charts this replaces: tenantId, appId, costCenter
(admission requires all three: enforce-tenant-abi, require-cost-labels).
*/}}
{{- define "platform-workload.defaults" -}}
class: stateless-web
image:
  repository: ""
  digest: ""
  pullSecret: ghcr-pull-secret
replicas: 1
canarySteps:
  - setWeight: 100
command: []
ports:
  http: 3000
  metrics: null
probes:
  # Liveness never depends on anything outside the process: an issuer or
  # database outage must not restart every healthy pod.
  liveness:
    path: /health
    initialDelaySeconds: 15
    periodSeconds: 10
    failureThreshold: 3
  # Readiness carries the dependencies (ADR-022): bffAuth's `readiness` serves
  # this path, 503 until the issuer's keys are held.
  readiness:
    path: /health/ready
    initialDelaySeconds: 5
    periodSeconds: 5
    failureThreshold: 3
  startup: null
resources:
  requests: {cpu: 50m, memory: 96Mi}
  limits: {cpu: 500m, memory: 256Mi}
env: []
envFrom: []
extraVolumes: []
extraVolumeMounts: []
podLabels: {}
terminationGracePeriodSeconds: 30
spreadAcrossNodes: true
serviceAccount:
  automountToken: false
networkPolicy:
  name: ""
  allowFromSameApp: []
  allowToSameApp: []
  sharedPostgres: false
  allowLifecycleCallbacks: false
  ingress: []
  egress: []
migration:
  enabled: false
  strategy: presync-hook
  image: ""
  command: []
  env: []
  envFrom: []
  sqlFiles: ""
  podSecurityContext: {}
  backoffLimit: 2
  activeDeadlineSeconds: 240
  resources:
    requests: {cpu: 50m, memory: 64Mi}
    limits: {cpu: 250m, memory: 256Mi}
{{- end -}}

{{- define "platform-workload.values" -}}
{{- $d := include "platform-workload.defaults" . | fromYaml -}}
{{- $v := mergeOverwrite $d (deepCopy (.Values.workload | default dict)) -}}
{{- range $k := list "tenantId" "appId" "costCenter" -}}
{{- if not (index $.Values $k) -}}
{{- fail (printf "%s is required: admission refuses a workload without it" $k) -}}
{{- end -}}
{{- end -}}
{{- if not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$" ($v.component | default "")) -}}
{{- fail (printf "workload.component must name the component, e.g. bff or sdk (got %q). It derives <component>-workload, which the platform gateway and sibling policies address by name" ($v.component | default "")) -}}
{{- end -}}
{{- if not $v.image.repository -}}
{{- fail "workload.image.repository is required" -}}
{{- end -}}
{{- if not (has $v.migration.strategy (list "presync-hook" "hashed-job")) -}}
{{- fail (printf "workload.migration.strategy must be presync-hook or hashed-job (got %q)" $v.migration.strategy) -}}
{{- end -}}
{{- if and $v.migration.enabled (not $v.migration.command) -}}
{{- fail "workload.migration.command is required when workload.migration.enabled" -}}
{{- end -}}
{{- toJson $v -}}
{{- end -}}
