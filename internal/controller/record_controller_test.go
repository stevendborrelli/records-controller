package controller

import (
	"context"
	"slices"
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

// subnetV1Documented is subnetV1 with documentation, including a property
// named description, which is data and is kept by the structural digest.
const subnetV1Documented = `{
  "type": "object",
  "title": "Subnet",
  "description": "A subnet published by the network team.",
  "required": ["cidr"],
  "properties": {
    "cidr": {"type": "string", "description": "IPv4 CIDR block.", "example": "10.21.0.0/16"},
    "location": {
      "type": "object",
      "properties": {"region": {"type": "string"}, "country": {"type": "string", "title": "Country"}}
    },
    "region": {"type": "string"},
    "zones": {"type": "array", "items": {"type": "string", "description": "An availability zone."}},
    "gateway": {"type": "string", "externalDocs": {"url": "https://example.org/gateways"}}
  }
}`

const subnetData = `{"cidr":"10.21.0.0/16","location":{"region":"NorthAmerica","country":"UnitedStates"},"region":"us-west-2","zones":["us-west-2a","us-west-2b","us-west-2c"],"gateway":"10.21.0.1"}`

func raw(s string) runtime.RawExtension { return runtime.RawExtension{Raw: []byte(s)} }

func schemaSpec(def string) v1alpha1.SchemaSpec {
	return v1alpha1.SchemaSpec{
		Publisher:    v1alpha1.Publisher{ID: "team-network"},
		ShapeGroup:   "network.example.org",
		Shape:        "subnet",
		ShapeVersion: "v1",
		Format:       v1alpha1.FormatStructuralSchema,
		Definition:   raw(def),
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
			Publisher:       v1alpha1.Publisher{ID: "team-network"},
			RecordTypeGroup: "network.example.org",
			RecordType:      "subnet",
			Schema:          v1alpha1.RecordSchema{Ref: v1alpha1.SchemaReference{Kind: v1alpha1.KindSchema, Name: schemaName}},
			Data:            raw(data),
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

func mustStructuralDigest(t *testing.T, def string) v1alpha1.Digest {
	t.Helper()
	d, err := StructuralDigest(schemaSpec(def))
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
	if want := mustStructuralDigest(t, subnetV1); s.Status.StructuralDigest != want {
		t.Errorf("status.structuralDigest: got %s, want %s", s.Status.StructuralDigest, want)
	}
}

// Schemas that differ only in documentation have different digests but the
// same structural digest.
func TestSchemaStructuralDigestIgnoresDocumentation(t *testing.T) {
	ns := namespace(t)
	plain := newSchema(t, ns, "plain", subnetV1)
	documented := newSchema(t, ns, "documented", subnetV1Documented)
	plain = waitForCondition(t, plain, schemaConds, v1alpha1.ConditionReady, metav1.ConditionTrue, ReasonVerified)
	documented = waitForCondition(t, documented, schemaConds, v1alpha1.ConditionReady, metav1.ConditionTrue, ReasonVerified)

	if plain.Status.Digest == documented.Status.Digest {
		t.Errorf("status.digest should differ when documentation differs, both are %s", plain.Status.Digest)
	}
	if plain.Status.StructuralDigest != documented.Status.StructuralDigest {
		t.Errorf("status.structuralDigest should not depend on documentation: %s and %s", plain.Status.StructuralDigest, documented.Status.StructuralDigest)
	}
}

func TestSchemaStatusDigestsAreWriteOnce(t *testing.T) {
	ns := namespace(t)
	s := newSchema(t, ns, "write-once", subnetV1)
	s = waitForCondition(t, s, schemaConds, v1alpha1.ConditionReady, metav1.ConditionTrue, ReasonVerified)

	t.Run("Digest", func(t *testing.T) {
		err := updateStatus(s.DeepCopy(), func(u *v1alpha1.Schema) { u.Status.Digest = bogusDigest })
		mustReject(t, err, "status.digest is write-once")
	})
	t.Run("StructuralDigest", func(t *testing.T) {
		err := updateStatus(s.DeepCopy(), func(u *v1alpha1.Schema) { u.Status.StructuralDigest = "" })
		mustReject(t, err, "status.structuralDigest is write-once")
	})
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
		err := update(r.DeepCopy(), func(u *v1alpha1.Record) { u.Spec.RecordType = "region" })
		mustReject(t, err, "spec is immutable")
	})
	t.Run("Data", func(t *testing.T) {
		err := update(r.DeepCopy(), func(u *v1alpha1.Record) { u.Spec.Data = raw(`{"cidr":"10.99.0.0/16"}`) })
		mustReject(t, err, "spec is immutable")
	})
	t.Run("RecordTypeGroup", func(t *testing.T) {
		err := update(r.DeepCopy(), func(u *v1alpha1.Record) { u.Spec.RecordTypeGroup = "storage.example.org" })
		mustReject(t, err, "spec is immutable")
	})
}

func TestRecordTypeGroupFormat(t *testing.T) {
	ns := namespace(t)
	for _, group := range []string{"", "Network.Example.org", "network_example.org"} {
		r := recordObj(ns, "", "subnet-v1", subnetData)
		r.GenerateName = "bad-group-"
		r.Spec.RecordTypeGroup = v1alpha1.Group(group)
		mustReject(t, k8s.Create(context.Background(), r), "spec.recordTypeGroup")
	}
}

// Two Publishers' subnet Records are distinct types, and a field selector
// separates them.
func TestRecordTypeSelectableFields(t *testing.T) {
	ns := namespace(t)
	newSchema(t, ns, "subnet-v1", subnetV1)
	network := recordObj(ns, "network-subnet", "subnet-v1", subnetData)
	storage := recordObj(ns, "storage-subnet", "subnet-v1", subnetData)
	storage.Spec.RecordTypeGroup = "storage.example.org"
	create(t, network)
	create(t, storage)

	l := &v1alpha1.RecordList{}
	fields := client.MatchingFields{"spec.recordTypeGroup": "network.example.org", "spec.recordType": "subnet"}
	if err := k8s.List(context.Background(), l, client.InNamespace(ns), fields); err != nil {
		t.Fatalf("cannot list Records by %v: %v", fields, err)
	}
	if len(l.Items) != 1 || l.Items[0].Name != "network-subnet" {
		var got []string
		for _, r := range l.Items {
			got = append(got, r.Name)
		}
		t.Errorf("Records matching %v: got %v, want [network-subnet]", fields, got)
	}
}

func TestRecordStatusDigestsAreWriteOnce(t *testing.T) {
	ns := namespace(t)
	newSchema(t, ns, "subnet-v1", subnetV1)
	r := recordObj(ns, "write-once", "subnet-v1", subnetData)
	create(t, r)
	r = waitForCondition(t, r, recordConds, v1alpha1.ConditionValid, metav1.ConditionTrue, ReasonVerified)

	t.Run("DataDigest", func(t *testing.T) {
		err := updateStatus(r.DeepCopy(), func(u *v1alpha1.Record) { u.Status.DataDigest = bogusDigest })
		mustReject(t, err, "status.dataDigest is write-once")
	})
	t.Run("SchemaDigest", func(t *testing.T) {
		err := updateStatus(r.DeepCopy(), func(u *v1alpha1.Record) { u.Status.Schema = nil })
		mustReject(t, err, "status.schema.digest is write-once")
	})
}

func TestSchemaContractIsImmutable(t *testing.T) {
	ns := namespace(t)
	s := newSchema(t, ns, "subnet-v1", subnetV1)
	s = waitForCondition(t, s, schemaConds, v1alpha1.ConditionReady, metav1.ConditionTrue, ReasonVerified)

	t.Run("Definition", func(t *testing.T) {
		err := update(s.DeepCopy(), func(u *v1alpha1.Schema) { u.Spec.Definition = raw(subnetV2) })
		mustReject(t, err, "spec.definition is immutable")
	})
	t.Run("Publisher", func(t *testing.T) {
		err := update(s.DeepCopy(), func(u *v1alpha1.Schema) { u.Spec.Publisher.ID = "someone-else" })
		mustReject(t, err, "spec.publisher is immutable")
	})
	t.Run("ShapeGroup", func(t *testing.T) {
		err := update(s.DeepCopy(), func(u *v1alpha1.Schema) { u.Spec.ShapeGroup = "storage.example.org" })
		mustReject(t, err, "spec.shapeGroup is immutable")
	})
	t.Run("Shape", func(t *testing.T) {
		err := update(s.DeepCopy(), func(u *v1alpha1.Schema) { u.Spec.Shape = "vpc" })
		mustReject(t, err, "spec.shape is immutable")
	})
	t.Run("ShapeVersion", func(t *testing.T) {
		err := update(s.DeepCopy(), func(u *v1alpha1.Schema) { u.Spec.ShapeVersion = "v2" })
		mustReject(t, err, "spec.shapeVersion is immutable")
	})
	t.Run("DeprecationIsMutable", func(t *testing.T) {
		err := update(s.DeepCopy(), func(u *v1alpha1.Schema) {
			u.Spec.Deprecation = &v1alpha1.Deprecation{Date: "2027-01-01", Message: "superseded by subnet-v2"}
		})
		if err != nil {
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

func TestSchemaShapeGroupFormat(t *testing.T) {
	ns := namespace(t)
	for _, group := range []string{"", "Network.Example.org", "network_example.org", "-network.example.org"} {
		s := &v1alpha1.Schema{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, GenerateName: "bad-group-"},
			Spec:       schemaSpec(subnetV1),
		}
		s.Spec.ShapeGroup = v1alpha1.Group(group)
		mustReject(t, k8s.Create(context.Background(), s), "spec.shapeGroup")
	}
}

// A lineage is a shapeGroup and shape, and a field selector can list it.
func TestSchemaLineageSelectableFields(t *testing.T) {
	ns := namespace(t)
	for name, lineage := range map[string][3]string{
		"network-subnet-v1": {"network.example.org", "subnet", "v1"},
		"network-subnet-v2": {"network.example.org", "subnet", "v2"},
		"network-vpc-v1":    {"network.example.org", "vpc", "v1"},
		"storage-subnet-v1": {"storage.example.org", "subnet", "v1"},
	} {
		s := &v1alpha1.Schema{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec:       schemaSpec(subnetV1),
		}
		s.Spec.ShapeGroup, s.Spec.Shape, s.Spec.ShapeVersion = v1alpha1.Group(lineage[0]), lineage[1], lineage[2]
		create(t, s)
	}

	names := func(fields client.MatchingFields) []string {
		t.Helper()
		l := &v1alpha1.SchemaList{}
		if err := k8s.List(context.Background(), l, client.InNamespace(ns), fields); err != nil {
			t.Fatalf("cannot list Schemas by %v: %v", fields, err)
		}
		var got []string
		for _, s := range l.Items {
			got = append(got, s.Name)
		}
		slices.Sort(got)
		return got
	}
	for _, tc := range []struct {
		fields client.MatchingFields
		want   []string
	}{
		{client.MatchingFields{"spec.shapeGroup": "network.example.org", "spec.shape": "subnet"}, []string{"network-subnet-v1", "network-subnet-v2"}},
		{client.MatchingFields{"spec.shapeGroup": "network.example.org", "spec.shape": "subnet", "spec.shapeVersion": "v1"}, []string{"network-subnet-v1"}},
		{client.MatchingFields{"spec.shape": "subnet"}, []string{"network-subnet-v1", "network-subnet-v2", "storage-subnet-v1"}},
	} {
		if got := names(tc.fields); !slices.Equal(got, tc.want) {
			t.Errorf("Schemas matching %v: got %v, want %v", tc.fields, got, tc.want)
		}
	}
}

// The lineage is Publisher metadata, not contract: identical definitions in
// different lineages have the same digest.
func TestContractDigestIgnoresLineage(t *testing.T) {
	network := schemaSpec(subnetV1)
	storage := schemaSpec(subnetV1)
	storage.ShapeGroup, storage.ShapeVersion = "storage.example.org", "v3"

	a, err := ContractDigest(network)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ContractDigest(storage)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("digests differ across lineages: %s and %s", a, b)
	}
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
