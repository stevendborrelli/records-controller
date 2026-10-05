# An Introduction to Records

**Author:** Steven Borrelli (@stevendborrelli)

This proposal introduces three Kubernetes-compatible data types: `Record`, `Schema`, and `RecordSet`. These types are designed for sharing data across distributed systems in a manner resilient to breaking API changes and other failure modes.

A `Record` is an immutable, schema-bound snapshot of data. A `Schema` defines the contract that snapshot conforms to. A `RecordSet` is the Publisher-owned index that describes which immutable snapshots are available and which one is current.

## Background

Crossplane is a Kubernetes extension that manages resources outside a cluster environment and is primarily used to manage cloud infrastructure.

Running on Kubernetes provides significant advantages for Crossplane, but challenges emerge as the platform grows within an organization. infrastructure teams often operate independently with limited coordination, in contrast to application teams who have well-defined methods for sharing data.

More subtle issues become clear as the infrastructure platform develops over time:

- Kubernetes Custom Resource Definitions (CRDs) make it difficult to support multiple API versions containing breaking changes. Every API version must be round-trip compatible with every other version.
- Kubernetes Objects are mutable, and a durable history of previous versions is generally not retained.
- Kubernetes is designed to immediately converge to a desired state. Updating an Object like an EnvironmentConfig can trigger a cascading change.

This proposal addresses part of these problems: a data structure that allows disparate teams to publish and consume data according to emerging patterns in Platform Engineering.

This proposal is intentionally limited to the data model. Data transport, authentication, authorization, and topology are separate concerns and may have multiple future implementations.

## Design Principles

The model is based on a distinction between immutable facts and mutable Publisher assertions.

A `Record` contains immutable facts:

- the data being published;
- the Schema contract against which the data was validated;
- the identity of the Publisher; and
- cryptographic digests that identify the data and contract.

A `Schema` contains an immutable contract plus mutable Publisher metadata describing that contract:

- the lineage and compatibility group it belongs to;
- the contracts it replaces; and
- its deprecation state.

A `RecordSet` contains mutable Publisher metadata describing a collection of immutable Records:

- which versions are available;
- which version is current;
- which versions have been retracted; and
- how long the Publisher intends to maintain the set.

Publisher assertions do not modify the immutable data or contract they describe. Consumers may choose whether and how to act on those assertions.

## Core Concepts

The primary use of Records is sharing data between Publishers and Consumers.

A Publisher could be a network team that publishes global topology or a policy team sharing cloud regions that support sovereign requirements. Consumers read Records and use the data in their environments. For example, a storage team might create a database using a subnet provisioned by the network team.

Events are generally not the primary abstraction for infrastructure provisioning because convergence to a desired state and reporting current state are more important than reacting to an individual event.

Records differ from events in several important ways:

- each Record is a complete representation of state;
- each Record is immutable;
- each Record is bound to a Schema;
- Records are independently addressable;
- multiple versions may coexist; and
- Records can be distributed without requiring a pub/sub framework.

Multiple Records representing the same logical resource may exist simultaneously. A Consumer can therefore choose how it follows published data: it may pin to a historical version, follow the current version, or select the newest version satisfying a schema compatibility requirement.

## Record

A `Record` is similar to a Kubernetes Object in that it contains data and references a contract under which the data was published. The primary difference is that a Record is immutable.

A Publisher MUST create a new Record when the data changes.

Unlike a mutable Kubernetes Object, whose identity is generally defined by its name and namespace, multiple Records representing the same logical resource can coexist. Each immutable Record is a point-in-time snapshot of that resource.

`metadata.name` identifies the immutable Record object. It does not define the identity of the underlying resource represented by `spec.data`. Publishers may encode resource identity or version information in the name, but Consumers MUST NOT infer semantic versioning rules from the name.

```yaml
apiVersion: records.crossplane.io/v1alpha1
kind: Record
metadata:
  name: prod-west-3
  namespace: platform
  labels:
    recordType: subnet
    version: "3"
spec:
  publisher:
    id: team-network
  recordType: subnet
  schema:
    ref:
      kind: Schema
      name: subnet-v1
    digest: sha256:38ddd8f8…
  dataDigest: sha256:c69a1339…
  data:
    cidr: "10.21.0.0/16"
    location:
      region: NorthAmerica
      country: UnitedStates
    region: us-west-2
    zones:
      - us-west-2a
      - us-west-2b
      - us-west-2c
    gateway: "10.21.0.1"
status:
  dataDigest: sha256:c69a1339…
  schema:
    digest: sha256:38ddd8f8…
```

