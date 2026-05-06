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

// MeterSpec defines the desired state of Meter
type MeterSpec struct {
	// TenantID is the OpenMeter namespace (tenant identifier)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`
	TenantID string `json:"tenantId"`

	// Slug is the unique identifier for the meter
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z0-9_]+$`
	// +kubebuilder:validation:MaxLength=64
	Slug string `json:"slug"`

	// Description of the meter
	// +optional
	Description string `json:"description,omitempty"`

	// Aggregation method (COUNT, SUM, MAX, MIN, AVG)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=COUNT;SUM;MAX;MIN;AVG
	Aggregation string `json:"aggregation"`

	// EventType is the OTLP event type to meter
	// +kubebuilder:validation:Required
	EventType string `json:"eventType"`

	// ValueProperty is the JSON path to the value field
	// +optional
	ValueProperty string `json:"valueProperty,omitempty"`

	// GroupBy defines aggregation dimensions
	// +optional
	GroupBy map[string]string `json:"groupBy,omitempty"`
}

// MeterStatus defines the observed state of Meter
type MeterStatus struct {
	// Conditions represent the latest available observations of the Meter's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// OpenMeterID is the ID assigned by OpenMeter
	// +optional
	OpenMeterID string `json:"openMeterId,omitempty"`

	// LastSyncTime is the last time the meter was synced to OpenMeter
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=meter
// +kubebuilder:printcolumn:name="Tenant",type=string,JSONPath=`.spec.tenantId`
// +kubebuilder:printcolumn:name="Slug",type=string,JSONPath=`.spec.slug`
// +kubebuilder:printcolumn:name="Aggregation",type=string,JSONPath=`.spec.aggregation`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Meter is the Schema for the meters API
type Meter struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MeterSpec   `json:"spec,omitempty"`
	Status MeterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MeterList contains a list of Meter
type MeterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Meter `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Meter{}, &MeterList{})
}
