package main

import (
	"context"
	"fmt"
	"time"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/fieldpath"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/function-sdk-go/errors"
	fnv1 "github.com/crossplane/function-sdk-go/proto/v1"
	"github.com/crossplane/function-sdk-go/request"
	"github.com/crossplane/function-sdk-go/resource"
	"github.com/crossplane/function-sdk-go/response"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	TimeoutThreshold = 15 * time.Minute

	// Typed Condition Constants
	ConditionTypeCertificatesMinted      xpv1.ConditionType = "CertificatesMinted"
	ConditionTypeCertificatesDistributed xpv1.ConditionType = "CertificatesDistributed"

	// Typed Reason Constants
	ReasonWaitingForCertManager      xpv1.ConditionReason = "WaitingForCertManager"
	ReasonWaitingForSecretProjection xpv1.ConditionReason = "WaitingForSecretProjection"
	ReasonMalformedSecretData        xpv1.ConditionReason = "MalformedSecretData"
	ReasonTimeout                    xpv1.ConditionReason = "Timeout"
)

// Function represents the composition function
type Function struct {
	fnv1.UnimplementedFunctionRunnerServiceServer
	log logging.Logger
}

// RunFunction executes the phased rendering logic for certificate distribution
func (f *Function) RunFunction(ctx context.Context, req *fnv1.RunFunctionRequest) (*fnv1.RunFunctionResponse, error) {
	log := f.log.WithValues("tag", req.GetMeta().GetTag())
	resp := response.To(req, response.DefaultTTL)

	// 1. Get the XR
	xr, err := request.GetObservedCompositeResource(req)
	if err != nil {
		return resp, errors.Wrap(err, "cannot get observed composite resource")
	}

	spokeName := xr.Resource.GetName()

	// 2. Extract Creation Timestamp for Timeout Evaluation
	// Note: Using time.Since() against wall-clock time is "functionally deterministic"
	// for orchestration purposes, though slightly reduces mathematical replay determinism.
	creationTimeStr, err := xr.Resource.GetString("metadata.creationTimestamp")
	if err != nil {
		return resp, errors.Wrap(err, "cannot get creation timestamp from XR")
	}
	creationTime, err := time.Parse(time.RFC3339, creationTimeStr)
	if err != nil {
		return resp, errors.Wrap(err, "cannot parse creation timestamp")
	}

	// 3. Safely Check Observed State for Dependency Readiness
	certResourceName := fmt.Sprintf("%s-argocd-agent-client-cert", spokeName)
	observedComposed, err := request.GetObservedComposedResources(req)
	if err != nil {
		return resp, errors.Wrap(err, "cannot get observed composed resources")
	}

	certObserved := observedComposed[resource.Name(certResourceName)]
	certReady := false
	if certObserved.Resource != nil {
		// Convert from composed.Unstructured to unstructured.Unstructured
		unstructuredObj := &unstructured.Unstructured{
			Object: certObserved.Resource.Object,
		}
		certReady = isNestedCertificateReady(unstructuredObj)
	}

	// 4. Phase 1 Gating: Waiting for cert-manager
	if !certReady {
		if time.Since(creationTime) > TimeoutThreshold {
			// Escalate to Degraded/Timeout state
			response.ConditionFalse(resp, string(ConditionTypeCertificatesMinted), string(ReasonTimeout)).
				WithMessage("Certificate generation timed out after 15 minutes. Check cert-manager logs on Hub.")
			log.Info("Certificate generation timed out", "spoke", spokeName)
		} else {
			// Normal pending state
			response.ConditionFalse(resp, string(ConditionTypeCertificatesMinted), string(ReasonWaitingForCertManager)).
				WithMessage("Waiting for cert-manager to mint the client certificate")
			log.Debug("Nested Certificate not ready, short-circuiting cert distribution", "spoke", spokeName)
		}

		// Return gracefully; omit the downstream resource to prevent patchesFrom deadlocks
		return resp, nil
	}

	// 5. Phase 2 Gating: Waiting for Secret Data Projection
	paved := fieldpath.Pave(certObserved.Resource.Object)
	secretDataRaw, err := paved.GetValue("status.atProvider.manifest.data")
	if err != nil {
		// Transient state: Cert is ready, but payload hasn't synced back yet.
		// Explicitly emit BOTH conditions to avoid overwrite semantics / state flapping.
		response.ConditionTrue(resp, string(ConditionTypeCertificatesMinted), string(xpv1.ReasonAvailable)).
			WithMessage("Certificates successfully minted")
		response.ConditionFalse(resp, string(ConditionTypeCertificatesDistributed), string(ReasonWaitingForSecretProjection)).
			WithMessage("Certificate is ready, waiting for secret data to project into observed state")
		log.Debug("Secret data not yet populated in observed state, waiting...", "spoke", spokeName)
		return resp, nil
	}

	// 6. Safely assert the secret data type to prevent panics
	secretDataMap, ok := secretDataRaw.(map[string]interface{})
	if !ok {
		response.ConditionTrue(resp, string(ConditionTypeCertificatesMinted), string(xpv1.ReasonAvailable)).
			WithMessage("Certificates successfully minted")
		response.ConditionFalse(resp, string(ConditionTypeCertificatesDistributed), string(ReasonMalformedSecretData)).
			WithMessage("Secret data projection is malformed in the observed state")
		log.Info("Failed to assert secret data to map[string]interface{}", "spoke", spokeName)
		return resp, nil
	}

	// 7. Preconditions Met: Set Ready Conditions and Render Downstream Object
	response.ConditionTrue(resp, string(ConditionTypeCertificatesMinted), string(xpv1.ReasonAvailable)).
		WithMessage("Certificates successfully minted")
	response.ConditionTrue(resp, string(ConditionTypeCertificatesDistributed), string(xpv1.ReasonAvailable)).
		WithMessage("Certificates successfully distributed to spoke cluster")

	certDistObject := generateCertDistributionObject(spokeName, secretDataMap)

	// Set the desired composed resource using the correct API
	desiredResources := map[resource.Name]*unstructured.Unstructured{
		resource.Name(certResourceName + "-dist"): certDistObject,
	}

	if err := response.SetDesiredResources(resp, desiredResources); err != nil {
		return resp, errors.Wrap(err, "cannot set desired composed resource")
	}

	return resp, nil
}

