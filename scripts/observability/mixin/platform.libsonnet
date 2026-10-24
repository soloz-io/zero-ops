// The platform mixin (ADR-078 add.2 §2, add.3 §4): pinned upstream mixins,
// configured ONCE for this platform. Job names are the conventional ones the
// collection floor uses; `cluster` selects the cluster in every dashboard.
local kubernetes = import 'kubernetes-mixin/mixin.libsonnet';

kubernetes {
  _config+:: {
    cadvisorSelector: 'job="cadvisor"',
    kubeletSelector: 'job="kubelet"',
    kubeStateMetricsSelector: 'job="kube-state-metrics"',
    nodeExporterSelector: 'job="node-exporter"',
    showMultiCluster: true,
    clusterLabel: 'cluster',
  },
}
