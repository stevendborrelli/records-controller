package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// SchemaSpec is the contract and Publisher metadata shared by Schema and
// ClusterSchema. Publisher, format, and definition form the immutable
// contract; replaces and deprecation are mutable Publisher assertions.
// +kubebuilder:validation:XValidation:rule="self.publisher == oldSelf.publisher",message="spec.publisher is immutable"
// +kubebuilder:validation:XValidation:rule="self.format == oldSelf.format",message="spec.format is immutable"
// +kubebuilder:validation:XValidation:rule="self.definition == oldSelf.definition",message="spec.definition is immutable"
type SchemaSpec struct {
	// Publisher of the Schema.
	Publisher Publisher `json:"publisher"`

	// Format is the schema language of the definition.
	Format SchemaFormat `json:"format"`

	// Definition is the contract, expressed in the schema language
	// identified by format.
	// +kubebuilder:validation:Type=object
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Definition runtime.RawExtension `json:"definition"`

	// Replaces identifies Schemas this Schema is the intended successor to.
	// +optional
	// +kubebuilder:validation:MaxItems=32
	Replaces []SchemaReference `json:"replaces,omitempty"`

	// Deprecation, when present, declares the Schema deprecated.
	// +optional
	Deprecation *Deprecation `json:"deprecation,omitempty"`
}

// Deprecation declares when a Publisher intends to stop publishing a
// contract.
type Deprecation struct {
	// Date is an RFC 3339 full-date, interpreted as a calendar date in UTC.
	// +kubebuilder:validation:Format=date
	Date string `json:"date"`

	// Message is an optional human-readable explanation.
	// +optional
	Message string `json:"message,omitempty"`
}

// SchemaStatus is the observed state of a Schema or ClusterSchema.
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.digest) || (has(self.digest) && self.digest == oldSelf.digest)",message="status.digest is write-once"
type SchemaStatus struct {
	// Digest of the canonicalized contract. Write-once.
	// +optional
	Digest Digest `json:"digest,omitempty"`

	// ObservedGeneration is the generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions of the Schema.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// A Schema is a namespaced contract that Records validate against.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Digest",type=string,JSONPath=`.status.digest`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type Schema struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SchemaSpec   `json:"spec"`
	Status SchemaStatus `json:"status,omitempty"`
}

// SchemaList contains a list of Schemas.
// +kubebuilder:object:root=true
type SchemaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Schema `json:"items"`
}

// A ClusterSchema is a cluster-scoped Schema. A ClusterSchema MUST NOT
// reference a namespaced Schema.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Digest",type=string,JSONPath=`.status.digest`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:validation:XValidation:rule="!has(self.spec.replaces) || self.spec.replaces.all(r, r.kind == 'ClusterSchema')",message="a ClusterSchema may only replace ClusterSchemas"
type ClusterSchema struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SchemaSpec   `json:"spec"`
	Status SchemaStatus `json:"status,omitempty"`
}

// ClusterSchemaList contains a list of ClusterSchemas.
// +kubebuilder:object:root=true
type ClusterSchemaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterSchema `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Schema{}, &SchemaList{}, &ClusterSchema{}, &ClusterSchemaList{})
}
