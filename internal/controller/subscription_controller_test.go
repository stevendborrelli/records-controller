package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stevendborrelli/records-controller/api/v1alpha1"
	"github.com/stevendborrelli/records-controller/internal/digest"
)

var (
	lineageV1 = v1alpha1.Lineage{ShapeGroup: "network.example.org", Shape: "subnet", ShapeVersion: "v1"}
	lineageV2 = v1alpha1.Lineage{ShapeGroup: "network.example.org", Shape: "subnet", ShapeVersion: "v2"}
)

// subnetSchemas creates subnet-v1 (shapeVersion v1) and subnet-v2
// (shapeVersion v2, gateway required).
func subnetSchemas(t *testing.T, ns string) {
	t.Helper()
	newSchema(t, ns, "subnet-v1", subnetV1)
	v2 := &v1alpha1.Schema{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "subnet-v2"}, Spec: schemaSpec(subnetV2)}
	v2.Spec.ShapeVersion = "v2"
	create(t, v2)
}

// cidrData is subnet data that differs by version, so each Record has a
// distinct digest.
func cidrData(n int64) string {
	return fmt.Sprintf(`{"cidr":"10.%d.0.0/16","gateway":"10.%d.0.1"}`, n, n)
}

// publish creates the Record for version n against schema.
func publish(t *testing.T, ns string, n int64, schema string) {
	t.Helper()
	r := recordObj(ns, fmt.Sprintf("subnets-%d", n), schema, cidrData(n))
	create(t, r)
	waitForCondition(t, r, recordConds, v1alpha1.ConditionValid, metav1.ConditionTrue, ReasonVerified)
}

func entry(n int64, schema string) v1alpha1.RecordSetVersion {
	return v1alpha1.RecordSetVersion{
		Version:   n,
		RecordRef: v1alpha1.RecordReference{Name: fmt.Sprintf("subnets-%d", n)},
		SchemaRef: v1alpha1.VersionSchemaReference{SchemaReference: v1alpha1.SchemaReference{Kind: v1alpha1.KindSchema, Name: schema}},
	}
}

// subnetSet creates a RecordSet over versions, newest first.
func subnetSet(t *testing.T, ns string, current int64, versions ...v1alpha1.RecordSetVersion) *v1alpha1.RecordSet {
	t.Helper()
	rs := &v1alpha1.RecordSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "subnets"},
		Spec: v1alpha1.RecordSetSpec{
			Publisher:       v1alpha1.Publisher{ID: "team-network"},
			RecordTypeGroup: "network.example.org",
			RecordType:      "subnet",
			Current:         ptr.To(current),
			HighestVersion:  ptr.To(versions[0].Version),
			Versions:        versions,
		},
	}
	create(t, rs)
	return rs
}

// republish adds a version to the RecordSet and makes it current.
func republish(t *testing.T, rs *v1alpha1.RecordSet, v v1alpha1.RecordSetVersion) {
	t.Helper()
	err := update(rs, func(u *v1alpha1.RecordSet) {
		u.Spec.Versions = append([]v1alpha1.RecordSetVersion{v}, u.Spec.Versions...)
		u.Spec.HighestVersion = ptr.To(v.Version)
		u.Spec.Current = ptr.To(v.Version)
	})
	if err != nil {
		t.Fatalf("cannot publish version %d: %v", v.Version, err)
	}
}

func subscription(ns, name string, spec v1alpha1.SubscriptionSpec) *v1alpha1.Subscription {
	spec.RecordSet = v1alpha1.RecordSetReference{Name: "subnets"}
	return &v1alpha1.Subscription{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}, Spec: spec}
}

func subConds(s *v1alpha1.Subscription) []metav1.Condition { return s.Status.Conditions }

// waitForSelected waits until a Subscription serves version n, and checks it
// serves that version's data.
func waitForSelected(t *testing.T, s *v1alpha1.Subscription, n int64) *v1alpha1.Subscription {
	t.Helper()
	eventually(t, fmt.Sprintf("%s to select version %d", s.Name, n), func() error {
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(s), s); err != nil {
			return err
		}
		if s.Status.Selected == nil || s.Status.Selected.Version != n {
			return fmt.Errorf("selected %+v", s.Status.Selected)
		}
		return nil
	})
	got, err := digest.Of(s.Status.Data.Raw)
	if err != nil {
		t.Fatal(err)
	}
	if want := mustDigest(t, cidrData(n)); v1alpha1.Digest(got) != want || s.Status.Selected.DataDigest != want {
		t.Errorf("version %d: serving data digest %s (selected.dataDigest %s), want %s", n, got, s.Status.Selected.DataDigest, want)
	}
	return s
}

