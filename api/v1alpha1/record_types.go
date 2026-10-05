package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// RecordSchema binds a Record to a contract.
type RecordSchema struct {
	// Ref identifies the Schema or ClusterSchema.
	Ref SchemaReference `json:"ref"`

	// Digest is an optional Publisher-asserted digest of the contract. When
	// present, the controller verifies it against the computed digest.
	// +optional
	Digest Digest `json:"digest,omitempty"`
}

// RecordSpec is the immutable content of a Record.
type RecordSpec struct {
	// Publisher of the Record.
	Publisher Publisher `json:"publisher"`

	// RecordType identifies the kind of thing the Record represents, such as
	// subnet or cloud-region.
	// +kubebuilder:validation:MinLength=1
	RecordType string `json:"recordType"`

	// Schema identifies the contract the data was published under.
	Schema RecordSchema `json:"schema"`

	// DataDigest is an optional Publisher-asserted digest of data. When
	// present, the controller verifies it against the computed digest.
	// +optional
	DataDigest Digest `json:"dataDigest,omitempty"`

	// Data is the published JSON object.
	// +kubebuilder:validation:Type=object
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Data runtime.RawExtension `json:"data"`
}

// RecordSchemaStatus is the verified contract of a Record.
type RecordSchemaStatus struct {
	// Digest of the contract the data was validated against. Write-once.
	// +optional
	Digest Digest `json:"digest,omitempty"`
}

// RecordStatus is the observed state of a Record.
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.dataDigest) || (has(self.dataDigest) && self.dataDigest == oldSelf.dataDigest)",message="status.dataDigest is write-once"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.schema) || !has(oldSelf.schema.digest) || (has(self.schema) && has(self.schema.digest) && self.schema.digest == oldSelf.schema.digest)",message="status.schema.digest is write-once"
type RecordStatus struct {
	// DataDigest is the computed digest of spec.data. Write-once.
	// +optional
	DataDigest Digest `json:"dataDigest,omitempty"`

	// Schema is the verified contract.
	// +optional
	Schema *RecordSchemaStatus `json:"schema,omitempty"`

	// ObservedGeneration is the generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions of the Record.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// A Record is an immutable, schema-bound snapshot of data.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.recordType`
// +kubebuilder:printcolumn:name="Schema",type=string,JSONPath=`.spec.schema.ref.name`
// +kubebuilder:printcolumn:name="Valid",type=string,JSONPath=`.status.conditions[?(@.type=="Valid")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type Record struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec is immutable.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
	Spec   RecordSpec   `json:"spec"`
	Status RecordStatus `json:"status,omitempty"`
}

// RecordList contains a list of Records.
// +kubebuilder:object:root=true
type RecordList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Record `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Record{}, &RecordList{})
}
