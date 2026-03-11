package controller

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/go-logr/logr"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"cnpg2monitor/pkg/config"
)

type observedState struct {
	cluster         *cnpgv1.Cluster
	podMonitor      *monitoringv1.PodMonitor
	podMonitorErr   error
	topologyLabels  map[string]string
	topologyErr     error
	skipReason      string
}

type desiredActions struct {
	patchPodMonitor bool
	emitEvent       bool
	eventReason     string
	eventMessage    string
}

const (
	MonitoredLabel                    = "zero-ops.io/monitored"
	TopologyLabelPrefix               = "zero-ops.io/"
	CNPGClusterLabel                  = "cnpg.io/cluster"
	LastScaledInstancesAnnotation     = "cnpg2monitor.zero-ops.io/last-scaled-instances"
	LastConfigGenerationAnnotation    = "cnpg2monitor.zero-ops.io/last-config-generation"
	LastStorageGenerationAnnotation   = "cnpg2monitor.zero-ops.io/last-storage-generation"
)

type Cnpg2Monitor struct {
	client.Client
	Log           logr.Logger
	Scheme        *runtime.Scheme
	Recorder      record.EventRecorder
	SyncPeriod    time.Duration
	Config        config.Config
	retryCounters sync.Map // map[types.NamespacedName]int
}

func (r *Cnpg2Monitor) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cnpgv1.Cluster{}, builder.WithPredicates(
			predicate.NewPredicateFuncs(func(obj client.Object) bool {
				labels := obj.GetLabels()
				return labels != nil && labels[MonitoredLabel] == "true"
			}))).
		Watches(&corev1.Namespace{}, handler.EnqueueRequestsFromMapFunc(
			r.mapNamespaceToCluster)).
		Watches(&monitoringv1.PodMonitor{}, handler.EnqueueRequestsFromMapFunc(
			r.mapPodMonitorToCluster)).
		Complete(r)
}

func (r *Cnpg2Monitor) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("cnpgCluster", req.NamespacedName)
	log.Info("Starting reconciliation")
	
	// Phase 1: Observe - fetch all state, no writes
	state, err := r.observe(ctx, req)
	if err != nil {
		return ctrl.Result{}, err
	}
	
	if state.cluster == nil {
		return r.handleDeletion(ctx, req.NamespacedName)
	}
	
	// Phase 2: Analyze - pure function, determines desired actions
	actions := r.analyze(state)
	
	// Phase 3: Act - execute writes based on actions
	return r.act(ctx, state, actions)
}

func (r *Cnpg2Monitor) observe(ctx context.Context, req ctrl.Request) (*observedState, error) {
	// Fetch CNPG cluster
	cluster := &cnpgv1.Cluster{}
	if err := r.Client.Get(ctx, req.NamespacedName, cluster); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return nil, err
		}
		return &observedState{cluster: nil}, nil
	}
	
	// Early validation
	if !r.hasMonitoringLabel(cluster) {
		return &observedState{cluster: cluster, skipReason: "missing monitoring label"}, nil
	}
	
	// Find PodMonitor and topology labels
	podMonitor, err := r.findPodMonitor(ctx, cluster)
	topologyLabels, err2 := r.getTopologyLabels(ctx, cluster.Namespace)
	
	return &observedState{
		cluster:        cluster,
		podMonitor:     podMonitor,
		podMonitorErr:  err,
		topologyLabels: topologyLabels,
		topologyErr:    err2,
	}, nil
}

func (r *Cnpg2Monitor) analyze(state *observedState) desiredActions {
	if state.skipReason != "" {
		return desiredActions{}
	}
	
	if state.podMonitorErr != nil {
		return desiredActions{
			emitEvent:    true,
			eventReason:  "CNPGPodMonitorNotFound",
			eventMessage: "CNPG PodMonitor not found, will retry",
		}
	}
	
	if state.topologyErr != nil {
		return desiredActions{
			emitEvent:    true,
			eventReason:  "CNPGTopologyLabelsMissing",
			eventMessage: "Namespace missing required zero-ops.io/* topology labels",
		}
	}
	
	if state.podMonitor != nil && len(state.topologyLabels) > 0 {
		return desiredActions{
			patchPodMonitor: true,
		}
	}
	
	return desiredActions{}
}

func (r *Cnpg2Monitor) act(ctx context.Context, state *observedState, actions desiredActions) (ctrl.Result, error) {
	if actions.emitEvent {
		r.Recorder.Event(state.cluster, corev1.EventTypeWarning, actions.eventReason, actions.eventMessage)
	}
	
	if actions.patchPodMonitor {
		if err := r.patchPodMonitorWithTopology(ctx, state.podMonitor, state.topologyLabels); err != nil {
			return ctrl.Result{}, err
		}
	}
	
	// Emit lifecycle events
	if err := r.emitLifecycleEvents(ctx, state.cluster); err != nil {
		r.Log.Error(err, "Failed to emit lifecycle events")
	}
	
	return ctrl.Result{RequeueAfter: r.SyncPeriod}, nil
}

