// Command preview-access serves session previews from a separate registrable
// domain (ADR-108).
//
// One process, three surfaces:
//
//	:8080  the app API, reached only by tenant gateways, behind each
//	       application's own login: mint a handoff code, read a preview's state
//	:8081  every preview host: redeem the handoff, then check each request and
//	       proxy it (HTTP and WebSocket) to the session's sandbox
//	:9443  admission for PreviewAccess records
//
// and the controller that gives each record its host and the network policy
// letting this service, alone, reach the sandbox's preview port.
//
// It runs as ONE replica: handoff codes are single-use and held in memory
// (cookie.go). Two replicas would let a code be redeemed twice.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	toolscache "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	computev1alpha1 "github.com/soloz-io/zero-ops/operators/ephemeral-job-operator/api/v1alpha1"
	"github.com/soloz-io/zero-ops/operators/ephemeral-job-operator/internal/previewaccess"
)

const (
	// The label the universal-tenant chart puts on every tenant gateway pod.
	// The app API admits connections from these pods only, and derives the
	// caller's namespace from which one it was.
	gatewayPodLabel = "zero-ops.io/tenant-gateway"

	signingKeySecret = "preview-access-signing-key"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(computev1alpha1.AddToScheme(scheme))
}

func main() {
	var apiAddr, previewAddr, probeAddr string
	flag.StringVar(&apiAddr, "api-bind-address", ":8080", "App API (tenant gateways only).")
	flag.StringVar(&previewAddr, "preview-bind-address", ":8081", "Preview hosts.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8082", "Health probes.")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("preview-access")

	// Required configuration fails startup, by name. A missing issuer cannot
	// be guessed, and a default here would be right in one environment only.
	env := func(k string, required bool) string {
		v := os.Getenv(k)
		if v == "" && required {
			log.Error(errors.New("missing"), "required environment variable is not set", "name", k)
			os.Exit(1)
		}
		return v
	}
	ns := env("POD_NAMESPACE", true)
	issuer := env("OIDC_ISSUER", true)
	jwks := env("OIDC_JWKS_URL", true)
	// Required: without a preview domain there is nowhere a preview can be
	// served from, and serving it from any other domain is the exposure
	// ADR-108 exists to prevent.
	domain := env("PREVIEW_DOMAIN", true)

	// Sandbox pods only: the request path reads a job's pod for its address,
	// and caching every pod on the cluster would be the operator's whole pod
	// set in this process's memory for nothing.
	sandboxPods, _ := labels.NewRequirement("compute.nutgraf.in/ephemeraljob-uid", selection.Exists, nil)
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                server.Options{BindAddress: "0"},
		HealthProbeBindAddress: probeAddr,
		WebhookServer:          webhook.NewServer(webhook.Options{Port: 9443}),
		Cache: cache.Options{ByObject: map[client.Object]cache.ByObject{
			&corev1.Pod{}: {Label: labels.NewSelector().Add(*sandboxPods)},
		}},
	})
	if err != nil {
		log.Error(err, "unable to create manager")
		os.Exit(1)
	}

	key, err := signingKey(context.Background(), mgr.GetAPIReader(), mgr.GetClient(), ns)
	if err != nil {
		log.Error(err, "preview signing key unavailable")
		os.Exit(1)
	}
	signer, err := previewaccess.NewSigner(key)
	if err != nil {
		log.Error(err, "preview signing key unusable")
		os.Exit(1)
	}

	index := &previewaccess.Index{Client: mgr.GetClient()}
	selfLabels := map[string]string{"app": "preview-access"}
	if err := (&previewaccess.Reconciler{
		Client: mgr.GetClient(), Scheme: mgr.GetScheme(), Index: index,
		Domain: domain, Namespace: ns, PodLabels: selfLabels,
	}).SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to set up the PreviewAccess controller")
		os.Exit(1)
	}
	mgr.GetWebhookServer().Register(previewaccess.PathValidate,
		&webhook.Admission{Handler: &previewaccess.Admission{Reader: mgr.GetClient()}})

	callers, err := newGatewayCallers(mgr)
	if err != nil {
		log.Error(err, "unable to watch tenant gateway pods")
		os.Exit(1)
	}

	srv := &previewaccess.Server{
		Reader: mgr.GetClient(), Index: index, Codes: &previewaccess.Codes{}, Signer: signer,
		Verify:  &previewaccess.Verifier{Issuer: issuer, JWKSURL: jwks, HTTP: &http.Client{Timeout: 10 * time.Second}},
		Callers: callers, Log: log,
	}
	for _, s := range []*http.Server{
		{Addr: apiAddr, Handler: srv.AppAPI(), ReadHeaderTimeout: 10 * time.Second},
		// No write timeout: a WebSocket to the dev server is long-lived.
		{Addr: previewAddr, Handler: srv.Previews(), ReadHeaderTimeout: 10 * time.Second},
	} {
		if err := mgr.Add(httpRunnable{s}); err != nil {
			log.Error(err, "unable to add server", "addr", s.Addr)
			os.Exit(1)
		}
	}
	_ = mgr.AddHealthzCheck("healthz", func(*http.Request) error { return nil })
	_ = mgr.AddReadyzCheck("readyz", mgr.GetWebhookServer().StartedChecker())

	log.Info("starting", "previewDomain", domain, "issuer", issuer)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "manager exited")
		os.Exit(1)
	}
}

