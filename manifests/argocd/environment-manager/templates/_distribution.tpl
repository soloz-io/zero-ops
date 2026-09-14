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
{{- /* ONE emitter, deliberately. This block used to restate the globals for the
       released path, and the two copies drifted: `dns` and `gitOrgURL` were
       added to globalValues and never here, so every released box rendered
       external-dns with `--provider=` and `--txt-owner-id=` empty and it
       crash-looped on "enum value must be one of ..., got ''". The unreleased
       path was correct throughout, which is why it was not noticed.

       A global is a fact about the box; which code path assembles the
       Application cannot change what is true of it.

       NOTE the tag below is `{{` and not `{{-`: a chomping tag swallows the
       newline that separates `enabled: true` from `global:`, and the two run
       together into `enabled: trueglobal:` -- which helm reports four layers
       away as "error converting YAML to JSON: yaml: line 2: mapping values are
       not allowed in this context". */}}
{{ include "environment-manager.globalValues" .root }}
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
