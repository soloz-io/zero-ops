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
{{/*
The git organisation that owns this box's repositories, as scheme://host/org.

ArgoCD matches a repo-creds credential by URL PREFIX, so the organisation covers
every repository this box reads -- the tenant's gitops repository and any other
in the same organisation -- with one credential.

Derived from instanceRepoURL rather than carried separately: two values naming
one organisation is two things to keep in step, and the one that drifts is the
one nobody looks at.
*/}}
{{- define "environment-manager.gitOrgURL" -}}
{{- $repo := .Values.instanceRepoURL | default "" | trimSuffix ".git" -}}
{{- if hasPrefix "git@" $repo -}}
{{- $repo = printf "https://%s" (replace ":" "/" (trimPrefix "git@" $repo)) -}}
{{- end -}}
{{- $parts := splitList "/" $repo -}}
{{- /* else, not a bare guard: `fail` does not stop evaluation here, so a bare
       guard still reaches the slice below and panics with "slice index out of
       bounds" -- burying the message that says what is actually wrong. */ -}}
{{- if lt (len $parts) 4 -}}
{{- fail (printf "cannot read an organisation out of instanceRepoURL %q: expected https://host/org/repo. ArgoCD needs it to hold one credential for every repository in this box's organisation (ADR-062)." $repo) -}}
{{- else -}}
{{- join "/" (slice $parts 0 4) -}}
{{- end -}}
{{- end -}}

{{- define "environment-manager.globalValues" -}}
global:
  environmentSlug: {{ .Values.environmentSlug | quote }}
  provider: {{ .Values.provider | quote }}
  hubDomain: {{ include "environment-manager.hubDomain" . | quote }}
  gitOrgURL: {{ include "environment-manager.gitOrgURL" . | quote }}
{{- end -}}
