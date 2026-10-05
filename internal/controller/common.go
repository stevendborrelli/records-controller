// Package controller contains the Records controllers.
package controller

import (
	"context"
	"encoding/json"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stevendborrelli/records-controller/api/v1alpha1"
	"github.com/stevendborrelli/records-controller/internal/digest"
	"github.com/stevendborrelli/records-controller/internal/structural"
)

// contract is the input to a Schema's digest: the fields that define the
// contract, excluding Publisher metadata.
type contract struct {
	Format     v1alpha1.SchemaFormat `json:"format"`
	Definition json.RawMessage       `json:"definition"`
}

// ContractDigest returns the digest of a Schema's contract.
func ContractDigest(s v1alpha1.SchemaSpec) (v1alpha1.Digest, error) {
	d, err := digest.OfValue(contract{Format: s.Format, Definition: s.Definition.Raw})
	return v1alpha1.Digest(d), err
}

// StructuralDigest returns the digest of a Schema's contract with
// documentation-only keywords removed from its definition.
func StructuralDigest(s v1alpha1.SchemaSpec) (v1alpha1.Digest, error) {
	def, err := structural.Strip(s.Definition.Raw)
	if err != nil {
		return "", err
	}
	d, err := digest.OfValue(contract{Format: s.Format, Definition: def})
	return v1alpha1.Digest(d), err
}

// getSchemaSpec resolves a Schema reference made by an object in namespace.
// A reference to a Schema always resolves in that namespace.
func getSchemaSpec(ctx context.Context, c client.Reader, namespace string, ref v1alpha1.SchemaReference) (*v1alpha1.SchemaSpec, error) {
	if ref.Kind == v1alpha1.KindClusterSchema {
		s := &v1alpha1.ClusterSchema{}
		err := c.Get(ctx, types.NamespacedName{Name: ref.Name}, s)
		return &s.Spec, err
	}
	s := &v1alpha1.Schema{}
	err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, s)
	return &s.Spec, err
}

// setCondition sets a condition and reports whether it changed.
func setCondition(conds *[]metav1.Condition, generation int64, t string, status metav1.ConditionStatus, reason, message string) bool {
	return meta.SetStatusCondition(conds, metav1.Condition{
		Type:               t,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: generation,
	})
}
