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
{{- fail (printf "placement.class is %q; the platform defines two placement classes (ADR-046 §11): \"hetzner\" (workload-location=hetzner, hcloud-volumes) and \"hybrid\" (workload-location=home, local-path). A class with no overlay behind it would render an Application against a path that does not exist." $class) -}}
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
`workload-location=home`: the local-path provisioner and its StorageClass above
all. Shipping those to a box with no such node is actively harmful rather than
merely wasteful -- the DaemonSet schedules zero pods and leaves a StorageClass
with nothing behind it, so any PVC naming it hangs for ever with no obvious cause.
*/}}
{{- define "environment-manager.onPremCapable" -}}
{{- if or .Values.onPrem.enabled (eq .Values.provider "hybrid") -}}true{{- end -}}
{{- end -}}
