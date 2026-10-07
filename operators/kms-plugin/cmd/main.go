// Command kms-plugin serves the Kubernetes KMS v2 provider interface over a unix
// socket, for one cluster, against one key in an external key store (ADR-100).
//
// IT IS A STATIC POD AND NOT A DEPLOYMENT, and that is not a packaging preference.
// The API server cannot decrypt anything in etcd until this plugin answers, so a
// workload the API server has to schedule in order to start is a deadlock: the
// scheduler needs the API server, the API server needs the plugin, the plugin needs
// the scheduler. kubelet starts a static pod from a file on disk with no API server
// involved, which is the only ordering that terminates.
package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/soloz-io/zero-ops/operators/kms-plugin/internal/metrics"
	"github.com/soloz-io/zero-ops/operators/kms-plugin/internal/nodecert"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"google.golang.org/grpc"
	kmsv2 "k8s.io/kms/apis/v2"

	"github.com/soloz-io/zero-ops/operators/kms-plugin/internal/keystore"
	"github.com/soloz-io/zero-ops/operators/kms-plugin/internal/service"
)

func main() {
	var (
		socket  = flag.String("socket", "/opt/infisicalkms.socket", "unix socket the API server connects to; the vendor plugin's default, so a provider configuration written for it keeps working")
		cluster = flag.String("cluster", "", "this cluster's name. A LOG LABEL ONLY: it is "+
			"not part of the reported key_id, which is the Cloud KMS CryptoKeyVersion resource "+
			"name exactly. Each cluster gets its own key and its own identity bound only to "+
			"that key, so cluster scoping is enforced by IAM rather than by a string in an "+
			"identifier")
		interval = flag.Duration("refresh-interval", 10*time.Minute, "how often the active key "+
			"version is re-read OFF the request path. A SAFETY NET, not the primary mechanism: "+
			"Status re-reads it on every poll, and the API server polls on its own TTL (20s "+
			"healthy, 3s unhealthy). This loop exists so an observation is established at start "+
			"and so a rotation is still noticed if Status polling ever stops")
		prepDir = flag.String("prepare-socket-dir", "", "run as an initContainer: create this "+
			"directory 0700 owned by --socket-dir-uid, then exit. The plugin runs non-root and "+
			"kubelet creates a hostPath root-owned 0755, so something has to chown it")
		prepUID     = flag.Int("socket-dir-uid", 65532, "uid to own the socket directory")
		metricsAddr = flag.String("metrics-addr", "127.0.0.1:8082", "address the Prometheus "+
			"endpoint binds. LOOPBACK ONLY by default and it should stay that way: this pod "+
			"runs with hostNetwork, so 0.0.0.0 would publish the plugin's latency and failure "+
			"counts on every control-plane interface. A node-local collector scrapes "+
			"127.0.0.1; nothing off-node needs to")
		credFile = flag.String("google-credentials", "", "path to a Google credential "+
			"CONFIGURATION for Workload Identity Federation, exported as "+
			"GOOGLE_APPLICATION_CREDENTIALS. It cannot be a Kubernetes Secret: the plugin must "+
			"authenticate before the API server can read one. A long-lived service-account key "+
			"is PROHIBITED (ADR-100); which external identity backs this file is a Gate 3 "+
			"question")
	)
	flag.Parse()

	// THE initContainer MODE, HANDLED BEFORE ANYTHING ELSE.
	//
	// One binary rather than borrowing busybox for a chown: the static pod then pulls a
	// single digest-pinned image, and there is no second supply chain in the API
	// server's start path for the sake of one syscall.
	//
	// It returns before --cluster is required, because preparing a directory needs
	// neither a cluster name nor a key store.
	if *prepDir != "" {
		if err := prepareSocketDir(*prepDir, *prepUID); err != nil {
			log.Fatal(err)
		}
		log.Printf("prepared %s as 0700 owned by %d:%d", *prepDir, *prepUID, *prepUID)

		// THE CERTIFICATE IS ISSUED HERE, IN THE SAME initContainer, AND THAT PLACEMENT
		// IS THE DESIGN. See internal/nodecert: the static pod manifest arrives as
		// cloud-init content BEFORE kubeadm runs, so on a fresh node the cluster CA does
		// not exist yet. An initContainer BLOCKS the pod and kubelet retries it, so "not
		// ready yet" resolves itself; a preKubeadmCommand could not, and the plugin
		// starting without a credential would deadlock kubeadm behind an API server that
		// cannot decrypt.
		//
		// Only this container ever touches ca.key. The long-running process runs as 65532
		// and cannot read it: anything able to sign with the cluster CA can mint any
		// identity in the cluster, and that capability lives for the seconds it is needed.
		if *cluster != "" {
			wrote, err := nodecert.Ensure(nodecert.DefaultPaths(), *cluster)
			if err != nil {
				log.Fatalf("issuing the plugin's client certificate: %v", err)
			}
			if wrote {
				log.Printf("issued a client certificate for %s, valid %s",
					nodecert.CommonName(*cluster), nodecert.Lifetime)
			} else {
				log.Printf("the existing client certificate for %s is still usable",
					nodecert.CommonName(*cluster))
			}
		}
		return
	}

	// --cluster is NO LONGER REQUIRED and supplies nothing. It used to carry the key_id
	// suffix; the identifier is the CryptoKeyVersion resource name and the cluster name
	// appears only in a log line.

	// One place where the key store is resolved, and it refuses rather than degrading.
	// See keystore.FromFile for why the credential is a file, and keystore.FromEnv for
	// why there is no in-memory fallback.
	// GOOGLE_APPLICATION_CREDENTIALS is how Application Default Credentials finds a
	// Workload Identity Federation credential configuration. Set here from the flag so
	// the path is visible in the pod spec while the CREDENTIAL itself is not: a WIF
	// configuration names an external identity source rather than carrying key
	// material, which is the property that makes mounting it acceptable and a
	// service-account key not.
	if *credFile != "" {
		if err := os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", *credFile); err != nil {
			log.Fatal(err)
		}
	}

	store, err := keystore.FromEnv()
	if err != nil {
		log.Fatal(err)
	}

	svc := service.New(store)

	// METRICS, AND WHY THEY EXIST AT ALL. ADR-100's condition 8 asks for an SLO and an
	// error budget, and neither can be computed from a component that exports nothing.
	// The out-of-band check reads Cloud KMS and answers "has state drifted"; it cannot
	// answer "how slow is the wrap path" or "how often does it fail", because only this
	// process sees those.
	//
	// A FAILURE TO SERVE THEM IS NOT FATAL. This binary is on the API server's start
	// path: a control plane that will not decrypt because a metrics port was taken is a
	// far worse outcome than one that decrypts without telemetry. Logged loudly, and the
	// plugin carries on.
	if err := metrics.Register(prometheus.DefaultRegisterer); err != nil {
		log.Printf("WARNING: metrics could not be registered, so this plugin will report "+
			"nothing about itself: %v", err)
	} else {
		go func() {
			mux := http.NewServeMux()
			mux.Handle("/metrics", promhttp.Handler())
			log.Printf("metrics on http://%s/metrics", *metricsAddr)
			srv := &http.Server{
				Addr:              *metricsAddr,
				Handler:           mux,
				ReadHeaderTimeout: 5 * time.Second,
			}
			if err := srv.ListenAndServe(); err != nil {
				log.Printf("WARNING: the metrics endpoint stopped, so this plugin is now "+
					"invisible to monitoring. Encryption is unaffected: %v", err)
			}
		}()
	}

	if err := run(*socket, *cluster, *interval, svc); err != nil {
		log.Fatal(err)
	}
}

