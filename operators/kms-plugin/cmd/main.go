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
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"google.golang.org/grpc"
	kmsv2 "k8s.io/kms/apis/v2"

	"github.com/soloz-io/zero-ops/operators/kms-plugin/internal/active"
	"github.com/soloz-io/zero-ops/operators/kms-plugin/internal/keystore"
	"github.com/soloz-io/zero-ops/operators/kms-plugin/internal/service"
)

func main() {
	var (
		socket   = flag.String("socket", "/var/run/kms/soloz-kms.sock", "unix socket the API server connects to")
		cluster  = flag.String("cluster", "", "this cluster's name; scopes the key identifier (required)")
		interval = flag.Duration("refresh-interval", 30*time.Second, "how often the key store's active version is re-read")
	)
	flag.Parse()

	if *cluster == "" {
		log.Fatal("--cluster is required: the key identifier is scoped to the cluster, because " +
			"two clusters' version 1 are different keys (ADR-100)")
	}

	// One place where the key store is resolved, and it refuses while none exists.
	// See keystore.FromEnv for why that is a refusal and not an in-memory fallback.
	store, err := keystore.FromEnv()
	if err != nil {
		log.Fatal(err)
	}

	svc := service.New(*cluster, store, active.NewHolder())
	if err := run(*socket, *cluster, *interval, svc); err != nil {
		log.Fatal(err)
	}
}

// run serves until the context is cancelled. Separated from main so the socket
// lifecycle is testable.
func run(socketPath, cluster string, interval time.Duration, svc *service.Service) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
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

	// The refresh loop, and the ONLY caller of the key store outside a request.
	//
	// Status and Encrypt read a snapshot instead of asking the store themselves, which
	// is what stops them observing different versions during a rotation (ADR-100
	// acceptance criterion 2).
	//
	// The first read is attempted immediately and its failure is NOT fatal: Status
	// reports unhealthy until a read succeeds, the API server retries, and a key store
	// that is briefly unreachable then costs a delayed start rather than a crash loop
	// against a socket the API server is waiting on.
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			if err := svc.Refresh(ctx); err != nil {
				// Logged every time rather than once. A rotation that this plugin refuses
				// -- a reactivated version, a changed key -- keeps the previous snapshot
				// active, so the cluster keeps working and the refusal is the only signal
				// that the intended rotation did not take effect.
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
