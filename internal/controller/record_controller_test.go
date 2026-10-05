package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stevendborrelli/records-controller/api/v1alpha1"
	"github.com/stevendborrelli/records-controller/internal/digest"
)

const subnetV1 = `{
  "type": "object",
  "required": ["cidr"],
  "properties": {
    "cidr": {"type": "string"},
    "location": {
      "type": "object",
      "properties": {"region": {"type": "string"}, "country": {"type": "string"}}
    },
    "region": {"type": "string"},
    "zones": {"type": "array", "items": {"type": "string"}},
    "gateway": {"type": "string"}
  }
}`

// subnetV2 makes gateway required.
const subnetV2 = `{
  "type": "object",
  "required": ["cidr", "gateway"],
  "properties": {
    "cidr": {"type": "string"},
    "location": {
      "type": "object",
      "properties": {"region": {"type": "string"}, "country": {"type": "string"}}
    },
    "region": {"type": "string"},
    "zones": {"type": "array", "items": {"type": "string"}},
    "gateway": {"type": "string"}
  }
}`

const subnetData = `{"cidr":"10.21.0.0/16","location":{"region":"NorthAmerica","country":"UnitedStates"},"region":"us-west-2","zones":["us-west-2a","us-west-2b","us-west-2c"],"gateway":"10.21.0.1"}`

func raw(s string) runtime.RawExtension { return runtime.RawExtension{Raw: []byte(s)} }

func schemaSpec(def string) v1alpha1.SchemaSpec {
	return v1alpha1.SchemaSpec{
		Publisher:  v1alpha1.Publisher{ID: "team-network"},
		Format:     v1alpha1.FormatStructuralSchema,
		Definition: raw(def),
	}
}

func newSchema(t *testing.T, ns, name, def string) *v1alpha1.Schema {
	t.Helper()
	s := &v1alpha1.Schema{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       schemaSpec(def),
	}
	if err := k8s.Create(context.Background(), s); err != nil {
		t.Fatalf("cannot create Schema: %v", err)
	}
	return s
}

func recordObj(ns, name, schemaName, data string) *v1alpha1.Record {
	return &v1alpha1.Record{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: v1alpha1.RecordSpec{
			Publisher:  v1alpha1.Publisher{ID: "team-network"},
			RecordType: "subnet",
			Schema:     v1alpha1.RecordSchema{Ref: v1alpha1.SchemaReference{Kind: v1alpha1.KindSchema, Name: schemaName}},
			Data:       raw(data),
		},
	}
}

func create(t *testing.T, obj client.Object) {
	t.Helper()
	if err := k8s.Create(context.Background(), obj); err != nil {
		t.Fatalf("cannot create %s: %v", obj.GetName(), err)
	}
}