// isNestedCertificateReady safely evaluates the conditions of the inner cert-manager Certificate
func isNestedCertificateReady(obj *unstructured.Unstructured) bool {
	if obj == nil {
		return false
	}
	paved := fieldpath.Pave(obj.Object)

	// Traverse to the cert-manager Certificate's status conditions
	conditions, err := paved.GetValue("status.atProvider.manifest.status.conditions")
	if err != nil {
		return false
	}

	condsList, ok := conditions.([]interface{})
	if !ok {
		return false
	}

	for _, c := range condsList {
		cond, ok := c.(map[string]interface{})
		if ok && cond["type"] == "Ready" && cond["status"] == "True" {
			return true
		}
	}
	return false
}

// generateCertDistributionObject creates a strictly idempotent Desired State object
func generateCertDistributionObject(spokeName string, secretData map[string]interface{}) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "kubernetes.crossplane.io/v1alpha2",
			"kind":       "Object",
			"metadata": map[string]interface{}{
				"name": fmt.Sprintf("%s-argocd-cert-dist", spokeName),
			},
			"spec": map[string]interface{}{
				"managementPolicies": []string{"*"},
				"providerConfigRef": map[string]interface{}{
					"name": spokeName,
				},
				"forProvider": map[string]interface{}{
					"manifest": map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Secret",
						"metadata": map[string]interface{}{
							"name":      "argocd-agent-client-cert",
							"namespace": "argocd",
							"labels": map[string]interface{}{
								"managed-by":       "crossplane",
								"source-cluster":   "hub",
								"source-namespace": "platform-capi",
								"cert-type":        "argocd-agent-client",
							},
						},
						"type": "kubernetes.io/tls",
						// Safe Injection: Replaces patchesFrom.
						// Note: Do NOT log this payload in debug mode.
						"data": secretData,
					},
				},
			},
		},
	}
}
