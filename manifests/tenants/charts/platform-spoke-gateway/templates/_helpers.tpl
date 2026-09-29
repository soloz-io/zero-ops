{{/* Sanitize a hostname into a valid Gateway listener (SectionName): dots
     become dashes, alphanumeric+dashes, <= 63 chars.

     IDENTICAL to tenant-public-tls.listenerName, and it must stay identical:
     the HTTPRoute that chart renders attaches to this Gateway, and a listener
     named differently from what the route expects attaches to nothing while
     every status still reports Programmed. */}}
{{- define "platform-spoke-gateway.listenerName" -}}
{{- . | replace "." "-" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "platform-spoke-gateway.requirements" -}}
{{- range .Values.publicHosts -}}
{{- if not .host -}}
{{- fail "publicHosts entry with no host: the aggregate is generated, so an entry missing its hostname means the producer is broken, not the input" -}}
{{- end -}}
{{- if not .tlsSecret -}}
{{- fail (printf "publicHosts entry %q has no tlsSecret: a listener with no certificate cannot terminate TLS, and Gateway API reports that as a listener condition rather than a render error" .host) -}}
{{- end -}}
{{- if hasPrefix "*." .host -}}
{{- fail (printf "wildcard hostname %q: unissuable under the HTTP-01 solver model (ADR-051), and a wildcard certificate covering every app on the spoke is a security boundary this platform has not chosen (ADR-096)" .host) -}}
{{- end -}}
{{- end -}}
{{- end -}}
