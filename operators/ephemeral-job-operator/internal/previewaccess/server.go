package previewaccess

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	computev1alpha1 "github.com/soloz-io/zero-ops/operators/ephemeral-job-operator/api/v1alpha1"
)

// Callers resolves which application namespace a connection on the app API
// came from, by the source address of the tenant gateway pod that made it.
//
// The namespace is never taken from the request: a header or path naming it
// would be something any caller could write. A gateway pod's address is
// assigned by the cluster, and the platform renders every gateway's routes, so
// "this connection came from namespace X's gateway" is a fact, not a claim.
type Callers interface {
	NamespaceFor(ip string) (string, bool)
}

// Server is the preview-access HTTP surface.
type Server struct {
	Reader  client.Reader // the manager's cache
	Index   *Index
	Codes   *Codes
	Signer  *Signer
	Verify  *Verifier
	Callers Callers
	Log     logr.Logger
}

const apiPrefix = "/_platform/preview-access/v1/"

// ─── App API: behind the application's own gateway login ───────────────────

// AppAPI is served to tenant gateways only (Cilium admits nothing else).
func (s *Server) AppAPI() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+apiPrefix+"codes", s.handleCodes)
	mux.HandleFunc("GET "+apiPrefix+"previews/{id}", s.handleState)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return mux
}

type caller struct {
	namespace string
	origin    string
	subject   string
}

// authenticate establishes who is asking and from which application, or
// writes the refusal itself.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (caller, bool) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	ns, ok := s.Callers.NamespaceFor(ip)
	if !ok {
		s.Log.Info("audit", "event", "api-refused", "reason", "not a tenant gateway", "remote", ip)
		writeJSON(w, http.StatusForbidden, map[string]string{"previewAccess": StateDenied})
		return caller{}, false
	}
	var nsObj corev1.Namespace
	if err := s.Reader.Get(r.Context(), client.ObjectKey{Name: ns}, &nsObj); err != nil {
		http.Error(w, "namespace unavailable", http.StatusServiceUnavailable)
		return caller{}, false
	}
	origin := nsObj.Annotations[AppOriginAnnotation]
	if origin == "" {
		s.Log.Info("audit", "event", "api-refused", "reason", "namespace declares no app origin", "namespace", ns)
		writeJSON(w, http.StatusForbidden, map[string]string{"previewAccess": StateDenied})
		return caller{}, false
	}
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"previewAccess": StateDenied})
		return caller{}, false
	}
	claims, err := s.Verify.Verify(r.Context(), raw)
	if err != nil {
		s.Log.Info("audit", "event", "api-refused", "reason", err.Error(), "namespace", ns)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"previewAccess": StateDenied})
		return caller{}, false
	}
	return caller{namespace: ns, origin: origin, subject: claims.Subject}, true
}

func (s *Server) record(ctx context.Context, ns, name string) (*computev1alpha1.PreviewAccess, error) {
	var rec computev1alpha1.PreviewAccess
	if err := s.Reader.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &rec); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return &rec, nil
}

