package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"cnpg2monitor/pkg/config"
)

func TestController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Suite")
}

var _ = Describe("Cnpg2Monitor Controller", func() {
	var (
		controller *Cnpg2Monitor
		fakeClient client.Client
		scheme     *runtime.Scheme
		ctx        context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(monitoringv1.AddToScheme(scheme)).To(Succeed())
		Expect(cnpgv1.AddToScheme(scheme)).To(Succeed())

		fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()
		
		controller = &Cnpg2Monitor{
			Client:     fakeClient,
			Log:        ctrl.Log.WithName("test"),
			Scheme:     scheme,
			Recorder:   &record.FakeRecorder{},
			SyncPeriod: 30 * time.Second,
			Config:     config.LoadConfigFromEnv(),
		}
	})

	Context("Configuration Loading", func() {
		It("should load default configuration", func() {
			cfg := config.LoadConfigFromEnv()
			Expect(cfg.MonitoringNamespace).To(Equal("zero-ops-system"))
			Expect(cfg.EnableEventEmission).To(BeTrue())
			Expect(cfg.TopologyLabelPrefix).To(Equal("zero-ops.io/"))
		})
	})

	Context("Namespace to Cluster Mapping", func() {
		It("should map namespace changes to monitored clusters", func() {
			// Create a cluster with monitoring label
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-ns",
					Labels: map[string]string{
						MonitoredLabel: "true",
					},
				},
			}
			Expect(fakeClient.Create(ctx, cluster)).To(Succeed())

			// Create namespace
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ns",
				},
			}

			// Test mapping
			requests := controller.mapNamespaceToCluster(ctx, ns)
			Expect(requests).To(HaveLen(1))
			Expect(requests[0].NamespacedName).To(Equal(types.NamespacedName{
				Name:      "test-cluster",
				Namespace: "test-ns",
			}))
		})

		It("should ignore clusters without monitoring label", func() {
			// Create a cluster without monitoring label
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-ns",
				},
			}
			Expect(fakeClient.Create(ctx, cluster)).To(Succeed())

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ns",
				},
			}

			requests := controller.mapNamespaceToCluster(ctx, ns)
			Expect(requests).To(HaveLen(0))
		})
	})

	Context("PodMonitor to Cluster Mapping", func() {
		It("should map PodMonitor to cluster pod", func() {
			podMonitor := &monitoringv1.PodMonitor{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-monitor",
					Namespace: "test-ns",
				},
				Spec: monitoringv1.PodMonitorSpec{
					Selector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							CNPGClusterLabel: "test-cluster",
						},
					},
				},
			}

			requests := controller.mapPodMonitorToCluster(ctx, podMonitor)
			Expect(requests).To(HaveLen(1))
			Expect(requests[0].NamespacedName).To(Equal(types.NamespacedName{
				Name:      "test-cluster",
				Namespace: "test-ns",
			}))
		})

		It("should return empty for PodMonitor without cluster label", func() {
			podMonitor := &monitoringv1.PodMonitor{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-monitor",
					Namespace: "test-ns",
				},
				Spec: monitoringv1.PodMonitorSpec{
					Selector: metav1.LabelSelector{
						MatchLabels: map[string]string{},
					},
				},
			}

			requests := controller.mapPodMonitorToCluster(ctx, podMonitor)
			Expect(requests).To(HaveLen(0))
		})
	})

	Context("PodMonitor Patching", func() {
		It("should build topology relabelings correctly", func() {
			topologyLabels := map[string]string{
				"cluster_id": "prod-cluster-1",
				"region":     "us-west-2",
			}

			relabelings := controller.buildTopologyRelabelings(topologyLabels)
			Expect(relabelings).To(HaveLen(2))
			
			// Check that all topology labels are converted to relabelings
			labelMap := make(map[string]string)
			for _, r := range relabelings {
				labelMap[r.TargetLabel] = *r.Replacement
			}
			Expect(labelMap).To(Equal(topologyLabels))
		})

		It("should handle empty topology labels", func() {
			relabelings := controller.buildTopologyRelabelings(map[string]string{})
			Expect(relabelings).To(HaveLen(0))
		})
	})

	Context("Event Emission", func() {
		It("should detect scaling events", func() {
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-ns",
					Annotations: map[string]string{
						LastScaledInstancesAnnotation: "2",
					},
				},
				Spec: cnpgv1.ClusterSpec{
					Instances: 3,
				},
			}

			detected := controller.detectScalingEvent(cluster, cluster.Annotations)
			Expect(detected).To(BeTrue())
		})

		It("should not detect scaling when instances unchanged", func() {
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						LastScaledInstancesAnnotation: "3",
					},
				},
				Spec: cnpgv1.ClusterSpec{
					Instances: 3,
				},
			}

			detected := controller.detectScalingEvent(cluster, cluster.Annotations)
			Expect(detected).To(BeFalse())
		})

		It("should detect config changes", func() {
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 5,
					Annotations: map[string]string{
						LastConfigGenerationAnnotation: "4",
					},
				},
			}

			detected := controller.detectConfigChange(cluster, cluster.Annotations)
			Expect(detected).To(BeTrue())
		})
	})

	Context("Reconciliation Logic", func() {
		It("should observe state correctly", func() {
			// Create a cluster with monitoring label
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-ns",
					Labels: map[string]string{
						MonitoredLabel: "true",
					},
				},
			}
			Expect(fakeClient.Create(ctx, cluster)).To(Succeed())

			// Create namespace with topology labels
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ns",
					Labels: map[string]string{
						"zero-ops.io/cluster_id": "mothership",
						"zero-ops.io/region":     "fsn1",
					},
				},
			}
			Expect(fakeClient.Create(ctx, ns)).To(Succeed())

			// Create PodMonitor
			podMonitor := &monitoringv1.PodMonitor{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-ns",
				},
				Spec: monitoringv1.PodMonitorSpec{
					Selector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							CNPGClusterLabel: "test-cluster",
						},
					},
				},
			}
			Expect(fakeClient.Create(ctx, podMonitor)).To(Succeed())

			// Test observe function
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "test-cluster",
					Namespace: "test-ns",
				},
			}

			state, err := controller.observe(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(state.cluster).NotTo(BeNil())
			Expect(state.podMonitor).NotTo(BeNil())
			Expect(state.topologyLabels).To(HaveKey("cluster_id"))
			Expect(state.topologyLabels).To(HaveKey("region"))
		})

		It("should analyze state and determine actions", func() {
			state := &observedState{
				cluster: &cnpgv1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-cluster",
					},
				},
				podMonitor: &monitoringv1.PodMonitor{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-monitor",
					},
				},
				topologyLabels: map[string]string{
					"cluster_id": "mothership",
					"region":     "fsn1",
				},
			}

			actions := controller.analyze(state)
			Expect(actions.patchPodMonitor).To(BeTrue())
			Expect(actions.emitEvent).To(BeFalse())
		})

		It("should handle missing PodMonitor", func() {
			state := &observedState{
				cluster: &cnpgv1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-cluster",
					},
				},
				podMonitorErr: fmt.Errorf("not found"),
			}

			actions := controller.analyze(state)
			Expect(actions.patchPodMonitor).To(BeFalse())
			Expect(actions.emitEvent).To(BeTrue())
			Expect(actions.eventReason).To(Equal("CNPGPodMonitorNotFound"))
		})

		It("should handle missing topology labels", func() {
			state := &observedState{
				cluster: &cnpgv1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-cluster",
					},
				},
				podMonitor: &monitoringv1.PodMonitor{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-monitor",
					},
				},
				topologyErr: fmt.Errorf("no labels found"),
			}

			actions := controller.analyze(state)
			Expect(actions.patchPodMonitor).To(BeFalse())
			Expect(actions.emitEvent).To(BeTrue())
			Expect(actions.eventReason).To(Equal("CNPGTopologyLabelsMissing"))
		})
	})
})
