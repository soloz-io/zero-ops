package main

import (
	"os"

	"github.com/crossplane/crossplane-runtime/pkg/logging"
	fn "github.com/crossplane/function-sdk-go"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	// 1. Initialize production-ready structured JSON logger
	// This ensures logs are properly parsed by Grafana Alloy / OpenSearch
	zl := zap.New(zap.UseDevMode(false))
	logger := logging.NewLogrLogger(zl.WithName("function-cert-distribution"))

	logger.Info("Starting function-cert-distribution gRPC server")

	// 2. Instantiate the function with the injected logger
	f := &Function{
		log: logger,
	}

	// 3. Start the Crossplane gRPC server
	// fn.Serve defaults to listening on :9443, which the package manager expects
	if err := fn.Serve(f, fn.WithLogger(logger)); err != nil {
		logger.Info("Fatal error running function server", "error", err)
		os.Exit(1)
	}
}
