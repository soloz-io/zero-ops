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

// PlanSpec defines the desired state of Plan
type PlanSpec struct {
	// TenantID is the OpenMeter namespace (tenant identifier)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`
	TenantID string `json:"tenantId"`

	// Key is the unique identifier for the plan
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z0-9_]+$`
	// +kubebuilder:validation:MaxLength=64
	Key string `json:"key"`

	// Name is the display name
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Description of the plan
	// +optional
	Description string `json:"description,omitempty"`

	// Currency (USD, EUR, etc.)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[A-Z]{3}$`
	Currency string `json:"currency"`

	// Phases define billing phases
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Phases []PlanPhase `json:"phases"`

	// ProRatingConfig defines proration behavior
	// +optional
	ProRatingConfig *ProRatingConfig `json:"proRatingConfig,omitempty"`
}

// PlanPhase represents a billing phase
type PlanPhase struct {
	// Key is the unique identifier for the phase
	// +kubebuilder:validation:Required
	Key string `json:"key"`

	// Name is the display name
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// StartAfter is ISO 8601 duration (e.g., "P1M" for 1 month)
	// +kubebuilder:validation:Required
	StartAfter string `json:"startAfter"`

	// RateCards define pricing for features
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	RateCards []RateCard `json:"rateCards"`
}

// RateCard defines pricing for a feature
type RateCard struct {
	// FeatureKey references a Feature CR
	// +kubebuilder:validation:Required
	FeatureKey string `json:"featureKey"`

	// EntitlementType defines the entitlement model
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=metered;static;boolean
	EntitlementType string `json:"entitlementType"`

	// Price defines the pricing model
	// +optional
	Price *Price `json:"price,omitempty"`
}

// Price defines pricing model
type Price struct {
	// Type defines the pricing model
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=flat;usage_based;tiered_volume;tiered_graduated
	Type string `json:"type"`

	// Amount is the price amount
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	Amount float64 `json:"amount"`

	// BillingCadence defines billing frequency
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=monthly;annual
	BillingCadence string `json:"billingCadence"`
}

// ProRatingConfig defines proration behavior
type ProRatingConfig struct {
	// Enabled controls whether proration is active
	// +kubebuilder:validation:Required
	Enabled bool `json:"enabled"`

	// Mode defines proration calculation method
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=prorate_prices
	Mode string `json:"mode"`
}

// PlanStatus defines the observed state of Plan
type PlanStatus struct {
	// Conditions represent the latest available observations of the Plan's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// OpenMeterID is the ID assigned by OpenMeter
	// +optional
	OpenMeterID string `json:"openMeterId,omitempty"`

	// LastSyncTime is the last time the plan was synced to OpenMeter
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// ValidationErrors contains feature reference validation errors
	// +optional
	ValidationErrors []string `json:"validationErrors,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=plan
// +kubebuilder:printcolumn:name="Tenant",type=string,JSONPath=`.spec.tenantId`
// +kubebuilder:printcolumn:name="Key",type=string,JSONPath=`.spec.key`
// +kubebuilder:printcolumn:name="Name",type=string,JSONPath=`.spec.name`
// +kubebuilder:printcolumn:name="Currency",type=string,JSONPath=`.spec.currency`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Plan is the Schema for the plans API
type Plan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PlanSpec   `json:"spec,omitempty"`
	Status PlanStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PlanList contains a list of Plan
type PlanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Plan `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Plan{}, &PlanList{})
}
