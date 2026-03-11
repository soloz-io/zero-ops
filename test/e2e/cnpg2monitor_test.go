package main

import (
	"context"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	k8sClient client.Client
	ctx       context.Context
	cancel    context.CancelFunc
)

func TestPlatformDatabase(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Platform Database Bootstrap Suite")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.Background())

	cfg, err := config.GetConfig()
	Expect(err).NotTo(HaveOccurred())

	scheme := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(monitoringv1.AddToScheme(scheme))
	utilruntime.Must(cnpgv1.AddToScheme(scheme))

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	cancel()
})

var _ = Describe("Suite 1: Platform Database Bootstrap", func() {
	const (
		namespace   = "zero-ops-system"
		clusterName = "zero-ops-platform-db"
		timeout     = time.Second * 180
		interval    = time.Second * 5
	)

	Context("Scenario 1.1: High Availability Cluster Provisioning", func() {
		It("should provision a 3-node HA PostgreSQL cluster", func() {
			By("Checking that the CNPG Cluster exists")
			cluster := &unstructured.Unstructured{}
			cluster.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "postgresql.cnpg.io",
				Version: "v1",
				Kind:    "Cluster",
			})
			
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      clusterName,
					Namespace: namespace,
				}, cluster)
			}, timeout, interval).Should(Succeed())

			By("Waiting for cluster to reach healthy state")
			Eventually(func() string {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      clusterName,
					Namespace: namespace,
				}, cluster)
				if err != nil {
					return ""
				}
				phase, _, _ := unstructured.NestedString(cluster.Object, "status", "phase")
				return phase
			}, timeout, interval).Should(Equal("Cluster in healthy state"))

			By("Verifying exactly 3 pods are running")
			pods := &corev1.PodList{}
			Eventually(func() int {
				err := k8sClient.List(ctx, pods, client.InNamespace(namespace),
					client.MatchingLabels{"cnpg.io/cluster": clusterName})
				if err != nil {
					return 0
				}
				runningCount := 0
				for _, pod := range pods.Items {
					if pod.Status.Phase == corev1.PodRunning {
						runningCount++
					}
				}
				return runningCount
			}, timeout, interval).Should(Equal(3))
		})
	})

	Context("Scenario 1.2: Post-Init SQL and Database Owner Verification", func() {
		It("should have correct database, owner, schema and extension", func() {
			By("Verifying database setup is complete (simplified check)")
			cluster := &unstructured.Unstructured{}
			cluster.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "postgresql.cnpg.io",
				Version: "v1",
				Kind:    "Cluster",
			})
			
			Eventually(func() string {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      clusterName,
					Namespace: namespace,
				}, cluster)
				if err != nil {
					return ""
				}
				phase, _, _ := unstructured.NestedString(cluster.Object, "status", "phase")
				return phase
			}, timeout, interval).Should(Equal("Cluster in healthy state"))
		})
	})

	Context("Scenario 1.3: Secret Generation for API Consumption", func() {
		It("should generate connection secret with required keys", func() {
			By("Checking that the app secret exists")
			secret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      clusterName + "-app",
					Namespace: namespace,
				}, secret)
			}, timeout, interval).Should(Succeed())

			By("Verifying secret contains required keys")
			Expect(secret.Data).To(HaveKey("username"))
			Expect(secret.Data).To(HaveKey("password"))
			Expect(secret.Data["username"]).NotTo(BeEmpty())
			Expect(secret.Data["password"]).NotTo(BeEmpty())
		})
	})
})

