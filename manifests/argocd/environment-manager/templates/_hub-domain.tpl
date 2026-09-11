{{/*
Resolve the base domain this box publishes on, refusing to render without one.

ADR-051 makes the environment overlay the system of record for the base domain,
and DeriveHubEndpoints (internal/soloz-cli/bootstrap/hubdomain.go) derives every
public host from it. The components never took part in that: agentgateway's
config carried the platform's own `dev.nutgraf.in` as a literal, so every box
built from a released bundle trusted the PLATFORM's Zitadel as its OIDC issuer
and fetched signing keys from it -- the runtime dependency on the vendor that
ADR-065 and ADR-066 exist to prevent, in the component fronting all traffic.

The refusal is the point, and it is the same argument as instanceRepoURL: there
is no correct default, because whichever domain the chart named would be some
other box's, and a published chart carries its defaults to every cluster that
installs it. A box that omits it must fail to render rather than quietly
authenticate against someone else's identity provider.

Day-0 supplies it: scaffolding knows the tenant's domain, having been given it.
*/}}
{{- define "environment-manager.hubDomain" -}}
{{- $d := .Values.hubDomain -}}
{{- if not $d -}}
{{- fail "hubDomain is not set: this chart cannot tell which domain this box publishes on, and the components that need it would fall back to the platform's own (ADR-051, ADR-065). Scaffolding writes it into clusters/<name>/values.yaml." -}}
{{- end -}}
{{- $d -}}
{{- end -}}
