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

  /spoke-pool/<cell>/tenants/<tenant>/apps/<app>/KEY

The cell segment already carried the customer while the segment called `tenants`
carried the product -- the inversion ADR-088 is named for. The reading order is
now the containment order.
*/}}
{{- define "universal-tenant.secretPrefix" -}}
/spoke-pool/{{ required "cellId must be supplied" .Values.cellId }}/tenants/{{ include "universal-tenant.tenantId" . }}/apps/{{ include "universal-tenant.appId" . }}
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
