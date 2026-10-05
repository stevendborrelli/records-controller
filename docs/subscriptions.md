# Subscriptions

A Subscription follows a RecordSet on behalf of a Consumer. The Consumer states
which version it wants and which contract its code can read. The controller
selects a version, verifies it, and serves its data in the Subscription's
status. Consumers never have to apply the selection rules in
[records.md](../records.md#consumer-selection) themselves.

```yaml
apiVersion: records.crossplane.io/v1alpha1
kind: Subscription
metadata:
  name: subnets
  namespace: platform
spec:
  recordSet:
    name: networks-dev        # in the Subscription's namespace
  follow: Current             # Current | LatestCompatible | Pinned
  compatibleWith:             # the contract this Consumer can read
    shapeGroup: platform.example.org
    shape: networks
    shapeVersion: v1
  onIncompatible: Hold        # Hold | Fail
```

## Choosing a version

`follow` selects the version:

| `follow` | Selects |
| --- | --- |
| `Current` (default) | the version the Publisher recommends, `spec.current` |
| `LatestCompatible` | the newest version that is not retracted and whose Schema matches `compatibleWith` |
| `Pinned` | exactly `version`, even if it is later retracted |

`compatibleWith` names the lineage and `shapeVersion` the Consumer's code
was written against. It is optional for `Current` and `Pinned`, and required
for `LatestCompatible`. A version is compatible when its Record's Schema has
exactly that `shapeGroup`, `shape`, and `shapeVersion`.

A version is served only once it verifies. Its Record must exist and be
`Valid`, have the RecordSet's publisher and record type, and match any digests
the version entry asserts.

## What happens when the followed version changes

`status.selected` and `status.data` change only when a new version is selected
and verified. Everything else keeps them, so a Consumer reading
`status.data` keeps working through these events:

| Event | `status.data` | `Ready` | `UpToDate` |
| --- | --- | --- | --- |
| A newer version is followed and verifies | the new version | `True` | `True` |
| `current` moves to an incompatible version, `onIncompatible: Hold` | unchanged | `True` | `False`, reason `Incompatible` |
| `current` moves to an incompatible version, `onIncompatible: Fail` | unchanged | `False`, reason `Incompatible` | `False`, reason `Incompatible` |
| The followed version's Record is missing or not valid yet | unchanged | `True` if a version was served before | `False`, reason `Unverified` |
| The RecordSet is deleted | unchanged | `False`, reason `RecordSetNotFound` | `False` |
| A pinned version is retracted | unchanged | `True`, reason `SelectedRetracted` | `True` |

`UpToDate=False` is how a Consumer learns a migration is waiting for it. For
example, after the Publisher moves `current` to a v2 contract:

```text
current is version 4 (network.example.org/subnet/v2), which is incompatible
with network.example.org/subnet/v1; holding version 2; the successor of
subnet-v1 is Schema subnet-v2
```

A third condition, `Deprecated`, reports whether the served version's Schema
is deprecated, with its date, message, and successor.

```sh
$ kubectl get subscriptions -o wide
NAME              RECORDSET   FOLLOW    SELECTED   READY   UPTODATE   RECORD        DATA DIGEST
follows-current   follow-me   Current   4          True    True       prod-west-4   sha256:52ac9565…
reads-v1          follow-me   Current   2          True    False      prod-west-2   sha256:52ac9565…
```

## Namespaces

A Subscription follows only a RecordSet in its own namespace. The controller
can read every namespace, so following one elsewhere would let anyone able to
create a Subscription read data that their own permissions do not allow.
Sharing Records across namespaces is a job for a replication mechanism that
copies them into the Consumer's namespace, under the Consumer's permissions.
