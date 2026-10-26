{{/*
Argo Rollout, not a Deployment: the platform's stateless-web contract (ADR-022)
delivers every workload by canary. The hardening (security context, read-only
root, dropped capabilities, resource limits) is enforced by Kyverno at
admission, so weakening it is rejected by the cluster rather than degraded.
*/}}
{{- define "platform-workload.rollout" -}}
{{- $v := .v -}}
apiVersion: argoproj.io/v1alpha1
kind: Rollout
metadata:
  name: {{ include "platform-workload.workload" . }}
  annotations:
    # The selector is immutable, so ArgoCD deletes and recreates to migrate one
    # rather than attempting an apply the API server refuses.
    argocd.argoproj.io/sync-options: Replace=true
  labels:
    {{- include "platform-workload.labels" . | nindent 4 }}
spec:
  replicas: {{ $v.replicas }}
  selector:
    matchLabels:
      {{- include "platform-workload.selectorLabels" . | nindent 6 }}
  strategy:
    canary:
      steps:
        {{- toYaml $v.canarySteps | nindent 8 }}
  template:
    metadata:
      labels:
        {{- include "platform-workload.labels" . | nindent 8 }}
        {{- with $v.podLabels }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
    spec:
      # Resolve a service FQDN in one query, not five. ndots:5 tries every search
      # domain first, and on this platform one forwards off-cluster: measured
      # from a tenant pod, 30 concurrent lookups took 15.7s against 3.1s, with
      # failures under load surfacing as EAI_AGAIN and application 500s.
      dnsConfig:
        options:
          - name: ndots
            value: "2"
      serviceAccountName: {{ include "platform-workload.serviceAccount" . }}
      automountServiceAccountToken: {{ $v.serviceAccount.automountToken }}
      imagePullSecrets:
        - name: {{ $v.image.pullSecret }}
      {{- if $v.spreadAcrossNodes }}
      # Preferences only: a single replica, or a single node, still schedules.
      topologySpreadConstraints:
        - maxSkew: 1
          topologyKey: kubernetes.io/hostname
          whenUnsatisfiable: ScheduleAnyway
          labelSelector:
            matchLabels:
              {{- include "platform-workload.selectorLabels" . | nindent 14 }}
      {{- end }}
      affinity:
        {{- if $v.spreadAcrossNodes }}
        podAntiAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
            - weight: 100
              podAffinityTerm:
                topologyKey: kubernetes.io/hostname
                labelSelector:
                  matchLabels:
                    {{- include "platform-workload.selectorLabels" . | nindent 20 }}
        {{- end }}
        # Tenant workloads do not run on the control plane.
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
              - matchExpressions:
                  - key: node-role.kubernetes.io/control-plane
                    operator: DoesNotExist
      terminationGracePeriodSeconds: {{ $v.terminationGracePeriodSeconds }}
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        fsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
      # readOnlyRootFilesystem is a platform invariant, so the writable scratch
      # every runtime needs is provided here.
      volumes:
        - name: tmp
          emptyDir: {}
        {{- with $v.extraVolumes }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
      containers:
        - name: {{ .ctx.Chart.Name }}
          image: {{ include "platform-workload.image" . }}
          {{- with $v.command }}
          command:
            {{- toYaml . | nindent 12 }}
          {{- end }}
          # Lets the Service stop routing before the process stops listening.
          lifecycle:
            preStop:
              exec:
                command: ["/bin/sleep", "5"]
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: [ALL]
          volumeMounts:
            - name: tmp
              mountPath: /tmp
            {{- with $v.extraVolumeMounts }}
            {{- toYaml . | nindent 12 }}
            {{- end }}
          ports:
            - containerPort: {{ $v.ports.http }}
              name: http
              protocol: TCP
            {{- if $v.ports.metrics }}
            - containerPort: {{ $v.ports.metrics }}
              name: metrics
              protocol: TCP
            {{- end }}
          {{- with $v.probes.startup }}
          # Separates a slow first boot from a hung process, so the kubelet does
          # not restart a pod that is still starting.
          startupProbe:
            httpGet:
              path: {{ .path | default $v.probes.liveness.path }}
              port: {{ $v.ports.http }}
            {{- toYaml (omit . "path") | nindent 12 }}
          {{- end }}
          livenessProbe:
            httpGet:
              path: {{ $v.probes.liveness.path }}
              port: {{ $v.ports.http }}
            {{- toYaml (omit $v.probes.liveness "path") | nindent 12 }}
          readinessProbe:
            httpGet:
              path: {{ $v.probes.readiness.path }}
              port: {{ $v.ports.http }}
            {{- toYaml (omit $v.probes.readiness "path") | nindent 12 }}
          resources:
            {{- toYaml $v.resources | nindent 12 }}
          {{- with $v.envFrom }}
          envFrom:
            {{- toYaml . | nindent 12 }}
          {{- end }}
          {{- with $v.env }}
          env:
            {{- toYaml . | nindent 12 }}
          {{- end }}
{{- end -}}

{{- define "platform-workload.service" -}}
{{- $v := .v -}}
apiVersion: v1
kind: Service
metadata:
  name: {{ include "platform-workload.workload" . }}
  labels:
    {{- include "platform-workload.labels" . | nindent 4 }}
spec:
  ports:
    - name: http
      port: {{ $v.ports.http }}
      targetPort: {{ $v.ports.http }}
    {{- if $v.ports.metrics }}
    - name: metrics
      port: {{ $v.ports.metrics }}
      targetPort: {{ $v.ports.metrics }}
    {{- end }}
  selector:
    {{- include "platform-workload.selectorLabels" . | nindent 4 }}
{{- end -}}

{{- define "platform-workload.serviceaccount" -}}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ include "platform-workload.serviceAccount" . }}
  labels:
    {{- include "platform-workload.labels" . | nindent 4 }}
automountServiceAccountToken: {{ .v.serviceAccount.automountToken }}
{{- end -}}
