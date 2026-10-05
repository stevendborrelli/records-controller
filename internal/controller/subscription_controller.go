package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/stevendborrelli/records-controller/api/v1alpha1"
)

// Subscription condition types.
const (
	// ConditionUpToDate is True when the Subscription serves the version
	// its follow policy points to.
	ConditionUpToDate = "UpToDate"

	// ConditionDeprecated is True when the selected version's Schema is
	// deprecated.
	ConditionDeprecated = "Deprecated"
)

// Subscription condition reasons.
const (
	ReasonSelected          = "Selected"
	ReasonSelectedRetracted = "SelectedRetracted"
	ReasonRecordSetNotFound = "RecordSetNotFound"
	ReasonNoVersion         = "NoVersion"
	ReasonIncompatible      = "Incompatible"
	ReasonUnverified        = "Unverified"
	ReasonFollowing         = "Following"
	ReasonSchemaDeprecated  = "SchemaDeprecated"
	ReasonNotDeprecated     = "NotDeprecated"
)

// Indexes used to find the Subscriptions an event affects.
const (
	IndexSubscriptionRecordSet = "spec.recordSet"
	IndexSubscriptionSchema    = "status.selected.schema"
)

// recordSetKey identifies a RecordSet by namespace and name.
func recordSetKey(namespace, name string) string { return namespace + "/" + name }

// schemaKey identifies a Schema or ClusterSchema referenced from namespace.
func schemaKey(namespace string, ref v1alpha1.SchemaReference) string {
	if ref.Kind == v1alpha1.KindClusterSchema {
		namespace = ""
	}
	return string(ref.Kind) + "/" + namespace + "/" + ref.Name
}

// SubscriptionReconciler selects, verifies, and serves a RecordSet version
// for each Subscription.
type SubscriptionReconciler struct {
	client.Client
}

