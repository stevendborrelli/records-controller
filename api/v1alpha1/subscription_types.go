package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// FollowPolicy selects which version of a RecordSet a Subscription follows.
// +kubebuilder:validation:Enum=Current;LatestCompatible;Pinned
type FollowPolicy string

// Follow policies.
const (
	// FollowCurrent follows the version the Publisher recommends.
	FollowCurrent FollowPolicy = "Current"
	// FollowLatestCompatible follows the newest version that is not
	// retracted and whose Schema matches compatibleWith.
	FollowLatestCompatible FollowPolicy = "LatestCompatible"
	// FollowPinned follows exactly one version.
	FollowPinned FollowPolicy = "Pinned"
)

// IncompatiblePolicy is what a Subscription does when the version it would
// follow does not match compatibleWith.
// +kubebuilder:validation:Enum=Hold;Fail
type IncompatiblePolicy string

// Incompatible policies.
const (
	// IncompatibleHold keeps the last selected version and stays Ready.
	IncompatibleHold IncompatiblePolicy = "Hold"
	// IncompatibleFail keeps the last selected version but is not Ready.
	IncompatibleFail IncompatiblePolicy = "Fail"
)

// RecordSetReference refers to a RecordSet in the Subscription's namespace.
// A Subscription never follows a RecordSet in another namespace: the
// controller can read every namespace, so following one would let anyone who
// can create a Subscription read data their own permissions do not allow.
type RecordSetReference struct {
	// Name of the RecordSet.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// Lineage identifies a Schema contract lineage and compatibility group.
type Lineage struct {
	// ShapeGroup of the lineage.
	ShapeGroup Group `json:"shapeGroup"`

	// Shape within the shapeGroup.
	// +kubebuilder:validation:MinLength=1
	Shape string `json:"shape"`

	// ShapeVersion is the compatibility group within the lineage.
	// +kubebuilder:validation:MinLength=1
	ShapeVersion string `json:"shapeVersion"`
}

// SubscriptionSpec is what a Consumer wants from a RecordSet.
// +kubebuilder:validation:XValidation:rule="self.follow != 'Pinned' || has(self.version)",message="version is required when follow is Pinned"
// +kubebuilder:validation:XValidation:rule="self.follow == 'Pinned' || !has(self.version)",message="version may be set only when follow is Pinned"
// +kubebuilder:validation:XValidation:rule="self.follow != 'LatestCompatible' || has(self.compatibleWith)",message="compatibleWith is required when follow is LatestCompatible"
type SubscriptionSpec struct {
	// RecordSet to follow, in the Subscription's namespace.
	RecordSet RecordSetReference `json:"recordSet"`

	// Follow selects which version to follow: Current, the version the
	// Publisher recommends; LatestCompatible, the newest version that is not
	// retracted and matches compatibleWith; or Pinned, exactly version.
	// +optional
	// +kubebuilder:default=Current
	Follow FollowPolicy `json:"follow,omitempty"`

	// Version to follow when follow is Pinned.
	// +optional
	// +kubebuilder:validation:Minimum=1
	Version *int64 `json:"version,omitempty"`

	// CompatibleWith is the contract lineage the Consumer can read. A version
	// whose Schema is in another lineage or shapeVersion is incompatible.
	// +optional
	CompatibleWith *Lineage `json:"compatibleWith,omitempty"`

	// OnIncompatible is what to do when the version to follow is
	// incompatible: Hold keeps the last selected version and stays Ready;
	// Fail keeps it but reports Ready=False.
	// +optional
	// +kubebuilder:default=Hold
	OnIncompatible IncompatiblePolicy `json:"onIncompatible,omitempty"`
}

// SelectedVersion is the version a Subscription selected and verified.
type SelectedVersion struct {
	// Version number in the RecordSet.
	Version int64 `json:"version"`

	// Record that holds the version's data.
	Record string `json:"record"`

	// DataDigest of the Record's data.
	DataDigest Digest `json:"dataDigest"`

	// Schema the Record is bound to.
	Schema SchemaReference `json:"schema"`

	// SchemaDigest of the contract the Record is bound to.
	SchemaDigest Digest `json:"schemaDigest"`

	// Lineage of the Schema the Record is bound to.
	Lineage Lineage `json:"lineage"`
}

// SubscriptionStatus is the observed state of a Subscription.
type SubscriptionStatus struct {
	// Selected is the version currently served. It changes only when a new
	// version is selected and verified, so it survives the RecordSet
	// becoming unavailable or the followed version becoming incompatible.
	// +optional
	Selected *SelectedVersion `json:"selected,omitempty"`

	// Data is the selected Record's verified data.
	// +optional
	// +kubebuilder:validation:Type=object
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Data *runtime.RawExtension `json:"data,omitempty"`

	// ObservedGeneration is the generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions of the Subscription.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// A Subscription follows a RecordSet on behalf of a Consumer. It selects a
// version according to its follow policy, verifies it, and serves the
// selected Record's data in its status.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="RecordSet",type=string,JSONPath=`.spec.recordSet.name`
// +kubebuilder:printcolumn:name="Follow",type=string,JSONPath=`.spec.follow`
// +kubebuilder:printcolumn:name="Selected",type=integer,JSONPath=`.status.selected.version`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="UpToDate",type=string,JSONPath=`.status.conditions[?(@.type=="UpToDate")].status`
// +kubebuilder:printcolumn:name="Record",type=string,JSONPath=`.status.selected.record`,priority=1
// +kubebuilder:printcolumn:name="Data Digest",type=string,JSONPath=`.status.selected.dataDigest`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type Subscription struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SubscriptionSpec   `json:"spec"`
	Status SubscriptionStatus `json:"status,omitempty"`
}

// SubscriptionList contains a list of Subscriptions.
// +kubebuilder:object:root=true
type SubscriptionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Subscription `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Subscription{}, &SubscriptionList{})
}
