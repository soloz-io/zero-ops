{{/*
Values for an Application that resolves the platform distribution.

ADR-063 publishes one artefact, so an Application names `platform` and enables
the component it owns. A component's own values nest under its name, because
that is where a subchart reads them; leaving them at the root would put them
where nothing looks and the component would render with its defaults, silently.

Topology travels as globals so a component that varies by environment or
provider receives them without every Application having to know which components
those are.

Takes: app, values (the component's own, possibly empty), root.
*/}}
{{- define "environment-manager.distributionValues" -}}
{{ .app }}:
  enabled: true
{{- with .values }}
{{ . | indent 2 }}
{{- end }}
global:
  environmentSlug: {{ .root.Values.environmentSlug | quote }}
  provider: {{ .root.Values.provider | quote }}
{{- end -}}

{{/*
Rewrite a descriptor into an element that resolves the distribution.

Platform-owned content becomes the one chart; a component naming its own
repoURL is third-party and is left alone, because its URL and version are pinned
by the descriptor and move with the bundle rather than being resolved here.
*/}}
{{- define "environment-manager.releasedElement" -}}
{{- $d := .descriptor -}}
{{- if not $d.repoURL -}}
{{- $_ := set $d "chart" "platform" -}}
{{- $_ := set $d "path" "" -}}
{{- $_ := set $d "helmValues" (include "environment-manager.distributionValues" (dict "app" $d.appName "values" $d.helmValues "root" .root)) -}}
{{- end -}}
{{- $d | toYaml -}}
{{- end -}}
