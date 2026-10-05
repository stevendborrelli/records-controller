# records-controller

A prototype Kubernetes controller for the Records proposal (`records.md`). It
implements a deliberately trimmed `v1alpha1` to test whether the core model
holds up:

- `Schema` and `ClusterSchema`: an immutable contract (`format` and
  `definition`) in a named lineage (`shapeGroup`, `shape`, `shapeVersion`),
  plus mutable `successor` and `deprecation`, with a contract digest and a
  structural digest.
- `Record`: an immutable, schema-bound snapshot with optional
  Publisher-asserted digests.
- `RecordSet`: a mutable index with `current`, retraction, and per-version
  data, contract, and structural digests.
- `Subscription`: a Consumer's choice of version from a RecordSet in its own
  namespace (`Current`, `LatestCompatible`, or `Pinned`, optionally limited to
  a contract it can read), with the selected version's verified data in its
  status. See [docs/subscriptions.md](docs/subscriptions.md).

API group: `records.crossplane.io/v1alpha1`.

Not implemented: inline schemas, `expiresAt`, and `phase`.

## What enforces what

| Rule | Enforced by |
| --- | --- |
| Record `spec` is immutable | CRD CEL rule (`self == oldSelf`) |
| Schema `publisher`, lineage, `format`, `definition` are immutable | CRD CEL rules |
| `shapeGroup` and `recordTypeGroup` are lowercase DNS subdomains | CRD pattern |
| Schemas can be listed by lineage (`kubectl get schemas --field-selector spec.shapeGroup=…,spec.shape=…`), and Records and RecordSets by type (`spec.recordTypeGroup`, `spec.recordType`) | CRD `selectableFields` |
| RecordSet `publisher`, `recordTypeGroup`, `recordType` are immutable | CRD CEL rules |
| `status` digests are write-once | CRD CEL transition rules |
| Digests are well formed (`sha256:` + 64 hex) | CRD pattern |
| `deprecation.date` is an RFC 3339 full-date | CRD `format: date` |
| A ClusterSchema's `successor` is a ClusterSchema | CRD CEL rule |
| Publishing under a `shapeGroup` or `recordTypeGroup` requires the `publish` verb on `records.crossplane.io` `groups` with that name | ValidatingAdmissionPolicy and RBAC; open to everyone by default (see [Authorization](#authorization)) |
| Versions are unique and positive | CRD list-map keys and minimum |
| `current` is published and not retracted; retracted versions are published | CRD CEL rules |
| New versions are higher than existing ones; published entries are immutable | CRD CEL transition rules |
| Version numbers are never reused, even after removal (`highestVersion` covers every version, never decreases, and new versions exceed it) | CRD CEL rules and transition rules |
| `versions` lists at most `retention.maxVersions` entries; the Publisher removes old ones | CRD CEL rule |
| Schema references resolve only in the Record's namespace, or to a ClusterSchema | API shape (no `namespace` field) and controller |
| Contract and data digests (SHA-256 over RFC 8785 JSON) | Schema and Record controllers |
| Structural digest strips documentation keywords only at keyword positions | Schema controller (`internal/structural`) |
| Asserted digests match computed digests | Record controller |
| Data validates against the Schema; unknown fields are rejected, not pruned | Record controller |
| A Schema name reused for a different contract invalidates bound Records | Record controller |
| The well-known `rawobject-v1` ClusterSchema exists, and is recreated if deleted; a conflicting one is reported, not replaced | Controller at startup, and the ClusterSchema controller |
| Versions ordered high to low; Records exist, match publisher, `recordTypeGroup`, `recordType`, schema, and digests, including structural digests | RecordSet controller |
| A Subscription serves only a verified version, and keeps its last one when the followed version is incompatible or unavailable | Subscription controller |
| A Subscription follows only a RecordSet in its own namespace | API shape (no `namespace` field) |
| `version` only with `Pinned`; `compatibleWith` required for `LatestCompatible` | CRD CEL rules |

The contract digest covers `format` and `definition` only; the proposal does
not yet define the digest inputs.

## Authorization

`shapeGroup` and `recordTypeGroup` are claims about names that may belong to
someone else. `make deploy` installs two ValidatingAdmissionPolicies, in
`config/policy`, that check each claim against RBAC when an object is created:

| Creating | Requires |
| --- | --- |
| a Schema or ClusterSchema with `shapeGroup: G` | verb `publish` on `groups` named `G` in `records.crossplane.io` |
| a Record or RecordSet with `recordTypeGroup: G` | verb `publish` on `groups` named `G` in `records.crossplane.io` |

`groups` is not a served resource, and `publish` is not a standard verb. RBAC
rules name them anyway, as `certificates.k8s.io` does for `signers`.

### The alpha default is open

For the alpha, `make deploy` also installs `config/policy/permissive.yaml`: a
ClusterRole and ClusterRoleBinding named `records-permissive` that grant
`publish` on every group to `system:authenticated`. The policies run, but
block no one.

The controller's own grant, `publish` on the reserved `records.crossplane.io`
group, is separate and survives locking down.

### Locking down

1. Grant each team the groups it owns. For example, to let the network team's
   service account publish under `network.example.org`:

   ```yaml
   apiVersion: rbac.authorization.k8s.io/v1
   kind: ClusterRole
   metadata:
     name: records-publish-network-example-org
   rules:
     - apiGroups: ["records.crossplane.io"]
       resources: ["groups"]
       resourceNames: ["network.example.org"]
       verbs: ["publish"]
   ---
   apiVersion: rbac.authorization.k8s.io/v1
   kind: ClusterRoleBinding
   metadata:
     name: records-publish-network-example-org
   roleRef:
     apiGroup: rbac.authorization.k8s.io
     kind: ClusterRole
     name: records-publish-network-example-org
   subjects:
     - kind: ServiceAccount
       name: publisher
       namespace: team-network
   ```

   Do not grant `publish` on `records.crossplane.io`; it is reserved for the
   controller.

2. Remove the open grant, and stop redeploys from restoring it:

   ```sh
   kubectl delete clusterrolebinding records-permissive
   kubectl delete clusterrole records-permissive
   ```

   Then remove `permissive.yaml` from `config/policy/kustomization.yaml`.

3. Check a grant with a SubjectAccessReview. `kubectl auth can-i` cannot check
   `groups`, because kubectl only resolves resources the API server serves.

   ```sh
   kubectl create -o jsonpath='{.status.allowed}' -f - <<EOF
   apiVersion: authorization.k8s.io/v1
   kind: SubjectAccessReview
   spec:
     user: system:serviceaccount:team-network:publisher
     groups: ["system:serviceaccounts", "system:authenticated"]
     resourceAttributes:
       group: records.crossplane.io
       resource: groups
       name: network.example.org
       verb: publish
   EOF
   ```

Cluster administrators in `system:masters` pass every check, locked down or
not. Authorization applies where an object is written: it does not travel
with an object copied to another cluster.

## Installing and testing on kind

See [docs/kind.md](docs/kind.md). The quickest path is:

```sh
make kind-e2e
```

which creates a kind cluster, deploys the controller, and runs the end-to-end
checks in `hack/kind-e2e.sh`.

[docs/subscriptions.md](docs/subscriptions.md) explains how Consumers follow
a RecordSet.

[docs/environmentconfig-migration.md](docs/environmentconfig-migration.md)
walks through migrating a Crossplane EnvironmentConfig to Records, step by
step, as an acceptance scenario for the controller.

## Development

```sh
go build ./...
make test-unit   # digest and validation packages
make test        # also runs envtest integration tests
make generate    # regenerate deepcopy code, CRDs, and RBAC
```

The integration tests start a real `kube-apiserver` and `etcd` with envtest.
They use the newest binaries under setup-envtest's default directory, or
`KUBEBUILDER_ASSETS` if it is set.

`make generate` expects controller-gen v0.20.1 at `bin/controller-gen`, or set
`CONTROLLER_GEN`. Install it with:

```sh
GOBIN=$PWD/bin go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.20.1
```

`go.mod` lists more dependencies than the module uses; run `go mod tidy` to
trim it.
