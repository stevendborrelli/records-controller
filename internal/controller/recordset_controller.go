package controller

import (
	"context"
	"fmt"
	"strings"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/stevendborrelli/records-controller/api/v1alpha1"
)

// RecordSet condition reasons.
const (
	ReasonInvalidVersions = "InvalidVersions"
)

// IndexRecordSetRecords indexes RecordSets by the Records they reference.
const IndexRecordSetRecords = "spec.versions.recordRef.name"

// RecordSetReconciler verifies a RecordSet's versions against their Records.
type RecordSetReconciler struct {
	client.Client
}

// SetupWithManager registers the reconciler and its Record index.
func (r *RecordSetReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha1.RecordSet{}, IndexRecordSetRecords, func(o client.Object) []string {
		vs := o.(*v1alpha1.RecordSet).Spec.Versions
		names := make([]string, 0, len(vs))
		for _, v := range vs {
			names = append(names, v.RecordRef.Name)
		}
		return names
	}); err != nil {
		return err
	}

	setsFor := func(ctx context.Context, o client.Object) []reconcile.Request {
		l := &v1alpha1.RecordSetList{}
		if err := r.List(ctx, l, client.InNamespace(o.GetNamespace()), client.MatchingFields{IndexRecordSetRecords: o.GetName()}); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(l.Items))
		for _, s := range l.Items {
			reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: s.Namespace, Name: s.Name}})
		}
		return reqs
	}

	// RecordSets are not filtered by generation: a Record becoming valid
	// changes only its status, and must still trigger verification.
	return ctrl.NewControllerManagedBy(mgr).
		Named("recordset").
		For(&v1alpha1.RecordSet{}).
		Watches(&v1alpha1.Record{}, handler.EnqueueRequestsFromMapFunc(setsFor)).
		Complete(r)
}

// Reconcile verifies every version of a RecordSet.
func (r *RecordSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	rs := &v1alpha1.RecordSet{}
	if err := r.Get(ctx, req.NamespacedName, rs); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	problems, err := r.verify(ctx, rs)
	if err != nil {
		return ctrl.Result{}, err
	}

	rs.Status.CurrentRecord = ""
	if rs.Spec.Current != nil {
		for _, v := range rs.Spec.Versions {
			if v.Version == *rs.Spec.Current {
				rs.Status.CurrentRecord = v.RecordRef.Name
			}
		}
	}

	status, reason, message := metav1.ConditionTrue, ReasonVerified, ""
	if len(problems) > 0 {
		status, reason, message = metav1.ConditionFalse, ReasonInvalidVersions, strings.Join(problems, "; ")
	}
	setCondition(&rs.Status.Conditions, rs.Generation, v1alpha1.ConditionReady, status, reason, message)
	rs.Status.ObservedGeneration = rs.Generation
	return ctrl.Result{}, r.Status().Update(ctx, rs)
}

// verify returns every RecordSet invariant the RecordSet violates that the
// API server cannot check itself.
func (r *RecordSetReconciler) verify(ctx context.Context, rs *v1alpha1.RecordSet) ([]string, error) {
	var problems []string
	for i, v := range rs.Spec.Versions {
		if i > 0 && rs.Spec.Versions[i-1].Version <= v.Version {
			problems = append(problems, fmt.Sprintf("versions must be ordered from highest to lowest, but %d follows %d", v.Version, rs.Spec.Versions[i-1].Version))
		}

		rec := &v1alpha1.Record{}
		err := r.Get(ctx, types.NamespacedName{Namespace: rs.Namespace, Name: v.RecordRef.Name}, rec)
		if kerrors.IsNotFound(err) {
			problems = append(problems, fmt.Sprintf("version %d: Record %q not found", v.Version, v.RecordRef.Name))
			continue
		}
		if err != nil {
			return nil, err
		}

		p := func(format string, args ...any) {
			problems = append(problems, fmt.Sprintf("version %d: ", v.Version)+fmt.Sprintf(format, args...))
		}
		if rec.Spec.Publisher != rs.Spec.Publisher {
			p("Record publisher %q does not match RecordSet publisher %q", rec.Spec.Publisher.ID, rs.Spec.Publisher.ID)
		}
		if rec.Spec.RecordTypeGroup != rs.Spec.RecordTypeGroup || rec.Spec.RecordType != rs.Spec.RecordType {
			p("Record type %s/%s does not match RecordSet type %s/%s", rec.Spec.RecordTypeGroup, rec.Spec.RecordType, rs.Spec.RecordTypeGroup, rs.Spec.RecordType)
		}
		if rec.Spec.Schema.Ref != v.SchemaRef.SchemaReference {
			p("schemaRef %s %q does not match the Record's schema %s %q", v.SchemaRef.Kind, v.SchemaRef.Name, rec.Spec.Schema.Ref.Kind, rec.Spec.Schema.Ref.Name)
		}
		if !meta.IsStatusConditionTrue(rec.Status.Conditions, v1alpha1.ConditionValid) {
			p("Record %q is not valid", rec.Name)
			continue
		}
		if d := v.RecordRef.DataDigest; d != "" && d != rec.Status.DataDigest {
			p("recordRef.dataDigest %s does not match the Record's data digest %s", d, rec.Status.DataDigest)
		}
		if d := v.SchemaRef.Digest; d != "" && (rec.Status.Schema == nil || d != rec.Status.Schema.Digest) {
			p("schemaRef.digest %s does not match the Record's contract digest", d)
		}
		if d := v.SchemaRef.StructuralDigest; d != "" {
			// The Record is valid, so its Schema resolves to the contract
			// it was bound to.
			spec, err := getSchemaSpec(ctx, r, rs.Namespace, v.SchemaRef.SchemaReference)
			if kerrors.IsNotFound(err) {
				p("%s %q not found", v.SchemaRef.Kind, v.SchemaRef.Name)
				continue
			}
			if err != nil {
				return nil, err
			}
			sd, err := StructuralDigest(*spec)
			if err != nil {
				p("cannot compute the structural digest of %s %q: %v", v.SchemaRef.Kind, v.SchemaRef.Name, err)
				continue
			}
			if d != sd {
				p("schemaRef.structuralDigest %s does not match the Schema's structural digest %s", d, sd)
			}
		}
	}
	return problems, nil
}