Every field under `spec` is immutable. On Kubernetes-compatible implementations this SHOULD be enforced using CEL or an equivalent mechanism.

`spec.data` is a JSON object represented through YAML or JSON serialization.

`spec.schema` MUST identify a Schema, either through a reference or an inline definition. A Record MUST validate against the identified Schema.

`recordType` identifies the kind of thing represented by the Record, such as `subnet`, `supported-eks-versions`, or `cloud-region`. It is independent of the Schema's `shape`.

### Record Identity

A Record's Kubernetes identity is independent of the logical resource represented by its data.

A Record is uniquely identified by its Kubernetes object identity:

```text
namespace + metadata.name
```

A version number, if used by a RecordSet, is separate from Kubernetes object identity.

Record names MUST NOT be reused for different Record contents. A Publisher that needs to publish changed data MUST create a new Record with a different name.

### Record Immutability

Once created, the following properties of a Record MUST NOT change:

- `spec.publisher`;
- `spec.recordType`;
- `spec.schema`, including `spec.schema.digest`;
- `spec.data`;
- `spec.dataDigest`; and
- the identity of the Record.

The `status` subresource is controller-owned and is not generally immutable. However, certain status fields are **write-once**:

- `status.dataDigest`; and
- `status.schema.digest`.

A write-once field may initially be absent and later be populated by the controller, but once established its value MUST NOT change. If the Publisher asserted the corresponding digest in `spec`, the write-once digest MUST equal it.

## Inline Schemas

A Record may carry its contract inline instead of referencing a Schema object.

The inline block consists of exactly the contract fields of a Schema's `spec`, excluding mutable Publisher metadata, so the inline contract and a separately stored Schema cannot drift.

```yaml
spec:
  schema:
    digest: sha256:38ddd8f8…
    inline:
      format: StructuralSchema
      definition:
        type: object
        properties:
          cidr:
            type: string
          location:
            type: object
            properties:
              region:
                type: string
              country:
                type: string
          region:
            type: string
          zones:
            type: array
            items:
              type: string
          gateway:
            type: string
```

Inline Schemas are useful for one-off Records that do not share a Schema with other versions.

The only supported `format` in `v1alpha1` is `StructuralSchema`.

An inline Schema's digest MUST be computed using the same canonicalization and digest rules used by a standalone Schema, and may be asserted in `spec.schema.digest` in the same way as a referenced Schema's digest.

## Computed Digests

Digests identify immutable content.

An implementation computes a Record's digests when the Record is created and records them in `status`.

A Publisher MAY also assert either or both digests in `spec`:

- `spec.dataDigest`, identifying the Record's data; and
- `spec.schema.digest`, identifying the contract the data was validated against.

Asserted digests are optional. When one is present, the implementation compares it with the value it computed. A Record whose computed digest differs from its asserted digest is invalid, and the implementation MUST NOT correct the asserted value. Because `spec` is immutable, an implementation cannot add an asserted digest after the Record is created.

Asserted digests are part of the immutable `spec`, so they travel with the Record wherever it is copied, distributed, or restored, even when `status` does not. An implementation that receives a Record without `status` recomputes the digests. If the Record has asserted digests, it compares the results with them. Without asserted digests, a receiving implementation can identify the data it received but cannot confirm that it matches what the Publisher originally published.

A Schema's own digests are derived entirely from its immutable `spec`, so they can always be recomputed and are stored only in `status`.

### Record Data Digest

`spec.dataDigest` and `status.dataDigest` cover `spec.data`.

Record data MUST be canonicalized using the JSON Canonicalization Scheme defined by RFC 8785 before the digest is calculated.

Therefore, equivalent JSON representations produce the same digest.

```text
Record.spec.dataDigest
optional Publisher-asserted hash of canonicalized spec.data

Record.status.dataDigest
hash of canonicalized spec.data, computed by the implementation
```

### Schema Digests

A Schema has two digests:

```text
Schema.status.digest
hash of the canonicalized complete definition

Schema.status.structuralDigest
hash of the canonicalized definition after removing
documentation-only keywords
```