// conditionMessage returns a condition's message, failing if it is absent.
func conditionMessage(t *testing.T, s *v1alpha1.Subscription, ct string) string {
	t.Helper()
	for _, c := range s.Status.Conditions {
		if c.Type == ct {
			return c.Message
		}
	}
	t.Fatalf("%s has no %s condition", s.Name, ct)
	return ""
}

func TestSubscriptionFollowsCurrent(t *testing.T) {
	ns := namespace(t)
	subnetSchemas(t, ns)
	publish(t, ns, 1, "subnet-v1")
	publish(t, ns, 2, "subnet-v1")
	rs := subnetSet(t, ns, 2, entry(2, "subnet-v1"), entry(1, "subnet-v1"))

	s := subscription(ns, "follows-current", v1alpha1.SubscriptionSpec{})
	create(t, s)
	s = waitForSelected(t, s, 2)
	if s.Spec.Follow != v1alpha1.FollowCurrent || s.Spec.OnIncompatible != v1alpha1.IncompatibleHold {
		t.Errorf("defaults: follow %q onIncompatible %q, want Current and Hold", s.Spec.Follow, s.Spec.OnIncompatible)
	}
	if s.Status.Selected.Lineage != lineageV1 {
		t.Errorf("selected lineage: got %s, want %s", formatLineage(s.Status.Selected.Lineage), formatLineage(lineageV1))
	}
	waitForCondition(t, s, subConds, ConditionUpToDate, metav1.ConditionTrue, ReasonFollowing)

	// Without compatibleWith, a new contract is followed like any version.
	publish(t, ns, 3, "subnet-v2")
	republish(t, rs, entry(3, "subnet-v2"))
	waitForSelected(t, s, 3)
}

// A Consumer that reads only v1 keeps serving its last v1 version when the
// Publisher moves current to v2, and is told why.
func TestSubscriptionHoldsWhenCurrentIsIncompatible(t *testing.T) {
	ns := namespace(t)
	subnetSchemas(t, ns)
	publish(t, ns, 1, "subnet-v1")
	rs := subnetSet(t, ns, 1, entry(1, "subnet-v1"))

	hold := subscription(ns, "holds", v1alpha1.SubscriptionSpec{CompatibleWith: &lineageV1})
	fail := subscription(ns, "fails", v1alpha1.SubscriptionSpec{CompatibleWith: &lineageV1, OnIncompatible: v1alpha1.IncompatibleFail})
	create(t, hold)
	create(t, fail)
	waitForSelected(t, hold, 1)
	waitForSelected(t, fail, 1)

	// The Publisher names v1's successor, deprecates it, and moves current
	// to a v2 Record.
	if err := update(&v1alpha1.Schema{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "subnet-v1"}}, func(u *v1alpha1.Schema) {
		u.Spec.Successor = &v1alpha1.SchemaReference{Kind: v1alpha1.KindSchema, Name: "subnet-v2"}
		u.Spec.Deprecation = &v1alpha1.Deprecation{Date: "2027-06-01", Message: "gateway is now required"}
	}); err != nil {
		t.Fatal(err)
	}
	publish(t, ns, 2, "subnet-v2")
	republish(t, rs, entry(2, "subnet-v2"))

	hold = waitForCondition(t, hold, subConds, ConditionUpToDate, metav1.ConditionFalse, ReasonIncompatible)
	for _, want := range []string{
		"current is version 2 (network.example.org/subnet/v2), which is incompatible with network.example.org/subnet/v1",
		"holding version 1",
		"the successor of subnet-v1 is Schema subnet-v2",
	} {
		if msg := conditionMessage(t, hold, ConditionUpToDate); !strings.Contains(msg, want) {
			t.Errorf("UpToDate message: want it to contain %q, got %q", want, msg)
		}
	}
	waitForCondition(t, hold, subConds, v1alpha1.ConditionReady, metav1.ConditionTrue, ReasonSelected)
	hold = waitForCondition(t, hold, subConds, ConditionDeprecated, metav1.ConditionTrue, ReasonSchemaDeprecated)
	if msg := conditionMessage(t, hold, ConditionDeprecated); !strings.Contains(msg, "deprecated from 2027-06-01: gateway is now required; its successor is Schema subnet-v2") {
		t.Errorf("Deprecated message: got %q", msg)
	}
	waitForSelected(t, hold, 1)

	// Fail keeps serving the held data, but is not Ready.
	waitForCondition(t, fail, subConds, v1alpha1.ConditionReady, metav1.ConditionFalse, ReasonIncompatible)
	waitForSelected(t, fail, 1)
}

