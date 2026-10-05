package controller

import (
	"context"
	"fmt"
	"path/filepath"
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
	role := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: name}, Rules: rules}
	binding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: name},
		Subjects:   []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: rbacv1.UserKind, Name: name}},
	}
	create(t, role)
	create(t, binding)
	// They are cluster-scoped, so remove them for the next run.
	t.Cleanup(func() {
		_ = k8s.Delete(context.Background(), binding)
		_ = k8s.Delete(context.Background(), role)
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

// The alpha installs permissive RBAC, so the policies block no one until it
// is removed. Removing it is the documented way to lock authorization down.
func TestPermissiveRBACIsTheAlphaDefault(t *testing.T) {
	ns := namespace(t)
	c := tenant(t, "alpha-user", authorRule) // No publish grants of its own.

	s := &v1alpha1.Schema{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "anything"}, Spec: schemaSpec(subnetV1)}
	eventuallyRejected(t, "publishing without grants while locked down", "not authorized to publish under shapeGroup network.example.org",
		dryRunCreate(c, s))

	permissive, err := createFromFile(filepath.Join(policyDir, permissiveFile))
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "publishing under permissive RBAC to be accepted", dryRunCreate(c, s))

	for _, o := range permissive {
		if err := k8s.Delete(context.Background(), o); err != nil {
			t.Fatal(err)
		}
	}
	eventuallyRejected(t, "publishing without grants after locking down", "not authorized to publish under shapeGroup network.example.org",
		dryRunCreate(c, s))
}
