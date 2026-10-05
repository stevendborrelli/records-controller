#!/usr/bin/env bash
# End-to-end test of records-controller on a kind cluster.
#
# Creates (or reuses) a kind cluster, builds and loads the controller image,
# deploys the CRDs and controller, and runs scenarios from the Records
# proposal against it. Prints PASS/FAIL per check and exits non-zero if any
# check fails.
#
# Environment:
#   CLUSTER         kind cluster name                (default: records)
#   IMAGE           controller image tag             (default: records-controller:dev)
#   SKIP_BUILD      set to 1 to reuse a loaded image
#   DELETE_CLUSTER  set to 1 to delete the cluster when done
set -euo pipefail

CLUSTER=${CLUSTER:-records}
IMAGE=${IMAGE:-records-controller:dev}
SKIP_BUILD=${SKIP_BUILD:-0}
DELETE_CLUSTER=${DELETE_CLUSTER:-0}
NS=records-e2e
OTHER_NS=records-e2e-other
ROOT=$(cd "$(dirname "$0")/.." && pwd)
BOGUS=sha256:0000000000000000000000000000000000000000000000000000000000000000

passed=0
failed=0

k() { kubectl --context "kind-${CLUSTER}" "$@"; }
step() { printf '\n==> %s\n' "$*"; }
pass() { printf '  PASS  %s\n' "$1"; passed=$((passed + 1)); }
fail() {
	printf '  FAIL  %s\n' "$1"
	[[ -n ${2:-} ]] && printf '        %s\n' "$2"
	failed=$((failed + 1))
}
info() { printf '  INFO  %s\n' "$*"; }

for tool in kind kubectl docker; do
	command -v "$tool" >/dev/null || { echo "missing required tool: $tool" >&2; exit 1; }
done

# condition prints "<status>/<reason>" of a condition on a namespaced object.
condition() { # namespace kind name type
	k -n "$1" get "$2" "$3" -o jsonpath="{.status.conditions[?(@.type==\"$4\")].status}/{.status.conditions[?(@.type==\"$4\")].reason}" 2>/dev/null || true
}

# expect_condition waits up to 60s for a condition to reach status/reason.
expect_condition() { # description namespace kind name type want
	local got=""
	for _ in $(seq 1 60); do
		got=$(condition "$2" "$3" "$4" "$5")
		[[ $got == "$6" ]] && { pass "$1"; return; }
		sleep 1
	done
	local msg
	msg=$(k -n "$2" get "$3" "$4" -o jsonpath="{.status.conditions[?(@.type==\"$5\")].message}" 2>/dev/null || true)
	fail "$1" "want $5=$6, got ${got:-<none>} ${msg:+($msg)}"
}

# expect_rejected runs a kubectl command and passes if the API server rejects
# it with a message containing want.
expect_rejected() { # description want command...
	local desc=$1 want=$2 out
	shift 2
	if out=$("$@" 2>&1); then
		fail "$desc" "request was accepted"
		return 1
	fi
	if [[ $out == *"$want"* ]]; then
		pass "$desc"
	else
		fail "$desc" "rejected, but without \"$want\": $out"
	fi
}

schema() { # namespace name required-fields
	cat <<EOF
apiVersion: records.crossplane.io/v1alpha1
kind: Schema
metadata:
  name: $2
  namespace: $1
spec:
  publisher:
    id: team-network
  shapeGroup: network.example.org
  shape: subnet
  shapeVersion: v1
  format: StructuralSchema
  definition:
    type: object
    required: [$3]
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
}

record() { # namespace name schema-name [extra spec lines] [extra data lines]
	cat <<EOF
apiVersion: records.crossplane.io/v1alpha1
kind: Record
metadata:
  name: $2
  namespace: $1
spec:
  publisher:
    id: team-network
  recordType: subnet
  schema:
    ref:
      kind: Schema
      name: $3
${4:-}
  data:
    cidr: "10.21.0.0/16"
    location:
      region: NorthAmerica
      country: UnitedStates
    region: us-west-2
    zones: [us-west-2a, us-west-2b, us-west-2c]
    gateway: "10.21.0.1"
${5:-}
EOF
}

version_entry() { # version record-name
	cat <<EOF
    - version: $1
      recordRef:
        name: $2
      schemaRef:
        kind: Schema
        name: subnet-v1
EOF
}

# ---------------------------------------------------------------------------
step "Cluster"
if kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
	info "reusing kind cluster $CLUSTER"
else
	kind create cluster --name "$CLUSTER" --wait 120s
fi

if [[ $SKIP_BUILD != 1 ]]; then
	step "Build and load $IMAGE"
	docker build -t "$IMAGE" "$ROOT"
	kind load docker-image "$IMAGE" --name "$CLUSTER"
fi

