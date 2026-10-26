{{/*
Resolve the ADR-046 §11 placement class for this box's stateful services.

A placement class is a PAIR: a `workload-location` nodeSelector and the storage
class provisionable on nodes carrying it -- `hetzner` with `hcloud-volumes`,
`hybrid` with `local-path`. §11 is explicit that "workload-location is never
sufficient on its own", and the pair is why these are kustomize overlays rather
than chart values: one value picking the location and another picking the storage
would let the two drift, which is the failure §11 was written to ban. So this
resolves ONE name that selects the whole overlay, never the halves.

Defaulting to the provider preserves what every existing box does. Making it
settable is what ADR-075 needs: on-prem nodes are a capability rather than a
provider identity, so a `hetzner` box that has joined on-prem capacity must be
able to place its stateful services there without becoming a `hybrid` box.

The class names are still provider names, and that is debt with a stated end.
`hybrid` here means "the on-prem placement class". ADR-075 withdraws `hybrid` as
a provider once a hetzner box has run on-prem nodes end to end, and these names
are renamed with it -- not before, because the overlay directories, the published
chart variants and the running clusters all carry them today.
*/}}
{{- define "environment-manager.placementClass" -}}
{{- $class := .Values.placement.class | default "" -}}
{{- if not $class -}}
{{- $class = .Values.provider -}}
{{- end -}}
{{- if not (has $class (list "hetzner" "hybrid")) -}}
{{- fail (printf "placement.class is %q; the platform defines two placement classes (ADR-046 §11): \"hetzner\" (workload-location=hetzner, hcloud-volumes) and \"hybrid\" (workload-location=on-prem, local-path). A class with no overlay behind it would render an Application against a path that does not exist." $class) -}}
{{- end -}}
{{- $class -}}
{{- end -}}

{{/*
Whether this box has, or may have, nodes on the tenant's own premises.

True when the on-prem capability is enabled (ADR-075), and true for the hybrid
provider whether or not it sets the value -- a hybrid box has on-prem workers by
definition, and ADR-075 retains that provider until a hetzner box has run the
capability end to end. The `or` is the transition, and it goes when hybrid does.

What it gates is anything that only makes sense where a node can carry
`workload-location=on-prem`: the local-path provisioner and its StorageClass above
all. Shipping those to a box with no such node is actively harmful rather than
merely wasteful -- the DaemonSet schedules zero pods and leaves a StorageClass
with nothing behind it, so any PVC naming it hangs for ever with no obvious cause.
*/}}
{{- define "environment-manager.onPremCapable" -}}
{{- if or .Values.onPrem.enabled (eq .Values.provider "hybrid") -}}true{{- end -}}
{{- end -}}

{{/*
Placement for the platform's own control components on a box with on-prem nodes.

ArgoCD and the CAPI providers are the platform reconciling itself. They talk to
the API server continuously -- leader-election leases, watches, manifest
generation -- and on a hybrid box that traffic crosses VXLAN-over-Tailscale to
the tenant's premises, because the Hetzner control-plane node carries
`node-role.kubernetes.io/control-plane:NoSchedule` and the on-prem worker is the
only untainted node. Everything without a toleration lands there by default.

Measured on acme-hub, 2026-09-16: every CAPI controller, every CAPH controller
and all three ArgoCD components were on the on-prem node. CAPH had restarted 9
times with "failed to renew lease ... timed out waiting for the condition;
leader election lost", ArgoCD's repo-server failed DNS for its own Service, and
Applications reported ComparisonError. Each was read as its own transient fault
for most of a session; they are one fault, and it is placement.

Expressed as NotIn on-prem rather than a control-plane nodeSelector: a hybrid box
may also have Hetzner workers, and those are a correct home for these. The
toleration is what actually makes the control-plane node reachable to the
scheduler; without it the affinity matches a node nothing may schedule on and the
pods stay Pending, which is a worse failure than the one being fixed.

Emits nothing on a box with no on-prem nodes, where every node is already cloud
and the default scheduler is right.
*/}}
{{- define "environment-manager.cloudControlPlacement" -}}
{{- if include "environment-manager.onPremCapable" . -}}
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
        - matchExpressions:
            - key: workload-location
              operator: NotIn
              values: ["on-prem"]
tolerations:
  - key: node-role.kubernetes.io/control-plane
    operator: Exists
    effect: NoSchedule
{{- end }}
{{- end -}}
