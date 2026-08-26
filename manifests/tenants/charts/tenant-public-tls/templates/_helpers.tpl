{{/* Sanitize a hostname into a valid Gateway listener (SectionName) name:
     dots become dashes; result is alphanumeric+dashes, ≤63 chars. */}}
{{- define "tenant-public-tls.listenerName" -}}
{{- . | replace "." "-" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "tenant-public-tls.certificateName" -}}
{{- printf "%s-public-tls" .Values.tenantId -}}
{{- end -}}

{{- define "tenant-public-tls.secretName" -}}
{{- printf "%s-public-tls" .Values.tenantId -}}
{{- end -}}

{{/* Hard guards: the chart refuses to render without tenant identity and an
     explicitly supplied environment issuer. Missing values must FAIL, never
     silently inherit — staging is an ephemeral-cluster policy, not a default. */}}
{{- define "tenant-public-tls.requirements" -}}
{{- if not .Values.tenantId -}}
{{- fail "tenantId is required (fleet-registry value)" -}}
{{- end -}}
{{- if not .Values.issuer -}}
{{- fail "issuer is required with no default: environment bootstrap must pass publicTlsIssuer (ephemeral=letsencrypt-staging, dev/stg/prod=letsencrypt-prod)" -}}
{{- end -}}
{{- range .Values.public.hosts -}}
{{- if hasPrefix "*." . -}}
{{- fail (printf "wildcard hostname %q declared: wildcard certificates are unissuable under the HTTP-01 solver model (ADR-051)" .) -}}
{{- end -}}
{{- end -}}
{{- end -}}
