package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stevendborrelli/records-controller/api/v1alpha1"
)

// publishedRecords creates subnet-v1 and subnet-v2 Schemas and valid Records
// prod-west-2 (v1), prod-west-3 (v1), prod-west-4 (v2), and prod-west-5 (v1).
func publishedRecords(t *testing.T, ns string) {
	t.Helper()
	newSchema(t, ns, "subnet-v1", subnetV1)
	newSchema(t, ns, "subnet-v2", subnetV2)
	for name, schema := range map[string]string{
		"prod-west-2": "subnet-v1",
		"prod-west-3": "subnet-v1",
		"prod-west-4": "subnet-v2",
		"prod-west-5": "subnet-v1",
	} {
		r := recordObj(ns, name, schema, subnetData)
		create(t, r)
		waitForCondition(t, r, recordConds, v1alpha1.ConditionValid, metav1.ConditionTrue, ReasonVerified)
	}
}

func version(t *testing.T, v int64, record, schema, def string) v1alpha1.RecordSetVersion {
	return v1alpha1.RecordSetVersion{
		Version:   v,
		RecordRef: v1alpha1.RecordReference{Name: record, DataDigest: mustDigest(t, subnetData)},
		SchemaRef: v1alpha1.VersionSchemaReference{
			SchemaReference:  v1alpha1.SchemaReference{Kind: v1alpha1.KindSchema, Name: schema},
			Digest:           mustContractDigest(t, def),
			StructuralDigest: mustStructuralDigest(t, def),
		},
	}
}

// exampleSet mirrors the RecordSet example in the proposal: versions
// published against both Schemas in any order, current pointing below the
// newest version, and a retracted version.
func exampleSet(t *testing.T, ns string) *v1alpha1.RecordSet {
	return &v1alpha1.RecordSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "prod-west"},
		Spec: v1alpha1.RecordSetSpec{
			Publisher:  v1alpha1.Publisher{ID: "team-network"},
			RecordType:     "subnet",
			Current:        ptr.To[int64](4),
			HighestVersion: ptr.To[int64](5),
			Retracted:      []v1alpha1.Retraction{{Version: 2, Message: "bad CIDR, superseded by 3"}},
			Versions: []v1alpha1.RecordSetVersion{
				version(t, 5, "prod-west-5", "subnet-v1", subnetV1),
				version(t, 4, "prod-west-4", "subnet-v2", subnetV2),
				version(t, 3, "prod-west-3", "subnet-v1", subnetV1),
				version(t, 2, "prod-west-2", "subnet-v1", subnetV1),
			},
		},
	}
}

func TestRecordSetReady(t *testing.T) {
	ns := namespace(t)
	publishedRecords(t, ns)
	rs := exampleSet(t, ns)
	create(t, rs)

	rs = waitForCondition(t, rs, setConds, v1alpha1.ConditionReady, metav1.ConditionTrue, ReasonVerified)
	if rs.Status.CurrentRecord != "prod-west-4" {
		t.Errorf("status.currentRecord: got %q, want prod-west-4", rs.Status.CurrentRecord)
	}
}

func TestRecordSetBecomesReadyWhenRecordsAreValid(t *testing.T) {
	ns := namespace(t)
	rs := &v1alpha1.RecordSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "early"},
		Spec: v1alpha1.RecordSetSpec{
			Publisher:  v1alpha1.Publisher{ID: "team-network"},
			RecordType:     "subnet",
			Current:        ptr.To[int64](1),
			HighestVersion: ptr.To[int64](1),
			Versions:       []v1alpha1.RecordSetVersion{version(t, 1, "prod-west-1", "subnet-v1", subnetV1)},
		},
	}
	create(t, rs)
	waitForCondition(t, rs, setConds, v1alpha1.ConditionReady, metav1.ConditionFalse, ReasonInvalidVersions)

	newSchema(t, ns, "subnet-v1", subnetV1)
	create(t, recordObj(ns, "prod-west-1", "subnet-v1", subnetData))
	waitForCondition(t, rs, setConds, v1alpha1.ConditionReady, metav1.ConditionTrue, ReasonVerified)
}

