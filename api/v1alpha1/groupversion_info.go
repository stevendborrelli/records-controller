// Package v1alpha1 contains the v1alpha1 Records API.
// +kubebuilder:object:generate=true
// +groupName=records.crossplane.io
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is the API group and version of the Records API.
	GroupVersion = schema.GroupVersion{Group: "records.crossplane.io", Version: "v1alpha1"}

	// SchemeBuilder registers the Records API types with a scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the Records API types to a scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
