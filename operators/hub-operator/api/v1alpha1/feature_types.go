/*
Copyright 2024.

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

// FeatureSpec defines the desired state of Feature
type FeatureSpec struct {
	// TenantID is the OpenMeter namespace (tenant identifier)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`
	TenantID string `json:"tenantId"`

	// Key is the unique identifier for the feature
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z0-9_]+$`
	// +kubebuilder:validation:MaxLength=64
	Key string `json:"key"`

	// Name is the display name
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// MeterSlugs are the meters associated with this feature
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	MeterSlugs []string `json:"meterSlugs"`
}

// FeatureStatus defines the observed state of Feature
type FeatureStatus struct {
	// Conditions represent the latest available observations of the Feature's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// OpenMeterID is the ID assigned by OpenMeter
	// +optional
	OpenMeterID string `json:"openMeterId,omitempty"`

	// LastSyncTime is the last time the feature was synced to OpenMeter
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=feature
// +kubebuilder:printcolumn:name="Tenant",type=string,JSONPath=`.spec.tenantId`
// +kubebuilder:printcolumn:name="Key",type=string,JSONPath=`.spec.key`
// +kubebuilder:printcolumn:name="Name",type=string,JSONPath=`.spec.name`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Feature is the Schema for the features API
type Feature struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FeatureSpec   `json:"spec,omitempty"`
	Status FeatureStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// FeatureList contains a list of Feature
type FeatureList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Feature `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Feature{}, &FeatureList{})
}