func TestRecordSetAdmission(t *testing.T) {
	ns := namespace(t)

	t.Run("CurrentMustBePublished", func(t *testing.T) {
		rs := exampleSet(t, ns)
		rs.Name = "current-unpublished"
		rs.Spec.Current = ptr.To[int64](9)
		mustReject(t, k8s.Create(context.Background(), rs), "current must identify a published version")
	})
	t.Run("CurrentMustNotBeRetracted", func(t *testing.T) {
		rs := exampleSet(t, ns)
		rs.Name = "current-retracted"
		rs.Spec.Current = ptr.To[int64](2)
		mustReject(t, k8s.Create(context.Background(), rs), "current must not identify a retracted version")
	})
	t.Run("RetractedMustBePublished", func(t *testing.T) {
		rs := exampleSet(t, ns)
		rs.Name = "retracted-unpublished"
		rs.Spec.Retracted = []v1alpha1.Retraction{{Version: 1}}
		mustReject(t, k8s.Create(context.Background(), rs), "retracted versions must be published versions")
	})
	t.Run("VersionsAreUnique", func(t *testing.T) {
		rs := exampleSet(t, ns)
		rs.Name = "duplicate"
		rs.Spec.Versions = append(rs.Spec.Versions, version(t, 3, "prod-west-3", "subnet-v1", subnetV1))
		mustReject(t, k8s.Create(context.Background(), rs), "Duplicate value")
	})
	t.Run("HighestVersionIsRequired", func(t *testing.T) {
		rs := exampleSet(t, ns)
		rs.Name = "no-highest"
		rs.Spec.HighestVersion = nil
		mustReject(t, k8s.Create(context.Background(), rs), "highestVersion must be set and at least every published version")
	})
	t.Run("HighestVersionCoversVersions", func(t *testing.T) {
		rs := exampleSet(t, ns)
		rs.Name = "highest-too-low"
		rs.Spec.HighestVersion = ptr.To[int64](4)
		mustReject(t, k8s.Create(context.Background(), rs), "highestVersion must be set and at least every published version")
	})
	t.Run("VersionsArePositive", func(t *testing.T) {
		rs := exampleSet(t, ns)
		rs.Name = "zero"
		rs.Spec.Versions = append(rs.Spec.Versions, version(t, 0, "prod-west-0", "subnet-v1", subnetV1))
		mustReject(t, k8s.Create(context.Background(), rs), "should be greater than or equal to 1")
	})

	rs := exampleSet(t, ns)
	create(t, rs)

	t.Run("NewVersionsMustBeHigher", func(t *testing.T) {
		err := update(rs.DeepCopy(), func(u *v1alpha1.RecordSet) {
			u.Spec.Versions = append(u.Spec.Versions, version(t, 1, "prod-west-1", "subnet-v1", subnetV1))
		})
		mustReject(t, err, "new versions must be greater than every existing version")
	})
	t.Run("PublishedEntriesAreImmutable", func(t *testing.T) {
		err := update(rs.DeepCopy(), func(u *v1alpha1.RecordSet) {
			u.Spec.Versions[1].RecordRef.Name = "prod-west-5"
		})
		mustReject(t, err, "published version entries are immutable")
	})
	t.Run("PublisherIsImmutable", func(t *testing.T) {
		err := update(rs.DeepCopy(), func(u *v1alpha1.RecordSet) {
			u.Spec.Publisher.ID = "someone-else"
		})
		mustReject(t, err, "spec.publisher is immutable")
	})
	t.Run("HighestVersionMustNotDecrease", func(t *testing.T) {
		err := update(rs.DeepCopy(), func(u *v1alpha1.RecordSet) {
			u.Spec.Versions = u.Spec.Versions[1:]
			u.Spec.HighestVersion = ptr.To[int64](4)
		})
		mustReject(t, err, "highestVersion must not decrease")
	})
	t.Run("PublishWithoutRaisingHighestVersion", func(t *testing.T) {
		err := update(rs.DeepCopy(), func(u *v1alpha1.RecordSet) {
			u.Spec.Versions = append([]v1alpha1.RecordSetVersion{version(t, 6, "prod-west-6", "subnet-v1", subnetV1)}, u.Spec.Versions...)
		})
		mustReject(t, err, "highestVersion must be set and at least every published version")
	})
	t.Run("PublishNewVersionAndRetract", func(t *testing.T) {
		err := update(rs.DeepCopy(), func(u *v1alpha1.RecordSet) {
			u.Spec.Versions = append([]v1alpha1.RecordSetVersion{version(t, 6, "prod-west-6", "subnet-v1", subnetV1)}, u.Spec.Versions...)
			u.Spec.HighestVersion = ptr.To[int64](6)
			u.Spec.Retracted = append(u.Spec.Retracted, v1alpha1.Retraction{Version: 5})
			u.Spec.Current = ptr.To[int64](6)
		})
		if err != nil {
			t.Fatalf("publishing a new version and retracting an old one should be allowed: %v", err)
		}
	})
}

