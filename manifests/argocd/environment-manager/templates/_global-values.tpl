{{/*
The globals every platform-owned component receives.

One definition, because nine hand-written copies is how a component ends up
without one. hubDomain was added to two of them and missed in the rest, so the
HubEnvironment -- the object ADR-051 makes the authority for the base domain --
rendered spec.domain empty and the API server rejected it: "spec.domain in body
should match ...". Six boundaries had already deployed.

Extra globals are ignored by a chart that does not read them, so passing the
same set everywhere costs nothing and removes the question of which component
needs which.

Takes the root context.
*/}}
{{- define "environment-manager.globalValues" -}}
global:
  environmentSlug: {{ .Values.environmentSlug | quote }}
  provider: {{ .Values.provider | quote }}
  hubDomain: {{ include "environment-manager.hubDomain" . | quote }}
{{- end -}}
