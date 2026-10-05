package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/stevendborrelli/records-controller/api/v1alpha1"
)

// These tests exercise the admission policies in config/policy as a user
// who is not a cluster administrator. They create with dry-run, so nothing
// persists while they wait for the policies and RBAC to take effect.

// authorRule lets a user create and read every Records kind.
var authorRule = rbacv1.PolicyRule{
	APIGroups: []string{v1alpha1.GroupVersion.Group},
	Resources: []string{"schemas", "clusterschemas", "records", "recordsets"},
	Verbs:     []string{"create", "get"},
}

// publishRule grants the right to publish under groups.
func publishRule(groups ...string) rbacv1.PolicyRule {
	return rbacv1.PolicyRule{
		APIGroups:     []string{v1alpha1.GroupVersion.Group},
		Resources:     []string{"groups"},
		ResourceNames: groups,
		Verbs:         []string{"publish"},
	}
}

// updateSchemasRule grants update on the named Schemas in every namespace.
func updateSchemasRule(names ...string) rbacv1.PolicyRule {
	return rbacv1.PolicyRule{
		APIGroups:     []string{v1alpha1.GroupVersion.Group},
		Resources:     []string{"schemas"},
		ResourceNames: names,
		Verbs:         []string{"update"},
	}
}

// tenant returns a client for a user who is not a cluster administrator,
// granted rules cluster-wide.
func tenant(t *testing.T, name string, rules ...rbacv1.PolicyRule) client.Client {
	t.Helper()
	u, err := testEnv.AddUser(envtest.User{Name: name}, nil)
	if err != nil {
		t.Fatalf("cannot add user %s: %v", name, err)
	}
	c, err := client.New(u.Config(), client.Options{Scheme: testScheme})
	if err != nil {
		t.Fatalf("cannot create client for %s: %v", name, err)
	}
	create(t, &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: name}, Rules: rules})
	create(t, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: name},
		Subjects:   []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: rbacv1.UserKind, Name: name}},
	})
	return c
}

// eventuallyRejected waits until fn is rejected with a message containing
// want.
func eventuallyRejected(t *testing.T, what, want string, fn func() error) {
	t.Helper()
	eventually(t, what+" to be rejected", func() error {
		err := fn()
		if err == nil {
			return fmt.Errorf("request was accepted")
		}
		if !strings.Contains(err.Error(), want) {
			return fmt.Errorf("rejected without %q: %v", want, err)
		}
		return nil
	})
}

func dryRunCreate(c client.Client, obj client.Object) func() error {
	return func() error { return c.Create(context.Background(), obj, client.DryRunAll) }
}

func TestPublishingUnderAGroupRequiresAuthorization(t *testing.T) {
	ns := namespace(t)
	c := tenant(t, "group-publisher", authorRule, publishRule("tenant.example.org"))

	schema := func(group v1alpha1.Group) *v1alpha1.Schema {
		s := &v1alpha1.Schema{ObjectMeta: metav1.ObjectMeta{Namespace: ns, GenerateName: "probe-"}, Spec: schemaSpec(subnetV1)}
		s.Spec.ShapeGroup = group
		return s
	}
	clusterSchema := func(group v1alpha1.Group) *v1alpha1.ClusterSchema {
		s := &v1alpha1.ClusterSchema{ObjectMeta: metav1.ObjectMeta{GenerateName: "probe-"}, Spec: schemaSpec(subnetV1)}
		s.Spec.ShapeGroup = group
		return s
	}
	record := func(group v1alpha1.Group) *v1alpha1.Record {
		r := recordObj(ns, "", "subnet-v1", subnetData)
		r.GenerateName = "probe-"
		r.Spec.RecordTypeGroup = group
		return r
	}
	recordSet := func(group v1alpha1.Group) *v1alpha1.RecordSet {
		return &v1alpha1.RecordSet{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, GenerateName: "probe-"},
			Spec: v1alpha1.RecordSetSpec{
				Publisher:       v1alpha1.Publisher{ID: "team-tenant"},
				RecordTypeGroup: group,
				RecordType:      "subnet",
			},
		}
	}

	eventuallyRejected(t, "a Schema in another team's shapeGroup", "not authorized to publish under shapeGroup network.example.org",
		dryRunCreate(c, schema("network.example.org")))
	eventuallyRejected(t, "a ClusterSchema in the reserved shapeGroup", "not authorized to publish under shapeGroup records.crossplane.io",
		dryRunCreate(c, clusterSchema("records.crossplane.io")))
	eventuallyRejected(t, "a Record in another team's recordTypeGroup", "not authorized to publish under recordTypeGroup network.example.org",
		dryRunCreate(c, record("network.example.org")))
	eventuallyRejected(t, "a RecordSet in another team's recordTypeGroup", "not authorized to publish under recordTypeGroup network.example.org",
		dryRunCreate(c, recordSet("network.example.org")))

	for what, obj := range map[string]client.Object{
		"a Schema":    schema("tenant.example.org"),
		"a Record":    record("tenant.example.org"),
		"a RecordSet": recordSet("tenant.example.org"),
	} {
		eventually(t, what+" in the tenant's own group to be accepted", dryRunCreate(c, obj))
	}
}