// run serves until the context is cancelled. Separated from main so the socket
// lifecycle is testable.
func run(socketPath, cluster string, interval time.Duration, svc *service.Service) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)

	// THE PLUGIN WATCHES ITS OWN CERTIFICATE, because nothing else does. It can read the
	// certificate (0644) and never the CA key (0600, root) -- so it can report how much
	// life is left and cannot renew. That split is deliberate: reporting needs no
	// privilege, renewing needs the ability to mint any identity in the cluster.
	go watchCertificate(ctx, nodecert.DefaultPaths().Cert)
	defer stop()

	// A LEFTOVER SOCKET FILE MUST BE REMOVED OR THE BIND FAILS.
	//
	// The file outlives the process: a node that was power-cycled, or a pod killed
	// without a graceful stop, leaves it behind. `listen unix ...: address already in
	// use` on a path nothing is listening to reads as a port conflict and sends the
	// reader looking for another process.
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return fmt.Errorf("creating the socket directory: %w", err)
	}
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing the stale socket %s: %w", socketPath, err)
	}

	lis, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", socketPath, err)
	}
	// 0600: the API server reads it as root, and anything that can talk to this socket
	// can ask for every data encryption key in the cluster to be unwrapped.
	if err := os.Chmod(socketPath, 0o600); err != nil {
		return fmt.Errorf("restricting %s to 0600: %w", socketPath, err)
	}

	srv := grpc.NewServer()
	kmsv2.RegisterKeyManagementServiceServer(srv, svc)

	// The refresh loop: a SAFETY NET, and no longer the primary mechanism.
	//
	// Status re-reads the active version on every poll, because `healthz: ok` has to
	// mean the key is reachable NOW -- and the API server polls on a TTL of its own, 20s
	// while healthy and 3s while not (k8s.io/apiserver encryptionconfig/config.go:95).
	// So the key authority is already being read at a known, bounded rate, and a second
	// 30-second ticker doing the same call was pure duplication. It is now 10 minutes.
	//
	// What it is still FOR: establishing an observation at start, before the API server
	// has polled anything, and noticing a rotation if Status polling ever stops.
	//
	// The first read is attempted immediately and its failure is NOT fatal: Status
	// reports unhealthy until a read succeeds, the API server retries, and a key
	// authority that is briefly unreachable then costs a delayed start rather than a
	// crash loop against a socket the API server is waiting on.
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			if err := svc.Refresh(ctx); err != nil {
				// Logged every time rather than once. A failed read keeps the previous
				// observation in place, so the cluster keeps working on cached data keys
				// and this line is the only signal that the key authority has gone --
				// which is the shape of outage ADR-100 calls out as growing with time
				// rather than appearing at once.
				log.Printf("refreshing the active key version: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	go func() {
		<-ctx.Done()
		// GracefulStop, so an Encrypt already in flight is not abandoned with the data
		// encryption key wrapped and the response lost.
		srv.GracefulStop()
	}()

	log.Printf("serving KMS v2 for cluster %q on %s", cluster, socketPath)
	if err := srv.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("serving: %w", err)
	}
	return nil
}