The exact canonicalization process MUST be deterministic and identical across implementations.

For `StructuralSchema`, the following documentation-only fields are removed before calculating `structuralDigest`:

- `description`;
- `title`;
- `example`; and
- `externalDocs`.

Implementations MUST NOT remove additional fields unless a future version of this specification explicitly defines them as documentation-only.

These keywords are removed only where they are keywords: from the root schema, and from every schema nested under `properties`, `patternProperties`, `additionalProperties`, `items`, `additionalItems`, `allOf`, `anyOf`, `oneOf`, `not`, `definitions`, or `dependencies`. Anywhere else the same names are data and MUST be kept. For example, a property named `description` under `properties`, a `title` key inside a `default` or `enum` value, and the fields of an `x-kubernetes-validations` rule are all part of the structure.

The complete definition is used for `digest` because documentation can carry semantic meaning to humans. For example, changing a description from "size in GB" to "size in MB" does not change the structural validation rules but can materially change the meaning of the data.

The documentation fields are excluded from `structuralDigest` because Consumers do not generally parse them when validating or consuming data.

## Binding a Record to a Schema

`spec.schema.ref` identifies a Schema object.

When a Record is validated, the implementation:

1. resolves the Schema reference;
2. obtains the immutable Schema contract;
3. validates `spec.data` against that contract;
4. calculates the Schema's contract digest and, if `spec.schema.digest` is present, verifies that it matches; and
5. records the verified digest in `status.schema.digest`.

A namespaced `Record` MUST reference only a `Schema` in its own namespace or a `ClusterSchema`. Cross-namespace references are not permitted.

A Record is bound to the immutable contract identified by the Schema's digest, not to mutable metadata associated with the Schema.

Changes to a Schema's `replaces` or `deprecation` fields therefore do not change the contract to which an existing Record is bound.

A Record MUST NOT be considered valid until both of its digests have been computed and recorded in `status`, and any asserted digests have been verified. If a Schema reference resolves to a contract whose digest does not match an asserted `spec.schema.digest`, the Record is invalid.

## Sensitive Data

The properties that make Records useful for sharing data also make them unsuitable for most sensitive data.

Records may be cached by Consumers, and the API does not provide mechanisms such as revocation, rotation, or redaction.

Sensitive credentials SHOULD NOT be stored directly in a Record.

Instead, a Record may contain a reference to the location of a Secret. This provides a secret discovery mechanism while keeping sensitive data outside the Record and allowing existing Secret infrastructure to manage its lifecycle.

The mechanisms for authenticating and authorizing access to the referenced Secret are outside the scope of this proposal. The Record merely provides the location; the Consumer is responsible for securely fetching it.

## Schema

A `Schema` describes the shape and validation rules of data. It is similar to a Kubernetes CRD schema, but it is not itself served as a CRD representing arbitrary user data.

Multiple Schema objects may describe the same shape.

```yaml
apiVersion: records.crossplane.io/v1alpha1
kind: Schema
metadata:
  name: subnet-v1
  namespace: platform
spec:
  publisher:
    id: team-network
  shapeGroup: network.example.org
  shape: subnet
  shapeVersion: v1
  format: StructuralSchema
  definition:
    type: object
    properties:
      cidr:
        type: string
      location:
        type: object
        properties:
          region:
            type: string
          country:
            type: string
      region:
        type: string
      zones:
        type: array
        items:
          type: string
      gateway:
        type: string
  replaces:
    - kind: Schema
      name: subnet-v0
  deprecation:
    date: "2027-01-01"
    message: "superseded by subnet-v2, which adds a required gateway"
```

The following properties of a Schema are immutable:

- `spec.publisher`;
- `spec.shapeGroup`;
- `spec.shape`;
- `spec.shapeVersion`;
- `spec.format`; and
- `spec.definition`.

The following properties are mutable Publisher assertions:

- `spec.replaces`; and
- `spec.deprecation`.

Changing an immutable contract requires creating a new Schema.

### `shapeGroup`, `shape`, and `shapeVersion`

A Schema names the contract lineage it belongs to using three fields, modeled on a Kubernetes API's group, kind, and version:

