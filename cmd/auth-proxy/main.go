package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	authproxy "github.com/soloz-io/zero-ops/internal/auth-proxy"
)

func main() {
	cfg := authproxy.LoadConfig()

	if err := authproxy.RegisterClient(cfg.HydraURL, cfg.ClientID); err != nil {
		fmt.Fprintf(os.Stderr, "failed to register client: %v\n", err)
		os.Exit(1)
	}

	h := authproxy.NewHandler(cfg.HydraURL)
	http.HandleFunc("/.well-known/oauth-authorization-server", h.ProxyMetadata)
	http.HandleFunc("/.well-known/jwks.json", h.ProxyJWKS)

	log.Printf("auth-proxy listening on :%s", cfg.Port)
	if err := http.ListenAndServe(":"+cfg.Port, nil); err != nil {
		log.Fatal(err)
	}
}