// prepareSocketDir makes the socket directory usable by a non-root plugin.
//
// 0700 and not 0755: anything that can reach this socket can ask for every data
// encryption key in the cluster to be unwrapped. The API server reads it as root and
// root bypasses the permission check, so restricting it to the owner costs nothing and
// excludes every other process on the node.
//
// THE ORDER IS THE WHOLE DESIGN, because this runs with CAP_CHOWN AND NOTHING ELSE.
//
// An earlier version did MkdirAll, Chmod, Chown, then unlinked a stale socket — and
// crash-looped on a real node in two different ways:
//
//	attempt 0    chmod and chown succeeded, leaving the directory 0700 owned by
//	             65532. The unlink then failed EACCES: `drop: ALL` removes
//	             CAP_DAC_OVERRIDE, so uid 0 is just uid 0 and cannot traverse a 0700
//	             directory owned by someone else.
//	attempts 1+  the directory was already 65532-owned, so chmod failed EPERM:
//	             without CAP_FOWNER, root cannot chmod an inode it does not own.
//
// The tempting fix is to add CAP_FOWNER and CAP_DAC_OVERRIDE. That is the wrong fix:
// DAC_OVERRIDE means "ignore every file permission check on this node", which is most
// of what dropping capabilities was for. The operations are reordered instead, so that
// each one is performed by an owner:
//
//  1. if the directory is ALREADY correct, do nothing. The restart path then performs
//     no privileged syscall at all, which is the common case.
//  2. otherwise take ownership back to root first — CAP_CHOWN permits that — so the
//     chmod that follows is an owner chmod and needs no CAP_FOWNER.
//  3. chmod while root owns it.
//  4. give it away last.
//
// The stale socket is NOT removed here any more. The plugin runs as 65532 and owns
// this directory, and unlink permission depends on the DIRECTORY's write bit rather
// than the file's owner, so the plugin can remove its own stale socket — which run()
// already does. Doing it here required exactly the capability this function is built
// to avoid.
func prepareSocketDir(dir string, uid int) error {
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			if int(st.Uid) == uid && int(st.Gid) == uid && fi.Mode().Perm() == 0o700 {
				// Already right. Returning here is what makes a pod restart free of
				// privileged syscalls, and it is why the CrashLoopBackOff above could
				// never recover: the old code re-ran the whole sequence every time.
				return nil
			}
		}
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	// Step 2: become the owner, so step 3 does not need CAP_FOWNER. A no-op when root
	// already owns it, and the only reason CAP_CHOWN is granted.
	if err := os.Chown(dir, 0, 0); err != nil {
		return fmt.Errorf("taking ownership of %s in order to set its mode (needs CAP_CHOWN): %w",
			dir, err)
	}
	// Step 3: an owner chmod.
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("restricting %s to 0700: %w", dir, err)
	}
	// Step 4: hand it to the plugin's uid.
	if err := os.Chown(dir, uid, uid); err != nil {
		return fmt.Errorf("giving %s to uid %d (needs CAP_CHOWN): %w", dir, uid, err)
	}
	return nil
}

// watchCertificate reports the remaining fraction of the plugin's certificate.
//
// Hourly, because the value changes on the scale of days and a tighter loop would only add
// file reads. A certificate it cannot read reports 0 rather than nothing: an absent
// certificate is not "unknown", it is a credential that will fail on the next token
// exchange, and a gauge that went missing would simply vanish from a dashboard.
func watchCertificate(ctx context.Context, path string) {
	report := func() {
		raw, err := os.ReadFile(path)
		if err != nil {
			metrics.CertificateRemaining.Set(0)
			return
		}
		block, _ := pem.Decode(raw)
		if block == nil {
			metrics.CertificateRemaining.Set(0)
			return
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			metrics.CertificateRemaining.Set(0)
			return
		}
		metrics.CertificateRemaining.Set(nodecert.RemainingFraction(cert, time.Now()))
	}
	report()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			report()
		case <-ctx.Done():
			return
		}
	}
}
