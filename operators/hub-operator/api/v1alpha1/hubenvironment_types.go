/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// HubEnvironmentSpec defines the desired state of HubEnvironment
//
// ADR-051 (2026-08-24 addendum): spec.domain is the sole authoritative base-domain
// value for hub public endpoints, and spec.environment is the sole authoritative
// environment identity. The env-as-zone scheme requires a non-production
// environment to own a labelled zone and production to use the apex unlabelled.
//
// These rules key off spec.environment, never off a service's own environment
// field. Environment identity is a platform concept: deriving it from, say, the
// secret store's project environment would couple the public DNS contract to one
// vendor, break if that vendor were replaced, and silently no-op wherever that
// optional block is absent. Services consume spec.environment; they do not define it.
//
// The list of non-production labels below is the single place the environment set is
// enumerated. Adding an environment touches that one line.
//
// +kubebuilder:validation:XValidation:rule="self.environment == 'prod' || self.domain.startsWith(self.environment + '.')",message="domain must be the environment's own zone: for a non-production environment, domain must begin with that environment as its leftmost label (ADR-051 env-as-zone)"
// +kubebuilder:validation:XValidation:rule="self.environment != 'prod' || !(['dev.','stg.','ephemeral.'].exists(p, self.domain.startsWith(p)))",message="production must use the apex domain unlabelled: domain must not begin with a non-production environment label (ADR-051 env-as-zone)"
// +kubebuilder:validation:XValidation:rule="!has(self.secrets) || !has(self.secrets.infisical) || !has(self.secrets.infisical.environmentSlug) || self.secrets.infisical.environmentSlug == ” || self.secrets.infisical.environmentSlug == self.environment",message="secrets.infisical.environmentSlug must equal spec.environment: environment identity has a single authority and services consume it (ADR-043 single authority per domain)"
type HubEnvironmentSpec struct {
	// Environment is the platform environment identity for this Hub and the single
	// authority for it. Every environment-scoped value — the public DNS zone, the
	// secret store's project environment, and anything added later — derives from
	// this field rather than declaring its own copy.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=dev;stg;prod;ephemeral
	Environment string `json:"environment"`

	// Domain is the base domain for the Hub cluster (e.g., nutgraf.in)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	Domain string `json:"domain"`

	// TLS configuration for the Hub cluster
	// +optional
	TLS *TLSConfig `json:"tls,omitempty"`

	// Observability configuration for metrics and monitoring
	// +optional
	Observability *ObservabilityConfig `json:"observability,omitempty"`

	// Database configuration for CNPG cluster and roles
	// +kubebuilder:validation:Required
	Database DatabaseConfig `json:"database"`

	// NATS configuration for JetStream streams
	// +optional
	NATS *NATSConfig `json:"nats,omitempty"`

	// OAuth configuration for Hydra clients
	// +optional
	OAuth *OAuthConfig `json:"oauth,omitempty"`

	// Secrets configuration for Infisical backup
	// +optional
	Secrets *SecretsConfig `json:"secrets,omitempty"`
}

// TLSConfig defines TLS certificate configuration
type TLSConfig struct {
	// Issuer is the cert-manager ClusterIssuer name
	// +kubebuilder:validation:Required
	Issuer string `json:"issuer"`

	// Email for Let's Encrypt notifications
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Format=email
	Email string `json:"email"`
}

// ObservabilityConfig defines observability settings
type ObservabilityConfig struct {
	// VictoriaMetrics configuration
	// +optional
	VictoriaMetrics *VictoriaMetricsConfig `json:"victoriaMetrics,omitempty"`

	// GrafanaAlloy configuration
	// +optional
	GrafanaAlloy *GrafanaAlloyConfig `json:"grafanaAlloy,omitempty"`
}

// VictoriaMetricsConfig defines VictoriaMetrics settings
type VictoriaMetricsConfig struct {
	// RetentionPeriod for metrics storage (e.g., "30d", "90d")
	// +kubebuilder:default="30d"
	// +optional
	RetentionPeriod string `json:"retentionPeriod,omitempty"`
}

// GrafanaAlloyConfig defines Grafana Alloy settings
type GrafanaAlloyConfig struct {
	// ScrapeInterval for metrics collection (e.g., "30s", "15s")
	// +kubebuilder:default="30s"
	// +optional
	ScrapeInterval string `json:"scrapeInterval,omitempty"`
}

