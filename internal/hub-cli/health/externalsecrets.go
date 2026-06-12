package health

import (
	"context"
	"fmt"
	"strings"
)

// ExternalSecretsReadyHealth verifies the operational readiness of the External
// Secrets Operator using identical 9.5/10 enterprise logic as the HubEnvironment
// controller. It ensures the CLI orchestrator does not proceed until ESO is ready.
type ExternalSecretsReadyHealth struct {
	Namespace string
	Label     string
}

// NewExternalSecretsReadyHealth returns a checker for ESO readiness.
func NewExternalSecretsReadyHealth(namespace string) *ExternalSecretsReadyHealth {
	return &ExternalSecretsReadyHealth{
		Namespace: namespace,
		Label:     "app.kubernetes.io/name=external-secrets-webhook",
	}
}

// Name returns the checker identifier.
func (e *ExternalSecretsReadyHealth) Name() string {
	return "ExternalSecrets Operational Readiness"
}

// Check performs the 6-stage verification via kubectl.
func (e *ExternalSecretsReadyHealth) Check(ctx context.Context, kubeconfig string) error {
	// 1. Check Deployment Available
	deployArgs := []string{
		"--kubeconfig", kubeconfig,
		"get", "deployment", "-n", e.Namespace,
		"-l", e.Label,
		"-o", "jsonpath={.items[0].status.conditions[?(@.type=='Available')].status}",
	}
	out, err := runKubectl(ctx, deployArgs)
	if err != nil || strings.TrimSpace(string(out)) != "True" {
		return fmt.Errorf("WaitingForWebhookDeployment: deployment is not available")
	}

	// 2. Check Service Exists
	svcArgs := []string{
		"--kubeconfig", kubeconfig,
		"get", "service", "-n", e.Namespace,
		"-l", e.Label,
		"-o", "name",
	}
	out, err = runKubectl(ctx, svcArgs)
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return fmt.Errorf("WaitingForWebhookService: service not found")
	}
	// Extract just the service name (e.g. "service/platform-external-secrets-webhook" -> "platform-external-secrets-webhook")
	svcNameParts := strings.Split(strings.TrimSpace(string(out)), "/")
	svcName := svcNameParts[len(svcNameParts)-1]

	// 3. Check EndpointSlice has Ready endpoints
	epArgs := []string{
		"--kubeconfig", kubeconfig,
		"get", "endpointslices", "-n", e.Namespace,
		"-l", e.Label,
		"-o", "jsonpath={.items[*].endpoints[*].conditions.ready}",
	}
	out, err = runKubectl(ctx, epArgs)
	if err != nil || !strings.Contains(string(out), "true") {
		return fmt.Errorf("WaitingForEndpointSlice: no ready endpoints found")
	}

	// 4 & 5. Check ValidatingWebhook references Service
	vwcArgs := []string{
		"--kubeconfig", kubeconfig,
		"get", "validatingwebhookconfigurations",
		"-o", fmt.Sprintf("jsonpath={range .items[*]}{range .webhooks[*]}{.clientConfig.service.namespace}{\"/\"}{.clientConfig.service.name}{\"\\n\"}{end}{end}"),
	}
	out, err = runKubectl(ctx, vwcArgs)
	if err != nil {
		return fmt.Errorf("WaitingForWebhookConfiguration: failed to list webhooks")
	}
	
	expectedSvcRef := fmt.Sprintf("%s/%s", e.Namespace, svcName)
	if !strings.Contains(string(out), expectedSvcRef) {
		return fmt.Errorf("WaitingForWebhookServiceReference: no ValidatingWebhook references %s", expectedSvcRef)
	}

	// 6. Check caBundle populated
	caArgs := []string{
		"--kubeconfig", kubeconfig,
		"get", "validatingwebhookconfigurations",
		"-o", fmt.Sprintf("jsonpath={range .items[*]}{range .webhooks[?(@.clientConfig.service.name=='%s')]}{.clientConfig.caBundle}{end}{end}", svcName),
	}
	out, err = runKubectl(ctx, caArgs)
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return fmt.Errorf("WaitingForCABundle: caBundle is not populated")
	}

	return nil
}
