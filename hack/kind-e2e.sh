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

schema() { # namespace name required-fields [shape-version]
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
  shapeVersion: ${4:-v1}
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
  recordTypeGroup: network.example.org
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

# status_of prints a status field of a Schema in $NS.
status_of() { # schema-name field
	k -n "$NS" get schema "$1" -o jsonpath="{.status.$2}"
}

version_entry() { # version record-name [schema-name]
	local schema=${3:-subnet-v1}
	cat <<EOF
    - version: $1
      recordRef:
        name: $2
      schemaRef:
        kind: Schema
        name: $schema
        structuralDigest: $(status_of "$schema" structuralDigest)
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
digest=$(status_of subnet-v1 digest)
info "subnet-v1 digest: $digest"
[[ $(status_of subnet-v1 structuralDigest) == sha256:* ]] &&
	pass "Schema gets a structural digest" || fail "Schema gets a structural digest" "status.structuralDigest is empty"

# subnet-v2 makes gateway required: a new shapeVersion in the same lineage.
schema "$NS" subnet-v2 "cidr, gateway" v2 | k apply -f - >/dev/null
expect_condition "v2 Schema is ready" "$NS" schema subnet-v2 Ready True/Verified
[[ $(status_of subnet-v2 structuralDigest) != "$(status_of subnet-v1 structuralDigest)" ]] &&
	pass "v1 and v2 have different structural digests" ||
	fail "v1 and v2 have different structural digests" "both are $(status_of subnet-v1 structuralDigest)"

# A documentation-only revision of v1 is a different contract with the same
# structure.
schema "$NS" subnet-v1-documented cidr |
	sed 's/cidr: {type: string}/cidr: {type: string, description: "IPv4 CIDR block"}/' |
	k apply -f - >/dev/null
expect_condition "Documented v1 Schema is ready" "$NS" schema subnet-v1-documented Ready True/Verified
if [[ $(status_of subnet-v1-documented digest) != "$digest" &&
	$(status_of subnet-v1-documented structuralDigest) == "$(status_of subnet-v1 structuralDigest)" ]]; then
	pass "Documentation changes the digest but not the structural digest"
else
	fail "Documentation changes the digest but not the structural digest" \
		"digests $(status_of subnet-v1-documented digest) vs $digest, structural $(status_of subnet-v1-documented structuralDigest) vs $(status_of subnet-v1 structuralDigest)"
fi

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
v2s=$(k -n "$NS" get schemas --field-selector spec.shapeGroup=network.example.org,spec.shape=subnet,spec.shapeVersion=v2 -o name)
[[ $v2s == schema.records.crossplane.io/subnet-v2 ]] &&
	pass "Field selectors separate shapeVersions" || fail "Field selectors separate shapeVersions" "v2 selected: $v2s"

if k -n "$NS" patch schema subnet-v1 --type=merge -p '{"spec":{"deprecation":{"date":"2027-01-01","message":"superseded by subnet-v2"}}}' >/dev/null 2>&1; then
	pass "Schema deprecation is mutable"
else
	fail "Schema deprecation is mutable" "patch was rejected"
fi

if k -n "$NS" patch schema subnet-v1 --type=merge -p '{"spec":{"successor":{"kind":"Schema","name":"subnet-v2"}}}' >/dev/null 2>&1; then
	pass "A Schema's Publisher can name its successor"
else
	fail "A Schema's Publisher can name its successor" "patch was rejected"
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
step "rawobject-v1"
# condition reads with -n, which kubectl ignores for cluster-scoped kinds.
expect_condition "The controller provides rawobject-v1" "$NS" clusterschema rawobject-v1 Ready True/Verified
raw_digest=$(k get clusterschema rawobject-v1 -o jsonpath='{.status.digest}')
cat <<EOF | k apply -f - >/dev/null
apiVersion: records.crossplane.io/v1alpha1
kind: Record
metadata:
  name: free-form
  namespace: $NS
spec:
  publisher:
    id: team-network
  recordTypeGroup: network.example.org
  recordType: notes
  schema:
    ref:
      kind: ClusterSchema
      name: rawobject-v1
  data:
    owner: team-a
    nested: {list: [1, two, {three: true}]}
EOF
expect_condition "Any JSON object is valid against rawobject-v1" "$NS" record free-form Valid True/Verified
notes=$(k -n "$NS" get records --field-selector spec.recordTypeGroup=network.example.org,spec.recordType=notes -o name)
[[ $notes == record.records.crossplane.io/free-form ]] &&
	pass "Field selectors list Records by type" || fail "Field selectors list Records by type" "selected: $notes"

old_uid=$(k get clusterschema rawobject-v1 -o jsonpath='{.metadata.uid}')
k delete clusterschema rawobject-v1 --wait=true >/dev/null
recreated=""
for _ in $(seq 1 60); do
	uid=$(k get clusterschema rawobject-v1 -o jsonpath='{.metadata.uid}' 2>/dev/null || true)
	d=$(k get clusterschema rawobject-v1 -o jsonpath='{.status.digest}' 2>/dev/null || true)
	if [[ -n $uid && $uid != "$old_uid" && $d == "$raw_digest" ]]; then
		recreated=1
		break
	fi
	sleep 1
done
[[ -n $recreated ]] && pass "A deleted rawobject-v1 is recreated with the same digest" ||
	fail "A deleted rawobject-v1 is recreated with the same digest" "uid=$uid digest=$d, want a new uid and $raw_digest"
expect_condition "Records bound to rawobject-v1 stay valid" "$NS" record free-form Valid True/Verified

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
record "$NS" prod-west-4 subnet-v2 | k apply -f - >/dev/null
expect_condition "Record is valid against the v2 Schema" "$NS" record prod-west-4 Valid True/Verified
# Every RecordSet here publishes its highest version as current.
recordset() { # name current versions-yaml [extra-spec-yaml]
	cat <<EOF
apiVersion: records.crossplane.io/v1alpha1
kind: RecordSet
metadata:
  name: $1
  namespace: $NS
spec:
  publisher:
    id: team-network
  recordTypeGroup: network.example.org
  recordType: subnet
  current: $2
  highestVersion: $2
${4:-}
  versions:
$3
EOF
}

recordset prod-west 4 "$(version_entry 4 prod-west-4 subnet-v2)
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

expect_rejected "RecordSet recordTypeGroup is immutable" "spec.recordTypeGroup is immutable" \
	k -n "$NS" patch recordset prod-west --type=merge -p '{"spec":{"recordTypeGroup":"storage.example.org"}}'

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

# Removing every version leaves highestVersion behind, so version 4 cannot
# come back with different content.
recordset reuse-probe 4 "$(version_entry 4 prod-west-4 subnet-v2)" | k apply -f - >/dev/null
if k -n "$NS" patch recordset reuse-probe --type=json \
	-p '[{"op":"remove","path":"/spec/current"},{"op":"remove","path":"/spec/versions"}]' >/dev/null 2>&1; then
	pass "Every version can be removed"
else
	fail "Every version can be removed" "patch was rejected"
fi
expect_rejected "Version numbers are never reused" "version numbers must not be reused" \
	k -n "$NS" patch recordset reuse-probe --type=merge \
	-p '{"spec":{"versions":[{"version":4,"recordRef":{"name":"prod-west-2"},"schemaRef":{"kind":"Schema","name":"subnet-v1"}}]}}'

recordset retention-probe 2 "$(version_entry 2 prod-west-2)" "  retention:
    maxVersions: 1" | k apply -f - >/dev/null
expect_condition "RecordSet within retention is ready" "$NS" recordset retention-probe Ready True/Verified
expect_rejected "Publishing beyond retention.maxVersions is rejected" "versions must not exceed retention.maxVersions" \
	k -n "$NS" patch recordset retention-probe --type=merge \
	-p '{"spec":{"highestVersion":4,"versions":[{"version":4,"recordRef":{"name":"prod-west-4"},"schemaRef":{"kind":"Schema","name":"subnet-v2"}},{"version":2,"recordRef":{"name":"prod-west-2"},"schemaRef":{"kind":"Schema","name":"subnet-v1","structuralDigest":"'"$(status_of subnet-v1 structuralDigest)"'"}}]}}'

# prod-west-4 is bound to subnet-v2, so claiming v1's structure is wrong.
recordset bad-structural 4 "    - version: 4
      recordRef:
        name: prod-west-4
      schemaRef:
        kind: Schema
        name: subnet-v2
        structuralDigest: $(status_of subnet-v1 structuralDigest)" | k apply -f - >/dev/null
expect_condition "Structural digest mismatch is detected" "$NS" recordset bad-structural Ready False/InvalidVersions

# ---------------------------------------------------------------------------
step "Subscription"
subscription() { # name [extra-spec-yaml]
	cat <<EOF
apiVersion: records.crossplane.io/v1alpha1
kind: Subscription
metadata:
  name: $1
  namespace: $NS
spec:
  recordSet:
    name: follow-me
${2:-}
EOF
}
selected() { k -n "$NS" get subscription "$1" -o jsonpath='{.status.selected.version}'; }
expect_selected() { # description subscription version
	local got=""
	for _ in $(seq 1 60); do
		got=$(selected "$2")
		[[ $got == "$3" ]] && { pass "$1"; return; }
		sleep 1
	done
	fail "$1" "selected version is \"$got\", want $3"
}

recordset follow-me 2 "$(version_entry 2 prod-west-2)" | k apply -f - >/dev/null
subscription follows-current | k apply -f - >/dev/null
subscription reads-v1 "  compatibleWith:
    shapeGroup: network.example.org
    shape: subnet
    shapeVersion: v1" | k apply -f - >/dev/null
expect_selected "A Subscription follows current" follows-current 2
expect_selected "A v1 Subscription selects a v1 version" reads-v1 2
data_cidr=$(k -n "$NS" get subscription follows-current -o jsonpath='{.status.data.cidr}')
[[ $data_cidr == 10.21.0.0/16 ]] && pass "A Subscription serves the Record's data" ||
	fail "A Subscription serves the Record's data" "status.data.cidr is \"$data_cidr\""

# The Publisher moves current to a version under subnet-v2.
k -n "$NS" patch recordset follow-me --type=merge -p "{\"spec\":{\"current\":4,\"highestVersion\":4,\"versions\":[
	{\"version\":4,\"recordRef\":{\"name\":\"prod-west-4\"},\"schemaRef\":{\"kind\":\"Schema\",\"name\":\"subnet-v2\"}},
	{\"version\":2,\"recordRef\":{\"name\":\"prod-west-2\"},\"schemaRef\":{\"kind\":\"Schema\",\"name\":\"subnet-v1\",\"structuralDigest\":\"$(status_of subnet-v1 structuralDigest)\"}}]}}" >/dev/null
expect_selected "A Subscription follows current to a new contract" follows-current 4
expect_condition "A v1 Subscription reports the incompatible current" "$NS" subscription reads-v1 UpToDate False/Incompatible
expect_condition "A v1 Subscription stays Ready while holding" "$NS" subscription reads-v1 Ready True/Selected
expect_selected "A v1 Subscription holds its v1 version" reads-v1 2
info "$(k -n "$NS" get subscription reads-v1 -o jsonpath='{.status.conditions[?(@.type=="UpToDate")].message}')"

# ---------------------------------------------------------------------------
step "Authorization"
# e2e-tenant may author Records kinds and publish under tenant.example.org
# only. Requests impersonate it, and use server-side dry run.
cat <<EOF | k apply -f - >/dev/null
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: records-e2e-tenant
rules:
  - apiGroups: [records.crossplane.io]
    resources: [schemas, clusterschemas, records, recordsets]
    verbs: [create, get]
  - apiGroups: [records.crossplane.io]
    resources: [groups]
    resourceNames: [tenant.example.org]
    verbs: [publish]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: records-e2e-tenant
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: records-e2e-tenant
subjects:
  - apiGroup: rbac.authorization.k8s.io
    kind: User
    name: e2e-tenant
EOF

tenant_schema() { # name shape-group
	cat <<EOF
apiVersion: records.crossplane.io/v1alpha1
kind: Schema
metadata:
  name: $1
  namespace: $NS
spec:
  publisher:
    id: team-tenant
  shapeGroup: $2
  shape: subnet
  shapeVersion: v1
  format: StructuralSchema
  definition:
    type: object
EOF
}
as_tenant() { # name shape-group
	tenant_schema "$@" | k --as=e2e-tenant create --dry-run=server -f -
}

# eventually_allowed retries a command for up to 30s while RBAC catches up.
eventually_allowed() { # description command...
	local desc=$1
	shift
	for _ in $(seq 1 30); do
		"$@" >/dev/null 2>&1 && { pass "$desc"; return; }
		sleep 1
	done
	fail "$desc" "$("$@" 2>&1)"
}

# The alpha default: permissive RBAC lets everyone publish under any group.
eventually_allowed "Alpha default: anyone may publish under any group" as_tenant tenant-v1 network.example.org

# Lock down, as the README describes, then check each denial.
k delete clusterrolebinding records-permissive --wait=true >/dev/null
denied=""
for _ in $(seq 1 30); do
	as_tenant tenant-v1 network.example.org >/dev/null 2>&1 || { denied=1; break; }
	sleep 1
done
[[ -n $denied ]] || info "permissive RBAC still in effect after 30s"
eventually_allowed "Locked down: a tenant can publish under its own group" as_tenant tenant-v1 tenant.example.org
expect_rejected "Locked down: publishing under another team's shapeGroup is denied" "not authorized to publish under shapeGroup network.example.org" \
	as_tenant tenant-v1 network.example.org
expect_rejected "Locked down: the reserved records.crossplane.io group is denied" "not authorized to publish under shapeGroup records.crossplane.io" \
	as_tenant tenant-v1 records.crossplane.io

# Restore the alpha default.
k apply -k "$ROOT/config/policy" >/dev/null

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
