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
{{/*
The cluster this chart is rendering for. Used as the default external-dns TXT
owner id, which must be unique per box.
*/}}
{{- define "environment-manager.clusterName" -}}
{{- .Values.clusterName | default (printf "%s-%s" .Values.environmentSlug .Values.provider) -}}
{{- end -}}

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

{{/*
The OCI registry holding this box's workload charts.
Derived from the git organisation rather than configured separately: a tenant
publishes its charts beside its repositories, and two settings that must agree
are two settings that can disagree (ADR-073).

No scheme. ArgoCD strips oci:// when it normalises a repository URL, so a
credential recorded with one matches nothing and fails as a 401 on a chart that
is certainly present.
*/}}
{{- define "environment-manager.chartRegistryURL" -}}
{{- $org := last (splitList "/" (include "environment-manager.gitOrgURL" .)) -}}
{{- printf "ghcr.io/%s" $org -}}
{{- end -}}

{{- define "environment-manager.globalValues" -}}
global:
  environmentSlug: {{ .Values.environmentSlug | quote }}
  provider: {{ .Values.provider | quote }}
  hubDomain: {{ include "environment-manager.hubDomain" . | quote }}
  gitOrgURL: {{ include "environment-manager.gitOrgURL" . | quote }}
  chartRegistryURL: {{ include "environment-manager.chartRegistryURL" . | quote }}
  dns:
    {{- /* The DNS SERVICE this box's zone lives in -- a platform-level choice,
           not external-dns's --provider flag. "hetzner" means Hetzner DNS,
           which external-dns reaches through a webhook sidecar; the translation
           to `--provider=webhook` happens where the flag is written, in
           external-dns's templated-fields.yaml. */}}
    provider: {{ .Values.dns.provider | default "hetzner" | quote }}
    {{- /* The TXT ownership key. Defaults to the cluster name because
           --policy=sync makes a shared key destructive: external-dns deletes
           the records it believes it owns, so two boxes sharing one would
           delete each other's. */}}
    ownerId: {{ .Values.dns.ownerId | default (include "environment-manager.clusterName" .) | quote }}
  {{- /* The spoke this box's burst-capacity autoscaler watches. A global for the
         same reason as hubDomain: it is a fact about the box, and the
         alternative was the component carrying one box's spoke name as a
         literal. */}}
  burstSpokePool: {{ include "environment-manager.burstSpokePool" . | quote }}
{{- end -}}

{{/*
The same globals, with `provider` resolved to the ADR-046 §11 placement class.

For the three stateful components the variant selector must follow where the
DATA is placed, not what the box is: the subchart resolves files/<env>-<provider>
then files/<env> then files/<provider>, and a hybrid box placing its data on-prem
would otherwise render the cloud overlay and pin a storage class its nodes cannot
provision (ADR-075).

Those elements used to emit globalValues and then restate `provider:` under the
same `global:` mapping. It produced the right answer only because YAML takes the
last of a duplicated key -- a correctness that depended on emission order, in a
document nothing parsed strictly. One emitter, so the override cannot be undone
by moving a line.
*/}}
{{- define "environment-manager.placementGlobalValues" -}}
{{- $g := fromYaml (include "environment-manager.globalValues" .) -}}
{{- $_ := set $g.global "provider" (include "environment-manager.placementClass" .) -}}
{{- toYaml $g -}}
{{- end -}}
