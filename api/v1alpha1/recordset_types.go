package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RecordReference refers to a Record in the RecordSet's namespace.
type RecordReference struct {
	// Name of the Record.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// DataDigest is the expected digest of the Record's data.
	// +optional
	DataDigest Digest `json:"dataDigest,omitempty"`
}

// VersionSchemaReference refers to the contract of a version's Record.
type VersionSchemaReference struct {
	SchemaReference `json:",inline"`

	// Digest is the expected digest of the Record's contract.
	// +optional
	Digest Digest `json:"digest,omitempty"`
}

// RecordSetVersion is one published version of a RecordSet. Entries are
// immutable once published.
type RecordSetVersion struct {
	// Version number. Positive, never reused, and increasing.
	// +kubebuilder:validation:Minimum=1
	Version int64 `json:"version"`

	// RecordRef identifies the Record published as this version.
	RecordRef RecordReference `json:"recordRef"`

	// SchemaRef identifies the Record's contract.
	SchemaRef VersionSchemaReference `json:"schemaRef"`

	// PublishedAt is when the version was published.
	// +optional
	PublishedAt *metav1.Time `json:"publishedAt,omitempty"`
}

// Retraction marks a published version as retracted.
type Retraction struct {
	// Version that is retracted.
	// +kubebuilder:validation:Minimum=1
	Version int64 `json:"version"`

	// Message explains the retraction.
	// +optional
	Message string `json:"message,omitempty"`
}

// RecordSetSpec is the Publisher-owned index over a collection of Records.
// +kubebuilder:validation:XValidation:rule="!has(self.current) || (has(self.versions) && self.versions.exists(v, v.version == self.current))",message="current must identify a published version"
// +kubebuilder:validation:XValidation:rule="!has(self.current) || !has(self.retracted) || !self.retracted.exists(r, r.version == self.current)",message="current must not identify a retracted version"
// +kubebuilder:validation:XValidation:rule="!has(self.retracted) || self.retracted.all(r, has(self.versions) && self.versions.exists(v, v.version == r.version))",message="retracted versions must be published versions"
// +kubebuilder:validation:XValidation:rule="self.publisher == oldSelf.publisher",message="spec.publisher is immutable"
// +kubebuilder:validation:XValidation:rule="self.recordType == oldSelf.recordType",message="spec.recordType is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.versions) || !has(oldSelf.versions) || self.versions.all(v, oldSelf.versions.exists(o, o.version == v.version) || oldSelf.versions.all(o, o.version < v.version))",message="new versions must be greater than every existing version"
// +kubebuilder:validation:XValidation:rule="!has(self.versions) || !has(oldSelf.versions) || self.versions.all(v, oldSelf.versions.all(o, o.version != v.version || o == v))",message="published version entries are immutable"
type RecordSetSpec struct {
	// Publisher of the RecordSet.
	Publisher Publisher `json:"publisher"`

	// RecordType of every Record in the set.
	// +kubebuilder:validation:MinLength=1
	RecordType string `json:"recordType"`

	// Current is the version the Publisher recommends.
	// +optional
	// +kubebuilder:validation:Minimum=1
	Current *int64 `json:"current,omitempty"`

	// Retracted versions.
	// +optional
	// +listType=map
	// +listMapKey=version
	// +kubebuilder:validation:MaxItems=100
	Retracted []Retraction `json:"retracted,omitempty"`

	// Versions ordered from highest to lowest version number.
	// +optional
	// +listType=map
	// +listMapKey=version
	// +kubebuilder:validation:MaxItems=100
	Versions []RecordSetVersion `json:"versions,omitempty"`
}

// RecordSetStatus is the observed state of a RecordSet.
type RecordSetStatus struct {
	// CurrentRecord is the name of the Record that current resolves to.
	// +optional
	CurrentRecord string `json:"currentRecord,omitempty"`

	// ObservedGeneration is the generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions of the RecordSet.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// A RecordSet is a mutable, Publisher-owned index over immutable Records.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.recordType`
// +kubebuilder:printcolumn:name="Current",type=integer,JSONPath=`.spec.current`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type RecordSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RecordSetSpec   `json:"spec"`
	Status RecordSetStatus `json:"status,omitempty"`
}

// RecordSetList contains a list of RecordSets.
// +kubebuilder:object:root=true
type RecordSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RecordSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RecordSet{}, &RecordSetList{})
}
