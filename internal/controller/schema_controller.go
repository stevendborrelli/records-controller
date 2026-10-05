package controller

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/stevendborrelli/records-controller/api/v1alpha1"
	"github.com/stevendborrelli/records-controller/internal/validate"
)

// Schema condition reasons.
const (
	ReasonVerified          = "Verified"
	ReasonInvalidDefinition = "InvalidDefinition"
	ReasonContractChanged   = "ContractChanged"
)

// SchemaReconciler records the contract and structural digests of Schemas
// and ClusterSchemas.
type SchemaReconciler struct {
	client.Client
	// Cluster selects ClusterSchemas instead of Schemas.
	Cluster bool
}

// SetupWithManager registers the reconciler.
func (r *SchemaReconciler) SetupWithManager(mgr ctrl.Manager) error {
	var obj client.Object = &v1alpha1.Schema{}
	name := "schema"
	if r.Cluster {
		obj, name = &v1alpha1.ClusterSchema{}, "clusterschema"
	}
	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(obj, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

// Reconcile computes and records a Schema's digests.
func (r *SchemaReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var (
		obj    client.Object
		spec   *v1alpha1.SchemaSpec
		status *v1alpha1.SchemaStatus
	)
	if r.Cluster {
		s := &v1alpha1.ClusterSchema{}
		obj, spec, status = s, &s.Spec, &s.Status
	} else {
		s := &v1alpha1.Schema{}
		obj, spec, status = s, &s.Spec, &s.Status
	}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	gen := obj.GetGeneration()
	reason, message := ReasonVerified, ""
	cond := metav1.ConditionTrue

	d, err := ContractDigest(*spec)
	var sd v1alpha1.Digest
	if err == nil {
		sd, err = StructuralDigest(*spec)
	}
	switch {
	case err != nil:
		cond, reason, message = metav1.ConditionFalse, ReasonInvalidDefinition, err.Error()
	case status.Digest != "" && status.Digest != d:
		// The contract changed after its digest was recorded. Immutability
		// should prevent this; if it happens the Schema no longer identifies
		// the contract Records were bound to.
		cond, reason = metav1.ConditionFalse, ReasonContractChanged
		message = fmt.Sprintf("contract digest is now %s but %s was recorded", d, status.Digest)
	default:
		if _, err := validate.Compile(spec.Definition.Raw); err != nil {
			cond, reason, message = metav1.ConditionFalse, ReasonInvalidDefinition, err.Error()
			break
		}
		status.Digest, status.StructuralDigest = d, sd
	}

	setCondition(&status.Conditions, gen, v1alpha1.ConditionReady, cond, reason, message)
	status.ObservedGeneration = gen
	return ctrl.Result{}, r.Status().Update(ctx, obj)
}
