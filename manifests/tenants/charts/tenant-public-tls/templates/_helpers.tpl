{{/* Sanitize a hostname into a valid Gateway listener (SectionName) name:
     dots become dashes; result is alphanumeric+dashes, ≤63 chars. */}}
{{- define "tenant-public-tls.listenerName" -}}
{{- . | replace "." "-" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "tenant-public-tls.certificateName" -}}
{{- printf "%s-public-tls" (include "tenant-public-tls.scope" .) -}}
{{- end -}}

{{- define "tenant-public-tls.secretName" -}}
{{- printf "%s-public-tls" (include "tenant-public-tls.scope" .) -}}
{{- end -}}

{{/* Hard guards: the chart refuses to render without tenant identity and an
     explicitly supplied environment issuer. Missing values must FAIL, never
     silently inherit — staging is an ephemeral-cluster policy, not a default. */}}
{{- define "tenant-public-tls.requirements" -}}
{{- if not .Values.appId -}}
{{- fail "appId must be supplied: this chart routes to ONE app's gateway (ADR-088). It does not default to tenantId." -}}
{{- end -}}
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

{{/*
The namespace the app's AgentGateway runs in (ADR-088).

Both axes: the Service is app-named (agentgateway-<app>) but it lives in
tenant-<tenant>-<app>, and this chart renders into the SHARED platform-ops
namespace -- so the objects it creates there need the scope in their NAMES too,
or waypoint's public ingress and oranger's would be one object.
*/}}
{{- define "tenant-public-tls.appNamespace" -}}
tenant-{{ .Values.tenantId }}-{{ .Values.appId }}
{{- end -}}

{{- define "tenant-public-tls.scope" -}}
{{ .Values.tenantId }}-{{ .Values.appId }}
{{- end -}}