// handleCodes mints a single-use handoff (ADR-108 §3).
func (s *Server) handleCodes(w http.ResponseWriter, r *http.Request) {
	c, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	// Same-origin JSON only: a cross-site form cannot set this content type,
	// and a cross-site fetch carries a foreign Origin.
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" ||
		r.Header.Get("Origin") != c.origin {
		writeJSON(w, http.StatusForbidden, map[string]string{"previewAccess": StateDenied})
		return
	}
	var body struct {
		PreviewID string `json:"previewId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.PreviewID == "" {
		http.Error(w, `{"error":"previewId is required"}`, http.StatusBadRequest)
		return
	}
	rec, err := s.record(r.Context(), c.namespace, body.PreviewID)
	if err != nil {
		http.Error(w, "record lookup failed", http.StatusServiceUnavailable)
		return
	}
	now := time.Now()
	switch {
	case rec == nil || RecordEnded(rec, now):
		writeJSON(w, http.StatusNotFound, map[string]string{"previewAccess": "not-found"})
		return
	case !Allowed(rec, c.subject):
		s.Log.Info("audit", "event", "code-refused", "namespace", c.namespace, "preview", rec.Name, "subject", c.subject, "state", StateDenied)
		writeJSON(w, http.StatusForbidden, map[string]string{"previewAccess": StateDenied})
		return
	case rec.Status.Host == "":
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"previewAccess": StateUnavailable})
		return
	}

	maxAge := cookieMaxAge(rec, now)
	k, err := s.Codes.Issue(ticket{
		Record: string(rec.UID), Host: rec.Status.Host, Subject: c.subject,
		Expiry: now.Add(maxAge).Unix(), Origin: c.origin,
	})
	if err != nil {
		http.Error(w, "could not mint a code", http.StatusInternalServerError)
		return
	}
	s.Log.Info("audit", "event", "code-issued", "namespace", c.namespace, "preview", rec.Name, "subject", c.subject, "host", rec.Status.Host)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"handoffUrl":           "https://" + rec.Status.Host + "/_handoff?code=" + url.QueryEscape(k),
		"codeExpiresInSeconds": int(CodeTTL / time.Second),
		"cookieMaxAgeSeconds":  int(maxAge / time.Second),
	})
}

// handleState is the authoritative answer the canvas acts on (ADR-108 §3a).
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	c, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	rec, err := s.record(r.Context(), c.namespace, id)
	if err != nil {
		http.Error(w, "record lookup failed", http.StatusServiceUnavailable)
		return
	}
	state, until, err := s.Index.StateFor(r.Context(), rec, c.subject)
	if err != nil {
		http.Error(w, "state lookup failed", http.StatusServiceUnavailable)
		return
	}
	out := map[string]any{"previewId": id, "state": state, "host": nil, "cookieExpiresAt": nil}
	if rec != nil && state != StateEnded {
		out["host"] = rec.Status.Host
	}
	if until != nil {
		out["cookieExpiresAt"] = until.UTC().Format(time.RFC3339)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func cookieMaxAge(rec *computev1alpha1.PreviewAccess, now time.Time) time.Duration {
	d := CookieMaxAge
	if rec.Spec.ExpiresAt != nil {
		if left := rec.Spec.ExpiresAt.Sub(now).Truncate(time.Second); left < d {
			d = left
		}
	}
	return d
}

// ─── Preview hosts: the untrusted origin ───────────────────────────────────

// Previews serves every preview host: the handoff, then every request checked
// and proxied to the sandbox.
func (s *Server) Previews() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The app's own copy of these never reaches the browser, so the canvas
		// can trust the header on a refusal (ADR-108 §6).
		host := strings.ToLower(strings.Split(r.Host, ":")[0])
		if r.URL.Path == "/_handoff" {
			s.handleHandoff(w, r, host)
			return
		}
		s.handlePreview(w, r, host)
	})
}

func (s *Server) handleHandoff(w http.ResponseWriter, r *http.Request, host string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	t, err := s.Codes.Redeem(r.URL.Query().Get("code"), host)
	if err != nil {
		s.Log.Info("audit", "event", "handoff-refused", "host", host, "reason", err.Error())
		s.refuse(w, r, host, StateExpired, "")
		return
	}
	rec, err := s.Index.ByHost(r.Context(), host)
	if err != nil || rec == nil || string(rec.UID) != t.Record || !Allowed(rec, t.Subject) {
		s.Log.Info("audit", "event", "handoff-refused", "host", host, "subject", t.Subject, "reason", "record changed since the code was issued")
		s.refuse(w, r, host, StateRevoked, t.Origin)
		return
	}
	exp := time.Unix(t.Expiry, 0)
	http.SetCookie(w, s.Signer.cookie(t, time.Until(exp)))
	s.Index.noteRedeemed(rec.UID, t.Subject, exp)
	s.Log.Info("audit", "event", "handoff-redeemed", "namespace", rec.Namespace, "preview", rec.Name, "subject", t.Subject, "host", host)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request, host string) {
	rec, err := s.Index.ByHost(r.Context(), host)
	if err != nil {
		http.Error(w, "preview lookup failed", http.StatusServiceUnavailable)
		return
	}
	if rec == nil || RecordEnded(rec, time.Now()) {
		s.refuse(w, r, host, StateEnded, "")
		return
	}
	var t ticket
	ok := false
	if ck, err := r.Cookie(CookieName); err == nil {
		if t, err = s.Signer.open(ck.Value); err == nil {
			ok = t.Record == string(rec.UID) && t.Host == host && time.Now().Before(time.Unix(t.Expiry, 0))
		}
	}
	switch {
	case !ok:
		s.refuse(w, r, host, StateExpired, t.Origin)
		return
	case !Allowed(rec, t.Subject):
		s.Log.Info("audit", "event", "request-refused", "namespace", rec.Namespace, "preview", rec.Name, "subject", t.Subject, "state", StateRevoked)
		s.refuse(w, r, host, StateRevoked, t.Origin)
		return
	}
	target, serving, err := s.Index.Resolve(r.Context(), rec)
	if err != nil {
		http.Error(w, "target lookup failed", http.StatusServiceUnavailable)
		return
	}
	if !serving {
		s.refuse(w, r, host, StateUnavailable, t.Origin)
		return
	}
	s.proxy(target.Addr).ServeHTTP(w, r)
}

// proxy forwards to the sandbox, HTTP and WebSocket alike, with the preview
// cookie removed on the way in and the platform's headers removed on the way
// out: the sandbox never sees the credential, and cannot forge a refusal or
// overwrite the cookie.
func (s *Server) proxy(addr string) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(&url.URL{Scheme: "http", Host: addr})
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
			stripCookie(pr.Out.Header, CookieName)
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("X-Preview-Access")
			kept := resp.Header.Values("Set-Cookie")[:0:0]
			for _, v := range resp.Header.Values("Set-Cookie") {
				if !strings.HasPrefix(strings.TrimSpace(v), CookieName+"=") {
					kept = append(kept, v)
				}
			}
			resp.Header.Del("Set-Cookie")
			for _, v := range kept {
				resp.Header.Add("Set-Cookie", v)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.Log.Info("upstream error", "addr", addr, "error", err.Error())
			w.Header().Set("X-Preview-Access", StateUnavailable)
			w.WriteHeader(http.StatusServiceUnavailable)
		},
	}
}

func stripCookie(h http.Header, name string) {
	var kept []string
	for _, line := range h.Values("Cookie") {
		for _, part := range strings.Split(line, ";") {
			if p := strings.TrimSpace(part); p != "" && !strings.HasPrefix(p, name+"=") {
				kept = append(kept, p)
			}
		}
	}
	h.Del("Cookie")
	if len(kept) > 0 {
		h.Set("Cookie", strings.Join(kept, "; "))
	}
}

var refusalPage = template.Must(template.New("r").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>Preview unavailable</title></head><body>
{{if .Origin}}<script nonce="{{.Nonce}}">
parent.postMessage({type: "zero-ops.preview-access", state: {{.State}}, previewId: {{.Preview}}}, {{.Origin}});
</script>{{end}}
</body></html>
`))

