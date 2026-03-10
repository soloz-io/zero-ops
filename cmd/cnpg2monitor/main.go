package main

import (
	"flag"
	"os"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"cnpg2monitor/internal/controller"
	"cnpg2monitor/pkg/config"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(monitoringv1.AddToScheme(scheme))
	utilruntime.Must(cnpgv1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr     = flag.String("metrics-bind-address", ":8080", "Metrics server bind address")
		probeAddr       = flag.String("health-probe-bind-address", ":8081", "Health probe bind address")
		syncDuration    = flag.Duration("sync-duration", 30*time.Second, "Reconciliation sync period")
		enableDebug     = flag.Bool("debug", false, "Enable debug logging")
		kubeQPS         = flag.Int("kube-qps", 20, "Kubernetes API QPS limit")
		kubeBurst       = flag.Int("kube-burst", 30, "Kubernetes API burst limit")
	)
	flag.Parse()

	// Setup logging
	opts := zap.Options{Development: *enableDebug}
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Load configuration
	cfg := config.LoadConfigFromEnv()

	// Setup manager with namespace restriction
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: server.Options{
			BindAddress: *metricsAddr,
		},
		HealthProbeBindAddress: *probeAddr,
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				cfg.MonitoringNamespace: {},
			},
		},
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Tune REST config
	mgr.GetConfig().QPS = float32(*kubeQPS)
	mgr.GetConfig().Burst = *kubeBurst

	// Setup controller
	if err = (&controller.Cnpg2Monitor{
		Client:     mgr.GetClient(),
		Log:        ctrl.Log.WithName("controllers").WithName("Cnpg2Monitor"),
		Scheme:     mgr.GetScheme(),
		Recorder:   mgr.GetEventRecorderFor("cnpg2monitor"),
		SyncPeriod: *syncDuration,
		Config:     cfg,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Cnpg2Monitor")
		os.Exit(1)
	}

	// Setup health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