| Field | Identifies | Kubernetes analogue |
| --- | --- | --- |
| `shapeGroup` | The owner of the lineage, such as `network.example.org` | API group |
| `shape` | The form of data within that group, such as `subnet`, `rawObject`, or `keyValue` | Kind |
| `shapeVersion` | A Publisher-defined compatibility group within the lineage | API version |

A lineage is identified by `shapeGroup` and `shape` together. Two Publishers may each define a `subnet` shape: `network.example.org/subnet` and `storage.example.org/subnet` are different lineages.

`shapeGroup` is required and MUST be a lowercase RFC 1123 DNS subdomain. The `records.crossplane.io` group is reserved for contracts defined by this specification. As with `publisher.id`, a `shapeGroup` is a claim, not proof that the Publisher controls that domain.

Unlike a Kubernetes API version, which has exactly one schema, a `shapeVersion` may contain several Schemas. Each is an immutable revision identified by its digest, such as a revision that adds an optional field.

Schemas sharing the same `shapeGroup`, `shape`, and `shapeVersion` are asserted by the Publisher to satisfy the same compatibility contract.

This assertion is not mechanically verified by the Records model.

The lineage fields are therefore metadata about the Publisher's compatibility model. They do not establish structural equivalence, and they are not inputs to a Schema's digests: two Schemas with identical definitions in different lineages have the same `digest`.

A `shapeVersion` MUST NOT be interpreted as proof that two Schemas have identical definitions.

On Kubernetes-compatible implementations, `shapeGroup`, `shape`, and `shapeVersion` SHOULD be declared as selectable fields, so that a lineage can be listed with a field selector:

```sh
kubectl get schemas --field-selector spec.shapeGroup=network.example.org,spec.shape=subnet
```

`recordType` remains separate from `shape`.

A single `records.crossplane.io/rawObject/v1` Schema, for example, may serve Records with `recordType` values such as `subnet`, `region`, and `policy`.

### `spec.format`

`definition` is expressed in a schema language. `format` identifies that language.

In `v1alpha1`, the only permitted value is `StructuralSchema`.

Implementations MUST reject unknown formats when validating a Record.

`format` is immutable. Re-expressing a contract using another schema language creates a new contract and therefore a new Schema identity.

A Consumer that does not implement a Schema's `format` MUST NOT claim that a Record conforms to that Schema.

The Consumer may still inspect the Record's data, Record type, and digest.

## Schema Equivalence and Compatibility

Schema relationships have different strengths.

| Relationship | Meaning | Established by |
| --- | --- | --- |
| `digest` | Same canonical contract | Computation |
| `structuralDigest` | Same validation structure, differing only in documentation | Computation |
| `shapeGroup` + `shape` + `shapeVersion` | Publisher asserts membership in the same compatibility group | Publisher |
| `replaces` | Publisher identifies another Schema as a predecessor | Publisher |

The first two relationships are mechanically verifiable.

The latter two are Publisher assertions and are not verified by this specification.

### `replaces`

`replaces` is a directed relationship.

If Schema B declares:

```yaml
replaces:
  - kind: Schema
    name: schema-a
```

then B is declaring that it is the intended successor to A.

`replaces` does not, by itself, establish that data valid under A is valid under B, nor that a Consumer can automatically migrate between them.

A Consumer may use `replaces` as a migration hint, but MUST determine independently whether it can consume the successor Schema.

A Schema may replace a Schema belonging to a different `shapeVersion`. This supports migrations where the representation or compatibility model changes.

### Deprecation

Deprecating a contract means updating the Schema to add a `deprecation` block.

The absence of the block means that the Publisher has not declared the Schema deprecated.

```yaml
deprecation:
  date: "2027-01-01"
  message: "superseded by subnet-v2, which adds a required gateway"
```

`date` is required when `deprecation` is present. `message` is optional.

`date` MUST be an RFC 3339 `full-date` (`YYYY-MM-DD`), such as `"2027-01-01"`. It is interpreted as a calendar date in UTC. A timestamp, or a date in any other format, MUST be rejected. On Kubernetes-compatible implementations, this SHOULD be enforced using `type: string` with `format: date` in the OpenAPI schema.

The date identifies when the Publisher intends to stop publishing the contract.

It is not a deadline imposed on Consumers and does not invalidate existing Records.

A Consumer may continue using a deprecated Schema if doing so is appropriate for its environment.