// refuse writes the refusal for state, in the shape ADR-108 §6 fixes: the
// header on every refusal; for a document, a page posting the state to the
// embedding app (a hint -- the canvas confirms against the state endpoint);
// otherwise a JSON body.
func (s *Server) refuse(w http.ResponseWriter, r *http.Request, host, state, origin string) {
	code := http.StatusForbidden
	switch state {
	case StateExpired:
		code = http.StatusUnauthorized
	case StateUnavailable:
		code = http.StatusServiceUnavailable
	}
	preview := ""
	ns := ""
	rec, err := s.Index.ByHost(r.Context(), host)
	if err != nil {
		s.Log.Error(err, "refusal: record lookup failed", "host", host)
		http.Error(w, "preview lookup failed", http.StatusServiceUnavailable)
		return
	}
	if rec != nil {
		preview, ns = rec.Name, rec.Namespace
	} else if n, ok := s.Index.endedNamespace(host); ok {
		ns = n
	}
	if origin == "" && ns != "" {
		if origin, err = s.namespaceOrigin(r.Context(), ns); err != nil {
			s.Log.Error(err, "refusal: app origin lookup failed", "namespace", ns)
			http.Error(w, "preview lookup failed", http.StatusServiceUnavailable)
			return
		}
	}

	w.Header().Set("X-Preview-Access", state)
	w.Header().Set("Cache-Control", "no-store")
	dest := r.Header.Get("Sec-Fetch-Dest")
	if dest == "document" || dest == "iframe" {
		nonce := make([]byte, 16)
		_, _ = rand.Read(nonce)
		n := base64.RawStdEncoding.EncodeToString(nonce)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", fmt.Sprintf("default-src 'none'; script-src 'nonce-%s'", n))
		w.WriteHeader(code)
		_ = refusalPage.Execute(w, map[string]string{"State": state, "Preview": preview, "Origin": origin, "Nonce": n})
		return
	}
	writeJSON(w, code, map[string]string{"previewAccess": state})
}

// namespaceOrigin is the app origin a namespace declares. A namespace that is
// gone, or declares none, has no app to post to: "" with no error, and the
// refusal page then posts nothing -- the canvas still sees the failed load and
// reads the state endpoint (§6).
func (s *Server) namespaceOrigin(ctx context.Context, ns string) (string, error) {
	var n corev1.Namespace
	if err := s.Reader.Get(ctx, client.ObjectKey{Name: ns}, &n); err != nil {
		return "", client.IgnoreNotFound(err)
	}
	return n.Annotations[AppOriginAnnotation], nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
