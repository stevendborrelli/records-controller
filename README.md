# records-controller

A prototype Kubernetes controller for the Records proposal (`records.md`). It
implements a deliberately trimmed `v1alpha1` to test whether the core model
holds up:

- `Schema` and `ClusterSchema`: an immutable contract (`format` and
  `definition`) in a named lineage (`shapeGroup`, `shape`, `shapeVersion`),
  plus mutable `replaces` and `deprecation`, with a contract digest and a
  structural digest.
- `Record`: an immutable, schema-bound snapshot with optional
  Publisher-asserted digests.
- `RecordSet`: a mutable index with `current`, retraction, and per-version
  data, contract, and structural digests.

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
| A ClusterSchema replaces only ClusterSchemas | CRD CEL rule |
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

The contract digest covers `format` and `definition` only; the proposal does
not yet define the digest inputs.

## Installing and testing on kind

See [docs/kind.md](docs/kind.md). The quickest path is:

```sh
make kind-e2e
```

which creates a kind cluster, deploys the controller, and runs the end-to-end
checks in `hack/kind-e2e.sh`.

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
