package infisical

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// PortForwardManager manages a kubectl port-forward subprocess to tunnel
// into the Infisical service from a local port. The Go binary uses this
// to reach the Infisical API without depending on the shell script's
// background port-forward (which dies when the Infisical pod restarts).
type PortForwardManager struct {
	namespace string
	svcName   string
	localPort string
	remotePort string
	kubeconfig string
	cmd       *exec.Cmd
	stopFn    context.CancelFunc
}

func NewPortForwardManager(namespace, svcName, localPort, remotePort, kubeconfig string) *PortForwardManager {
	return &PortForwardManager{
		namespace:  namespace,
		svcName:    svcName,
		localPort:  localPort,
		remotePort: remotePort,
		kubeconfig: kubeconfig,
	}
}

func (pf *PortForwardManager) LocalURL() string {
	return "http://localhost:" + pf.localPort
}

func (pf *PortForwardManager) Start() error {
	args := []string{"port-forward", "-n", pf.namespace, "svc/" + pf.svcName, pf.localPort + ":" + pf.remotePort}
	if pf.kubeconfig != "" {
		args = append([]string{"--kubeconfig", pf.kubeconfig}, args...)
	}

	// Create a cancellable context so Stop() can kill the subprocess
	ctx, cancel := context.WithCancel(context.Background())
	pf.stopFn = cancel

	pf.cmd = exec.CommandContext(ctx, "kubectl", args...)
	if err := pf.cmd.Start(); err != nil {
		cancel()
		pf.stopFn = nil
		return fmt.Errorf("start port-forward: %w", err)
	}

	return nil
}

func (pf *PortForwardManager) IsRunning() bool {
	if pf.cmd == nil || pf.cmd.Process == nil {
		return false
	}
	return pf.cmd.ProcessState == nil || !pf.cmd.ProcessState.Exited()
}

func (pf *PortForwardManager) Stop() {
	if pf.stopFn != nil {
		pf.stopFn()
		pf.stopFn = nil
	}
	if pf.cmd != nil && pf.cmd.Process != nil {
		pf.cmd.Process.Kill()
	}
}

func (pf *PortForwardManager) WaitForAPI(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	checkURL := pf.LocalURL() + "/api/v1/auth/checkAuth"

	for time.Now().Before(deadline) {
		if !pf.IsRunning() {
			return fmt.Errorf("port-forward process exited before API became available")
		}

		resp, err := http.Get(checkURL)
		if err == nil {
			resp.Body.Close()
			// Any response (200, 401, etc.) means the API is reachable
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	return fmt.Errorf("timeout after %v waiting for Infisical API at %s", timeout, checkURL)
}

// EnsureAPIAccess starts the port-forward (if not running), waits for the API
// to respond, and sets INFISICAL_API_URL so infisical.NewClient picks it up.
func (pf *PortForwardManager) EnsureAPIAccess(ctx context.Context) error {
	if !pf.IsRunning() {
		fmt.Println("[port-forward] Starting kubectl port-forward to Infisical service...")
		if err := pf.Start(); err != nil {
			return fmt.Errorf("start port-forward: %w", err)
		}
		// Brief pause for the PF to bind the local port
		time.Sleep(1 * time.Second)
	} else {
		fmt.Println("[port-forward] Port-forward already running")
	}

	if err := pf.WaitForAPI(ctx, 30*time.Second); err != nil {
		return fmt.Errorf("wait for Infisical API: %w", err)
	}

	localURL := pf.LocalURL()
	if current := os.Getenv("INFISICAL_API_URL"); current != localURL {
		os.Setenv("INFISICAL_API_URL", localURL)
		fmt.Printf("[port-forward] INFISICAL_API_URL set to %s\n", localURL)
	}

	return nil
}

// RunWithPortForward wraps a function with port-forward lifecycle management.
// It starts the PF, ensures the API is reachable, runs fn, and stops the PF.
// If fn returns a connection error, the PF is restarted and fn is retried.
func RunWithPortForward(ctx context.Context, namespace, svcName, localPort, remotePort, kubeconfig string, retries int, fn func() error) error {
	pf := NewPortForwardManager(namespace, svcName, localPort, remotePort, kubeconfig)

	for attempt := 0; attempt <= retries; attempt++ {
		if err := pf.EnsureAPIAccess(ctx); err != nil {
			return fmt.Errorf("port-forward setup failed: %w", err)
		}

		err := fn()
		if err == nil {
			pf.Stop()
			return nil
		}

		if isConnectionError(err) && attempt < retries {
			fmt.Printf("[port-forward] connection error (attempt %d/%d), restarting port-forward: %v\n", attempt+1, retries, err)
			pf.Stop()
			time.Sleep(2 * time.Second)
			continue
		}

		pf.Stop()
		return err
	}

	return nil // unreachable
}

func isConnectionError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "EOF")
}
