# Example: Migrating from an EnvironmentConfig to a Record

Crossplane's [`EnvironmentConfig`](https://docs.crossplane.io/latest/composition/environment-configs/)
holds untyped data for Compositions to read. It is mutable, its `data` is
untyped, and no history survives an edit unless
`kubectl.kubernetes.io/last-applied-configuration` happens to be populated.

We start with a typical EnvironmentConfig:

```yaml
apiVersion: apiextensions.crossplane.io/v1beta1
kind: EnvironmentConfig
metadata:
  name: networks-dev
data:
  region: us-west-2
  vpcId: vpc-0a1b2c3d
```

`data` becomes a Record's `spec.data` unchanged. Its name becomes the
RecordSet's name: `networks-dev` is one instance, the dev environment, of a
type of thing the platform team publishes. That type is
`platform.example.org/network-config`, and a `networks-prod` RecordSet would
share it.

---

## Preconditions

- The CRDs and records-controller installed (`make deploy`, or `make kind-e2e`).
- The well-known `rawobject-v1` ClusterSchema present and `Ready`. The
  controller creates it at startup ([The Well-Known `rawObject`
  Contract](../records.md#the-well-known-rawobject-contract)), or you can install it manually.
- Namespace `platform` exists.

Let's confirm ClusterSchema `rawobject-v1` is installed and has a computed digest in `status.digest`:

```sh
kubectl get clusterschema rawobject-v1 -o jsonpath='{.status.digest}'
```

---

## Step 0: Create a Record with a Free-Form schema to match the EnvironmentConfig

A team with an EnvironmentConfig may not be ready to define an API, but can
start keeping immutable versions immediately. `rawobject-v1` accepts any JSON object.

**Apply:**

```yaml
apiVersion: records.crossplane.io/v1alpha1
kind: Record
metadata:
  name: networks-dev-1
  namespace: platform
spec:
  publisher:
    id: team-platform
  recordTypeGroup: platform.example.org
  recordType: network-config
  schema:
    ref:
      kind: ClusterSchema
      name: rawobject-v1
  data:
    region: us-west-2
    vpcId: vpc-0a1b2c3d
```

If we look at this record, we can see the record has computed digests for its data and the schema it is linked to.

```shell
$ kubectl get record -n platform networks-dev-1  -o wide 
 NAME             GROUP                  TYPE             SCHEMA         VALID   DATA DIGEST                                                               SCHEMA DIGEST                                                             AGE
networks-dev-1   platform.example.org   network-config   rawobject-v1   True    sha256:6d0dd1a6713678b9fb90b304e67785e539e62692b4d46f72ff94df3e2a02ae17   sha256:50832d9195f8288b6d8eaca1e891084c317e01d6008e3f476177ec2b1a4e4563   40m
```

Let's try to change the data in the Record. It should be blocked by the API server:

```shell
$ kubectl -n platform patch record networks-dev-1 --type=merge \
  -p '{"spec":{"data":{"region":"eu-west-1"}}}'

The Record "networks-dev-1" is invalid: spec: Invalid value: spec is immutable
```

Schemas are also immutable:

```shell
$ kubectl patch clusterschema networks-v1 --type=json \
  -p '[{"op":"replace","path":"/spec/definition","value":{"type":"object"}}]'
The ClusterSchema "networks-v1" is invalid: spec: Invalid value: spec.definition is immutable
```

When a `Record` is created the controller must resolve the reference, validate `data` against the
`rawObject` contract, compute the digest of `spec.data`, and record which
contract it validated against.

The Controller is not required, and for a more lightweight deployment the record can be deployed with the calculated `digests` in the spec. If a controller is running, it will validate the digest, otherwise the Consumer is expected to validate the data.

Here is the same Record with both digests asserted by the Publisher in the `spec`:

```yaml
apiVersion: records.crossplane.io/v1alpha1
kind: Record
metadata:
  name: networks-dev-1
  namespace: platform
spec:
  publisher:
    id: team-platform
  recordTypeGroup: platform.example.org
  recordType: network-config
  schema:
    ref:
      kind: ClusterSchema
      name: rawobject-v1
    # The digest of the contract the data was validated against.
    digest: sha256:50832d9195f8288b6d8eaca1e891084c317e01d6008e3f476177ec2b1a4e4563
  # The digest of spec.data.
  dataDigest: sha256:6d0dd1a6713678b9fb90b304e67785e539e62692b4d46f72ff94df3e2a02ae17
  data:
    region: us-west-2
    vpcId: vpc-0a1b2c3d
```

Each digest is SHA-256 over the RFC 8785 canonical JSON of what it covers. For
data this small, the canonical form can be written by hand: keys sorted, no
whitespace.

```shell
# spec.dataDigest covers spec.data.
$ printf '%s' '{"region":"us-west-2","vpcId":"vpc-0a1b2c3d"}' | shasum -a 256
6d0dd1a6713678b9fb90b304e67785e539e62692b4d46f72ff94df3e2a02ae17  -

# spec.schema.digest covers the contract: the Schema's format and definition.
$ printf '%s' '{"definition":{"type":"object","x-kubernetes-preserve-unknown-fields":true},"format":"StructuralSchema"}' | shasum -a 256
50832d9195f8288b6d8eaca1e891084c317e01d6008e3f476177ec2b1a4e4563  -
```

For anything larger, use an RFC 8785 library rather than writing the canonical
form by hand. Number formatting and key ordering of non-ASCII keys are easy to
get wrong.

With the records-controller running, the asserted digests are compared with the
computed ones. The signatures in the `spec` are verified and the data is validated against the Schema.

| Asserted | Expect |
|---|---|
| both digests correct | `Valid=True`, reason `Verified` |
| a wrong `dataDigest` | `Valid=False`, reason `DataDigestMismatch`: `asserted spec.dataDigest … does not match the computed digest sha256:6d0dd1a6…` |
| a wrong `schema.digest` | `Valid=False`, reason `SchemaDigestMismatch` |

The controller never corrects an asserted digest. Because `spec` is immutable,
digests must be asserted when the Record is created. Adding one to an existing
Record is rejected with `spec is immutable`.

### Creating a RecordSet

Then create the `RecordSet`. This is an optional step indicating the Publisher's preferences. Currently we need to manually set the fields:

```yaml
apiVersion: records.crossplane.io/v1alpha1
kind: RecordSet
metadata:
  name: networks-dev
  namespace: platform
spec:
  publisher:
    id: team-platform
  recordTypeGroup: platform.example.org
  recordType: network-config
  current: 1
  highestVersion: 1
  versions:
    - version: 1
      recordRef:
        name: networks-dev-1
      schemaRef:
        kind: ClusterSchema
        name: rawobject-v1
      publishedAt: "2026-10-01T14:03:00Z"
```

`highestVersion` is required whenever `versions` is set
([`highestVersion`](../records.md#highestversion)).

**Assert:** `Ready=True` with reason `Verified`, and `status.currentRecord` is
`networks-dev-1`.

Order does not matter. A RecordSet applied before its Record exists is
accepted, reports `Ready=False` with reason `InvalidVersions` and the message
`version 1: Record "networks-dev-1" not found`, and becomes `Ready` once the
Record is valid.

---

## Step 1: Define a Schema for the Network

The team is now ready to describe what it publishes and creates a `network` shape, which is similar to a Kubernetes CRD. However, a shape is immutable and can have multiple incompatible and compatible versions coexisting at the same time.

**Apply:**

```yaml
apiVersion: records.crossplane.io/v1alpha1
kind: ClusterSchema
metadata:
  name: networks-v1
spec:
  publisher:
    id: team-platform
  shapeGroup: platform.example.org
  shape: networks
  shapeVersion: v1
  format: StructuralSchema
  definition:
    type: object
    properties:
      region:
        type: string
        description: Cloud region the environment runs in
      vpcId:
        type: string
        pattern: "^vpc-[0-9a-f]{8}$"
        description: Identifier of the shared VPC
    required:
      - region
      - vpcId
```

`networks-v1` does not declare `replaces: rawobject-v1`, and Consumers would
not act on it if it did. `replaces` is a claim the successor makes about
someone else's Schema. If Consumers followed it, any Publisher could declare
`replaces: rawobject-v1` and redirect every Consumer migrating away from the
free-form contract to a Schema of their choosing. Consumers discover
successors through `successor` instead, which only the predecessor's
Publisher sets, and treat `replaces` as informational
([`successor`](../records.md#successor), [`replaces`](../records.md#replaces)).

Nothing names a successor for `rawobject-v1` either: every free-form Record
shares it, so it has no single successor. The migration is recorded in the
RecordSet instead: version 1 uses `rawobject-v1` and version 2 uses
`networks-v1`.

**Assert:**

| | Expect |
|---|---|
| `Ready` condition | `True`, reason `Verified` |
| `status.digest` | present |
| `status.structuralDigest` | present, and different from `status.digest`, because this definition carries descriptions |
| re-applying unchanged | neither digest moves |

The structural digest row is worth checking deliberately. A definition with no
documentation has equal digests, as `rawobject-v1` does, so a fixture that only
uses free-form contracts never catches a `structuralDigest` computed the same
way as `digest` ([Schema Digests](../records.md#schema-digests)).

---

## Step 2: Update the Record to use the new Schema

**Apply** a second Record of the same type, naming the new contract:

```yaml
apiVersion: records.crossplane.io/v1alpha1
kind: Record
metadata:
  name: networks-dev-2
  namespace: platform
spec:
  publisher:
    id: team-platform
  recordTypeGroup: platform.example.org   # unchanged
  recordType: network-config              # unchanged
  schema:
    ref:
      kind: ClusterSchema
      name: networks-v1                   # changed
  data:
    region: us-west-2
    vpcId: vpc-0a1b2c3d
```

Then publish it as version 2, raising `highestVersion` in the same update and
leaving version 1 in place:

```yaml
spec:
  current: 2
  highestVersion: 2
  versions:
    - version: 2
      recordRef:
        name: networks-dev-2
      schemaRef:
        kind: ClusterSchema
        name: networks-v1
      publishedAt: "2026-10-04T09:12:00Z"
    - version: 1
      recordRef:
        name: networks-dev-1
      schemaRef:
        kind: ClusterSchema
        name: rawobject-v1
      publishedAt: "2026-10-01T14:03:00Z"
```

**Assert:**

| | Expect |
|---|---|
| `networks-dev-2` `status.schema.digest` | equal to `networks-v1`'s digest, not `rawobject-v1`'s |
| `networks-dev-1` | still `Valid`, still bound to `rawobject-v1` |
| RecordSet | `Ready=True`, with entries referencing different Schemas |
| record type | `platform.example.org/network-config` on both Records and the RecordSet throughout |

The last row is the point of the exercise. The contract tightened, but the
kind of thing being published did not. Had the type lived on the Schema, this
migration would have changed it and every Consumer would have needed
re-pointing.

---

## Step 3: retraction

Suppose version 1's `vpcId` turns out to have been wrong.

```yaml
spec:
  retracted:
    - version: 1
      message: "wrong VPC, superseded by 2"
```

**Assert:** accepted, and version 1 is still in `versions` and its Record still
exists. Retracting is not deleting ([Retraction](../records.md#retraction)).

---

## Step 4: retention

The team decides to keep two versions. Setting `retention.maxVersions: 2` is
accepted, because two are listed. Then the VPC changes, so the team creates
`networks-dev-3` against `networks-v1` with `vpcId: vpc-0e5f6a7b`, and
publishes it.

The Publisher applies retention ([Retention](../records.md#retention)). In the
update that publishes version 3, it removes version 1, the oldest, along with
version 1's retraction:

```yaml
spec:
  current: 3
  highestVersion: 3
  retention:
    maxVersions: 2
  retracted: null
  versions:
    - version: 3
      recordRef:
        name: networks-dev-3
      schemaRef:
        kind: ClusterSchema
        name: networks-v1
      publishedAt: "2026-10-06T08:30:00Z"
    - version: 2
      # unchanged
```

**Assert:**

| Attempt | Expect |
|---|---|
| Publish version 3 and keep version 1 | rejected: `versions must not exceed retention.maxVersions` |
| Remove version 1 but keep its retraction | rejected: `retracted versions must be published versions` |
| Remove version 1 and its retraction | accepted; `Ready=True`, current 3, highest 3 |
| `networks-dev-1` afterwards | still exists. Removal from a RecordSet does not delete a Record |
| Re-add version 1 | rejected: `version numbers must not be reused` |

A reader can now tell three situations apart from the RecordSet alone:

| Version | Situation |
|---|---|
| listed in `versions` and in `retracted` | retracted: published, then withdrawn by the Publisher |
| not listed, and at most `highestVersion` | aged out under retention, or removed by the Publisher |
| above `highestVersion` | never published |

---

## Negative cases

These matter more than the happy path. Each is invisible when everything works.

Rejected by the API server:

| Attempt | Expect |
|---|---|
| Edit `spec.data` of a Record | `spec is immutable` ([Record Immutability](../records.md#record-immutability)) |
| Replace `spec.definition` of `networks-v1` | `spec.definition is immutable` |
| Edit `status.schema.digest` of a Record | `status.schema.digest is write-once` |
| Rewrite an existing `versions` entry | `published version entries are immutable` |
| Change the RecordSet's `recordType` or `recordTypeGroup` | `spec.recordType is immutable`, `spec.recordTypeGroup is immutable` |
| Lower `highestVersion` | `highestVersion must not decrease` |
| A namespaced Schema declaring `replaces` of `networks-v1`, a ClusterSchema | `a Schema may only replace Schemas in its own namespace` ([`replaces`](../records.md#replaces)) |
| A Schema with `format: Avro` | `Unsupported value: "Avro"`. `v1alpha1` permits only `StructuralSchema` ([`spec.format`](../records.md#specformat)) |

Reported by the controller, because they depend on other objects:

| Attempt | Expect |
|---|---|
| A Record with `vpcId: not-a-vpc` against `networks-v1` | `Valid=False`, reason `DataInvalid`, naming the `pattern`; `status.schema.digest` stays unset |
| A RecordSet entry naming a Record that does not exist | `Ready=False`, `InvalidVersions`: `Record "…" not found` |
| A RecordSet entry naming a Record that is not valid | `Ready=False`, `InvalidVersions`: `Record "…" is not valid` |
| A RecordSet entry naming a Record of type `something-else` | `Ready=False`, `InvalidVersions`: `Record type platform.example.org/something-else does not match RecordSet type platform.example.org/network-config` (invariant 9) |

And these must succeed, since over-strict immutability is as wrong as too
little:

| Attempt | Expect |
|---|---|
| Add `deprecation` to `networks-v1`, or name its `successor` | accepted. `successor`, `replaces`, and `deprecation` are the mutable part of a Schema |
| Move `current`, add to `retracted`, raise `retention.maxVersions` | accepted |

---

## What this does not cover

- **Replication between clusters.** Everything here is one cluster. The rules
  about asserted digests travelling with a Record, and a receiving side
  recomputing them, need a second cluster to exercise.
- **Consumer behavior.** Selection policies, caching and compatibility
  following belong to a Consumer mechanism, such as a Composition Function, and
  are not part of this model.
- **Deleting and recreating a RecordSet.** That resets `highestVersion`, and
  records.md forbids reusing version numbers this way, but nothing can detect
  it from the RecordSet alone.
- **Inline schemas, `phase`, and `expiresAt`.** Specified in records.md, but
  not implemented by records-controller.
- **Signing.** Not in `v1alpha1`.