// SetupWithManager registers the reconciler and its indexes. The RecordSet
// controller must be set up first: Records are mapped to Subscriptions
// through its index of RecordSets by Record.
func (r *SubscriptionReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	idx := mgr.GetFieldIndexer()
	if err := idx.IndexField(ctx, &v1alpha1.Subscription{}, IndexSubscriptionRecordSet, func(o client.Object) []string {
		s := o.(*v1alpha1.Subscription)
		return []string{recordSetKey(s.Namespace, s.Spec.RecordSet.Name)}
	}); err != nil {
		return err
	}
	if err := idx.IndexField(ctx, &v1alpha1.Subscription{}, IndexSubscriptionSchema, func(o client.Object) []string {
		s := o.(*v1alpha1.Subscription)
		if s.Status.Selected == nil {
			return nil
		}
		return []string{schemaKey(s.Namespace, s.Status.Selected.Schema)}
	}); err != nil {
		return err
	}

	subscriptionsFor := func(ctx context.Context, field, value string) []reconcile.Request {
		l := &v1alpha1.SubscriptionList{}
		if err := r.List(ctx, l, client.MatchingFields{field: value}); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(l.Items))
		for _, s := range l.Items {
			reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: s.Namespace, Name: s.Name}})
		}
		return reqs
	}
	forRecordSet := func(ctx context.Context, o client.Object) []reconcile.Request {
		return subscriptionsFor(ctx, IndexSubscriptionRecordSet, recordSetKey(o.GetNamespace(), o.GetName()))
	}
	forRecord := func(ctx context.Context, o client.Object) []reconcile.Request {
		l := &v1alpha1.RecordSetList{}
		if err := r.List(ctx, l, client.InNamespace(o.GetNamespace()), client.MatchingFields{IndexRecordSetRecords: o.GetName()}); err != nil {
			return nil
		}
		var reqs []reconcile.Request
		for _, rs := range l.Items {
			reqs = append(reqs, forRecordSet(ctx, &rs)...)
		}
		return reqs
	}
	forSchema := func(kind v1alpha1.SchemaKind) handler.MapFunc {
		return func(ctx context.Context, o client.Object) []reconcile.Request {
			ref := v1alpha1.SchemaReference{Kind: kind, Name: o.GetName()}
			return subscriptionsFor(ctx, IndexSubscriptionSchema, schemaKey(o.GetNamespace(), ref))
		}
	}

	// RecordSets and Records are not filtered by generation: a Record
	// becoming valid changes only its status.
	return ctrl.NewControllerManagedBy(mgr).
		Named("subscription").
		For(&v1alpha1.Subscription{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&v1alpha1.RecordSet{}, handler.EnqueueRequestsFromMapFunc(forRecordSet)).
		Watches(&v1alpha1.Record{}, handler.EnqueueRequestsFromMapFunc(forRecord)).
		Watches(&v1alpha1.Schema{}, handler.EnqueueRequestsFromMapFunc(forSchema(v1alpha1.KindSchema)), builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&v1alpha1.ClusterSchema{}, handler.EnqueueRequestsFromMapFunc(forSchema(v1alpha1.KindClusterSchema)), builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

// candidate is a RecordSet version whose Record has been verified.
type candidate struct {
	entry  v1alpha1.RecordSetVersion
	record *v1alpha1.Record
	schema *v1alpha1.SchemaSpec
}

func (c *candidate) lineage() v1alpha1.Lineage {
	return v1alpha1.Lineage{ShapeGroup: c.schema.ShapeGroup, Shape: c.schema.Shape, ShapeVersion: c.schema.ShapeVersion}
}

func formatLineage(l v1alpha1.Lineage) string {
	return string(l.ShapeGroup) + "/" + l.Shape + "/" + l.ShapeVersion
}

// Reconcile selects the version a Subscription follows, verifies it, and
// serves it.
func (r *SubscriptionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	sub := &v1alpha1.Subscription{}
	if err := r.Get(ctx, req.NamespacedName, sub); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if err := r.reconcile(ctx, sub); err != nil {
		return ctrl.Result{}, err
	}
	sub.Status.ObservedGeneration = sub.Generation
	return ctrl.Result{}, r.Status().Update(ctx, sub)
}

// reconcile updates sub's status. The selected version changes only when a
// new version is selected and verified; every other outcome keeps it.
func (r *SubscriptionReconciler) reconcile(ctx context.Context, sub *v1alpha1.Subscription) error {
	ns := sub.Namespace
	held := sub.Status.Selected != nil
	notReady := func(reason, message string) {
		setCondition(&sub.Status.Conditions, sub.Generation, v1alpha1.ConditionReady, metav1.ConditionFalse, reason, message)
	}
	stale := func(reason, message string) {
		setCondition(&sub.Status.Conditions, sub.Generation, ConditionUpToDate, metav1.ConditionFalse, reason, message)
	}
	// holding keeps a previously selected version Ready, and otherwise
	// reports not Ready.
	holding := func(reason, message string) {
		if held {
			setCondition(&sub.Status.Conditions, sub.Generation, v1alpha1.ConditionReady, metav1.ConditionTrue, ReasonSelected,
				fmt.Sprintf("serving version %d", sub.Status.Selected.Version))
			return
		}
		notReady(reason, message)
	}

	rs := &v1alpha1.RecordSet{}
	err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: sub.Spec.RecordSet.Name}, rs)
	if kerrors.IsNotFound(err) {
		msg := fmt.Sprintf("RecordSet %s/%s not found", ns, sub.Spec.RecordSet.Name)
		notReady(ReasonRecordSetNotFound, msg)
		stale(ReasonRecordSetNotFound, msg)
		return r.setDeprecated(ctx, sub)
	}
	if err != nil {
		return err
	}

	retracted := map[int64]string{}
	for _, rt := range rs.Spec.Retracted {
		retracted[rt.Version] = rt.Message
	}
	versions := slices.Clone(rs.Spec.Versions)
	slices.SortFunc(versions, func(a, b v1alpha1.RecordSetVersion) int { return int(b.Version - a.Version) })
	entry := func(v int64) *v1alpha1.RecordSetVersion {
		for i := range versions {
			if versions[i].Version == v {
				return &versions[i]
			}
		}
		return nil
	}
	compatible := func(c *candidate) bool {
		return sub.Spec.CompatibleWith == nil || c.lineage() == *sub.Spec.CompatibleWith
	}

	// Choose the version to follow, and verify it.
	var target *candidate
	switch sub.Spec.Follow {
	case v1alpha1.FollowLatestCompatible:
		var skipped []string
		for i := range versions {
			v := versions[i]
			if _, ok := retracted[v.Version]; ok {
				continue
			}
			c, problem, err := r.verify(ctx, rs, v)
			if err != nil {
				return err
			}
			if problem != "" {
				skipped = append(skipped, problem)
				continue
			}
			if compatible(c) {
				target = c
				break
			}
		}
		if target == nil {
			msg := fmt.Sprintf("no version of RecordSet %s/%s is compatible with %s", ns, rs.Name, formatLineage(*sub.Spec.CompatibleWith))
			if len(skipped) > 0 {
				msg += "; skipped " + strings.Join(skipped, "; ")
			}
			holding(ReasonNoVersion, msg)
			stale(ReasonNoVersion, msg)
			return r.setDeprecated(ctx, sub)
		}
	default:
		var want *int64
		what := "current"
		if sub.Spec.Follow == v1alpha1.FollowPinned {
			want, what = sub.Spec.Version, "pinned version"
		} else {
			want = rs.Spec.Current
		}
		if want == nil || entry(*want) == nil {
			msg := fmt.Sprintf("RecordSet %s/%s has no %s to follow", ns, rs.Name, what)
			if want != nil {
				msg = fmt.Sprintf("version %d is not listed in RecordSet %s/%s", *want, ns, rs.Name)
			}
			holding(ReasonNoVersion, msg)
			stale(ReasonNoVersion, msg)
			return r.setDeprecated(ctx, sub)
		}
		c, problem, err := r.verify(ctx, rs, *entry(*want))
		if err != nil {
			return err
		}
		if problem != "" {
			holding(ReasonUnverified, problem)
			stale(ReasonUnverified, problem)
			return r.setDeprecated(ctx, sub)
		}
		if !compatible(c) {
			msg := fmt.Sprintf("%s is version %d (%s), which is incompatible with %s", what, c.entry.Version, formatLineage(c.lineage()), formatLineage(*sub.Spec.CompatibleWith))
			if held {
				msg += fmt.Sprintf("; holding version %d", sub.Status.Selected.Version)
			}
			if s := r.successorOf(ctx, ns, sub.Status.Selected); s != "" {
				msg += "; " + s
			}
			stale(ReasonIncompatible, msg)
			if sub.Spec.OnIncompatible == v1alpha1.IncompatibleFail {
				notReady(ReasonIncompatible, msg)
			} else {
				holding(ReasonIncompatible, msg)
			}
			return r.setDeprecated(ctx, sub)
		}
		target = c
	}

	// Serve the target.
	sub.Status.Selected = &v1alpha1.SelectedVersion{
		Version:      target.entry.Version,
		Record:       target.record.Name,
		DataDigest:   target.record.Status.DataDigest,
		Schema:       target.record.Spec.Schema.Ref,
		SchemaDigest: target.record.Status.Schema.Digest,
		Lineage:      target.lineage(),
	}
	sub.Status.Data = &runtime.RawExtension{Raw: target.record.Spec.Data.Raw}
	reason, message := ReasonSelected, fmt.Sprintf("serving version %d", target.entry.Version)
	if m, ok := retracted[target.entry.Version]; ok {
		reason, message = ReasonSelectedRetracted, fmt.Sprintf("serving pinned version %d, which the Publisher retracted: %s", target.entry.Version, m)
	}
	setCondition(&sub.Status.Conditions, sub.Generation, v1alpha1.ConditionReady, metav1.ConditionTrue, reason, message)
	following := "following the Publisher's current version"
	switch sub.Spec.Follow {
	case v1alpha1.FollowLatestCompatible:
		following = "following the newest version compatible with " + formatLineage(*sub.Spec.CompatibleWith)
	case v1alpha1.FollowPinned:
		following = fmt.Sprintf("following pinned version %d", target.entry.Version)
	}
	setCondition(&sub.Status.Conditions, sub.Generation, ConditionUpToDate, metav1.ConditionTrue, ReasonFollowing, following)
	return r.setDeprecated(ctx, sub)
}

