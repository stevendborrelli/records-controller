// Package controller contains the Records controllers.
package controller

import (
	"encoding/json"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stevendborrelli/records-controller/api/v1alpha1"
	"github.com/stevendborrelli/records-controller/internal/digest"
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
