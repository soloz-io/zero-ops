package readiness

import (
	"context"
	"fmt"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ExternalSecretsChecker struct {
	Client    client.Client
	Namespace string
	Selector  map[string]string
}

func NewExternalSecretsChecker(c client.Client, namespace string) *ExternalSecretsChecker {
	return &ExternalSecretsChecker{
		Client:    c,
		Namespace: namespace,
		Selector: map[string]string{
			"app.kubernetes.io/name": "external-secrets-webhook",
		},
	}
}

func (e *ExternalSecretsChecker) Check(ctx context.Context) (ReadyStatus, error) {
	// 1. Check Deployment Available
	deployList := &appsv1.DeploymentList{}
	if err := e.Client.List(ctx, deployList, client.InNamespace(e.Namespace), client.MatchingLabels(e.Selector)); err != nil {
		return ReadyStatus{}, fmt.Errorf("list deployments: %w", err)
	}
	if len(deployList.Items) == 0 {
		return ReadyStatus{Ready: false, Reason: "WaitingForWebhookDeployment", Message: "Webhook deployment not found"}, nil
	}
	deploy := deployList.Items[0]
	available := false
	for _, cond := range deploy.Status.Conditions {
		if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
			available = true
			break
		}
	}
	if !available {
		return ReadyStatus{Ready: false, Reason: "WaitingForWebhookDeployment", Message: "Webhook deployment is not available"}, nil
	}

	// 2. Check Service Exists
	svcList := &corev1.ServiceList{}
	if err := e.Client.List(ctx, svcList, client.InNamespace(e.Namespace), client.MatchingLabels(e.Selector)); err != nil {
		return ReadyStatus{}, fmt.Errorf("list services: %w", err)
	}
	if len(svcList.Items) == 0 {
		return ReadyStatus{Ready: false, Reason: "WaitingForWebhookService", Message: "Webhook service not found"}, nil
	}
	svc := svcList.Items[0]

	// 3. Check EndpointSlice has Ready endpoints
	epList := &discoveryv1.EndpointSliceList{}
	if err := e.Client.List(ctx, epList, client.InNamespace(e.Namespace), client.MatchingLabels(e.Selector)); err != nil {
		return ReadyStatus{}, fmt.Errorf("list endpointslices: %w", err)
	}
	if len(epList.Items) == 0 {
		return ReadyStatus{Ready: false, Reason: "WaitingForEndpointSlice", Message: "Webhook endpointslice not found"}, nil
	}
	hasReadyEndpoint := false
	for _, slice := range epList.Items {
		for _, endpoint := range slice.Endpoints {
			if endpoint.Conditions.Ready != nil && *endpoint.Conditions.Ready {
				hasReadyEndpoint = true
				break
			}
		}
		if hasReadyEndpoint {
			break
		}
	}
	if !hasReadyEndpoint {
		return ReadyStatus{Ready: false, Reason: "WaitingForEndpointSlice", Message: "Webhook endpointslice has no ready endpoints"}, nil
	}

	// 4. Check ValidatingWebhookConfiguration
	vwcList := &admissionregistrationv1.ValidatingWebhookConfigurationList{}
	if err := e.Client.List(ctx, vwcList); err != nil {
		return ReadyStatus{}, fmt.Errorf("list validatingwebhookconfigurations: %w", err)
	}

	var foundWebhook *admissionregistrationv1.ValidatingWebhook
	for _, vwc := range vwcList.Items {
		for i, wh := range vwc.Webhooks {
			if wh.ClientConfig.Service != nil &&
				wh.ClientConfig.Service.Name == svc.Name &&
				wh.ClientConfig.Service.Namespace == svc.Namespace {
				foundWebhook = &vwc.Webhooks[i]
				break
			}
		}
		if foundWebhook != nil {
			break
		}
	}

	if foundWebhook == nil {
		return ReadyStatus{Ready: false, Reason: "WaitingForWebhookServiceReference", Message: "No ValidatingWebhook references the webhook service"}, nil
	}

	// 5. Check caBundle
	if len(foundWebhook.ClientConfig.CABundle) == 0 {
		return ReadyStatus{Ready: false, Reason: "WaitingForCABundle", Message: "Webhook caBundle is empty"}, nil
	}

	return ReadyStatus{Ready: true}, nil
}
