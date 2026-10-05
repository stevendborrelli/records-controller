# Installing and testing with kind

This guide installs records-controller on a local [kind](https://kind.sigs.k8s.io/)
cluster and walks through the behaviour described in the Records proposal.

You can run everything in one step with the end-to-end script, or follow the
manual steps to see each behaviour for yourself.

## Prerequisites

- Go 1.26 or later
- kubectl 1.30 or later
- kind 0.20 or later
- A container runtime that kind supports: Docker (Docker Desktop, Colima, or
  OrbStack) or Podman

> **macOS note:** kind does not support Apple's `container` tool. If `docker`
> on your machine is a shim for `container`, `kind create cluster` fails with
> `Plugin 'container-ps' not found`. Use Docker Desktop, Colima, or OrbStack,
> or Podman with `export KIND_EXPERIMENTAL_PROVIDER=podman`.

Clone the repository:

```sh
git clone https://github.com/stevendborrelli/records-controller.git
cd records-controller
```

## Quick start: the end-to-end script

```sh
make kind-e2e
```

This runs `hack/kind-e2e.sh`, which:

1. creates a kind cluster named `records`, or reuses an existing one;
2. builds the controller image and loads it into the cluster;
3. deploys the CRDs, RBAC, and controller into `records-system`;
4. runs about 30 checks in the `records-e2e` namespace and prints `PASS` or
   `FAIL` for each; and
5. exits non-zero if any check fails.

The cluster is kept afterwards so you can inspect it. Options are set with
environment variables:

| Variable | Default | Effect |
| --- | --- | --- |
| `CLUSTER` | `records` | kind cluster name |
| `IMAGE` | `records-controller:dev` | controller image tag |
| `SKIP_BUILD` | `0` | `1` reuses an already-loaded image |
| `DELETE_CLUSTER` | `0` | `1` deletes the cluster at the end |

For example, `DELETE_CLUSTER=1 make kind-e2e` runs the checks and cleans up.

### Reading the results

Most checks have a single expected outcome. Two are probes of an open
question: whether the API server's `self == oldSelf` rules can protect fields
that hold arbitrary JSON (Record `spec.data` and Schema `spec.definition`).

- If the API server rejects the change, the check passes.
- If it accepts the change, the check fails, and the script then verifies the
  controller's fallback: the object is marked invalid with
  `DataDigestMismatch` or `ContractChanged`.

Either result is useful input for the proposal.

## Manual installation

### 1. Create a cluster

```sh
kind create cluster --name records
```

### 2. Build and load the image

```sh
make docker-build
kind load docker-image records-controller:dev --name records
```

### 3. Deploy

```sh
make deploy
kubectl -n records-system rollout status deployment/records-controller
```

This applies `config/default`: the four CRDs, a ClusterRole and binding, and
the controller Deployment in the `records-system` namespace.

Check the controller is running:

```sh
kubectl -n records-system logs deployment/records-controller
```

### 4. Apply the samples

```sh
kubectl apply -f config/samples/records.yaml
kubectl get schemas,records,recordsets -n platform
```

Expected output, after a few seconds:

```text
NAME                                       READY   DIGEST                AGE
schema.records.crossplane.io/subnet-v1     True    sha256:…              5s

NAME                                        TYPE     SCHEMA      VALID   AGE
record.records.crossplane.io/prod-west-3    subnet   subnet-v1   True    5s

NAME                                        TYPE     CURRENT   READY   AGE
recordset.records.crossplane.io/prod-west   subnet   3         True    5s
```

Inspect the digests the controller computed:

```sh
kubectl -n platform get record prod-west-3 -o jsonpath='{.status}' | jq
```

## Manual testing

Each example below uses the samples from step 4.

### Records are immutable

```sh
kubectl -n platform patch record prod-west-3 --type=merge \
  -p '{"spec":{"recordType":"region"}}'
```

Expected: rejected with `spec is immutable`.

### Status digests are write-once

```sh
kubectl -n platform patch record prod-west-3 --subresource=status --type=merge \
  -p '{"status":{"dataDigest":"sha256:0000000000000000000000000000000000000000000000000000000000000000"}}'
```

Expected: rejected with `status.dataDigest is write-once`.

### Data must match the Schema

Unknown fields are rejected rather than pruned, because pruning would change
the data and its digest:

```sh
kubectl apply -f - <<'EOF'
apiVersion: records.crossplane.io/v1alpha1
kind: Record
metadata:
  name: unknown-field
  namespace: platform
spec:
  publisher:
    id: team-network
  recordTypeGroup: network.example.org
  recordType: subnet
  schema:
    ref:
      kind: Schema
      name: subnet-v1
  data:
    cidr: "10.0.0.0/8"
    owner: team-a
EOF
kubectl -n platform get record unknown-field \
  -o jsonpath='{.status.conditions[?(@.type=="Valid")].message}'
```

Expected: `Valid=False` with reason `DataInvalid` and a message naming
`owner`.

### Asserted digests are verified

A Publisher may set `spec.dataDigest` or `spec.schema.digest`. A mismatch
makes the Record invalid:

```sh
kubectl apply -f - <<'EOF'
apiVersion: records.crossplane.io/v1alpha1
kind: Record
metadata:
  name: wrong-digest
  namespace: platform
spec:
  publisher:
    id: team-network
  recordTypeGroup: network.example.org
  recordType: subnet
  schema:
    ref:
      kind: Schema
      name: subnet-v1
  dataDigest: sha256:0000000000000000000000000000000000000000000000000000000000000000
  data:
    cidr: "10.0.0.0/8"
EOF
kubectl -n platform get record wrong-digest \
  -o jsonpath='{.status.conditions[?(@.type=="Valid")].reason}'
```

Expected: `DataDigestMismatch`.

### A reused Schema name is detected

Deleting a Schema and recreating it under the same name with a different
contract must not silently rebind existing Records:

```sh
kubectl -n platform delete schema subnet-v1
kubectl apply -f - <<'EOF'
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
    required: [cidr, gateway]
    properties:
      cidr: {type: string}
      location:
        type: object
        properties:
          region: {type: string}
          country: {type: string}
      region: {type: string}
      zones: {type: array, items: {type: string}}
      gateway: {type: string}
EOF
kubectl -n platform get record prod-west-3 \
  -o jsonpath='{.status.conditions[?(@.type=="Valid")].message}'
```

Expected: `Valid=False` with reason `SchemaDigestMismatch`. The Record's
`status.schema.digest` still identifies the original contract.

### RecordSet rules

`current` cannot point at a retracted version:

```sh
kubectl -n platform patch recordset prod-west --type=merge \
  -p '{"spec":{"retracted":[{"version":3}]}}'
```

Expected: rejected with `current must not identify a retracted version`.

New versions must be higher than existing ones:

```sh
kubectl -n platform patch recordset prod-west --type=json -p '[
  {"op":"add","path":"/spec/versions/-","value":
    {"version":1,"recordRef":{"name":"prod-west-3"},"schemaRef":{"kind":"Schema","name":"subnet-v1"}}}]'
```

Expected: rejected with `new versions must be greater than every existing
version`.

Version numbers are never reused, even after retention removes a version.
`spec.highestVersion` remembers the highest number ever published, and the
Publisher raises it in the same update that publishes a higher version.
Removing every version and publishing version 3 again is rejected:

```sh
kubectl -n platform patch recordset prod-west --type=json \
  -p '[{"op":"remove","path":"/spec/current"},{"op":"remove","path":"/spec/versions"}]'
kubectl -n platform patch recordset prod-west --type=merge -p '{"spec":{"versions":[
  {"version":3,"recordRef":{"name":"prod-west-3"},"schemaRef":{"kind":"Schema","name":"subnet-v1"}}]}}'
```

Expected: the first patch succeeds; the second is rejected with `version
numbers must not be reused`.

`spec.retention.maxVersions` caps how many versions a RecordSet lists. The
Publisher removes the oldest versions when it publishes; a RecordSet listing
more is rejected with `versions must not exceed retention.maxVersions`.

## Integration tests

The repository also has envtest integration tests that run against a real
`kube-apiserver` and `etcd` without a cluster:

```sh
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
setup-envtest use 1.35.0
make test
```

## Cleaning up

```sh
make undeploy      # remove the controller and CRDs
make kind-delete   # delete the kind cluster
```
