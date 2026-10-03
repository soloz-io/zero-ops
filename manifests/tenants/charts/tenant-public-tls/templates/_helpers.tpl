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

{{- /*
  The paths this application serves WITHOUT a login (gateway.publicAssets.paths),
  validated, as a JSON list. Empty when none are declared.

  For static files a browser fetches outside the user's session -- a web app
  manifest and its icons, a service worker. The rules below are what keep the
  setting to that:

    - EXACT paths only: no wildcard, no regex, no query, no trailing slash, and
      no character outside [A-Za-z0-9._~-] in a segment. A prefix would make
      whatever is added under it later public by accident.
    - never "/" or "/index.html" (the app shell), and nothing whose first
      segment is api, internal, v1, oauth, health or healthz (case-insensitive):
      those are the APIs, the webhook surface, the login flow and the probes.
    - no "." or ".." segment, no duplicates, at most 32 paths (each renders two
      Gateway API matches, GET and HEAD, and a rule holds at most 64).

  The SAME rules are defined in universal-tenant and tenant-public-tls, because
  both charts render from the one per-app values file and must refuse the same
  input: the gateway bind and the public route that reaches it. preflight
  101-public-assets checks the two still agree.
*/ -}}
{{- define "tenant-public-tls.publicAssetPaths" -}}
{{- $paths := ((.Values.gateway).publicAssets).paths | default list -}}
{{- if not (kindIs "slice" $paths) -}}
{{- fail "gateway.publicAssets.paths must be a list of exact paths, e.g. [/manifest.json, /sw.js]" -}}
{{- end -}}
{{- if gt (len $paths) 32 -}}
{{- fail (printf "gateway.publicAssets.paths declares %d paths; at most 32 are allowed" (len $paths)) -}}
{{- end -}}
{{- $seen := dict -}}
{{- range $p := $paths -}}
{{- if not (kindIs "string" $p) -}}
{{- fail (printf "gateway.publicAssets.paths entry %v is not a string" $p) -}}
{{- end -}}
{{- if not (regexMatch "^(/[A-Za-z0-9._~-]+)+$" $p) -}}
{{- fail (printf "gateway.publicAssets.paths entry %q is not an exact path: it must start with /, have no trailing slash, wildcard, query or regex, and use only [A-Za-z0-9._~-] in each segment" $p) -}}
{{- end -}}
{{- $segments := splitList "/" (trimPrefix "/" $p) -}}
{{- range $s := $segments -}}
{{- if or (eq $s ".") (eq $s "..") -}}
{{- fail (printf "gateway.publicAssets.paths entry %q contains a %q segment" $p $s) -}}
{{- end -}}
{{- end -}}
{{- if has (lower (first $segments)) (list "api" "internal" "v1" "oauth" "health" "healthz") -}}
{{- fail (printf "gateway.publicAssets.paths entry %q is under /%s, which always requires a login" $p (first $segments)) -}}
{{- end -}}
{{- if eq (lower $p) "/index.html" -}}
{{- fail "gateway.publicAssets.paths may not include /index.html: the app shell always requires a login" -}}
{{- end -}}
{{- if hasKey $seen $p -}}
{{- fail (printf "gateway.publicAssets.paths lists %q more than once" $p) -}}
{{- end -}}
{{- $_ := set $seen $p true -}}
{{- end -}}
{{- toJson $paths -}}
{{- end -}}