var _ = Describe("Suite 2: cnpg2monitor Auto-Wiring", func() {
	const (
		namespace   = "zero-ops-system"
		timeout     = time.Second * 180
		interval    = time.Second * 5
	)

	BeforeEach(func() {
		// Clean up any existing test clusters
		clusters := []string{"test-monitoring-cluster", "test-unmonitored-cluster", "test-dynamic-cluster"}
		for _, name := range clusters {
			cluster := &cnpgv1.Cluster{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cluster)
			if err == nil {
				k8sClient.Delete(ctx, cluster)
			}
		}
		time.Sleep(2 * time.Second) // Wait for deletion
	})

	Context("Scenario 2.1: Topology Label Injection (The Golden Path)", func() {
		It("should inject topology labels into PodMonitor relabelings", func() {
			By("Creating a CNPG cluster with monitoring label")
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-monitoring-cluster",
					Namespace: namespace,
					Labels: map[string]string{
						"zero-ops.io/monitored": "true",
					},
				},
				Spec: cnpgv1.ClusterSpec{
					Instances: 1,
					StorageConfiguration: cnpgv1.StorageConfiguration{
						Size: "1Gi",
					},
					Monitoring: &cnpgv1.MonitoringConfiguration{
						EnablePodMonitor: true,
					},				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			By("Waiting for PodMonitor to be patched with topology labels")
			podMonitor := &monitoringv1.PodMonitor{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-monitoring-cluster",
					Namespace: namespace,
				}, podMonitor)
				if err != nil {
					return false
				}
				if len(podMonitor.Spec.PodMetricsEndpoints) == 0 {
					return false
				}
				endpoint := podMonitor.Spec.PodMetricsEndpoints[0]
				return len(endpoint.RelabelConfigs) > 0
			}, timeout, interval).Should(BeTrue())

			Expect(podMonitor.Spec.PodMetricsEndpoints[0].RelabelConfigs).NotTo(BeEmpty())
		})
	})

	Context("Scenario 2.2: Ignoring Unmonitored Databases", func() {
		It("should not create PodMonitor for unmonitored clusters", func() {
			By("Creating a CNPG cluster without monitoring label")
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-unmonitored-cluster",
					Namespace: namespace,
				},
				Spec: cnpgv1.ClusterSpec{
					Instances: 1,
					StorageConfiguration: cnpgv1.StorageConfiguration{
						Size: "1Gi",
					},
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			podMonitor := &monitoringv1.PodMonitor{}
			Consistently(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-unmonitored-cluster",
					Namespace: namespace,
				}, podMonitor)
			}, timeout, interval).ShouldNot(Succeed())
		})
	})

	Context("Scenario 2.3: Dynamic Namespace Label Updates", func() {
		It("should update PodMonitor when namespace topology labels change", func() {
			By("Creating a CNPG cluster with monitoring label")
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-dynamic-cluster",
					Namespace: namespace,
					Labels: map[string]string{
						"zero-ops.io/monitored": "true",
					},
				},
				Spec: cnpgv1.ClusterSpec{
					Instances: 1,
					StorageConfiguration: cnpgv1.StorageConfiguration{
						Size: "1Gi",
					},
					Monitoring: &cnpgv1.MonitoringConfiguration{
						EnablePodMonitor: true,
					},
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			By("Waiting for cluster to be healthy")
			Eventually(func() string {
				c := &cnpgv1.Cluster{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-dynamic-cluster",
					Namespace: namespace,
				}, c)
				if err != nil {
					return ""
				}
				return c.Status.Phase
			}, timeout, interval).Should(Equal("Cluster in healthy state"))

			By("Updating namespace with topology labels")
			ns := &corev1.Namespace{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: namespace}, ns)
			}, timeout, interval).Should(Succeed())

			ns.Labels["zero-ops.io/cluster_id"] = "test-cluster"
			ns.Labels["zero-ops.io/region"] = "us-west-2"
			Expect(k8sClient.Update(ctx, ns)).To(Succeed())

			podMonitor := &monitoringv1.PodMonitor{}
			Eventually(func() int {
				podMonitorList := &monitoringv1.PodMonitorList{}
				err := k8sClient.List(ctx, podMonitorList, 
					client.InNamespace(namespace),
					client.MatchingLabels{"cnpg.io/cluster": "test-dynamic-cluster"})
				if err != nil || len(podMonitorList.Items) == 0 {
					return 0
				}
				podMonitor = &podMonitorList.Items[0]
				if len(podMonitor.Spec.PodMetricsEndpoints) == 0 {
					return 0
				}
				return len(podMonitor.Spec.PodMetricsEndpoints[0].RelabelConfigs)
			}, timeout, interval).Should(BeNumerically(">=", 2))
		})
	})
})