// signingKey reads the environment's cookie-signing key, creating it on first
// start. It is platform-generated rather than supplied: nobody needs to know
// it, and it never leaves the cluster. Kubernetes Secrets are encrypted at
// rest (ADR-105). Deleting the Secret rotates it, which expires every preview
// cookie -- they then take the `expired` path and are re-issued.
func signingKey(ctx context.Context, r client.Reader, w client.Client, ns string) ([]byte, error) {
	var s corev1.Secret
	err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: signingKeySecret}, &s)
	if err == nil {
		return s.Data["key"], nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	s = corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: signingKeySecret},
		Data:       map[string][]byte{"key": b},
	}
	if err := w.Create(ctx, &s); err != nil {
		return nil, fmt.Errorf("creating %s/%s: %w", ns, signingKeySecret, err)
	}
	return b, nil
}

// gatewayCallers maps a tenant gateway pod's address to its namespace, from a
// cache of those pods alone.
type gatewayCallers struct {
	mu   sync.RWMutex
	byIP map[string]string
}

func (g *gatewayCallers) NamespaceFor(ip string) (string, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	ns, ok := g.byIP[ip]
	return ns, ok
}

func newGatewayCallers(mgr manager.Manager) (*gatewayCallers, error) {
	sel, _ := labels.NewRequirement(gatewayPodLabel, selection.Equals, []string{"true"})
	c, err := cache.New(mgr.GetConfig(), cache.Options{
		Scheme: mgr.GetScheme(),
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Pod{}: {Label: labels.NewSelector().Add(*sel)},
		},
	})
	if err != nil {
		return nil, err
	}
	inf, err := c.GetInformer(context.Background(), &corev1.Pod{})
	if err != nil {
		return nil, err
	}
	g := &gatewayCallers{byIP: map[string]string{}}
	log := ctrl.Log.WithName("tenant-gateways")
	rebuild := func() {
		var l corev1.PodList
		if err := c.List(context.Background(), &l); err != nil {
			// The previous map stays: a failed relist is not evidence that any
			// gateway went away. Logged, because a persistent failure means new
			// gateways are refused until it clears.
			log.Error(err, "relisting tenant gateway pods failed")
			return
		}
		m := make(map[string]string, len(l.Items))
		for _, p := range l.Items {
			if p.Status.PodIP != "" && p.DeletionTimestamp == nil {
				m[p.Status.PodIP] = p.Namespace
			}
		}
		g.mu.Lock()
		g.byIP = m
		g.mu.Unlock()
	}
	if _, err := inf.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { rebuild() },
		UpdateFunc: func(any, any) { rebuild() },
		DeleteFunc: func(any) { rebuild() },
	}); err != nil {
		return nil, err
	}
	return g, mgr.Add(c)
}

type httpRunnable struct{ s *http.Server }

func (h httpRunnable) Start(ctx context.Context) error {
	errc := make(chan error, 1)
	go func() { errc <- h.s.ListenAndServe() }()
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return h.s.Shutdown(sctx)
	case err := <-errc:
		return err
	}
}

// Servers answer regardless of leadership; this process runs as one replica.
func (httpRunnable) NeedLeaderElection() bool { return false }
