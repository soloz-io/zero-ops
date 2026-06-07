package health

import (
	"context"
	"fmt"
	"strings"
)

// CRDRegisteredHealth is a HealthChecker that waits for one or more
// CustomResourceDefinitions to be registered. The check passes when
// every named CRD appears in `kubectl get crd` output.
type CRDRegisteredHealth struct {
	CRDs []string
}

// NewCRDRegisteredHealth returns a checker that verifies all listed CRDs
// are registered. Names should be the fully-qualified form, e.g.,
// "clusters.cluster.x-k8s.io".
func NewCRDRegisteredHealth(crds ...string) *CRDRegisteredHealth {
	return &CRDRegisteredHealth{CRDs: crds}
}

// Name returns the checker identifier.
func (c *CRDRegisteredHealth) Name() string {
	return fmt.Sprintf("CRDs [%s]", strings.Join(c.CRDs, ", "))
}

// Check verifies each CRD is registered.
func (c *CRDRegisteredHealth) Check(ctx context.Context, kubeconfig string) error {
	args := []string{"get", "crd", "-o", "name"}
	out, err := runKubectl(ctx, append([]string{"--kubeconfig", kubeconfig}, args...))
	if err != nil {
		return fmt.Errorf("list CRDs: %w", err)
	}
	haystack := string(out)
	missing := make([]string, 0, len(c.CRDs))
	for _, crd := range c.CRDs {
		if !strings.Contains(haystack, crd) {
			missing = append(missing, crd)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing CRDs: %s", strings.Join(missing, ", "))
	}
	return nil
}

// ValidatingWebhookHealth is a HealthChecker that waits for one or more
// validating webhook configurations to exist. Names are matched as
// substrings of `kubectl get validatingwebhookconfigurations -o name`,
// so callers can pass short identifiers like "capi", "cert-manager".
type ValidatingWebhookHealth struct {
	Patterns []string
}

// NewValidatingWebhookHealth returns a checker that verifies all listed
// substrings appear in the validating webhook configurations.
func NewValidatingWebhookHealth(patterns ...string) *ValidatingWebhookHealth {
	return &ValidatingWebhookHealth{Patterns: patterns}
}

// Name returns the checker identifier.
func (v *ValidatingWebhookHealth) Name() string {
	return fmt.Sprintf("validating webhooks [%s]", strings.Join(v.Patterns, ", "))
}

// Check probes validating webhook configurations.
func (v *ValidatingWebhookHealth) Check(ctx context.Context, kubeconfig string) error {
	args := []string{
		"--kubeconfig", kubeconfig,
		"get", "validatingwebhookconfigurations", "-o", "name",
	}
	out, err := runKubectl(ctx, args)
	if err != nil {
		return fmt.Errorf("list validating webhooks: %w", err)
	}
	haystack := string(out)
	missing := make([]string, 0, len(v.Patterns))
	for _, p := range v.Patterns {
		if !strings.Contains(haystack, p) {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing webhook patterns: %s", strings.Join(missing, ", "))
	}
	return nil
}

// OperatorPodsHealth is a HealthChecker that waits for at least one pod
// matching a label selector in a given namespace to be in the Running phase.
type OperatorPodsHealth struct {
	Namespace     string
	LabelSelector string
}

// NewOperatorPodsHealth returns a checker for the given label selector.
func NewOperatorPodsHealth(namespace, labelSelector string) *OperatorPodsHealth {
	return &OperatorPodsHealth{Namespace: namespace, LabelSelector: labelSelector}
}

// Name returns the checker identifier.
func (o *OperatorPodsHealth) Name() string {
	return fmt.Sprintf("pods -n %s -l %s", o.Namespace, o.LabelSelector)
}

// Check verifies at least one pod is Running.
func (o *OperatorPodsHealth) Check(ctx context.Context, kubeconfig string) error {
	args := []string{
		"--kubeconfig", kubeconfig,
		"get", "pods", "-n", o.Namespace,
		"-l", o.LabelSelector,
		"-o", "jsonpath={.items[?(@.status.phase=='Running')].metadata.name}",
	}
	out, err := runKubectl(ctx, args)
	if err != nil {
		return fmt.Errorf("list pods: %w", err)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return fmt.Errorf("no Running pods match")
	}
	return nil
}

// CAPIResourceReadyHealth is a HealthChecker that waits for a CAPI
// resource (CoreProvider, BootstrapProvider, ControlPlaneProvider,
// InfrastructureProvider, Cluster, etc.) to report a Ready condition
// of status=True.
//
// The check is generic — the caller passes the resource kind and name.
type CAPIResourceReadyHealth struct {
	Kind         string
	ResourceName string
	Namespace    string
}

// NewCAPIResourceReadyHealth returns a checker for a CAPI resource's
// Ready condition.
func NewCAPIResourceReadyHealth(kind, name, namespace string) *CAPIResourceReadyHealth {
	return &CAPIResourceReadyHealth{Kind: kind, ResourceName: name, Namespace: namespace}
}

// Name returns the checker identifier.
func (c *CAPIResourceReadyHealth) Name() string {
	return fmt.Sprintf("%s/%s ready", c.Kind, c.ResourceName)
}

// Check probes the Ready condition.
func (c *CAPIResourceReadyHealth) Check(ctx context.Context, kubeconfig string) error {
	args := []string{
		"--kubeconfig", kubeconfig,
		"get", c.Kind, c.ResourceName,
		"-n", c.Namespace,
		"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}",
	}
	out, err := runKubectl(ctx, args)
	if err != nil {
		return fmt.Errorf("get %s/%s: %w", c.Kind, c.ResourceName, err)
	}
	if strings.TrimSpace(string(out)) != "True" {
		return fmt.Errorf("Ready condition not True (got %q)", strings.TrimSpace(string(out)))
	}
	return nil
}

// AllMachinesHaveNodesHealth is a HealthChecker that waits for every
// CAPI Machine in the namespace to have a status.nodeRef assigned
// (i.e., the underlying node has joined the cluster).
type AllMachinesHaveNodesHealth struct {
	Namespace string
}

// NewAllMachinesHaveNodesHealth returns a checker that verifies all
// machines in the namespace have joined.
func NewAllMachinesHaveNodesHealth(namespace string) *AllMachinesHaveNodesHealth {
	return &AllMachinesHaveNodesHealth{Namespace: namespace}
}

// Name returns the checker identifier.
func (a *AllMachinesHaveNodesHealth) Name() string {
	return fmt.Sprintf("all machines in %s joined", a.Namespace)
}

// Check probes machines for nodeRef.
func (a *AllMachinesHaveNodesHealth) Check(ctx context.Context, kubeconfig string) error {
	args := []string{
		"--kubeconfig", kubeconfig,
		"get", "machines",
		"-n", a.Namespace,
		"-o", "jsonpath={range .items[*]}{.metadata.name}:{.status.nodeRef.name}{\"\\n\"}{end}",
	}
	out, err := runKubectl(ctx, args)
	if err != nil {
		return fmt.Errorf("list machines: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return fmt.Errorf("no machines found")
	}
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) != 2 || parts[1] == "" {
			return fmt.Errorf("machine %q has no nodeRef", parts[0])
		}
	}
	return nil
}