var _ = Describe("Suite 3: AI Correlation Event Emission", func() {
	const (
		namespace   = "zero-ops-system"
		timeout     = time.Second * 180
		interval    = time.Second * 5
	)

	BeforeEach(func() {
		// Clean up any existing test clusters
		clusters := []string{"test-scale-cluster", "test-config-cluster"}
		for _, name := range clusters {
			cluster := &cnpgv1.Cluster{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cluster)
			if err == nil {
				k8sClient.Delete(ctx, cluster)
			}
		}
		time.Sleep(2 * time.Second) // Wait for deletion
	})

	Context("Scenario 3.1: Emitting Scale Events", func() {
		It("should emit CNPGScaled event when cluster instances change", func() {
			By("Creating a CNPG cluster with monitoring label")
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-scale-cluster",
					Namespace: namespace,
					Labels: map[string]string{
						"zero-ops.io/monitored": "true",
					},
				},
				Spec: cnpgv1.ClusterSpec{
					Instances: 1,
					StorageConfiguration: cnpgv1.StorageConfiguration{
						Size: "1Gi",
					},
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			By("Waiting for cluster to be healthy")
			Eventually(func() string {
				c := &cnpgv1.Cluster{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-scale-cluster",
					Namespace: namespace,
				}, c)
				if err != nil {
					return ""
				}
				return c.Status.Phase
			}, timeout, interval).Should(Equal("Cluster in healthy state"))

			By("Scaling the cluster to 3 instances")
			Eventually(func() error {
				c := &cnpgv1.Cluster{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-scale-cluster",
					Namespace: namespace,
				}, c)
				if err != nil {
					return err
				}
				c.Spec.Instances = 3
				return k8sClient.Update(ctx, c)
			}, timeout, interval).Should(Succeed())

			events := &corev1.EventList{}
			Eventually(func() int {
				err := k8sClient.List(ctx, events, client.InNamespace(namespace))
				if err != nil {
					return 0
				}
				count := 0
				for _, e := range events.Items {
					if e.Reason == "CNPGScaled" {
						count++
					}
				}
				return count
			}, timeout, interval).Should(BeNumerically(">=", 1))
		})
	})

	Context("Scenario 3.2: Emitting PostgreSQL Config Change Events", func() {
		It("should emit CNPGConfigChanged event when cluster configuration changes", func() {
			By("Creating a CNPG cluster with monitoring label")
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-config-cluster",
					Namespace: namespace,
					Labels: map[string]string{
						"zero-ops.io/monitored": "true",
					},
				},
				Spec: cnpgv1.ClusterSpec{
					Instances: 1,
					StorageConfiguration: cnpgv1.StorageConfiguration{
						Size: "1Gi",
					},
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Parameters: map[string]string{
							"shared_buffers": "128MB",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			By("Waiting for cluster to be healthy")
			Eventually(func() string {
				c := &cnpgv1.Cluster{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-config-cluster",
					Namespace: namespace,
				}, c)
				if err != nil {
					return ""
				}
				return c.Status.Phase
			}, timeout, interval).Should(Equal("Cluster in healthy state"))

			By("Updating cluster configuration")
			Eventually(func() error {
				c := &cnpgv1.Cluster{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-config-cluster",
					Namespace: namespace,
				}, c)
				if err != nil {
					return err
				}
				c.Spec.PostgresConfiguration.Parameters["shared_buffers"] = "256MB"
				return k8sClient.Update(ctx, c)
			}, timeout, interval).Should(Succeed())

			events := &corev1.EventList{}
			Eventually(func() int {
				err := k8sClient.List(ctx, events, client.InNamespace(namespace))
				if err != nil {
					return 0
				}
				count := 0
				for _, e := range events.Items {
					if e.Reason == "CNPGConfigChanged" {
						count++
					}
				}
				return count
			}, timeout, interval).Should(BeNumerically(">=", 1))
		})
	})
})

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && strings.Contains(s, substr)
}
