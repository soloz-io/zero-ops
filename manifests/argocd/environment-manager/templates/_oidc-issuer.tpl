{{/*
This box's OIDC issuer, and the JWKS endpoint that goes with it.

DERIVED from the box's own hub domain, not declared. ADR-051 derives every
public hostname from the base domain, and the identity host is `id.<hubDomain>`
-- the same rule the Day-0 CLI applies when it seeds a cluster
(bootstrap/seed.go: "https://" + DeriveHubEndpoints(zone).ID). Deriving it in
both places from one input is what keeps them from disagreeing.

It was a seed PARAMETER only, and the parameter is not reliably present. A box
whose seed Application is declared in its own repository -- which is every box
now, bundle.yaml being the seed (ADR-072) -- carries `bundleVersion` and nothing
else, because the tenant's file is the one that survives. So the chart received
an empty issuer and every fleet failed to render:

  execution error at (universal-tenant/templates/agentgateway.yaml:13:15):
  oidcIssuer must be supplied by the environment

That surfaced on the FLEET's Application, naming a template in a chart the
tenant does not own, for a value the tenant was never asked for. The namespace
it would have created was therefore never created either, so the workload
Application beside it failed on "namespaces tenant-<fleet> not found" -- a
second, unrelated-looking error from the same missing value.

An explicit oidcIssuer still wins. A box federating to an issuer that is not its
own -- a shared corporate IdP -- sets it and this derivation stands aside.
*/}}
{{- define "environment-manager.oidcIssuer" -}}
{{- if .Values.oidcIssuer -}}
{{- .Values.oidcIssuer -}}
{{- else -}}
{{- printf "https://id.%s" (include "environment-manager.hubDomain" .) -}}
{{- end -}}
{{- end -}}

{{/*
The JWKS endpoint.

Zitadel publishes its signing keys at /oauth/v2/keys and returns 404 for the
conventional /.well-known/jwks.json. Confirmed against this box's own discovery
document, which advertises jwks_uri as https://id.<domain>/oauth/v2/keys.

Left to a chart default, the conventional suffix is appended to a Zitadel issuer
and token validation fails with

  Token validation failed: Expected 200 OK from the JSON Web Key Set HTTP response

which names neither the path nor the provider and reads as an unreachable
issuer. Derived from the same value as the issuer above so the two cannot
disagree.
*/}}
{{- define "environment-manager.oidcJwksUrl" -}}
{{- if .Values.oidcJwksUrl -}}
{{- .Values.oidcJwksUrl -}}
{{- else -}}
{{- printf "%s/oauth/v2/keys" (include "environment-manager.oidcIssuer" .) -}}
{{- end -}}
{{- end -}}
