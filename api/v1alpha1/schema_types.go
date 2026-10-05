package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// SchemaSpec is the contract and Publisher metadata shared by Schema and
// ClusterSchema. Publisher, the lineage (shapeGroup, shape, and
// shapeVersion), format, and definition are immutable; successor, replaces,
// and deprecation are mutable Publisher assertions. Only format and definition
// are inputs to the contract digest.
// +kubebuilder:validation:XValidation:rule="self.publisher == oldSelf.publisher",message="spec.publisher is immutable"
// +kubebuilder:validation:XValidation:rule="self.shapeGroup == oldSelf.shapeGroup",message="spec.shapeGroup is immutable"
// +kubebuilder:validation:XValidation:rule="self.shape == oldSelf.shape",message="spec.shape is immutable"
// +kubebuilder:validation:XValidation:rule="self.shapeVersion == oldSelf.shapeVersion",message="spec.shapeVersion is immutable"
// +kubebuilder:validation:XValidation:rule="self.format == oldSelf.format",message="spec.format is immutable"
// +kubebuilder:validation:XValidation:rule="self.definition == oldSelf.definition",message="spec.definition is immutable"
type SchemaSpec struct {
	// Publisher of the Schema.
	Publisher Publisher `json:"publisher"`

	// ShapeGroup owns the contract lineage. Together with shape it
	// identifies the lineage.
	ShapeGroup Group `json:"shapeGroup"`

	// Shape is the form of data within the shapeGroup, like a Kubernetes
	// kind.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Shape string `json:"shape"`

	// ShapeVersion is a Publisher-asserted compatibility group within the
	// lineage, like a Kubernetes API version. Several Schemas may share a
	// shapeVersion; each is an immutable revision.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	ShapeVersion string `json:"shapeVersion"`

	// Format is the schema language of the definition.
	Format SchemaFormat `json:"format"`

	// Definition is the contract, expressed in the schema language
	// identified by format.
	// +kubebuilder:validation:Type=object
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Definition runtime.RawExtension `json:"definition"`

	// Successor identifies the Schema this Schema's Publisher names as its
	// intended successor. Consumers discover successors here: the claim is
	// made by the Publisher of the Schema they already use.
	// +optional
	Successor *SchemaReference `json:"successor,omitempty"`

	// Replaces identifies Schemas this Schema claims to succeed. The claim is
	// made by this Schema's Publisher, not by the owner of the Schemas it
	// names, so it is informational only: Consumers must not use it to
	// discover successors. A Schema may replace only Schemas in its own
	// namespace; a ClusterSchema only ClusterSchemas.
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
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.structuralDigest) || (has(self.structuralDigest) && self.structuralDigest == oldSelf.structuralDigest)",message="status.structuralDigest is write-once"
type SchemaStatus struct {
	// Digest of the canonicalized contract. Write-once.
	// +optional
	Digest Digest `json:"digest,omitempty"`

	// StructuralDigest of the canonicalized contract after removing
	// documentation-only keywords. Schemas that differ only in
	// documentation have the same structural digest. Write-once.
	// +optional
	StructuralDigest Digest `json:"structuralDigest,omitempty"`

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
// +kubebuilder:validation:XValidation:rule="!has(self.spec.replaces) || self.spec.replaces.all(r, r.kind == 'Schema')",message="a Schema may only replace Schemas in its own namespace"
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Group",type=string,JSONPath=`.spec.shapeGroup`
// +kubebuilder:printcolumn:name="Shape",type=string,JSONPath=`.spec.shape`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.shapeVersion`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Digest",type=string,JSONPath=`.status.digest`,priority=1
// +kubebuilder:printcolumn:name="Structural Digest",type=string,JSONPath=`.status.structuralDigest`,priority=1
// +kubebuilder:selectablefield:JSONPath=`.spec.shapeGroup`
// +kubebuilder:selectablefield:JSONPath=`.spec.shape`
// +kubebuilder:selectablefield:JSONPath=`.spec.shapeVersion`
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
// +kubebuilder:printcolumn:name="Group",type=string,JSONPath=`.spec.shapeGroup`
// +kubebuilder:printcolumn:name="Shape",type=string,JSONPath=`.spec.shape`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.shapeVersion`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Digest",type=string,JSONPath=`.status.digest`,priority=1
// +kubebuilder:printcolumn:name="Structural Digest",type=string,JSONPath=`.status.structuralDigest`,priority=1
// +kubebuilder:selectablefield:JSONPath=`.spec.shapeGroup`
// +kubebuilder:selectablefield:JSONPath=`.spec.shape`
// +kubebuilder:selectablefield:JSONPath=`.spec.shapeVersion`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:validation:XValidation:rule="!has(self.spec.replaces) || self.spec.replaces.all(r, r.kind == 'ClusterSchema')",message="a ClusterSchema may only replace ClusterSchemas"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.successor) || self.spec.successor.kind == 'ClusterSchema'",message="a ClusterSchema's successor must be a ClusterSchema"
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