// DatabaseConfig defines CNPG cluster and role configuration
type DatabaseConfig struct {
	// ClusterRef is the name of the CNPG Cluster (e.g., platform-db)
	// +kubebuilder:validation:Required
	ClusterRef string `json:"clusterRef"`

	// Namespace of the CNPG Cluster
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`

	// Roles to be created in the database
	// +optional
	Roles []DatabaseRole `json:"roles,omitempty"`
}

// DatabaseRole defines a database role specification
type DatabaseRole struct {
	// Name of the database role
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z_][a-z0-9_]*$`
	Name string `json:"name"`

	// Database name where the role has permissions
	// +kubebuilder:validation:Required
	Database string `json:"database"`

	// Permissions granted to the role (e.g., SELECT, INSERT, UPDATE, DELETE)
	// +optional
	Permissions []string `json:"permissions,omitempty"`
}

// NATSConfig defines NATS JetStream configuration
type NATSConfig struct {
	// Streams to be created in NATS JetStream
	// +optional
	Streams []NATSStream `json:"streams,omitempty"`
}

// NATSStream defines a NATS JetStream stream specification
type NATSStream struct {
	// Name of the stream
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Subjects that the stream listens to
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Subjects []string `json:"subjects"`

	// Retention policy for the stream
	// +kubebuilder:validation:Enum=limits;interest;workqueue
	// +kubebuilder:default="limits"
	// +optional
	Retention string `json:"retention,omitempty"`

	// Storage type for the stream
	// +kubebuilder:validation:Enum=file;memory
	// +kubebuilder:default="file"
	// +optional
	Storage string `json:"storage,omitempty"`
}

// OAuthConfig defines OAuth client configuration
type OAuthConfig struct {
	// Clients to be registered with Hydra
	// +optional
	Clients []OAuthClient `json:"clients,omitempty"`
}

// OAuthClient defines an OAuth client specification
type OAuthClient struct {
	// ClientID is the unique identifier for the OAuth client
	// +kubebuilder:validation:Required
	ClientID string `json:"clientId"`

	// ClientName is the human-readable name for the OAuth client
	// +kubebuilder:validation:Required
	ClientName string `json:"clientName"`

	// RedirectURIs are the allowed redirect URIs for the client
	// +optional
	RedirectURIs []string `json:"redirectUris,omitempty"`

	// GrantTypes are the allowed OAuth grant types
	// +optional
	GrantTypes []string `json:"grantTypes,omitempty"`

	// ResponseTypes are the allowed OAuth response types
	// +optional
	ResponseTypes []string `json:"responseTypes,omitempty"`
}

// SecretsConfig defines secret management configuration
type SecretsConfig struct {
	// Infisical configuration for secret backup
	// +optional
	Infisical *InfisicalConfig `json:"infisical,omitempty"`
}

// InfisicalConfig defines Infisical project configuration
type InfisicalConfig struct {
	// ProjectSlug is the Infisical project identifier
	// +kubebuilder:default="hub-platform"
	// +optional
	ProjectSlug string `json:"projectSlug,omitempty"`

	// EnvironmentSlug is the Infisical environment identifier
	// +kubebuilder:default="prod"
	// +optional
	EnvironmentSlug string `json:"environmentSlug,omitempty"`
}

// HubEnvironmentStatus defines the observed state of HubEnvironment.
type HubEnvironmentStatus struct {
	// Phase represents the high-level lifecycle phase of the HubEnvironment
	// Valid phases: Provisioning, Available, Degraded, Failed
	// - Provisioning: Controllers are actively converging, pods may be NotReady
	// - Available: Readiness contract met, system is fully operational
	// - Degraded: System was Available, but a dependency dropped or drifted
	// - Failed: Terminal failure requiring manual intervention
	// +kubebuilder:validation:Enum=Provisioning;Available;Degraded;Failed
	// +optional
	Phase string `json:"phase,omitempty"`

	// Conditions represent the current state of the HubEnvironment resource.
	// Standard condition types:
	// - SecretZeroGenerated: Secret Zero bootstrap secrets have been created
	// - SecretsBackedUp: Secrets have been uploaded to Infisical
	// - OAuthClientsRegistered: OAuth clients have been registered with Hydra
	// - NATSStreamsConfigured: NATS JetStream streams have been created
	// - Ready: All phases complete successfully
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration reflects the generation of the most recently observed HubEnvironment
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// UploadedSecrets tracks which secrets have been uploaded to Infisical
	// This prevents re-uploading secrets that already exist
	// +optional
	UploadedSecrets []string `json:"uploadedSecrets,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// HubEnvironment is the Schema for the hubenvironments API
type HubEnvironment struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of HubEnvironment
	// +required
	Spec HubEnvironmentSpec `json:"spec"`

	// status defines the observed state of HubEnvironment
	// +optional
	Status HubEnvironmentStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// HubEnvironmentList contains a list of HubEnvironment
type HubEnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []HubEnvironment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HubEnvironment{}, &HubEnvironmentList{})
}
