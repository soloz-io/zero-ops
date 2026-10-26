{{/*
The workload's own CiliumNetworkPolicy: explicit allowlists (ADR-021).

What the platform already grants every pod carrying tenant-id is NOT restated:
cluster DNS (tenant-dns-egress) and the issuer's endpoints for the JWKS
(tenant-identity-endpoint-egress). Declaring DNS per chart is what let one
datapath change stop every workload resolving at once (2026-09-24).

  allowToSameApp      [{app: sdk, port: 3000}]  egress to a sibling component of
                                                this application, in this namespace
  allowFromSameApp    [{app: bff, port: 3000}]  ingress from one
  sharedPostgres      true                      egress to the spoke's shared
                                                Postgres (platform-data, 5432)
  allowLifecycleCallbacks true                  ingress from the platform's
                                                EphemeralJob operator, for an SDK
                                                that creates EphemeralJobs (ADR-052)
  egress / ingress    raw Cilium rules, for anything else; each is a reviewed
                      exception

INGRESS IS OPT-IN, AND DECLARING ANY OF IT IS A DECISION. An `ingress` section
flips the pod from default-allow to default-deny, and a denied packet is
dropped without an RST, so a caller that is not admitted hangs rather than
being refused. The universal-tenant chart's cross-app policy did this twice: to
an app's own BFF (2026-09-30) and to the EphemeralJob operator's callbacks
(2026-10-03). Before declaring ingress, list every caller.

Nothing is rendered when no rule is declared.
*/}}
{{- define "platform-workload.networkpolicy" -}}
{{- $v := .v -}}
{{- $np := $v.networkPolicy -}}
{{- $ns := .ctx.Release.Namespace -}}
{{- $hasIngress := or $np.allowFromSameApp $np.ingress $np.allowLifecycleCallbacks -}}
{{- $hasEgress := or $np.allowToSameApp $np.sharedPostgres $np.egress -}}
{{- if or $hasIngress $hasEgress }}
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: {{ $np.name | default (printf "%s-policy" (include "platform-workload.workload" .)) }}
  labels:
    {{- include "platform-workload.labels" . | nindent 4 }}
  annotations:
    argocd.argoproj.io/sync-options: SkipDryRunOnMissingResource=true
spec:
  endpointSelector:
    matchLabels:
      {{- include "platform-workload.selectorLabels" . | nindent 6 }}
  {{- if $hasIngress }}
  ingress:
    {{- range $np.allowFromSameApp }}
    - fromEndpoints:
        - matchLabels:
            app: {{ required "networkPolicy.allowFromSameApp[].app is required" .app }}
            k8s:io.kubernetes.pod.namespace: {{ $ns }}
      toPorts:
        - ports:
            - port: {{ .port | default $v.ports.http | quote }}
              protocol: TCP
    {{- end }}
    {{- if $np.allowLifecycleCallbacks }}
    - fromEndpoints:
        - matchLabels:
            app: ephemeral-job-operator
            k8s:io.kubernetes.pod.namespace: platform-ops
      toPorts:
        - ports:
            - port: {{ $v.ports.http | quote }}
              protocol: TCP
    {{- end }}
    {{- with $np.ingress }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
  {{- end }}
  {{- if $hasEgress }}
  egress:
    {{- range $np.allowToSameApp }}
    - toEndpoints:
        - matchLabels:
            app: {{ required "networkPolicy.allowToSameApp[].app is required" .app }}
            k8s:io.kubernetes.pod.namespace: {{ $ns }}
      toPorts:
        - ports:
            - port: {{ required "networkPolicy.allowToSameApp[].port is required" .port | quote }}
              protocol: TCP
    {{- end }}
    {{- if $np.sharedPostgres }}
    {{- include "platform-workload.sharedPostgresEgress" . | nindent 4 }}
    {{- end }}
    {{- with $np.egress }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
  {{- end }}
{{- end }}
{{- end -}}

{{/* The spoke's shared Postgres, by its CNPG cluster label. Pooler and primary share it. */}}
{{- define "platform-workload.sharedPostgresEgress" -}}
- toEndpoints:
    - matchLabels:
        k8s:cnpg.io/cluster: shared-cnpg
        k8s:io.kubernetes.pod.namespace: platform-data
  toPorts:
    - ports:
        - port: "5432"
          protocol: TCP
{{- end -}}