step "Deploy"
k apply -k "$ROOT/config/default"
k -n records-system set image deployment/records-controller manager="$IMAGE"
# The tag may be unchanged after a rebuild, so always restart.
k -n records-system rollout restart deployment/records-controller
k -n records-system rollout status deployment/records-controller --timeout=120s
k wait --for condition=Established --timeout=60s \
	crd/schemas.records.crossplane.io crd/clusterschemas.records.crossplane.io \
	crd/records.records.crossplane.io crd/recordsets.records.crossplane.io

k delete namespace "$NS" "$OTHER_NS" --ignore-not-found --wait=true >/dev/null
k create namespace "$NS" >/dev/null
k create namespace "$OTHER_NS" >/dev/null

# ---------------------------------------------------------------------------
step "Schema"
schema "$NS" subnet-v1 cidr | k apply -f - >/dev/null
expect_condition "Schema gets a contract digest" "$NS" schema subnet-v1 Ready True/Verified
digest=$(k -n "$NS" get schema subnet-v1 -o jsonpath='{.status.digest}')
info "subnet-v1 digest: $digest"

# Probe immutability on throwaway objects, so that if the API server accepts
# the change the remaining checks are unaffected.
schema "$NS" immutable-probe cidr | k apply -f - >/dev/null
expect_condition "Probe Schema is ready" "$NS" schema immutable-probe Ready True/Verified
expect_rejected "Schema definition is immutable" "spec.definition is immutable" \
	k -n "$NS" patch schema immutable-probe --type=merge -p '{"spec":{"definition":{"type":"object","properties":{"cidr":{"type":"integer"}}}}}' ||
	expect_condition "Controller flags the changed contract" "$NS" schema immutable-probe Ready False/ContractChanged
expect_rejected "Schema lineage is immutable" "spec.shapeGroup is immutable" \
	k -n "$NS" patch schema immutable-probe --type=merge -p '{"spec":{"shapeGroup":"storage.example.org"}}'

if k -n "$NS" get schemas --field-selector spec.shapeGroup=network.example.org,spec.shape=subnet,spec.shapeVersion=v1 -o name |
	grep -qx schema.records.crossplane.io/subnet-v1 &&
	[[ -z $(k -n "$NS" get schemas --field-selector spec.shapeGroup=storage.example.org -o name) ]]; then
	pass "Field selectors list a Schema lineage"
else
	fail "Field selectors list a Schema lineage" "subnet-v1 not selected by its lineage, or another lineage matched"
fi

if k -n "$NS" patch schema subnet-v1 --type=merge -p '{"spec":{"deprecation":{"date":"2027-01-01","message":"superseded by subnet-v2"}}}' >/dev/null 2>&1; then
	pass "Schema deprecation is mutable"
else
	fail "Schema deprecation is mutable" "patch was rejected"
fi

expect_rejected "Deprecation date must be a full-date" "spec.deprecation.date" \
	k -n "$NS" patch schema subnet-v1 --type=merge -p '{"spec":{"deprecation":{"date":"2027-01-01T00:00:00Z"}}}'

# ---------------------------------------------------------------------------
step "Record"
record "$NS" prod-west-3 subnet-v1 | k apply -f - >/dev/null
expect_condition "Record is valid" "$NS" record prod-west-3 Valid True/Verified
info "prod-west-3 dataDigest: $(k -n "$NS" get record prod-west-3 -o jsonpath='{.status.dataDigest}')"

data_digest=$(k -n "$NS" get record prod-west-3 -o jsonpath='{.status.dataDigest}')
record "$NS" asserted subnet-v1 "    digest: ${digest}
  dataDigest: ${data_digest}" | k apply -f - >/dev/null
expect_condition "Record with correct asserted digests is valid" "$NS" record asserted Valid True/Verified

record "$NS" bad-data-digest subnet-v1 "  dataDigest: ${BOGUS}" | k apply -f - >/dev/null
expect_condition "Wrong asserted dataDigest is detected" "$NS" record bad-data-digest Valid False/DataDigestMismatch

record "$NS" bad-schema-digest subnet-v1 "    digest: ${BOGUS}" | k apply -f - >/dev/null
expect_condition "Wrong asserted schema digest is detected" "$NS" record bad-schema-digest Valid False/SchemaDigestMismatch

record "$NS" unknown-field subnet-v1 "" "    owner: team-a" | k apply -f - >/dev/null
expect_condition "Unknown data fields are rejected, not pruned" "$NS" record unknown-field Valid False/DataInvalid

record "$NS" no-schema-yet subnet-late | k apply -f - >/dev/null
expect_condition "Missing Schema is reported" "$NS" record no-schema-yet Valid False/SchemaNotFound
schema "$NS" subnet-late cidr | k apply -f - >/dev/null
expect_condition "Record becomes valid when its Schema arrives" "$NS" record no-schema-yet Valid True/Verified