func TestReplacesRequiresAuthorization(t *testing.T) {
	ns := namespace(t)
	newSchema(t, ns, "tenant-v1", subnetV1)
	newSchema(t, ns, "platform-v1", subnetV1)
	c := tenant(t, "successor-claimant", authorRule, publishRule("tenant.example.org"), updateSchemasRule("tenant-v1", "claims"))

	claim := func(name string, replaces ...v1alpha1.SchemaReference) *v1alpha1.Schema {
		s := &v1alpha1.Schema{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}, Spec: schemaSpec(subnetV1)}
		s.Spec.ShapeGroup = "tenant.example.org"
		s.Spec.Replaces = replaces
		return s
	}
	ref := func(name string) v1alpha1.SchemaReference {
		return v1alpha1.SchemaReference{Kind: v1alpha1.KindSchema, Name: name}
	}

	eventuallyRejected(t, "claiming to replace a Schema the tenant cannot update", "replaces may name only Schemas the requester is authorized to update",
		dryRunCreate(c, claim("tenant-v2", ref("platform-v1"))))
	eventually(t, "claiming to replace the tenant's own Schema to be accepted",
		dryRunCreate(c, claim("tenant-v2", ref("tenant-v1"))))

	// The case from the EnvironmentConfig migration: claiming to succeed the
	// cluster-wide free-form contract.
	cs := &v1alpha1.ClusterSchema{ObjectMeta: metav1.ObjectMeta{Name: "tenant-networks-v1"}, Spec: schemaSpec(subnetV1)}
	cs.Spec.ShapeGroup = "tenant.example.org"
	cs.Spec.Replaces = []v1alpha1.SchemaReference{{Kind: v1alpha1.KindClusterSchema, Name: RawObjectName}}
	eventuallyRejected(t, "claiming to replace rawobject-v1", "replaces may name only Schemas the requester is authorized to update",
		dryRunCreate(c, cs))

	// Entries already present are not re-checked: the tenant can edit a
	// Schema whose replaces someone else set, but cannot add to it.
	create(t, claim("claims", ref("platform-v1")))
	edit := func(mutate func(*v1alpha1.Schema)) error {
		s := &v1alpha1.Schema{}
		if err := c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: "claims"}, s); err != nil {
			return err
		}
		mutate(s)
		return c.Update(context.Background(), s, client.DryRunAll)
	}
	eventually(t, "editing a Schema with an existing replaces to be accepted", func() error {
		return edit(func(s *v1alpha1.Schema) {
			s.Spec.Deprecation = &v1alpha1.Deprecation{Date: "2027-01-01"}
		})
	})
	eventuallyRejected(t, "adding an unauthorized replaces entry", "replaces may name only Schemas the requester is authorized to update", func() error {
		return edit(func(s *v1alpha1.Schema) { s.Spec.Replaces = append(s.Spec.Replaces, ref("platform-v2")) })
	})
}
