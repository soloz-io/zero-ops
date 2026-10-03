{{/*
Everything a standard workload renders, from the app chart's root context:

  templates/workload.yaml in the app chart:
    {{ include "platform-workload.all" . }}

ServiceAccount, Service, Rollout, the workload's network policy (when it
declares any rule), and the migration (when enabled). An app that needs one
object rendered differently includes the pieces it keeps instead, each with
(dict "ctx" . "v" (include "platform-workload.values" . | fromJson)).
*/}}
{{- define "platform-workload.all" -}}
{{- $s := dict "ctx" . "v" (include "platform-workload.values" . | fromJson) -}}
{{ include "platform-workload.serviceaccount" $s }}
---
{{ include "platform-workload.service" $s }}
---
{{ include "platform-workload.rollout" $s }}
{{- with include "platform-workload.networkpolicy" $s | trim }}
---
{{ . }}
{{- end }}
{{- with include "platform-workload.migration" $s | trim }}
---
{{ . }}
{{- end }}
{{- end -}}
