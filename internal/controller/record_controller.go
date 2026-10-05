package controller

import (
	"context"
	"fmt"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/stevendborrelli/records-controller/api/v1alpha1"
	"github.com/stevendborrelli/records-controller/internal/digest"
	"github.com/stevendborrelli/records-controller/internal/validate"
)

// Record condition reasons.
const (
	ReasonSchemaNotFound       = "SchemaNotFound"
	ReasonSchemaInvalid        = "SchemaInvalid"
	ReasonSchemaDigestMismatch = "SchemaDigestMismatch"
	ReasonDataDigestMismatch   = "DataDigestMismatch"
	ReasonDataInvalid          = "DataInvalid"
)

// IndexRecordSchema indexes Records by the Schema they reference.
const IndexRecordSchema = "spec.schema.ref"

func schemaIndexKey(kind v1alpha1.SchemaKind, name string) string {
	return string(kind) + "/" + name
}

// RecordReconciler verifies Records against their Schemas.
type RecordReconciler struct {
	client.Client
}

// SetupWithManager registers the reconciler and its Schema index.
func (r *RecordReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha1.Record{}, IndexRecordSchema, func(o client.Object) []string {
		ref := o.(*v1alpha1.Record).Spec.Schema.Ref
		return []string{schemaIndexKey(ref.Kind, ref.Name)}
	}); err != nil {
		return err
	}

	recordsFor := func(kind v1alpha1.SchemaKind) handler.MapFunc {
		return func(ctx context.Context, o client.Object) []reconcile.Request {
			l := &v1alpha1.RecordList{}
			opts := []client.ListOption{client.MatchingFields{IndexRecordSchema: schemaIndexKey(kind, o.GetName())}}
			if kind == v1alpha1.KindSchema {
				// A Schema reference always resolves in the Record's namespace.
				opts = append(opts, client.InNamespace(o.GetNamespace()))
			}
			if err := r.List(ctx, l, opts...); err != nil {
				return nil
			}
			reqs := make([]reconcile.Request, 0, len(l.Items))
			for _, rec := range l.Items {
				reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: rec.Namespace, Name: rec.Name}})
			}
			return reqs
		}
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("record").
		For(&v1alpha1.Record{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&v1alpha1.Schema{}, handler.EnqueueRequestsFromMapFunc(recordsFor(v1alpha1.KindSchema)), builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&v1alpha1.ClusterSchema{}, handler.EnqueueRequestsFromMapFunc(recordsFor(v1alpha1.KindClusterSchema)), builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

// Reconcile computes a Record's digests, verifies any asserted digests, and
// validates its data against its Schema.
func (r *RecordReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	rec := &v1alpha1.Record{}
	if err := r.Get(ctx, req.NamespacedName, rec); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	reason, message, err := r.verify(ctx, rec)
	if err != nil {
		return ctrl.Result{}, err
	}
	status := metav1.ConditionTrue
	if reason != ReasonVerified {
		status = metav1.ConditionFalse
	}
	setCondition(&rec.Status.Conditions, rec.Generation, v1alpha1.ConditionValid, status, reason, message)
	rec.Status.ObservedGeneration = rec.Generation
	return ctrl.Result{}, r.Status().Update(ctx, rec)
}

// verify checks a Record and, if it is valid, records its digests in status.
// It returns the reason and message of the Valid condition.
func (r *RecordReconciler) verify(ctx context.Context, rec *v1alpha1.Record) (string, string, error) {
	spec, err := r.resolveSchema(ctx, rec)
	if kerrors.IsNotFound(err) {
		return ReasonSchemaNotFound, fmt.Sprintf("%s %q not found", rec.Spec.Schema.Ref.Kind, rec.Spec.Schema.Ref.Name), nil
	}
	if err != nil {
		return "", "", err
	}

	schemaDigest, err := ContractDigest(*spec)
	if err != nil {
		return ReasonSchemaInvalid, err.Error(), nil
	}
	if a := rec.Spec.Schema.Digest; a != "" && a != schemaDigest {
		return ReasonSchemaDigestMismatch, fmt.Sprintf("asserted spec.schema.digest %s does not match the referenced contract's digest %s", a, schemaDigest), nil
	}
	if s := rec.Status.Schema; s != nil && s.Digest != "" && s.Digest != schemaDigest {
		// The name now resolves to a different contract than the one this
		// Record was bound to, e.g. because the Schema was deleted and
		// recreated.
		return ReasonSchemaDigestMismatch, fmt.Sprintf("Record was bound to contract %s but the referenced Schema now has digest %s", s.Digest, schemaDigest), nil
	}

	d, err := digest.Of(rec.Spec.Data.Raw)
	if err != nil {
		return ReasonDataInvalid, err.Error(), nil
	}
	dataDigest := v1alpha1.Digest(d)
	if a := rec.Spec.DataDigest; a != "" && a != dataDigest {
		return ReasonDataDigestMismatch, fmt.Sprintf("asserted spec.dataDigest %s does not match the computed digest %s", a, dataDigest), nil
	}
	if s := rec.Status.DataDigest; s != "" && s != dataDigest {
		return ReasonDataDigestMismatch, fmt.Sprintf("status.dataDigest %s does not match the computed digest %s; spec.data changed after it was recorded", s, dataDigest), nil
	}

	c, err := validate.Compile(spec.Definition.Raw)
	if err != nil {
		return ReasonSchemaInvalid, err.Error(), nil
	}
	if err := c.Validate(rec.Spec.Data.Raw); err != nil {
		return ReasonDataInvalid, err.Error(), nil
	}

	rec.Status.DataDigest = dataDigest
	rec.Status.Schema = &v1alpha1.RecordSchemaStatus{Digest: schemaDigest}
	return ReasonVerified, "", nil
}

func (r *RecordReconciler) resolveSchema(ctx context.Context, rec *v1alpha1.Record) (*v1alpha1.SchemaSpec, error) {
	ref := rec.Spec.Schema.Ref
	if ref.Kind == v1alpha1.KindClusterSchema {
		s := &v1alpha1.ClusterSchema{}
		err := r.Get(ctx, types.NamespacedName{Name: ref.Name}, s)
		return &s.Spec, err
	}
	s := &v1alpha1.Schema{}
	err := r.Get(ctx, types.NamespacedName{Namespace: rec.Namespace, Name: ref.Name}, s)
	return &s.Spec, err
}
