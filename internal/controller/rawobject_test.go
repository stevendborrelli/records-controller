package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stevendborrelli/records-controller/api/v1alpha1"
)

func rawObjectRecord(ns, name, data string) *v1alpha1.Record {
	r := recordObj(ns, name, RawObjectName, data)
	r.Spec.Schema.Ref.Kind = v1alpha1.KindClusterSchema
	return r
}

// The controller provides rawobject-v1, keeps it when it is deleted, and
// Records bound to it stay bound to the same contract.
func TestRawObjectSchemaIsProvided(t *testing.T) {
	cs := &v1alpha1.ClusterSchema{ObjectMeta: metav1.ObjectMeta{Name: RawObjectName}}
	cs = waitForCondition(t, cs, clusterConds, v1alpha1.ConditionReady, metav1.ConditionTrue, ReasonVerified)
	want := RawObject().Spec
	if g := cs.Spec; g.Publisher != want.Publisher || g.ShapeGroup != want.ShapeGroup || g.Shape != want.Shape || g.ShapeVersion != want.ShapeVersion {
		t.Errorf("rawobject-v1: got publisher %q lineage %s/%s/%s, want %q %s/%s/%s",
			g.Publisher.ID, g.ShapeGroup, g.Shape, g.ShapeVersion, want.Publisher.ID, want.ShapeGroup, want.Shape, want.ShapeVersion)
	}
	digest := cs.Status.Digest

	ns := namespace(t)
	r := rawObjectRecord(ns, "anything", `{"owner":"team-a","nested":{"list":[1,"two",{"three":true}]},"empty":null}`)
	create(t, r)
	waitForCondition(t, r, recordConds, v1alpha1.ConditionValid, metav1.ConditionTrue, ReasonVerified)

	if err := k8s.Delete(context.Background(), cs); err != nil {
		t.Fatal(err)
	}
	eventually(t, "rawobject-v1 to be recreated", func() error {
		got := &v1alpha1.ClusterSchema{}
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(cs), got); err != nil {
			return err
		}
		if got.UID == cs.UID {
			return fmt.Errorf("rawobject-v1 has not been deleted yet")
		}
		if got.Status.Digest != digest {
			return fmt.Errorf("recreated rawobject-v1 has digest %q, want %s", got.Status.Digest, digest)
		}
		return nil
	})
	waitForCondition(t, r, recordConds, v1alpha1.ConditionValid, metav1.ConditionTrue, ReasonVerified)
}

// A rawobject-v1 with another contract is reported, never replaced.
func TestEnsureRawObjectDoesNotReplaceADifferentContract(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	impostor := RawObject()
	impostor.Spec.Definition = raw(subnetV1)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(impostor).Build()

	err := ensureRawObject(context.Background(), c, c)
	if !errors.Is(err, errNotRawObject) {
		t.Fatalf("want errNotRawObject, got %v", err)
	}
	got := &v1alpha1.ClusterSchema{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(impostor), got); err != nil {
		t.Fatal(err)
	}
	gd, err := ContractDigest(got.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if want := mustContractDigest(t, subnetV1); gd != want {
		t.Errorf("rawobject-v1 was modified: contract digest %s, want %s", gd, want)
	}
}