func mustDigest(t *testing.T, s string) v1alpha1.Digest {
	t.Helper()
	d, err := digest.Of([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v1alpha1.Digest(d)
}

func mustContractDigest(t *testing.T, def string) v1alpha1.Digest {
	t.Helper()
	d, err := ContractDigest(schemaSpec(def))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A digest that is well formed but matches nothing.
const bogusDigest v1alpha1.Digest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

func TestSchemaRecordsContractDigest(t *testing.T) {
	ns := namespace(t)
	s := newSchema(t, ns, "subnet-v1", subnetV1)
	s = waitForCondition(t, s, schemaConds, v1alpha1.ConditionReady, metav1.ConditionTrue, ReasonVerified)
	if want := mustContractDigest(t, subnetV1); s.Status.Digest != want {
		t.Errorf("status.digest: got %s, want %s", s.Status.Digest, want)
	}
}

func TestSchemaRejectsNonStructuralDefinition(t *testing.T) {
	ns := namespace(t)
	s := newSchema(t, ns, "bad", `{"type":"object","properties":{"a":{}}}`)
	s = waitForCondition(t, s, schemaConds, v1alpha1.ConditionReady, metav1.ConditionFalse, ReasonInvalidDefinition)
	if s.Status.Digest != "" {
		t.Errorf("status.digest should not be recorded for an invalid definition, got %s", s.Status.Digest)
	}
}

func TestRecordValidWithoutAssertedDigests(t *testing.T) {
	ns := namespace(t)
	newSchema(t, ns, "subnet-v1", subnetV1)
	r := recordObj(ns, "prod-west-3", "subnet-v1", subnetData)
	create(t, r)

	r = waitForCondition(t, r, recordConds, v1alpha1.ConditionValid, metav1.ConditionTrue, ReasonVerified)
	if want := mustDigest(t, subnetData); r.Status.DataDigest != want {
		t.Errorf("status.dataDigest: got %s, want %s", r.Status.DataDigest, want)
	}
	if want := mustContractDigest(t, subnetV1); r.Status.Schema == nil || r.Status.Schema.Digest != want {
		t.Errorf("status.schema.digest: got %+v, want %s", r.Status.Schema, want)
	}
}

func TestRecordValidWithAssertedDigests(t *testing.T) {
	ns := namespace(t)
	newSchema(t, ns, "subnet-v1", subnetV1)
	r := recordObj(ns, "prod-west-3", "subnet-v1", subnetData)
	r.Spec.DataDigest = mustDigest(t, subnetData)
	r.Spec.Schema.Digest = mustContractDigest(t, subnetV1)
	create(t, r)
	waitForCondition(t, r, recordConds, v1alpha1.ConditionValid, metav1.ConditionTrue, ReasonVerified)
}

func TestRecordAssertedDigestMismatch(t *testing.T) {
	ns := namespace(t)
	newSchema(t, ns, "subnet-v1", subnetV1)

	data := recordObj(ns, "bad-data-digest", "subnet-v1", subnetData)
	data.Spec.DataDigest = bogusDigest
	create(t, data)

	sch := recordObj(ns, "bad-schema-digest", "subnet-v1", subnetData)
	sch.Spec.Schema.Digest = bogusDigest
	create(t, sch)

	data = waitForCondition(t, data, recordConds, v1alpha1.ConditionValid, metav1.ConditionFalse, ReasonDataDigestMismatch)
	sch = waitForCondition(t, sch, recordConds, v1alpha1.ConditionValid, metav1.ConditionFalse, ReasonSchemaDigestMismatch)
	if data.Status.DataDigest != "" || sch.Status.Schema != nil {
		t.Error("an invalid Record must not have its digests recorded")
	}
}

func TestRecordRejectsMalformedAssertedDigest(t *testing.T) {
	ns := namespace(t)
	r := recordObj(ns, "truncated", "subnet-v1", subnetData)
	r.Spec.DataDigest = "sha256:c69a1339…"
	mustReject(t, k8s.Create(context.Background(), r), "spec.dataDigest")
}

func TestRecordDataInvalid(t *testing.T) {
	ns := namespace(t)
	newSchema(t, ns, "subnet-v1", subnetV1)

	unknown := recordObj(ns, "unknown-field", "subnet-v1", `{"cidr":"10.0.0.0/8","owner":"team-a"}`)
	create(t, unknown)
	missing := recordObj(ns, "missing-cidr", "subnet-v1", `{"region":"us-west-2"}`)
	create(t, missing)

	waitForCondition(t, unknown, recordConds, v1alpha1.ConditionValid, metav1.ConditionFalse, ReasonDataInvalid)
	waitForCondition(t, missing, recordConds, v1alpha1.ConditionValid, metav1.ConditionFalse, ReasonDataInvalid)
}

func TestRecordBecomesValidWhenSchemaArrives(t *testing.T) {
	ns := namespace(t)
	r := recordObj(ns, "early", "subnet-v1", subnetData)
	create(t, r)
	waitForCondition(t, r, recordConds, v1alpha1.ConditionValid, metav1.ConditionFalse, ReasonSchemaNotFound)

	newSchema(t, ns, "subnet-v1", subnetV1)
	waitForCondition(t, r, recordConds, v1alpha1.ConditionValid, metav1.ConditionTrue, ReasonVerified)
}

func TestRecordSchemaReferenceScope(t *testing.T) {
	owner, other := namespace(t), namespace(t)
	newSchema(t, owner, "subnet-v1", subnetV1)

	// A Schema reference resolves only in the Record's own namespace.
	cross := recordObj(other, "cross-namespace", "subnet-v1", subnetData)
	create(t, cross)
	waitForCondition(t, cross, recordConds, v1alpha1.ConditionValid, metav1.ConditionFalse, ReasonSchemaNotFound)

	// A ClusterSchema can be referenced from any namespace.
	cs := &v1alpha1.ClusterSchema{
		ObjectMeta: metav1.ObjectMeta{Name: "subnet-v1-" + other},
		Spec:       schemaSpec(subnetV1),
	}
	create(t, cs)
	waitForCondition(t, cs, clusterConds, v1alpha1.ConditionReady, metav1.ConditionTrue, ReasonVerified)

	r := recordObj(other, "cluster-scoped", cs.Name, subnetData)
	r.Spec.Schema.Ref.Kind = v1alpha1.KindClusterSchema
	create(t, r)
	waitForCondition(t, r, recordConds, v1alpha1.ConditionValid, metav1.ConditionTrue, ReasonVerified)
}

func TestRecordSpecIsImmutable(t *testing.T) {
	ns := namespace(t)
	newSchema(t, ns, "subnet-v1", subnetV1)
	r := recordObj(ns, "immutable", "subnet-v1", subnetData)
	create(t, r)
	r = waitForCondition(t, r, recordConds, v1alpha1.ConditionValid, metav1.ConditionTrue, ReasonVerified)

	t.Run("RecordType", func(t *testing.T) {
		u := r.DeepCopy()
		u.Spec.RecordType = "region"
		mustReject(t, k8s.Update(context.Background(), u), "spec is immutable")
	})
	t.Run("Data", func(t *testing.T) {
		u := r.DeepCopy()
		u.Spec.Data = raw(`{"cidr":"10.99.0.0/16"}`)
		mustReject(t, k8s.Update(context.Background(), u), "spec is immutable")
	})
}

func TestRecordStatusDigestsAreWriteOnce(t *testing.T) {
	ns := namespace(t)
	newSchema(t, ns, "subnet-v1", subnetV1)
	r := recordObj(ns, "write-once", "subnet-v1", subnetData)
	create(t, r)
	r = waitForCondition(t, r, recordConds, v1alpha1.ConditionValid, metav1.ConditionTrue, ReasonVerified)

	t.Run("DataDigest", func(t *testing.T) {
		u := r.DeepCopy()
		u.Status.DataDigest = bogusDigest
		mustReject(t, k8s.Status().Update(context.Background(), u), "status.dataDigest is write-once")
	})
	t.Run("SchemaDigest", func(t *testing.T) {
		u := r.DeepCopy()
		u.Status.Schema = nil
		mustReject(t, k8s.Status().Update(context.Background(), u), "status.schema.digest is write-once")
	})
}

func TestSchemaContractIsImmutable(t *testing.T) {
	ns := namespace(t)
	s := newSchema(t, ns, "subnet-v1", subnetV1)
	s = waitForCondition(t, s, schemaConds, v1alpha1.ConditionReady, metav1.ConditionTrue, ReasonVerified)

	t.Run("Definition", func(t *testing.T) {
		u := s.DeepCopy()
		u.Spec.Definition = raw(subnetV2)
		mustReject(t, k8s.Update(context.Background(), u), "spec.definition is immutable")
	})
	t.Run("Publisher", func(t *testing.T) {
		u := s.DeepCopy()
		u.Spec.Publisher.ID = "someone-else"
		mustReject(t, k8s.Update(context.Background(), u), "spec.publisher is immutable")
	})
	t.Run("DeprecationIsMutable", func(t *testing.T) {
		u := s.DeepCopy()
		u.Spec.Deprecation = &v1alpha1.Deprecation{Date: "2027-01-01", Message: "superseded by subnet-v2"}
		if err := k8s.Update(context.Background(), u); err != nil {
			t.Fatalf("deprecating a Schema should be allowed: %v", err)
		}
	})
}

func TestSchemaDeprecationDateFormat(t *testing.T) {
	ns := namespace(t)
	s := &v1alpha1.Schema{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "bad-date"},
		Spec:       schemaSpec(subnetV1),
	}
	s.Spec.Deprecation = &v1alpha1.Deprecation{Date: "2027-01-01T00:00:00Z"}
	mustReject(t, k8s.Create(context.Background(), s), "spec.deprecation.date")
}

func TestClusterSchemaCannotReplaceNamespacedSchema(t *testing.T) {
	cs := &v1alpha1.ClusterSchema{
		ObjectMeta: metav1.ObjectMeta{Name: "replaces-namespaced"},
		Spec:       schemaSpec(subnetV1),
	}
	cs.Spec.Replaces = []v1alpha1.SchemaReference{{Kind: v1alpha1.KindSchema, Name: "subnet-v0"}}
	mustReject(t, k8s.Create(context.Background(), cs), "a ClusterSchema may only replace ClusterSchemas")
}

// Deleting a Schema and recreating it under the same name with a different
// contract must not silently rebind existing Records.
func TestRecordDetectsSchemaNameReuse(t *testing.T) {
	ns := namespace(t)
	s := newSchema(t, ns, "subnet", subnetV1)
	r := recordObj(ns, "bound", "subnet", subnetData)
	create(t, r)
	waitForCondition(t, r, recordConds, v1alpha1.ConditionValid, metav1.ConditionTrue, ReasonVerified)

	if err := k8s.Delete(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, r, recordConds, v1alpha1.ConditionValid, metav1.ConditionFalse, ReasonSchemaNotFound)

	// The data is also valid under subnetV2, so only the digest reveals the
	// contract changed.
	newSchema(t, ns, "subnet", subnetV2)
	r = waitForCondition(t, r, recordConds, v1alpha1.ConditionValid, metav1.ConditionFalse, ReasonSchemaDigestMismatch)
	if want := mustContractDigest(t, subnetV1); r.Status.Schema == nil || r.Status.Schema.Digest != want {
		t.Errorf("status.schema.digest should still identify the original contract %s, got %+v", want, r.Status.Schema)
	}
}
