{{/*
Names and labels. These are a COMPATIBILITY CONTRACT, not style.

  <component>-workload      the Rollout and Service. The platform's gateway
                            derives bff-workload and sdk-workload from the tenant
                            axes; a sibling's policy selects app: <component>.
  <component>-workload-sa   the ServiceAccount.
  app: <component>          with workload-class, the Rollout SELECTOR, which is
  workload-class            immutable: changing it orphans running ReplicaSets.

Both the BFF and SDK charts this replaces already used exactly these, so moving
an app onto this chart replaces objects in place.

All functions take a dict: { ctx: <chart root>, v: <resolved values> }.
*/}}
{{- define "platform-workload.workload" -}}{{ .v.component }}-workload{{- end -}}
{{- define "platform-workload.serviceAccount" -}}{{ .v.component }}-workload-sa{{- end -}}

{{- define "platform-workload.selectorLabels" -}}
app: {{ .v.component }}
workload-class: {{ .v.class }}
{{- end -}}

{{/*
tenant-id and cost-center are required at admission (enforce-tenant-abi,
require-cost-labels); tenant-id is also what the platform's DNS and identity
egress select on, so a pod without it cannot resolve or fetch the issuer's keys.
*/}}
{{- define "platform-workload.identityLabels" -}}
tenant-id: {{ .ctx.Values.tenantId }}
app-id: {{ .ctx.Values.appId }}
cost-center: {{ .ctx.Values.costCenter }}
{{- end -}}

{{- define "platform-workload.labels" -}}
{{ include "platform-workload.selectorLabels" . }}
app.kubernetes.io/name: {{ .ctx.Chart.Name }}
app.kubernetes.io/component: {{ .v.component }}
app.kubernetes.io/managed-by: {{ .ctx.Release.Service }}
{{ include "platform-workload.identityLabels" . }}
{{- end -}}

{{/*
Joined with @: the spoke's Kyverno policy admits only `*@sha256:*` images.
The digest is the chart's appVersion, stamped by CI; workload.image.digest pins
one by hand.
*/}}
{{- define "platform-workload.image" -}}
{{- printf "%s@%s" .v.image.repository (.v.image.digest | default .ctx.Chart.AppVersion) -}}
{{- end -}}

{{- define "platform-workload.migrationName" -}}{{ .ctx.Chart.Name }}-migration{{- end -}}