func (r *Cnpg2Monitor) handleDeletion(ctx context.Context, namespacedName types.NamespacedName) (ctrl.Result, error) {
	r.Log.Info("CNPG Cluster deleted, cleanup handled by CNPG", "cluster", namespacedName)
	return ctrl.Result{}, nil
}

func (r *Cnpg2Monitor) hasMonitoringLabel(cluster *cnpgv1.Cluster) bool {
	labels := cluster.GetLabels()
	return labels != nil && labels[MonitoredLabel] == "true"
}

func (r *Cnpg2Monitor) findPodMonitor(ctx context.Context, cluster *cnpgv1.Cluster) (*monitoringv1.PodMonitor, error) {
	// Use cluster name directly
	clusterName := cluster.Name
	
	// Find PodMonitor by cluster name
	podMonitorList := &monitoringv1.PodMonitorList{}
	if err := r.List(ctx, podMonitorList, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, err
	}
	
	for i := range podMonitorList.Items {
		pm := &podMonitorList.Items[i]
		if pm.Spec.Selector.MatchLabels != nil &&
			pm.Spec.Selector.MatchLabels[CNPGClusterLabel] == clusterName {
			return pm, nil
		}
	}
	
	return nil, fmt.Errorf("PodMonitor not found for cluster %s", clusterName)
}

func (r *Cnpg2Monitor) getTopologyLabels(ctx context.Context, namespace string) (map[string]string, error) {
	ns := &corev1.Namespace{}
	if err := r.Get(ctx, types.NamespacedName{Name: namespace}, ns); err != nil {
		return nil, err
	}
	
	topologyLabels := make(map[string]string)
	for k, v := range ns.Labels {
		if strings.HasPrefix(k, r.Config.TopologyLabelPrefix) {
			// Remove prefix for relabeling
			key := strings.TrimPrefix(k, r.Config.TopologyLabelPrefix)
			topologyLabels[key] = v
		}
	}
	
	if len(topologyLabels) == 0 {
		return nil, fmt.Errorf("no topology labels found")
	}
	
	return topologyLabels, nil
}

func (r *Cnpg2Monitor) patchPodMonitorWithTopology(ctx context.Context, podMonitor *monitoringv1.PodMonitor, topologyLabels map[string]string) error {
	// Build relabelings from topology labels
	relabelings := r.buildTopologyRelabelings(topologyLabels)
	
	// Create patch targeting metrics port relabelings
	metricsPort := "metrics"
	patch := &monitoringv1.PodMonitor{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "monitoring.coreos.com/v1",
			Kind:       "PodMonitor",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      podMonitor.Name,
			Namespace: podMonitor.Namespace,
		},
		Spec: monitoringv1.PodMonitorSpec{
			PodMetricsEndpoints: []monitoringv1.PodMetricsEndpoint{{
				Port:           &metricsPort,
				RelabelConfigs: relabelings,
			}},
		},
	}
	
	// Apply patch using Server-Side Apply
	return r.Patch(ctx, patch, client.Apply, client.FieldOwner("cnpg2monitor"), client.ForceOwnership)
}

func (r *Cnpg2Monitor) buildTopologyRelabelings(topologyLabels map[string]string) []monitoringv1.RelabelConfig {
	var relabelings []monitoringv1.RelabelConfig
	
	for key, value := range topologyLabels {
		valuePtr := value
		relabelings = append(relabelings, monitoringv1.RelabelConfig{
			TargetLabel: key,
			Replacement: &valuePtr,
		})
	}
	
	return relabelings
}