// highestVersion keeps a removed version's number from being reused.
func TestRecordSetVersionsAreNeverReused(t *testing.T) {
	ns := namespace(t)
	rs := exampleSet(t, ns)
	create(t, rs)

	// Retention removes versions 2 and 3.
	err := update(rs, func(u *v1alpha1.RecordSet) {
		u.Spec.Retracted = nil
		u.Spec.Versions = u.Spec.Versions[:2]
	})
	if err != nil {
		t.Fatalf("removing old versions should be allowed: %v", err)
	}

	err = update(rs, func(u *v1alpha1.RecordSet) {
		u.Spec.Versions = append(u.Spec.Versions, version(t, 3, "something-else", "subnet-v1", subnetV1))
	})
	mustReject(t, err, "version numbers must not be reused")

	// Even with every version removed, highestVersion remembers 5.
	err = update(rs, func(u *v1alpha1.RecordSet) {
		u.Spec.Versions, u.Spec.Current = nil, nil
	})
	if err != nil {
		t.Fatalf("removing every version should be allowed: %v", err)
	}
	err = update(rs, func(u *v1alpha1.RecordSet) {
		u.Spec.Versions = []v1alpha1.RecordSetVersion{version(t, 4, "something-else", "subnet-v1", subnetV1)}
	})
	mustReject(t, err, "version numbers must not be reused")

	err = update(rs, func(u *v1alpha1.RecordSet) {
		u.Spec.Versions = []v1alpha1.RecordSetVersion{version(t, 6, "prod-west-6", "subnet-v1", subnetV1)}
		u.Spec.HighestVersion = ptr.To[int64](6)
	})
	if err != nil {
		t.Fatalf("publishing above highestVersion should be allowed: %v", err)
	}

	// Deleting the RecordSet discards highestVersion with it.
	if err := k8s.Delete(context.Background(), rs); err != nil {
		t.Fatal(err)
	}
	eventually(t, "RecordSet deletion", func() error {
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(rs), &v1alpha1.RecordSet{}); !kerrors.IsNotFound(err) {
			return fmt.Errorf("RecordSet still exists: %v", err)
		}
		return nil
	})
	again := exampleSet(t, ns)
	if err := k8s.Create(context.Background(), again); err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
	t.Log("KNOWN GAP: a RecordSet deleted and recreated under the same name can reuse its version numbers")
}

