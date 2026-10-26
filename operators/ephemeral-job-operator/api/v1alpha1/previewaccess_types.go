package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PreviewAccess declares a live preview of a session's app and who may open it
// (ADR-108).
//
// The product creates one per preview, in its tenant namespace, with its SDK's
// own service account. The platform assigns the preview its host on a separate
// registrable domain, admits the people named here through a single-use
// handoff, checks every request against this record, and proxies to the
// session's sandbox. The product implements no authentication on the preview
// path and holds no keys.
//
// Revocation is a change to this record: patching sharedWith, or deleting it.
// With an ownerReference to the session's EphemeralJob, ending the session
// deletes it.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=pva
// +kubebuilder:printcolumn:name="Job",type=string,JSONPath=`.spec.jobRef`
// +kubebuilder:printcolumn:name="Port",type=integer,JSONPath=`.spec.port`
// +kubebuilder:printcolumn:name="Host",type=string,JSONPath=`.status.host`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
type PreviewAccess struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PreviewAccessSpec   `json:"spec,omitempty"`
	Status PreviewAccessStatus `json:"status,omitempty"`
}

// PreviewAccessSpec is the product's statement of one preview.
type PreviewAccessSpec struct {
	// JobRef is the EphemeralJob whose sandbox serves the preview, in this
	// namespace. It may be changed: re-pointing keeps the record's host, and
	// cookies already issued keep working.
	// +kubebuilder:validation:MinLength=1
	JobRef string `json:"jobRef"`

	// Port is the app server's port in that sandbox, for HTTP and WebSocket.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// OwnerSubject is the issuer's subject ID (`sub`) of the session's owner.
	// Not an email or username.
	// +kubebuilder:validation:MinLength=1
	OwnerSubject string `json:"ownerSubject"`

	// SharedWith are further issuer subject IDs allowed to open the preview.
	// +optional
	// +listType=set
	SharedWith []string `json:"sharedWith,omitempty"`

	// EmbeddableBy are further applications of this tenant that may embed the
	// preview: ADR-088 app ids (each application namespace's `app-id` label).
	// The record's own application always may. A listed app's gateway finds
	// the record by name exactly as the owning app's does; who may open it is
	// still ownerSubject and sharedWith. An app id with no namespace in this
	// tenant embeds nothing.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=63
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	EmbeddableBy []string `json:"embeddableBy,omitempty"`

	// ExpiresAt bounds the preview. Required when the record has no
	// ownerReference, so a record the product forgets to delete cannot outlive
	// it. Cookies never outlive it either.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
}

// PreviewAccessStatus is the platform's side.
type PreviewAccessStatus struct {
	// Host is the preview's host on the preview domain. It is fixed for the
	// record's whole life.
	// +optional
	Host string `json:"host,omitempty"`

	// Conditions: Ready, with reason PreviewTargetInUse when another record
	// already serves the same (jobRef, port).
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the generation the conditions describe.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// PreviewAccessList is a list of PreviewAccess.
//
// +kubebuilder:object:root=true
type PreviewAccessList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PreviewAccess `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PreviewAccess{}, &PreviewAccessList{})
}
