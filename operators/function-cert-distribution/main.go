package main

import (
	"github.com/alecthomas/kong"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	fn "github.com/crossplane/function-sdk-go"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// CLI configures the function.
type CLI struct {
	Debug              bool   `help:"Emit debug logs in addition to info logs." short:"d"`
	Network            string `default:"tcp"   help:"Network on which to listen for gRPC connections."`
	Address            string `default:":9443" help:"Address at which to listen for gRPC connections."`
	TLSCertsDir        string `env:"TLS_SERVER_CERTS_DIR" help:"Directory containing server certs (tls.key, tls.crt) and the CA used to verify client certificates (ca.crt)"`
	Insecure           bool   `help:"Run without mTLS credentials. If you supply this flag --tls-certs-dir will be ignored."`
	MaxRecvMessageSize int    `default:"4" help:"Maximum size of received messages in MB."`
}

// Run executes the function.
func (c *CLI) Run() error {
	// Initialize production-ready structured JSON logger
	// This ensures logs are properly parsed by Grafana Alloy / OpenSearch
	zl := zap.New(zap.UseDevMode(c.Debug))
	log := logging.NewLogrLogger(zl.WithName("function-cert-distribution"))

	log.Info("Starting function-cert-distribution gRPC server", "address", c.Address)

	// Start the Crossplane gRPC server with mTLS, correct listen address,
	// and a safe max receive message size to prevent ResourceExhausted panics
	// under large Composition payloads.
	return fn.Serve(&Function{log: log},
		fn.Listen(c.Network, c.Address),
		fn.MTLSCertificates(c.TLSCertsDir),
		fn.Insecure(c.Insecure),
		fn.MaxRecvMessageSize(c.MaxRecvMessageSize*1024*1024),
	)
}

func main() {
	ctx := kong.Parse(&CLI{}, kong.Description("Zero-Ops Crossplane Cert-Distribution Function."))
	ctx.FatalIfErrorf(ctx.Run())
}
