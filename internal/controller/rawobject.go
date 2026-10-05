package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stevendborrelli/records-controller/api/v1alpha1"
)

// RawObjectName is the name of the well-known ClusterSchema that accepts any
// JSON object. Every implementation provides it.
const RawObjectName = "rawobject-v1"

// errNotRawObject reports that a ClusterSchema named rawobject-v1 exists but
// is not the well-known contract.
var errNotRawObject = errors.New("is not the well-known rawObject contract")

// RawObject returns the well-known rawobject-v1 ClusterSchema.
func RawObject() *v1alpha1.ClusterSchema {
	return &v1alpha1.ClusterSchema{
		ObjectMeta: metav1.ObjectMeta{Name: RawObjectName},
		Spec: v1alpha1.SchemaSpec{
			Publisher:    v1alpha1.Publisher{ID: "system"},
			ShapeGroup:   v1alpha1.Group(v1alpha1.GroupVersion.Group),
			Shape:        "rawObject",
			ShapeVersion: "v1",
			Format:       v1alpha1.FormatStructuralSchema,
			Definition:   runtime.RawExtension{Raw: []byte(`{"type":"object","x-kubernetes-preserve-unknown-fields":true}`)},
		},
	}
}

// ensureRawObject creates rawobject-v1 if it does not exist. It returns an
// error wrapping errNotRawObject if a ClusterSchema of that name exists with
// a different contract, Publisher, or lineage. Its spec is immutable and
// Records may be bound to it, so it is never replaced.
func ensureRawObject(ctx context.Context, c client.Writer, r client.Reader) error {
	want := RawObject()
	err := c.Create(ctx, want.DeepCopy())
	if !kerrors.IsAlreadyExists(err) {
		return err
	}
	got := &v1alpha1.ClusterSchema{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(want), got); err != nil {
		return err
	}
	wd, err := ContractDigest(want.Spec)
	if err != nil {
		return err
	}
	gd, err := ContractDigest(got.Spec)
	g, w := got.Spec, want.Spec
	if err != nil || gd != wd || g.Publisher != w.Publisher || g.ShapeGroup != w.ShapeGroup || g.Shape != w.Shape || g.ShapeVersion != w.ShapeVersion {
		return fmt.Errorf("ClusterSchema %s %w", RawObjectName, errNotRawObject)
	}
	return nil
}

// provideRawObject creates rawobject-v1 when the manager starts, retrying
// until it succeeds. A conflicting rawobject-v1 is reported, not replaced.
func provideRawObject(c client.Writer, r client.Reader) func(context.Context) error {
	return func(ctx context.Context) error {
		log := ctrl.LoggerFrom(ctx).WithName("rawobject")
		_ = wait.PollUntilContextCancel(ctx, 5*time.Second, true, func(ctx context.Context) (bool, error) {
			err := ensureRawObject(ctx, c, r)
			switch {
			case errors.Is(err, errNotRawObject):
				log.Error(err, "cannot provide the well-known ClusterSchema; Records that reference it are validated against the existing one")
				return true, nil
			case err != nil:
				log.Error(err, "cannot create ClusterSchema, retrying", "name", RawObjectName)
				return false, nil
			}
			return true, nil
		})
		return nil
	}
}
