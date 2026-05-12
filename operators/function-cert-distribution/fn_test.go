package main

import (
	"testing"
	"time"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestIsNestedCertificateReady(t *testing.T) {
	tests := []struct {
		name     string
		obj      *unstructured.Unstructured
		expected bool
	}{
		{
			name:     "NilObject",
			obj:      nil,
			expected: false,
		},
		{
			name: "EmptyObject",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{},
			},
			expected: false,
		},
		{
			name: "NoConditions",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"atProvider": map[string]interface{}{
							"manifest": map[string]interface{}{
								"status": map[string]interface{}{},
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "ReadyConditionTrue",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"atProvider": map[string]interface{}{
							"manifest": map[string]interface{}{
								"status": map[string]interface{}{
									"conditions": []interface{}{
										map[string]interface{}{
											"type":   "Ready",
											"status": "True",
										},
									},
								},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "ReadyConditionFalse",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"atProvider": map[string]interface{}{
							"manifest": map[string]interface{}{
								"status": map[string]interface{}{
									"conditions": []interface{}{
										map[string]interface{}{
											"type":   "Ready",
											"status": "False",
										},
									},
								},
							},
						},
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isNestedCertificateReady(tt.obj)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGenerateCertDistributionObject(t *testing.T) {
	spokeName := "test-spoke"
	secretData := map[string]interface{}{
		"tls.crt": "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----",
		"tls.key": "-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----",
	}

	obj := generateCertDistributionObject(spokeName, secretData)

	// Verify basic structure
	assert.Equal(t, "kubernetes.crossplane.io/v1alpha2", obj.GetAPIVersion())
	assert.Equal(t, "Object", obj.GetKind())
	assert.Equal(t, "test-spoke-argocd-cert-dist", obj.GetName())

	// Verify spec exists
	spec, exists := obj.Object["spec"]
	require.True(t, exists)
	specMap, ok := spec.(map[string]interface{})
	require.True(t, ok)

	// Check management policies
	policies, exists := specMap["managementPolicies"]
	require.True(t, exists)

	// Try both []string and []interface{} types
	if policiesSlice, ok := policies.([]string); ok {
		assert.Equal(t, []string{"*"}, policiesSlice)
	} else if policiesSlice, ok := policies.([]interface{}); ok {
		assert.Equal(t, []interface{}{"*"}, policiesSlice)
	} else {
		t.Fatalf("Unexpected type for managementPolicies: %T", policies)
	}

	// Check provider config reference
	providerRef, exists := specMap["providerConfigRef"]
	require.True(t, exists)
	providerRefMap, ok := providerRef.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "test-spoke", providerRefMap["name"])

	// Check forProvider structure
	forProvider, exists := specMap["forProvider"]
	require.True(t, exists)
	forProviderMap, ok := forProvider.(map[string]interface{})
	require.True(t, ok)

	// Check manifest structure
	manifest, exists := forProviderMap["manifest"]
	require.True(t, exists)
	manifestMap, ok := manifest.(map[string]interface{})
	require.True(t, ok)

	assert.Equal(t, "v1", manifestMap["apiVersion"])
	assert.Equal(t, "Secret", manifestMap["kind"])
	assert.Equal(t, "kubernetes.io/tls", manifestMap["type"])

	// Check metadata
	metadata, exists := manifestMap["metadata"]
	require.True(t, exists)
	metadataMap, ok := metadata.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "argocd-agent-client-cert", metadataMap["name"])
	assert.Equal(t, "argocd", metadataMap["namespace"])

	// Check labels
	labels, exists := metadataMap["labels"]
	require.True(t, exists)
	labelsMap, ok := labels.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "crossplane", labelsMap["managed-by"])
	assert.Equal(t, "hub", labelsMap["source-cluster"])
	assert.Equal(t, "platform-capi", labelsMap["source-namespace"])
	assert.Equal(t, "argocd-agent-client", labelsMap["cert-type"])

	// Check secret data
	data, exists := manifestMap["data"]
	require.True(t, exists)
	dataMap, ok := data.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, secretData, dataMap)
}

func TestConstants(t *testing.T) {
	// Verify typed constants are properly set
	assert.Equal(t, xpv1.ConditionType("CertificatesMinted"), ConditionTypeCertificatesMinted)
	assert.Equal(t, xpv1.ConditionType("CertificatesDistributed"), ConditionTypeCertificatesDistributed)
	assert.Equal(t, xpv1.ConditionReason("WaitingForCertManager"), ReasonWaitingForCertManager)
	assert.Equal(t, xpv1.ConditionReason("WaitingForSecretProjection"), ReasonWaitingForSecretProjection)
	assert.Equal(t, xpv1.ConditionReason("MalformedSecretData"), ReasonMalformedSecretData)
	assert.Equal(t, xpv1.ConditionReason("Timeout"), ReasonTimeout)
	assert.Equal(t, 15*time.Minute, TimeoutThreshold)
}

func TestFunction_Structure(t *testing.T) {
	// Test that Function struct can be instantiated
	f := &Function{}
	assert.NotNil(t, f)
}

func TestFieldpathPave(t *testing.T) {
	// Test the fieldpath.Pave functionality used in isNestedCertificateReady
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"status": map[string]interface{}{
				"atProvider": map[string]interface{}{
					"manifest": map[string]interface{}{
						"status": map[string]interface{}{
							"conditions": []interface{}{
								map[string]interface{}{
									"type":   "Ready",
									"status": "True",
								},
							},
						},
					},
				},
			},
		},
	}

	// This should not panic
	result := isNestedCertificateReady(obj)
	assert.True(t, result)
}