There is no machine-readable successor field in `deprecation`. Consumers that want to discover a successor may inspect the `replaces` relationship declared by newer Schemas.

Deprecation is intentionally not copied into a RecordSet. A Schema may be deprecated while a RecordSet continues to publish Records using that Schema.

## `ClusterSchema`

`ClusterSchema` is identical to `Schema` except that it is cluster-scoped.

It exists because a contract is often defined once and referenced from multiple namespaces.

A namespaced object may reference either:

- a namespaced `Schema` in the same namespace; or
- a `ClusterSchema`.

A cluster-scoped object MUST NOT reference a namespaced `Schema`.

This prevents a cluster-scoped object from depending on content owned by an individual namespace.

Schema references use the following form:

```yaml
kind: Schema
name: subnet-v1
```

A reference to a `Schema` has no `namespace` field. It always resolves to a `Schema` in the referencing object's own namespace.

A reference to a `ClusterSchema` uses the same form with a different `kind`:

```yaml
kind: ClusterSchema
name: subnet-v1
```

## The Well-Known `rawObject` Contract

A Record MUST always identify a Schema.

There is no schemaless Record. Instead, the free-form case is represented by a well-known Schema that accepts arbitrary JSON objects.

Every implementation MUST provide the following `ClusterSchema`:

```yaml
apiVersion: records.crossplane.io/v1alpha1
kind: ClusterSchema
metadata:
  name: rawobject-v1
spec:
  publisher:
    id: system
  shapeGroup: records.crossplane.io
  shape: rawObject
  shapeVersion: v1
  format: StructuralSchema
  definition:
    type: object
    x-kubernetes-preserve-unknown-fields: true
```

The contract accepts arbitrary JSON objects.

If arbitrary JSON values, including arrays, strings, numbers, booleans, or null, are required in a future version, a separate contract MUST be defined rather than silently changing this contract.

The distinction between an explicit `rawObject` contract and no contract is intentional.

Referencing `rawobject-v1` says:

> I am deliberately publishing data without a more specific schema.

An absent Schema reference could instead indicate an invalid or incomplete Record.

## RecordSet

A `RecordSet` is the mutable Publisher-owned index over a collection of immutable Records.

It describes:

- which versions have been published;
- which version is currently recommended;
- which versions have been retracted; and
- the Publisher's retention and maintenance intentions.

The RecordSet does not contain the Record data itself. Each version entry instead carries the digests of its Record's data and contract, so Consumers can verify a resolved Record against the Publisher's index and filter versions by contract without resolving every Record.

```yaml
apiVersion: records.crossplane.io/v1alpha1
kind: RecordSet
metadata:
  name: prod-west
  namespace: platform
  labels:
    recordType: subnet
spec:
  publisher:
    id: team-network
  recordType: subnet
  phase: Active
  # The Publisher recommends version 4, even though version 5 is newer.
  current: 4
  # The highest version ever published. It never decreases, so removed
  # versions cannot be published again.
  highestVersion: 5
  expiresAt: "2027-01-01T00:00:00Z"
  # The RecordSet lists at most four versions.
  retention:
    maxVersions: 4
  # Version 2 was retracted. It remains in `versions` and stays addressable,
  # but Consumers following normal selection rules MUST NOT select it.
  retracted:
    - version: 2
      message: "bad CIDR, superseded by 3"
  # Version 1 aged out of the RecordSet under `retention.maxVersions`.
  # Its version number is never reused, but the Record is no longer listed.
  versions:
    # Published after the subnet-v2 Record, but against the older subnet-v1
    # contract, for Consumers that have not migrated yet.
    - version: 5
      recordRef:
        name: prod-west-5
        dataDigest: sha256:4d8e1f07…
      schemaRef:
        kind: Schema
        name: subnet-v1
        digest: sha256:38ddd8f8…
        structuralDigest: sha256:5f1c2a90…
      publishedAt: "2026-10-04T15:02:41Z"
    - version: 4
      recordRef:
        name: prod-west-4
        dataDigest: sha256:a12c9e55…
      schemaRef:
        kind: Schema
        name: subnet-v2
        digest: sha256:9b0e47d2…
        structuralDigest: sha256:e3a7b614…
      publishedAt: "2026-10-03T18:45:10Z"
    - version: 3
      recordRef:
        name: prod-west-3
        dataDigest: sha256:c69a1339…
      schemaRef:
        kind: Schema
        name: subnet-v1
        digest: sha256:38ddd8f8…
        structuralDigest: sha256:5f1c2a90…
      publishedAt: "2026-10-02T20:26:23Z"
    - version: 2
      recordRef:
        name: prod-west-2
        dataDigest: sha256:7f30b2c8…
      schemaRef:
        kind: Schema
        name: subnet-v1
        digest: sha256:38ddd8f8…
        structuralDigest: sha256:5f1c2a90…
      publishedAt: "2026-10-02T20:16:11Z"
```

