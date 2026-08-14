// +kubebuilder:object:generate=true
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=smi;smids
// +kubebuilder:printcolumn:name="Spoke",type=string,JSONPath=".spec.spokeRef.name"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// SpokeMachineIdentity manages a Machine Identity in Infisical for a Spoke cluster.
// It is the declarative representation of a Spoke's infrastructure identity.
// The Spoke Identity Operator reconciles this CR against the Infisical API.
//
// NOTE: This is the initial implementation of the Platform Identity Domain.
// It may evolve to support full identity lifecycle (rotation, revocation, attestation, audit)
// as defined in a future ADR.
type SpokeMachineIdentity struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SpokeMachineIdentitySpec   `json:"spec,omitempty"`
	Status SpokeMachineIdentityStatus `json:"status,omitempty"`
}

type SpokeMachineIdentitySpec struct {
	// SpokeRef identifies the SpokePool this identity belongs to
	SpokeRef SpokeRef `json:"spokeRef"`

	// Infisical configures the Infisical provider
	Infisical InfisicalConfig `json:"infisical"`

	// SecretName overrides the default CRS wrapper Secret name.
	// Default: "<spokeRef.name>-machine-identity"
	// +optional
	SecretName string `json:"secretName,omitempty"`

	// RotationPolicy defines credential rotation settings
	// +optional
	RotationPolicy *RotationPolicy `json:"rotationPolicy,omitempty"`

	// RevocationPolicy defines credential revocation settings
	// +optional
	RevocationPolicy *RevocationPolicy `json:"revocationPolicy,omitempty"`
}

type SpokeRef struct {
	Name string `json:"name"`
}

type InfisicalConfig struct {
	// +optional
	OrganizationID string `json:"organizationID,omitempty"`
	// +optional
	ProjectID string `json:"projectID,omitempty"`
	// SecretsProjectID is the Infisical project ID for secret-manager access
	// (e.g. hub-secrets) that the spoke machine identity must be granted
	// access to so the spoke ESO ClusterSecretStore can read tenant secrets.
	// Defaults to the operator's --infisical-secrets-project-id flag when unset.
	// +optional
	SecretsProjectID string `json:"secretsProjectID,omitempty"`
	// +kubebuilder:default="universal-auth"
	// +optional
	AuthMethod string `json:"authMethod,omitempty"`
	// +kubebuilder:default="90d"
	// +optional
	ClientSecretTTL string `json:"clientSecretTTL,omitempty"`
}

type RotationPolicy struct {
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`
	// +kubebuilder:default="60d"
	// +optional
	Interval string `json:"interval,omitempty"`
	// +kubebuilder:default="24h"
	// +optional
	OverlapPeriod string `json:"overlapPeriod,omitempty"`
}

type RevocationPolicy struct {
	// +kubebuilder:default=true
	RevokeOnDelete bool `json:"revokeOnDelete"`
	// +kubebuilder:default="72h"
	// +optional
	GracePeriod string `json:"gracePeriod,omitempty"`
}

type SpokeMachineIdentityStatus struct {
	// IdentityId is the Infisical Machine Identity ID
	// +optional
	IdentityID string `json:"identityID,omitempty"`
	// ClientId is the Infisical Universal Auth client ID
	// +optional
	ClientID string `json:"clientID,omitempty"`
	// ClientSecretIds tracks active client secrets for lifecycle management
	// +optional
	ClientSecretIDs []string `json:"clientSecretIDs,omitempty"`
	// Conditions represent the latest state observations
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// LastRotated is when the most recent rotation occurred
	// +optional
	LastRotated *metav1.Time `json:"lastRotated,omitempty"`
	// NextRotation is when the next rotation is due
	// +optional
	NextRotation *metav1.Time `json:"nextRotation,omitempty"`
}

// +kubebuilder:object:root=true

type SpokeMachineIdentityList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SpokeMachineIdentity `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SpokeMachineIdentity{}, &SpokeMachineIdentityList{})
}
