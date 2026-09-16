# Kubernetes HA deployment contract

The `kubernetes` election backend uses the stable `coordination.k8s.io/v1` Lease API through HTTPS. It reads the in-cluster API address, service-account namespace/token/CA by default. The token file is read for each request so projected token rotation works. `election.kubernetes.api_server`, `namespace`, `token_file` and `ca_file` can override those defaults. Kubernetes credentials are never sent through an HTTP redirect.

The default Lease name is `wire-coordinator`. Defaults are `lease_duration: 10s`, `renew_deadline: 6s`, `retry_period: 1s`. Lease duration must be whole seconds and greater than renewal deadline, which must exceed retry period. Nodes must agree on these settings. Acquisition uses resource-version optimistic concurrency. A candidate waits a full lease duration from its own observation of an unchanged record; it does not trust another node's wall clock. A separate timer revokes the local grant when renewal fails or hangs. A later API response cannot revive an expired grant. Keep host clocks well behaved; elapsed clock-rate differences must fit the lease/renewal margin.

## Authoritative storage is mandatory

Every candidate must open the **same current metadata directory** after election. Independent local volumes, periodic snapshots, rsync copies and object-store copies are not automatic HA metadata. They can omit acknowledged submissions or transactional sink decisions.

Use storage that supports Pebble's filesystem operations (including durable fsync, atomic rename and hard links), exclusive cross-client database locks, and fencing of a failed client's outstanding I/O. Kubernetes Lease ownership does not supply storage fencing. A generic RWX PVC declaration is not proof of those guarantees. Verify the selected storage driver's failure and client-eviction behavior before deploying. Never mount a filesystem that can let two clients write the database after a partition. If the old database owner cannot be fenced, takeover must remain unavailable rather than open a stale copy.

The elected process opens Pebble only after acquiring its Lease, and closes its term-scoped handle before voluntary release. Standbys run health/discovery without opening the database. Storage-open failures do not publish readiness.

## Configuration

Give each coordinator a distinct node ID and individually reachable advertised addresses (for example, StatefulSet pod DNS names). Do not advertise a load-balanced Service as an individual leader address.

```yaml
mode: coordinator
listen: :4002
node:
  id: wire-coordinator-0
  data_dir: /wire-metadata/coordinator
  rpc_advertise_addr: wire-coordinator-0.wire-coordinators:4002
http:
  addr: :4001
  adv_addr: wire-coordinator-0.wire-coordinators:4001
election:
  backend: kubernetes
  kubernetes:
    namespace: wire
    lease_name: wire-coordinator
    lease_duration: 10s
    renew_deadline: 6s
    retry_period: 1s
```

On the other coordinator change only its identity and advertised pod addresses; use the same authoritative storage and Lease. The metadata path must be available on either candidate under the storage guarantees above.

```yaml
mode: worker
worker:
  worker_id: worker-0
  listen_addr: :4003
  task_slots: 4
  coordinator_seeds:
    - wire-coordinator-0.wire-coordinators:4001
    - wire-coordinator-1.wire-coordinators:4001
  epoch_path: /wire-worker/epoch
```

Preserve each worker's epoch file across restarts and do not share it between workers. Discovery verifies readiness on the advertised leader before connecting to its RPC endpoint. It is a routing hint, not authentication; configure `node_tls` and network access controls for production. Worker RPC registration enforces the persisted highest-seen epoch. WIP-08's contact deadline remains active across discovery/reconnects; a supervisor restarts a worker that exceeds it.

## Least-privilege election RBAC

Precreate the Lease and grant access to that specific resource. With a precreated Lease, the coordinator only requires `get` and `update`; its optional create-on-missing path needs `create` if provisioning chooses not to precreate it. Do not grant cluster-wide lease access.

```yaml
apiVersion: coordination.k8s.io/v1
kind: Lease
metadata:
  name: wire-coordinator
  namespace: wire
spec: {}
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: wire-coordinator
  namespace: wire
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: wire-coordinator-election
  namespace: wire
rules:
  - apiGroups: [coordination.k8s.io]
    resources: [leases]
    resourceNames: [wire-coordinator]
    verbs: [get, update]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: wire-coordinator-election
  namespace: wire
subjects:
  - kind: ServiceAccount
    name: wire-coordinator
    namespace: wire
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: wire-coordinator-election
```

Use this ServiceAccount on coordinator pods. `/healthz` is process liveness; standby processes must not be killed for lacking leadership. `/api/v1/cluster/leader` reports readiness and both endpoints. `/readyz` redirects a standby toward the leader; for Kubernetes leader-only traffic routing, use an exec/readiness check that verifies the local leader response's `is_self` and `ready` fields instead of treating followed redirects as local readiness.

References: [Kubernetes Lease API](https://kubernetes.io/docs/reference/kubernetes-api/coordination/lease-v1/) and [client-go election clock/renewal semantics](https://github.com/kubernetes/client-go/blob/master/tools/leaderelection/leaderelection.go). This implementation's tests exercise HTTPS API contention, conflicts, expiry, discovery and outage behavior. A real cluster/storage-driver fault test is separate deployment validation; no storage driver is certified merely by these API tests.
