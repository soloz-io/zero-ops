// Package localmode is preview-access's local development mode (ADR-108,
// "Local development mode"): everything local about the service, and nothing
// else.
//
// It exists so a developer can verify code builds and the preview logic on a
// Kind cluster: records, hosts, Ready ranking, the codes and state endpoints,
// proxying (WebSocket included) and the `ended`/`unavailable` refusals. It
// deliberately does NOT reproduce what only a real environment can prove —
// origin isolation on a separate registrable domain, login, the handoff and
// the partitioned cookie. Those are verified in dev.
//
// It refuses to run anywhere but Kind (RequireKind), so it cannot be switched
// on in a real environment by mistake. The manifests and scripts that use it
// are under zero-ops/scripts/local/.
package localmode

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	computev1alpha1 "github.com/soloz-io/zero-ops/operators/ephemeral-job-operator/api/v1alpha1"
	"github.com/soloz-io/zero-ops/operators/ephemeral-job-operator/internal/previewaccess"
)

// Config is local mode's whole configuration, from the environment.
type Config struct {
	// Namespace is where the local application's records live.
	Namespace string
	// AppOrigin is the local application's origin, e.g. http://localhost:3002:
	// the only Origin the codes endpoint accepts, and where refusal pages post.
	AppOrigin string
	// Subject is the identity every local request acts as: the subject of the
	// application's development identity.
	Subject string
	// PreviewBase is the URL a preview is opened at, with {host} for the
	// record's host, e.g. http://{host}:3010.
	PreviewBase string
}

// ConfigFromEnv reads Config. Every value is required: a missing one is named.
func ConfigFromEnv() (Config, error) {
	c := Config{
		Namespace:   os.Getenv("LOCAL_NAMESPACE"),
		AppOrigin:   os.Getenv("LOCAL_APP_ORIGIN"),
		Subject:     os.Getenv("LOCAL_SUBJECT"),
		PreviewBase: os.Getenv("LOCAL_PREVIEW_BASE"),
	}
	for name, v := range map[string]string{
		"LOCAL_NAMESPACE": c.Namespace, "LOCAL_APP_ORIGIN": c.AppOrigin,
		"LOCAL_SUBJECT": c.Subject, "LOCAL_PREVIEW_BASE": c.PreviewBase,
	} {
		if v == "" {
			return Config{}, fmt.Errorf("local mode requires %s", name)
		}
	}
	if !strings.Contains(c.PreviewBase, "{host}") {
		return Config{}, fmt.Errorf("LOCAL_PREVIEW_BASE %q must contain {host}", c.PreviewBase)
	}
	return c, nil
}

// RequireKind refuses to proceed unless every node of the cluster is a Kind
// node. This is what makes local mode impossible to run in a real environment.
func RequireKind(ctx context.Context, r client.Reader) error {
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return fmt.Errorf("listing nodes to confirm this is a Kind cluster: %w", err)
	}
	if len(nodes.Items) == 0 {
		return fmt.Errorf("local mode runs only on a Kind cluster; this cluster reports no nodes")
	}
	for _, n := range nodes.Items {
		if !strings.HasPrefix(n.Spec.ProviderID, "kind://") {
			return fmt.Errorf("local mode runs only on a Kind cluster; node %s is %q", n.Name, n.Spec.ProviderID)
		}
	}
	return nil
}

// Identity is local mode's previewaccess.Identity: every caller is the
// configured development identity of the configured application.
type Identity struct{ Config Config }

func (i Identity) Caller(*http.Request) (previewaccess.Caller, error) {
	return previewaccess.Caller{Namespace: i.Config.Namespace, Origin: i.Config.AppOrigin, Subject: i.Config.Subject}, nil
}

func (i Identity) AppOrigin(context.Context, string) (string, error) { return i.Config.AppOrigin, nil }

// Session is local mode's previewaccess.Session: no handoff and no cookie.
// A preview request is admitted when the record admits the development
// identity; every other check (the record exists and is current, its target
// is running) is the server's and applies unchanged.
type Session struct{ Config Config }

func (s Session) Begin(rec *computev1alpha1.PreviewAccess, _ previewaccess.Caller, now time.Time) (string, time.Duration, error) {
	return strings.ReplaceAll(s.Config.PreviewBase, "{host}", rec.Status.Host) + "/",
		previewaccess.SessionLifetime(rec, now), nil
}

func (Session) ServeOwn(http.ResponseWriter, *http.Request, string, previewaccess.RefuseFunc) bool {
	return false
}

func (s Session) Admit(_ *http.Request, rec *computev1alpha1.PreviewAccess) (string, string, string) {
	if !previewaccess.Allowed(rec, s.Config.Subject) {
		return s.Config.Subject, s.Config.AppOrigin, previewaccess.StateDenied
	}
	return s.Config.Subject, s.Config.AppOrigin, ""
}

// Holds: with no cookie, an allowed identity is always admitted.
func (Session) Holds(*computev1alpha1.PreviewAccess, string, time.Time) (bool, *time.Time) {
	return true, nil
}

func (Session) WasAdmitted(*computev1alpha1.PreviewAccess, string) bool { return false }