// verify resolves a RecordSet version and checks it the way a Consumer
// must: the Record exists, is valid, has the RecordSet's publisher and
// type, and matches the digests the version entry asserts. It returns a
// problem rather than an error when the version cannot be used.
func (r *SubscriptionReconciler) verify(ctx context.Context, rs *v1alpha1.RecordSet, v v1alpha1.RecordSetVersion) (*candidate, string, error) {
	p := func(format string, args ...any) string {
		return fmt.Sprintf("version %d: ", v.Version) + fmt.Sprintf(format, args...)
	}
	rec := &v1alpha1.Record{}
	err := r.Get(ctx, types.NamespacedName{Namespace: rs.Namespace, Name: v.RecordRef.Name}, rec)
	if kerrors.IsNotFound(err) {
		return nil, p("Record %q not found", v.RecordRef.Name), nil
	}
	if err != nil {
		return nil, "", err
	}
	switch {
	case !meta.IsStatusConditionTrue(rec.Status.Conditions, v1alpha1.ConditionValid) || rec.Status.Schema == nil:
		return nil, p("Record %q is not valid", rec.Name), nil
	case rec.Spec.Publisher != rs.Spec.Publisher || rec.Spec.RecordTypeGroup != rs.Spec.RecordTypeGroup || rec.Spec.RecordType != rs.Spec.RecordType:
		return nil, p("Record %q does not match the RecordSet's publisher and type", rec.Name), nil
	case v.RecordRef.DataDigest != "" && v.RecordRef.DataDigest != rec.Status.DataDigest:
		return nil, p("Record %q data digest does not match the version entry", rec.Name), nil
	case v.SchemaRef.Digest != "" && v.SchemaRef.Digest != rec.Status.Schema.Digest:
		return nil, p("Record %q contract digest does not match the version entry", rec.Name), nil
	}
	spec, err := getSchemaSpec(ctx, r, rs.Namespace, rec.Spec.Schema.Ref)
	if kerrors.IsNotFound(err) {
		return nil, p("%s %q not found", rec.Spec.Schema.Ref.Kind, rec.Spec.Schema.Ref.Name), nil
	}
	if err != nil {
		return nil, "", err
	}
	return &candidate{entry: v, record: rec, schema: spec}, "", nil
}