// The Publisher applies retention, and the API server enforces the limit.
func TestRecordSetRetention(t *testing.T) {
	ns := namespace(t)
	rs := exampleSet(t, ns)
	rs.Spec.Retention = &v1alpha1.Retention{MaxVersions: 4}
	create(t, rs)

	t.Run("PublishingMustNotExceedMaxVersions", func(t *testing.T) {
		err := update(rs.DeepCopy(), func(u *v1alpha1.RecordSet) {
			u.Spec.Versions = append([]v1alpha1.RecordSetVersion{version(t, 6, "prod-west-6", "subnet-v1", subnetV1)}, u.Spec.Versions...)
			u.Spec.HighestVersion = ptr.To[int64](6)
		})
		mustReject(t, err, "versions must not exceed retention.maxVersions")
	})
	t.Run("LoweringMaxVersionsRequiresRemoval", func(t *testing.T) {
		err := update(rs.DeepCopy(), func(u *v1alpha1.RecordSet) { u.Spec.Retention.MaxVersions = 2 })
		mustReject(t, err, "versions must not exceed retention.maxVersions")
	})
	t.Run("PublishAndRemoveTheOldest", func(t *testing.T) {
		// Version 2 is the oldest, and retracted, so its retraction goes too.
		err := update(rs.DeepCopy(), func(u *v1alpha1.RecordSet) {
			v := u.Spec.Versions
			u.Spec.Versions = append([]v1alpha1.RecordSetVersion{version(t, 6, "prod-west-6", "subnet-v1", subnetV1)}, v[:len(v)-1]...)
			u.Spec.HighestVersion = ptr.To[int64](6)
			u.Spec.Retracted = nil
		})
		if err != nil {
			t.Fatalf("publishing and removing the oldest version should be allowed: %v", err)
		}
	})
}

func TestRecordSetVerification(t *testing.T) {
	ns := namespace(t)
	publishedRecords(t, ns)

	other := recordObj(ns, "other-publisher", "subnet-v1", subnetData)
	other.Spec.Publisher.ID = "team-storage"
	create(t, other)
	waitForCondition(t, other, recordConds, v1alpha1.ConditionValid, metav1.ConditionTrue, ReasonVerified)

	cases := map[string]struct {
		mutate func(rs *v1alpha1.RecordSet)
		want   string
	}{
		"Unordered": {
			mutate: func(rs *v1alpha1.RecordSet) {
				v := rs.Spec.Versions
				v[0], v[1] = v[1], v[0]
			},
			want: "versions must be ordered from highest to lowest",
		},
		"MissingRecord": {
			mutate: func(rs *v1alpha1.RecordSet) { rs.Spec.Versions[0].RecordRef.Name = "does-not-exist" },
			want:   `version 5: Record "does-not-exist" not found`,
		},
		"PublisherMismatch": {
			mutate: func(rs *v1alpha1.RecordSet) { rs.Spec.Versions[0].RecordRef.Name = "other-publisher" },
			want:   `version 5: Record publisher "team-storage" does not match`,
		},
		"DataDigestMismatch": {
			mutate: func(rs *v1alpha1.RecordSet) { rs.Spec.Versions[0].RecordRef.DataDigest = bogusDigest },
			want:   "version 5: recordRef.dataDigest",
		},
		"SchemaDigestMismatch": {
			mutate: func(rs *v1alpha1.RecordSet) { rs.Spec.Versions[1].SchemaRef.Digest = bogusDigest },
			want:   "version 4: schemaRef.digest",
		},
		"StructuralDigestMismatch": {
			mutate: func(rs *v1alpha1.RecordSet) { rs.Spec.Versions[1].SchemaRef.StructuralDigest = bogusDigest },
			want:   "version 4: schemaRef.structuralDigest",
		},
		"SchemaRefMismatch": {
			mutate: func(rs *v1alpha1.RecordSet) {
				rs.Spec.Versions[1].SchemaRef.Name = "subnet-v1"
				rs.Spec.Versions[1].SchemaRef.Digest = ""
			},
			want: `version 4: schemaRef Schema "subnet-v1" does not match`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rs := exampleSet(t, ns)
			rs.Name = strings.ToLower(name)
			tc.mutate(rs)
			create(t, rs)
			rs = waitForCondition(t, rs, setConds, v1alpha1.ConditionReady, metav1.ConditionFalse, ReasonInvalidVersions)
			if msg := rs.Status.Conditions[0].Message; !strings.Contains(msg, tc.want) {
				t.Errorf("Ready message: want it to contain %q, got %q", tc.want, msg)
			}
		})
	}
}
