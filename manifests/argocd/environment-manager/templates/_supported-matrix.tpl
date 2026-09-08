{{/*
Refuse an environment and provider combination the platform does not support.

Included by every boundary template, so the refusal happens whichever one Helm
renders first and no boundary can be reached by a combination the others reject.

The failure is deliberate and named. Before this, an unsupported combination
rendered an Application pointing at a source path that does not exist: ArgoCD
reported it Synced and Healthy while it managed nothing, which is
indistinguishable from a boundary with nothing to do. A tenant would discover it
when the thing it was supposed to create was missing.

Absence is the contract here. A combination is unsupported because no supported
topology was built for it, and saying so at render time is what keeps that a
decision rather than an accident.
*/}}
{{- define "environment-manager.assertSupported" -}}
{{- $env := .Values.environmentSlug -}}
{{- $provider := .Values.provider -}}
{{- $matrix := .Values.supportedMatrix -}}
{{- if not $matrix -}}
{{- fail "supportedMatrix is empty: the chart cannot tell a supported topology from an unsupported one, and would render Applications for combinations that have no sources behind them" -}}
{{- end -}}
{{- /*
Both must be set before a combination can be judged. They have no defaults, and
the boundaries already refuse to render without them, so an unset value here is
`helm lint` with no values rather than a topology claim -- reporting it as an
unsupported combination would be reporting the wrong fault.
*/ -}}
{{- if or (not $env) (not $provider) -}}
{{- else -}}
{{- $allowed := index $matrix $env | default (list) -}}
{{- if not (has $provider $allowed) -}}
{{- $supported := list -}}
{{- range $e, $providers := $matrix -}}
{{- range $p := $providers -}}
{{- $supported = append $supported (printf "%s+%s" $e $p) -}}
{{- end -}}
{{- end -}}
{{- fail (printf "unsupported topology: environment=%s provider=%s. No spoke-pool source is defined for this combination and it is not part of the supported platform matrix. Supported: %s" $env $provider (join ", " (sortAlpha $supported))) -}}
{{- end -}}
{{- end -}}
{{- end -}}