// successorOf describes the successor of the selected version's Schema, if
// its Publisher names one.
func (r *SubscriptionReconciler) successorOf(ctx context.Context, ns string, s *v1alpha1.SelectedVersion) string {
	if s == nil {
		return ""
	}
	spec, err := getSchemaSpec(ctx, r, ns, s.Schema)
	if err != nil || spec.Successor == nil {
		return ""
	}
	return fmt.Sprintf("the successor of %s is %s %s", s.Schema.Name, spec.Successor.Kind, spec.Successor.Name)
}

// setDeprecated reports whether the selected version's Schema is deprecated.
func (r *SubscriptionReconciler) setDeprecated(ctx context.Context, sub *v1alpha1.Subscription) error {
	s := sub.Status.Selected
	if s == nil {
		meta.RemoveStatusCondition(&sub.Status.Conditions, ConditionDeprecated)
		return nil
	}
	spec, err := getSchemaSpec(ctx, r, sub.Namespace, s.Schema)
	if kerrors.IsNotFound(err) {
		meta.RemoveStatusCondition(&sub.Status.Conditions, ConditionDeprecated)
		return nil
	}
	if err != nil {
		return err
	}
	if d := spec.Deprecation; d != nil {
		msg := fmt.Sprintf("%s %s is deprecated from %s", s.Schema.Kind, s.Schema.Name, d.Date)
		if d.Message != "" {
			msg += ": " + d.Message
		}
		if spec.Successor != nil {
			msg += fmt.Sprintf("; its successor is %s %s", spec.Successor.Kind, spec.Successor.Name)
		}
		setCondition(&sub.Status.Conditions, sub.Generation, ConditionDeprecated, metav1.ConditionTrue, ReasonSchemaDeprecated, msg)
		return nil
	}
	msg := ""
	if spec.Successor != nil {
		msg = fmt.Sprintf("%s %s names a successor, %s %s", s.Schema.Kind, s.Schema.Name, spec.Successor.Kind, spec.Successor.Name)
	}
	setCondition(&sub.Status.Conditions, sub.Generation, ConditionDeprecated, metav1.ConditionFalse, ReasonNotDeprecated, msg)
	return nil
}
