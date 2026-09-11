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
  # Every platform-owned component, not only the ones that need it today. The
  # api-gateway's OIDC issuer, required audience and JWKS URL are all this
  # domain, and they reached clusters as the platform's own literal because no
  # component had a way to ask. A global costs nothing where it is unused and
  # removes the reason to hard-code it where it is.
  hubDomain: {{ include "environment-manager.hubDomain" .root | quote }}
  # The spoke this box's burst-capacity autoscaler watches. A global for the same
  # reason as hubDomain: it is a fact about the box, and the alternative was the
  # component carrying one box's spoke name as a literal.
  burstSpokePool: {{ include "environment-manager.burstSpokePool" .root | quote }}
{{- end -}}

{{/*
Rewrite a descriptor into an element that resolves the distribution.

Platform-owned content becomes the one chart; a component naming its own
repoURL is third-party and is left alone, because its URL and version are pinned
by the descriptor and move with the bundle rather than being resolved here.
*/}}
{{- define "environment-manager.releasedElement" -}}
{{- $d := .descriptor -}}
{{/*
  Rendered for every component, third-party included. A descriptor's helmValues
  may name a fact about this box -- ArgoCD's external URL is argocd.<hubDomain>,
  and it was a literal pointing at the platform's own. Applied only inside the
  platform-owned branch below, a third-party component never reached it: the
  value passed through verbatim and the cluster received the template text.
*/}}
{{- $_ := set $d "helmValues" (tpl ($d.helmValues | default "") .root) -}}
{{- if not $d.repoURL -}}
{{- $_ := set $d "chart" "platform" -}}
{{- $_ := set $d "path" "" -}}
{{- $_ := set $d "helmValues" (include "environment-manager.distributionValues" (dict "app" $d.appName "values" $d.helmValues "root" .root)) -}}
{{- end -}}
{{- $d | toYaml -}}
{{- end -}}
