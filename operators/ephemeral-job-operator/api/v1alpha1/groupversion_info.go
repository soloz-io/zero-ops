// Package v1alpha1 contains the compute.nutgraf.in/v1alpha1 API group.
//
// +kubebuilder:object:generate=true
// +groupName=compute.nutgraf.in
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	GroupVersion  = schema.GroupVersion{Group: "compute.nutgraf.in", Version: "v1alpha1"}
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}
	AddToScheme   = SchemeBuilder.AddToScheme
)