func TestSubscriptionFollowsLatestCompatible(t *testing.T) {
	ns := namespace(t)
	subnetSchemas(t, ns)
	publish(t, ns, 1, "subnet-v1")
	publish(t, ns, 2, "subnet-v1")
	publish(t, ns, 3, "subnet-v2")
	rs := subnetSet(t, ns, 3, entry(3, "subnet-v2"), entry(2, "subnet-v1"), entry(1, "subnet-v1"))
	if err := update(rs, func(u *v1alpha1.RecordSet) {
		u.Spec.Retracted = []v1alpha1.Retraction{{Version: 2, Message: "bad CIDR"}}
	}); err != nil {
		t.Fatal(err)
	}

	// 3 is incompatible and 2 is retracted, so the newest compatible is 1.
	s := subscription(ns, "latest-v1", v1alpha1.SubscriptionSpec{Follow: v1alpha1.FollowLatestCompatible, CompatibleWith: &lineageV1})
	create(t, s)
	waitForSelected(t, s, 1)

	// The Publisher dual-publishes a v1 Record for Consumers not yet on v2.
	publish(t, ns, 4, "subnet-v1")
	if err := update(rs, func(u *v1alpha1.RecordSet) {
		u.Spec.Versions = append([]v1alpha1.RecordSetVersion{entry(4, "subnet-v1")}, u.Spec.Versions...)
		u.Spec.HighestVersion = ptr.To[int64](4)
	}); err != nil {
		t.Fatal(err)
	}
	waitForSelected(t, s, 4)
	waitForCondition(t, s, subConds, ConditionUpToDate, metav1.ConditionTrue, ReasonFollowing)
}

func TestSubscriptionPinned(t *testing.T) {
	ns := namespace(t)
	subnetSchemas(t, ns)
	publish(t, ns, 1, "subnet-v1")
	publish(t, ns, 2, "subnet-v1")
	rs := subnetSet(t, ns, 2, entry(2, "subnet-v1"), entry(1, "subnet-v1"))

	s := subscription(ns, "pinned", v1alpha1.SubscriptionSpec{Follow: v1alpha1.FollowPinned, Version: ptr.To[int64](1)})
	create(t, s)
	waitForSelected(t, s, 1)

	// A pinned Consumer keeps a retracted version, and is told.
	if err := update(rs, func(u *v1alpha1.RecordSet) {
		u.Spec.Retracted = []v1alpha1.Retraction{{Version: 1, Message: "wrong gateway"}}
	}); err != nil {
		t.Fatal(err)
	}
	s = waitForCondition(t, s, subConds, v1alpha1.ConditionReady, metav1.ConditionTrue, ReasonSelectedRetracted)
	if msg := conditionMessage(t, s, v1alpha1.ConditionReady); !strings.Contains(msg, "wrong gateway") {
		t.Errorf("Ready message: want the retraction message, got %q", msg)
	}
}

func TestSubscriptionWaitsForItsRecordSet(t *testing.T) {
	ns := namespace(t)
	s := subscription(ns, "early", v1alpha1.SubscriptionSpec{})
	create(t, s)
	waitForCondition(t, s, subConds, v1alpha1.ConditionReady, metav1.ConditionFalse, ReasonRecordSetNotFound)

	subnetSchemas(t, ns)
	publish(t, ns, 1, "subnet-v1")
	subnetSet(t, ns, 1, entry(1, "subnet-v1"))
	waitForSelected(t, s, 1)
}

// A version is served only once its Record is valid.
func TestSubscriptionWaitsForAValidRecord(t *testing.T) {
	ns := namespace(t)
	create(t, recordObj(ns, "subnets-1", "subnet-v1", cidrData(1))) // Its Schema does not exist yet.
	subnetSet(t, ns, 1, entry(1, "subnet-v1"))

	s := subscription(ns, "unverified", v1alpha1.SubscriptionSpec{})
	create(t, s)
	s = waitForCondition(t, s, subConds, v1alpha1.ConditionReady, metav1.ConditionFalse, ReasonUnverified)
	if s.Status.Data != nil {
		t.Errorf("an unverified version's data was served: %s", s.Status.Data.Raw)
	}

	subnetSchemas(t, ns)
	waitForSelected(t, s, 1)
}

func TestSubscriptionAdmission(t *testing.T) {
	ns := namespace(t)
	cases := map[string]struct {
		spec v1alpha1.SubscriptionSpec
		want string
	}{
		"PinnedNeedsVersion": {
			spec: v1alpha1.SubscriptionSpec{Follow: v1alpha1.FollowPinned},
			want: "version is required when follow is Pinned",
		},
		"VersionOnlyWhenPinned": {
			spec: v1alpha1.SubscriptionSpec{Follow: v1alpha1.FollowCurrent, Version: ptr.To[int64](1)},
			want: "version may be set only when follow is Pinned",
		},
		"LatestCompatibleNeedsCompatibleWith": {
			spec: v1alpha1.SubscriptionSpec{Follow: v1alpha1.FollowLatestCompatible},
			want: "compatibleWith is required when follow is LatestCompatible",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mustReject(t, k8s.Create(context.Background(), subscription(ns, strings.ToLower(name), tc.spec)), tc.want)
		})
	}
}
