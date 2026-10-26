{{/*
The application's schema migration (ADR-099: the platform provisions a database
and a role; everything inside it belongs to the application).

Two strategies, both already in use, each for reasons paid for:

  presync-hook  an ArgoCD PreSync hook, run on every sync, so the command MUST
                be idempotent (drizzle/graphile migrate are). Fixed name;
                BeforeHookCreation replaces the previous Job, HookSucceeded
                removes a successful one, and a FAILED one is kept until the
                next attempt -- HookFailed would delete the pod, its logs and its
                events at the only moment they explain anything (2026-09-24).
  hashed-job    an ordinary Job named by a hash of its input (the SQL files,
                the command, the image): changed input runs, identical input is
                a no-op. For an Application that may already be Synced, where a
                PreSync hook would not fire.

Either way the pod runs with NO ServiceAccount token and needs none: it speaks
only Postgres. Its own egress policy reaches the shared cluster's PRIMARY (the
server certificate names the cluster, not the pooler). Bounded by
activeDeadlineSeconds, because a hanging Job holds its Application in
Progressing forever.

workload.migration:
  command   required; runs in `image` (default: the workload's own image, so the
            schema matches the code that expects it)
  sqlFiles  optional glob in the APP chart (e.g. files/migrations/*.sql),
            mounted read-only at /migrations
  extraVolumes / extraVolumeMounts
            for what the command needs beyond tmp and migrations, e.g. a CA
            bundle from a Secret for a client that verifies the server
            certificate (NODE_EXTRA_CA_CERTS). Names `tmp` and `migrations`
            are taken.
  env, envFrom, podSecurityContext, backoffLimit, activeDeadlineSeconds, resources
*/}}
{{- define "platform-workload.migration" -}}
{{- $v := .v -}}
{{- $m := $v.migration -}}
{{- if $m.enabled }}
{{- $name := include "platform-workload.migrationName" . -}}
{{- $image := $m.image | default (include "platform-workload.image" .) -}}
{{- $sql := "" -}}
{{- if $m.sqlFiles }}{{ $sql = (.ctx.Files.Glob $m.sqlFiles).AsConfig }}{{ end -}}
{{- $hook := eq $m.strategy "presync-hook" -}}
{{- $jobName := $name -}}
{{- if not $hook -}}
{{- $jobName = printf "%s-%s" $name (printf "%s%s%s" $sql (toJson $m.command) $image | sha256sum | trunc 10) -}}
{{- end -}}
{{- $podLabels := dict "app.kubernetes.io/name" $name "workload-class" "migration" -}}
{{- if $m.sqlFiles }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ $name }}s
  labels:
    {{- include "platform-workload.identityLabels" . | nindent 4 }}
    {{- toYaml $podLabels | nindent 4 }}
  annotations:
    {{- if $hook }}
    argocd.argoproj.io/hook: PreSync
    argocd.argoproj.io/hook-delete-policy: BeforeHookCreation
    argocd.argoproj.io/sync-wave: "-6"
    {{- else }}
    argocd.argoproj.io/sync-wave: "-11"
    {{- end }}
data:
  {{- $sql | nindent 2 }}
---
{{- end }}
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: {{ $name }}-egress
  labels:
    {{- include "platform-workload.identityLabels" . | nindent 4 }}
  annotations:
    argocd.argoproj.io/sync-options: SkipDryRunOnMissingResource=true
    {{- if $hook }}
    # A hook too, one wave earlier: a PreSync Job runs before anything in the
    # sync phase, so a policy left there would not exist yet.
    argocd.argoproj.io/hook: PreSync
    argocd.argoproj.io/hook-delete-policy: BeforeHookCreation
    argocd.argoproj.io/sync-wave: "-6"
    {{- else }}
    argocd.argoproj.io/sync-wave: "-11"
    {{- end }}
spec:
  endpointSelector:
    matchLabels:
      {{- toYaml $podLabels | nindent 6 }}
  egress:
    {{- include "platform-workload.sharedPostgresEgress" . | nindent 4 }}
---
apiVersion: batch/v1
kind: Job
metadata:
  name: {{ $jobName }}
  labels:
    {{- include "platform-workload.identityLabels" . | nindent 4 }}
    {{- toYaml $podLabels | nindent 4 }}
  annotations:
    {{- if $hook }}
    argocd.argoproj.io/hook: PreSync
    argocd.argoproj.io/sync-wave: "-5"
    argocd.argoproj.io/hook-delete-policy: BeforeHookCreation,HookSucceeded
    {{- else }}
    argocd.argoproj.io/sync-wave: "-10"
    {{- end }}
spec:
  backoffLimit: {{ $m.backoffLimit }}
  activeDeadlineSeconds: {{ $m.activeDeadlineSeconds }}
  template:
    metadata:
      labels:
        {{- include "platform-workload.identityLabels" . | nindent 8 }}
        {{- toYaml $podLabels | nindent 8 }}
    spec:
      restartPolicy: Never
      automountServiceAccountToken: false
      # Named on the pod: a PreSync hook runs before this chart's ServiceAccount
      # exists, so nothing may be inherited from it.
      imagePullSecrets:
        - name: {{ $v.image.pullSecret }}
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
              - matchExpressions:
                  - key: node-role.kubernetes.io/control-plane
                    operator: DoesNotExist
      securityContext:
        {{- toYaml (mergeOverwrite (dict "runAsNonRoot" true "runAsUser" 65532 "fsGroup" 65532 "seccompProfile" (dict "type" "RuntimeDefault")) $m.podSecurityContext) | nindent 8 }}
      containers:
        - name: migrate
          image: {{ $image }}
          command:
            {{- toYaml $m.command | nindent 12 }}
          {{- with $m.envFrom }}
          envFrom:
            {{- toYaml . | nindent 12 }}
          {{- end }}
          env:
            - name: HOME
              value: /tmp
            {{- with $m.env }}
            {{- toYaml . | nindent 12 }}
            {{- end }}
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: [ALL]
          volumeMounts:
            - name: tmp
              mountPath: /tmp
            {{- if $m.sqlFiles }}
            - name: migrations
              mountPath: /migrations
              readOnly: true
            {{- end }}
            {{- with $m.extraVolumeMounts }}
            {{- toYaml . | nindent 12 }}
            {{- end }}
          resources:
            {{- toYaml $m.resources | nindent 12 }}
      volumes:
        - name: tmp
          emptyDir: {}
        {{- if $m.sqlFiles }}
        - name: migrations
          configMap:
            name: {{ $name }}s
        {{- end }}
        {{- with $m.extraVolumes }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
{{- end }}
{{- end -}}