### RecordSet Invariants

The following rules apply:

1. A RecordSet version MUST be a positive integer.
2. A version number MUST NOT be reused, including after the version has been removed from `versions`.
3. Versions MUST increase monotonically.
4. Gaps between versions may exist.
5. Each version MUST appear at most once in `versions`.
6. `versions` MUST be ordered from newest to oldest.
7. `current`, when present, MUST identify a published version.
8. `current` MUST NOT identify a retracted version.
9. Every Record referenced by a version MUST have the same `publisher` and `recordType` as the RecordSet.
10. A version entry's `recordRef.dataDigest` and `schemaRef.digest` MUST equal the referenced Record's `status.dataDigest` and `status.schema.digest`, and `schemaRef.structuralDigest` MUST equal the referenced Schema's `structuralDigest`.
11. A Record may exist without being referenced by a RecordSet.
12. A RecordSet MUST NOT modify a Record it references; Records are immutable as described in [Record Immutability](#record-immutability).
13. `highestVersion` MUST be present when `versions` is not empty, MUST be at least every version in `versions`, and MUST NOT decrease.
14. A version added to `versions` MUST be greater than the previous `highestVersion`.
15. When `retention.maxVersions` is present, `versions` MUST NOT list more than `retention.maxVersions` versions.

On Kubernetes-compatible implementations, all of these except 9 and 10 can be enforced by the API server with CEL, and SHOULD be. Invariants 9 and 10 depend on other objects and are verified by a controller.

### `current`

`current` identifies the version that the Publisher currently recommends as the default for Consumers that have not selected a version explicitly.

`current` does not mean:

- the newest version by creation timestamp;
- the most recently created Record;
- highest version number in all circumstances; or
- a version that every Consumer is required to adopt.

A Consumer that follows `current` is explicitly choosing to follow Publisher intent.

### Retraction

A Publisher may retract a previously published version.

Retraction is a mutable Publisher assertion and does not modify the Record.

A retracted Record:

- remains immutable;
- remains addressable;
- remains identifiable by its digest;
- MUST NOT be selected by Consumers following normal RecordSet selection rules; and
- may continue to be used by Consumers that have explicitly pinned to it.

Retraction does not delete the Record and does not invalidate the Record's Schema.

A retracted version MUST NOT become `current`.

### `highestVersion`

`highestVersion` is the highest version number the RecordSet has ever published.

It exists because removing a version, for example under retention, leaves nothing in `versions` to compare a new version against. Without it, a Publisher could remove version 5 and later publish different data as version 5, and a Consumer pinned to version 5 would silently receive it.

The Publisher raises `highestVersion` in the same update that publishes a higher version. Invariants 13 and 14 then make reuse detectable from that single update, without consulting any other object or any controller.

`highestVersion` is part of the RecordSet, so it does not survive the RecordSet's deletion. A Publisher MUST NOT delete and recreate a RecordSet to reuse its version numbers. Implementations cannot detect this from the RecordSet alone.

### Retention

`retention.maxVersions` is the most versions the RecordSet may list.

The Publisher applies retention. When publishing a version would exceed the limit, the Publisher removes the oldest versions in the same update, together with any retraction of a removed version, and moves `current` if it identified one.

An implementation MUST reject a RecordSet that lists more than `retention.maxVersions` versions, and MUST NOT remove versions itself. This keeps the Publisher the only writer of a RecordSet's `spec`: a Publisher that re-applies its full list of versions would otherwise restore versions the implementation removed, and the update would be rejected because those version numbers are below `highestVersion`.

Retention is not a statement that older Records cease to exist immediately.

Whether the underlying Record remains accessible after removal from the RecordSet is an implementation and storage concern.

Because a Record can exist independently of any RecordSet, removal from a RecordSet does not delete it. Implementations SHOULD provide a mechanism to reap Records that are no longer referenced, and Consumers that pin to an unreferenced Record do so at their own risk.

### `expiresAt`

`expiresAt` expresses the Publisher's intention to stop actively maintaining the RecordSet after the specified time.

It does not invalidate existing Records.

Consumers MUST NOT interpret `expiresAt` as an expiration time for Record data unless a future specification explicitly defines such behavior.

### RecordSet and Schema Deprecation

Schema deprecation and Record retraction are intentionally separate.

A Schema may be deprecated while a RecordSet continues publishing Records using that Schema.

Likewise, an individual Record may be retracted while its Schema remains current.

This allows Consumers to reason separately about:

- whether a contract is still supported;
- whether a particular Record should be selected; and
- which version a Publisher currently recommends.

## Consumer Selection

A Consumer SHOULD make Record selection explicit.

For example, a Consumer that follows the current compatible Record may use the following process:

1. Locate the desired RecordSet.
2. Verify the expected Publisher identity and `recordType`.
3. Read the available versions.
4. Exclude retracted versions unless explicitly configured otherwise.
5. Select `current` or another version according to its policy.
6. Resolve the referenced Record.
7. Verify that the Record's data hashes to the version entry's `recordRef.dataDigest`.
8. Resolve or reconstruct the immutable Schema contract.
9. Verify that the contract's digest matches the version entry's `schemaRef.digest`.
10. Validate the Record data against the Schema.
11. Consume the resulting immutable snapshot.

A Consumer that requires a specific contract may instead filter available versions by Schema digest or structural digest, using the digests in each version entry without resolving the Records, or by Publisher-defined compatibility group.

The Records model does not require Consumers to follow `current`, `shapeVersion`, `replaces`, or `deprecation`. These are Publisher signals, not commands.

### Integration with Crossplane Compositions

> **Note:** This section describes an example implementation. It is not part of the Records data model and will be moved out of this document in the future.

Although the selection process requires complex logic, Consumers are not expected to implement it themselves.

The primary intended Consumer mechanism for the Records model is a **Crossplane Composition Function** (e.g., `function-records`).

By encapsulating the selection, digest verification, and Schema validation logic inside a reusable Crossplane Function, Platform Engineers can securely consume published data with minimal boilerplate. A Composition step can simply declare its intent:

```yaml
- step: get-network-topology
  functionRef:
    name: function-records
  input:
    apiVersion: records.fn.crossplane.io/v1alpha1
    kind: RecordSelector
    target:
      recordSet: prod-west
      namespace: platform
    selectionPolicy: FollowCurrent
    # To pin to a specific version instead:
    # selectionPolicy: PinVersion
    # version: 3
```

The Function handles the mechanical verification of the immutable contract. If a Record is newly published and its controller has not yet verified its digests, the Function can safely yield, allowing Crossplane's standard eventual consistency model to retry until the Record is fully valid.

## Publisher Identity

`publisher.id` identifies the logical Publisher responsible for a Record, Schema, or RecordSet.

Authentication and authorization mechanisms that establish control over that identity are outside the scope of this proposal.

An implementation MUST NOT assume that the string contained in `publisher.id` is itself proof of Publisher authority.

For example, the following:

```yaml
publisher:
  id: team-network
```

identifies the claimed Publisher but does not establish that the object was actually created by the network team.

## Summary

The Records model separates immutable data from mutable Publisher intent.

```text
Schema
immutable contract
      │
      ▼
Record
immutable snapshot
      │
      ▼
RecordSet
mutable view over published snapshots
```

The resulting model provides:

- immutable point-in-time data;
- explicit schema contracts;
- independently addressable historical versions;
- deterministic data and contract identity;
- Publisher-defined compatibility groups;
- explicit migration and deprecation signals; and
- Consumer-controlled version selection.

The core guarantee is intentionally narrow:

> A `Record` never changes what it is.

A Consumer may choose to follow a Publisher's current recommendation, remain pinned to a historical Record, or apply its own compatibility policy. None of those choices changes the immutable Record or the contract against which it was validated.

Transport, authentication, authorization, caching, replication, and topology remain separate concerns and can evolve independently of the data model.
