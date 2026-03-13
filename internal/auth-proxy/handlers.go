package authproxy

import (
	"io"
	"net/http"
)

type Handler struct {
	hydraURL string
}

func NewHandler(hydraURL string) *Handler {
	return &Handler{hydraURL: hydraURL}
}

func (h *Handler) ProxyMetadata(w http.ResponseWriter, r *http.Request) {
	h.proxy(w, r, "/.well-known/oauth-authorization-server")
}

func (h *Handler) ProxyJWKS(w http.ResponseWriter, r *http.Request) {
	h.proxy(w, r, "/.well-known/jwks.json")
}

func (h *Handler) proxy(w http.ResponseWriter, r *http.Request, path string) {
	resp, err := http.Get(h.hydraURL + path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
