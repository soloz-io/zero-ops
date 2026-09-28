{{/*
The two axes, in one place (ADR-088).

`tenantId` is the organisation whose box this is. `appId` is a product running
on it. They were one field until 2026-09-24, which meant the platform could not
tell two products of one customer from two customers -- and resolved that, when
it came up, by putting one product in the other's namespace.

Neither defaults to the other. A chart rendered without `appId` fails here
rather than silently reproducing the collapse it replaces: an appId that falls
back to tenantId is exactly the old model wearing the new field's name.
*/}}

{{- define "universal-tenant.tenantId" -}}
{{- required "tenantId must be supplied: the organisation whose box this is (ADR-088)" .Values.tenantId -}}
{{- end -}}

{{- define "universal-tenant.appId" -}}
{{- required "appId must be supplied: the product this release renders (ADR-088). It does NOT default to tenantId -- that default is the collapse ADR-088 removes." .Values.appId -}}
{{- end -}}

{{/*
The namespace an app's workloads run in.

`tenant-` is kept as the prefix. It distinguishes tenant namespaces from the
platform's own `platform-*` ones at a glance, and dropping it to save five
characters would make `nutgraf-waypoint` indistinguishable from a platform
component by name alone.
*/}}
{{- define "universal-tenant.namespace" -}}
tenant-{{ include "universal-tenant.tenantId" . }}-{{ include "universal-tenant.appId" . }}
{{- end -}}

{{/*
Required on every tenant workload by the spoke's Kyverno ABI
(enforce-tenant-abi). BOTH are required, because attribution needs both: a cost
line against the tenant alone cannot be split between its products, and one
against the product alone cannot be billed to anyone.
*/}}
{{- define "universal-tenant.abiLabels" -}}
tenant-id: {{ include "universal-tenant.tenantId" . }}
app-id: {{ include "universal-tenant.appId" . }}
{{- end -}}

{{/*
Where this app's secret material lives (ADR-031 cell scoping, ADR-087 layout).

  /spoke-pool/<cell>/tenants/<app>/KEY

The segment is the APP, and always has been: before ADR-088 the field feeding it
was called tenantId but held a product. So the CONTENT of this path was already
right and only its label was wrong.

ADR-088 first moved it to /tenants/<tenant>/apps/<app>/, which reads correctly
and requires every existing key to be copied to a new location. That is a data
migration bought for a naming improvement, and it was the riskiest step in a
change that otherwise only renames identifiers -- an ExternalSecret fails as a
WHOLE object (ADR-087), so a partial copy withholds unrelated keys from
unrelated workloads.

Deferred deliberately. The path is corrected on its own, when moving it is the
only thing that can go wrong, rather than on the day the namespaces change too.
*/}}
{{- define "universal-tenant.secretPrefix" -}}
/spoke-pool/{{ required "cellId must be supplied" .Values.cellId }}/tenants/{{ include "universal-tenant.appId" . }}
{{- end -}}

{{/*
This app's logical database on the spoke's shared CloudNativePG cluster.

One per APP, not per tenant. Being siblings grants nothing: two apps of one
customer are as separated in their data as two apps of different customers,
because the namespace is the isolation boundary and a datastore reachable from
two namespaces is a hole in both. Sharing is possible only through an explicit
`sharedData` declaration naming both sides (ADR-088).
*/}}
{{- define "universal-tenant.databaseName" -}}
{{ include "universal-tenant.tenantId" . }}-{{ include "universal-tenant.appId" . }}-db
{{- end -}}

{{/*
The postgres role an app's database credential authenticates as.

THE ONLY PLACE THIS NAME IS BUILT. Every consumer -- the Role that creates it,
the database it owns, the DefaultPrivileges granted on it, the role it is a
member of, and the username templated into both ExternalSecrets -- reads
spec.dbRoleName from the XR. Before this it was rebuilt at six patch sites in the
composition and once more in the hub operator, and the operator built it on a
different axis (tenantId, not appId), so the credential named a role that did not
exist. Nothing compared the two: the only symptom was

  psql: FATAL: password authentication failed for user "tenant-<tenant>-user"

logged by the baseline migration Job, whose visible effect was a fleet with no
users table.

'_' IS A STRUCTURAL DELIMITER, NOT A STYLE CHOICE. A postgres role is
CLUSTER-WIDE -- pg_authid and pg_auth_members are shared catalogs, so two
databases in one cluster cannot hold distinct roles of the same name -- and one
spoke serves several tenants (ADR-051). The name must therefore be injective over
(tenantId, appId). Delimiting with '-' is NOT: '-' is legal inside both
components, so ("foo", "bar-baz") and ("foo-bar", "baz") both render
"foo-bar-baz", and the two tenants would have been handed a single role that owns
both their databases. '_' is forbidden by the identifier grammar
(^[a-z0-9]([-a-z0-9]*[a-z0-9])?$, enforced by the API server), so a name built
with it has exactly one parse.

LENGTH IS A CORRECTNESS PROPERTY. postgres truncates an identifier longer than 63
BYTES to 63 with a NOTICE and then succeeds, so two apps sharing a 51-byte prefix
would collide by truncation -- reintroducing exactly what the delimiter prevents.
The fixed parts cost 13 bytes, leaving 50 for the two components together; the
grammar admits only single-byte ASCII, so characters are bytes and the bound is
exact. Both XRDs carry it as a CEL rule, and this fails first with a message that
names the two lengths.
*/}}
{{- define "universal-tenant.dbRoleName" -}}
{{- $t := include "universal-tenant.tenantId" . -}}
{{- $a := include "universal-tenant.appId" . -}}
{{- if gt (add (len $t) (len $a)) 50 -}}
{{- fail (printf "tenantId (%q, %d chars) and appId (%q, %d chars) total %d, over the 50 available: the postgres role tenant_%s_%s_user would exceed postgres's 63-byte identifier limit, and postgres truncates rather than failing -- which would silently give two apps one role" $t (len $t) $a (len $a) (add (len $t) (len $a)) $t $a) -}}
{{- end -}}
tenant_{{ $t }}_{{ $a }}_user
{{- end -}}