record "$OTHER_NS" cross-namespace subnet-v1 | k apply -f - >/dev/null
expect_condition "Schema references do not cross namespaces" "$OTHER_NS" record cross-namespace Valid False/SchemaNotFound

expect_rejected "Record recordType is immutable" "spec is immutable" \
	k -n "$NS" patch record prod-west-3 --type=merge -p '{"spec":{"recordType":"region"}}'

record "$NS" data-probe subnet-v1 | k apply -f - >/dev/null
expect_condition "Probe Record is valid" "$NS" record data-probe Valid True/Verified
expect_rejected "Record data is immutable" "spec is immutable" \
	k -n "$NS" patch record data-probe --type=merge -p '{"spec":{"data":{"cidr":"10.99.0.0/16"}}}' ||
	expect_condition "Controller flags the changed data" "$NS" record data-probe Valid False/DataDigestMismatch

expect_rejected "status.dataDigest is write-once" "status.dataDigest is write-once" \
	k -n "$NS" patch record asserted --subresource=status --type=merge -p "{\"status\":{\"dataDigest\":\"${BOGUS}\"}}"

# ---------------------------------------------------------------------------
step "Schema name reuse"
schema "$NS" reused cidr | k apply -f - >/dev/null
record "$NS" bound reused | k apply -f - >/dev/null
expect_condition "Record binds to the original contract" "$NS" record bound Valid True/Verified
k -n "$NS" delete schema reused --wait=true >/dev/null
expect_condition "Deleting the Schema invalidates the Record" "$NS" record bound Valid False/SchemaNotFound
schema "$NS" reused "cidr, gateway" | k apply -f - >/dev/null
expect_condition "Recreating the Schema with a new contract is detected" "$NS" record bound Valid False/SchemaDigestMismatch

# ---------------------------------------------------------------------------
step "RecordSet"
record "$NS" prod-west-2 subnet-v1 | k apply -f - >/dev/null
record "$NS" prod-west-4 subnet-v1 | k apply -f - >/dev/null
recordset() { # name current versions-yaml [retracted-yaml]
	cat <<EOF
apiVersion: records.crossplane.io/v1alpha1
kind: RecordSet
metadata:
  name: $1
  namespace: $NS
spec:
  publisher:
    id: team-network
  recordType: subnet
  current: $2
${4:-}
  versions:
$3
EOF
}

recordset prod-west 4 "$(version_entry 4 prod-west-4)
$(version_entry 2 prod-west-2)" "  retracted:
    - version: 2
      message: bad CIDR" | k apply -f - >/dev/null
expect_condition "RecordSet is ready" "$NS" recordset prod-west Ready True/Verified
current=$(k -n "$NS" get recordset prod-west -o jsonpath='{.status.currentRecord}')
[[ $current == prod-west-4 ]] && pass "current resolves to prod-west-4" || fail "current resolves to prod-west-4" "got $current"

expect_rejected "current must not be retracted" "current must not identify a retracted version" \
	k -n "$NS" patch recordset prod-west --type=merge -p '{"spec":{"current":2}}'

expect_rejected "new versions must be higher" "new versions must be greater than every existing version" \
	k -n "$NS" patch recordset prod-west --type=json -p '[{"op":"add","path":"/spec/versions/-","value":{"version":1,"recordRef":{"name":"prod-west-2"},"schemaRef":{"kind":"Schema","name":"subnet-v1"}}}]'

expect_rejected "published entries are immutable" "published version entries are immutable" \
	k -n "$NS" patch recordset prod-west --type=json -p '[{"op":"replace","path":"/spec/versions/0/recordRef/name","value":"prod-west-2"}]'

recordset bad-digest 3 "    - version: 3
      recordRef:
        name: prod-west-3
        dataDigest: ${BOGUS}
      schemaRef:
        kind: Schema
        name: subnet-v1" | k apply -f - >/dev/null
expect_condition "Version digest mismatch is detected" "$NS" recordset bad-digest Ready False/InvalidVersions

recordset missing-record 9 "$(version_entry 9 does-not-exist)" | k apply -f - >/dev/null
expect_condition "Missing Record is detected" "$NS" recordset missing-record Ready False/InvalidVersions

# ---------------------------------------------------------------------------
step "Result"
printf '  %d passed, %d failed\n' "$passed" "$failed"

if [[ $DELETE_CLUSTER == 1 ]]; then
	kind delete cluster --name "$CLUSTER"
else
	info "cluster kind-$CLUSTER kept; inspect with: kubectl --context kind-$CLUSTER -n $NS get schemas,records,recordsets"
	info "delete it with: kind delete cluster --name $CLUSTER"
fi

[[ $failed -eq 0 ]]