func (r *Cnpg2Monitor) emitLifecycleEvents(ctx context.Context, cluster *cnpgv1.Cluster) error {
	if cluster == nil {
		return nil
	}
	
	// Find PodMonitor to read/write state
	podMonitor, err := r.findPodMonitor(ctx, cluster)
	if err != nil {
		return nil // PodMonitor not ready yet, skip
	}
	
	annotations := podMonitor.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	
	changed := false
	
	// Initialize or check for scaling events
	if annotations[LastScaledInstancesAnnotation] == "" {
		annotations[LastScaledInstancesAnnotation] = fmt.Sprintf("%d", cluster.Spec.Instances)
		changed = true
	} else if r.detectScalingEvent(cluster, annotations) {
		r.Recorder.Event(cluster, corev1.EventTypeNormal, "CNPGScaled", 
			fmt.Sprintf("Cluster scaled to %d instances", cluster.Spec.Instances))
		annotations[LastScaledInstancesAnnotation] = fmt.Sprintf("%d", cluster.Spec.Instances)
		changed = true
	}
	
	// Initialize or check for config changes
	if annotations[LastConfigGenerationAnnotation] == "" {
		annotations[LastConfigGenerationAnnotation] = fmt.Sprintf("%d", cluster.Generation)
		changed = true
	} else if r.detectConfigChange(cluster, annotations) {
		r.Recorder.Event(cluster, corev1.EventTypeNormal, "CNPGConfigChanged", 
			"Cluster configuration updated")
		annotations[LastConfigGenerationAnnotation] = fmt.Sprintf("%d", cluster.Generation)
		changed = true
	}
	
	// Initialize or check for storage expansion
	if annotations[LastStorageGenerationAnnotation] == "" {
		annotations[LastStorageGenerationAnnotation] = fmt.Sprintf("%d", cluster.Generation)
		changed = true
	} else if r.detectStorageExpansion(cluster, annotations) {
		r.Recorder.Event(cluster, corev1.EventTypeNormal, "CNPGStorageExpanded", 
			"Cluster storage expanded")
		annotations[LastStorageGenerationAnnotation] = fmt.Sprintf("%d", cluster.Generation)
		changed = true
	}
	
	// Update PodMonitor annotations if changed
	if changed {
		return r.updatePodMonitorAnnotations(ctx, podMonitor, annotations)
	}
	
	return nil
}

func (r *Cnpg2Monitor) detectScalingEvent(cluster *cnpgv1.Cluster, annotations map[string]string) bool {
	lastInstances := annotations[LastScaledInstancesAnnotation]
	currentInstances := fmt.Sprintf("%d", cluster.Spec.Instances)
	return lastInstances != "" && lastInstances != currentInstances
}

func (r *Cnpg2Monitor) detectConfigChange(cluster *cnpgv1.Cluster, annotations map[string]string) bool {
	lastGeneration := annotations[LastConfigGenerationAnnotation]
	currentGeneration := fmt.Sprintf("%d", cluster.Generation)
	return lastGeneration != "" && lastGeneration != currentGeneration
}

func (r *Cnpg2Monitor) detectStorageExpansion(cluster *cnpgv1.Cluster, annotations map[string]string) bool {
	// For storage expansion, we check if storage size increased
	lastGeneration := annotations[LastStorageGenerationAnnotation]
	currentGeneration := fmt.Sprintf("%d", cluster.Generation)
	return lastGeneration != "" && lastGeneration != currentGeneration
}

func (r *Cnpg2Monitor) updatePodMonitorAnnotations(ctx context.Context, podMonitor *monitoringv1.PodMonitor, annotations map[string]string) error {
	// Refetch to get latest version
	fresh := &monitoringv1.PodMonitor{}
	if err := r.Get(ctx, types.NamespacedName{Name: podMonitor.Name, Namespace: podMonitor.Namespace}, fresh); err != nil {
		return err
	}
	
	// Merge with existing annotations
	existing := fresh.GetAnnotations()
	if existing == nil {
		existing = make(map[string]string)
	}
	for k, v := range annotations {
		existing[k] = v
	}
	
	fresh.SetAnnotations(existing)
	return r.Update(ctx, fresh)
}

func (r *Cnpg2Monitor) mapNamespaceToCluster(ctx context.Context, obj client.Object) []reconcile.Request {
	namespace := obj.(*corev1.Namespace)
	
	// Find all CNPG clusters with monitoring label in this namespace
	clusterList := &cnpgv1.ClusterList{}
	if err := r.List(ctx, clusterList, client.InNamespace(namespace.Name)); err != nil {
		return nil
	}
	
	var requests []reconcile.Request
	for _, cluster := range clusterList.Items {
		if cluster.Labels[MonitoredLabel] == "true" {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      cluster.Name,
					Namespace: cluster.Namespace,
				},
			})
		}
	}
	
	return requests
}

func (r *Cnpg2Monitor) mapPodMonitorToCluster(ctx context.Context, obj client.Object) []reconcile.Request {
	podMonitor := obj.(*monitoringv1.PodMonitor)
	
	// Extract cluster name from selector
	clusterName := ""
	if podMonitor.Spec.Selector.MatchLabels != nil {
		clusterName = podMonitor.Spec.Selector.MatchLabels[CNPGClusterLabel]
	}
	
	if clusterName == "" {
		return nil
	}
	
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{
			Name:      clusterName, // CNPG cluster name
			Namespace: podMonitor.Namespace,
		},
	}}
}
