package v1alpha1

// Condition types and reasons shared by the Records API.
const (
	// ConditionReady indicates that a Schema or RecordSet has been
	// verified by the controller.
	ConditionReady = "Ready"

	// ConditionValid indicates that a Record's digests have been computed and
	// recorded, any asserted digests verified, and its data validated against
	// its Schema.
	ConditionValid = "Valid"
)

// Publisher identifies the logical Publisher of an object. The identity is a
// claim; it is not proof of Publisher authority.
type Publisher struct {
	// ID of the Publisher.
	// +kubebuilder:validation:MinLength=1
	ID string `json:"id"`
}

// SchemaFormat identifies the schema language of a Schema definition.
// +kubebuilder:validation:Enum=StructuralSchema
type SchemaFormat string

// FormatStructuralSchema is the only supported format in v1alpha1.
const FormatStructuralSchema SchemaFormat = "StructuralSchema"

// SchemaKind identifies the kind of a Schema reference.
// +kubebuilder:validation:Enum=Schema;ClusterSchema
type SchemaKind string

// Schema kinds.
const (
	KindSchema        SchemaKind = "Schema"
	KindClusterSchema SchemaKind = "ClusterSchema"
)

// SchemaReference refers to a Schema or ClusterSchema. A reference to a
// Schema has no namespace; it always resolves in the referencing object's own
// namespace.
type SchemaReference struct {
	// Kind of the referenced Schema.
	Kind SchemaKind `json:"kind"`

	// Name of the referenced Schema.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// Digest is a SHA-256 digest of RFC 8785 canonical JSON.
// +kubebuilder:validation:Pattern=`^sha256:[0-9a-f]{64}$`
type Digest string
