// The platform mixin (ADR-078 add.2 §2, add.3 §4): pinned upstream mixins,
// configured ONCE for this platform. Job names are the conventional ones the
// collection floor uses; `cluster` selects the cluster in every dashboard.
// Full module path: jsonnetfile.json sets legacyImports false, so jb vendors by
// path and creates no short-name links.
local kubernetes = import 'github.com/kubernetes-monitoring/kubernetes-mixin/mixin.libsonnet';

kubernetes {
  _config+:: {
    cadvisorSelector: 'job="cadvisor"',
    kubeletSelector: 'job="kubelet"',
    kubeStateMetricsSelector: 'job="kube-state-metrics"',
    nodeExporterSelector: 'job="node-exporter"',
    showMultiCluster: true,
    clusterLabel: 'cluster',
    // Upstream refreshes every 10s. A multi-day range takes longer than that to
    // answer, so each refresh cancelled the queries before they returned and
    // the panels stayed empty. Data arrives every minute; so does the refresh.
    grafanaK8s+: { refresh: '1m' },
  },
}
