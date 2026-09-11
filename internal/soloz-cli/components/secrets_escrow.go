package components

import (
	"context"
	"fmt"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/soloz-io/zero-ops/internal/soloz-cli/constants"
)

// escrowEnv are the four values hub-operator reads to reach the escrow, in the
// order a human supplies them. Declared once because three places must agree on
// them: this installer, the operator's Deployment, and the scaffolding that asks.
var escrowEnv = []string{
	"INFISICAL_ESCROW_URL",
	"INFISICAL_ESCROW_CLIENT_ID",
	"INFISICAL_ESCROW_CLIENT_SECRET",
	"INFISICAL_ESCROW_PROJECT_ID",
}

// InstallEscrowCredentials creates the Secret hub-operator reads to reach the
// escrow that holds this box's Infisical master keys (ADR-076).
//
// The escrow is an Infisical the TENANT controls and this cluster does not host --
// Infisical Cloud, or one they run elsewhere. It cannot be the box's own Infisical:
// the keys being escrowed are the keys that decrypt it, and it is unreachable
// exactly when they are needed.
//
// The credentials are the tenant's and stay theirs. ADR-065 says the platform holds
// no secret credential belonging to a tenant, and the master keys are the one that
// unlocks all the others.
func (i *Installer) InstallEscrowCredentials(ctx context.Context) error {
	values := map[string]string{}
	var missing []string
	for _, name := range escrowEnv {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			values[name] = v
		} else {
			missing = append(missing, name)
		}
	}

	if len(missing) == len(escrowEnv) {
		// Whether a box may be built without an escrow is decided before this
		// point. Reaching here with none means that decision was made, so this
		// reports rather than refuses -- and reports plainly, because nothing
		// later will mention it again.
		fmt.Println("[bootstrap] no escrow configured — this box's Infisical master keys")
		fmt.Println("[bootstrap]   exist only inside it, and are lost with the cluster")
		return nil
	}
	if len(missing) > 0 {
		return fmt.Errorf("the escrow is partially configured; missing %s.\n"+
			"hub-operator reads all four from one Secret, so a partial set produces "+
			"an operator that fails every backup while reporting an escrow exists",
			strings.Join(missing, ", "))
	}

	config, err := clientcmd.BuildConfigFromFlags("", i.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	obj := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hub-operator-escrow-credentials",
			Namespace: constants.NamespaceOps,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "soloz-cli",
				"app.kubernetes.io/component":  "secret-zero",
				"app.kubernetes.io/part-of":    "hub-operator",
			},
		},
		Type:       corev1.SecretTypeOpaque,
		StringData: values,
	}

	secrets := clientset.CoreV1().Secrets(constants.NamespaceOps)
	if _, err := secrets.Create(ctx, obj, metav1.CreateOptions{}); err != nil {
		if _, uerr := secrets.Update(ctx, obj, metav1.UpdateOptions{}); uerr != nil {
			return fmt.Errorf("failed to create or update the escrow credentials: %w", uerr)
		}
		fmt.Println("[bootstrap] ✓ escrow credentials updated")
		return nil
	}
	fmt.Println("[bootstrap] ✓ escrow credentials created")
	return nil
}
