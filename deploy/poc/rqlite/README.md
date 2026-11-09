# rqlite HA proof of concept

Run from the repository root with Python 3 and a reachable local Docker daemon:

```sh
task poc:rqlite
# Or without Task:
python3 deploy/poc/rqlite/verify.py
```

The runner uses only the Python standard library. It creates three voting nodes
on a uniquely named Docker network, publishes HTTP on random loopback ports,
and removes its containers, anonymous volumes, and network on completion or
failure. The downloaded image stays cached. An uncatchable termination of the
runner can leave resources named `mini-harbor-rqlite-poc-<run-id>*`; remove only
that run's resources manually. No existing service or database is used.

The official `rqlite/rqlite:10.6.0` image is pinned by registry digest in the
runner. Each container retains its own data across the tested process restarts.
Docker may reassign host ports on restart, so the runner rediscovers them.

## Checks

- Bootstrap three voting nodes using their Raft addresses (port 4002).
- Create a representative organization table with the current business fields.
- Parameterized create, list, update, and delete, including Unicode and quotes.
- Forward requests through a follower; read back with `level=linearizable`.
- Reject duplicate IDs and roll back an entire batch after a constraint failure.
- Check every replica's local data with `level=none`, rather than accidentally
  testing three requests forwarded to the same leader.
- SIGKILL the elected leader, retain acknowledged data, elect a replacement,
  and commit a new organization with two remaining voters.
- SIGKILL the replacement leader and verify the remaining minority rejects a
  write. Restore both nodes and verify that write is absent on all replicas.
- SIGKILL and restart all three nodes with their existing data, verify local
  convergence, rerun schema creation, and issue another write request.

Readiness and convergence checks have bounded retries. Business writes are not
blindly retried after an ambiguous result. Failure output includes node logs.

## Observed result

2026-11-09, Linux amd64, Docker 29.9.3, rqlite 10.6.0: all checks passed.
The successful run reported **1.81 seconds** from starting the leader-kill
command to a successful post-failover write and read-back. This includes Docker
command overhead and readiness sampling; it is one local observation, not an
RTO guarantee or performance benchmark.

## Integration conclusions

The tested organization workload is compatible with rqlite's HTTP SQL API and
single-request batch transactions. A single rqlite backend is a viable next
step; single-node and three-node deployments can share the same application
implementation without a selectable SQLite backend.

Important client details:

1. SQL errors can arrive with HTTP 200. Inspect top-level and per-statement
   `error` / `error_v2` fields as well as HTTP status.
2. Zero `rows_affected` can be omitted. Decode omission as zero when translating
   missing-row updates/deletes to application errors.
3. Use explicit read consistency. The PoC uses `linearizable` for business reads;
   `none` is used only to inspect replica convergence.
4. Send transaction statements together with `?transaction`; do not emulate
   interactive `BEGIN` / `COMMIT` across independent requests.
5. A failed HTTP request can have an unknown write outcome. Client endpoint
   failover needs an explicit idempotency/reconciliation policy, not unconditional
   write retries. The PoC discovers the new leader before the next business write.
6. Version 10.6.0 joins through `host:4002` Raft addresses, not HTTP URLs.

## Scope and remaining verification

This is a database-level feasibility test, not an integrated Mini Harbor HA
implementation. The SQL represents the organization fields; application-level
validation, Ent replacement, existing SQLite import, versioned migrations, Go
error mapping, and automatic client endpoint failover still need implementation
and integration tests. Repeating `CREATE TABLE IF NOT EXISTS` only verifies
initial-schema idempotency, not a complete migration system.

All nodes run on one host. Network partitions, concurrent client histories,
snapshot-based catch-up, disk failure, real power loss, backup restoration,
latency/throughput, and resource sizing are not tested here. Process restart
does not prove power-loss durability. Registry/S3, gateway caching, image usage,
sessions, and cleanup coordination require separate end-to-end HA validation.

References:

- https://rqlite.io/docs/api/api/
- https://rqlite.io/docs/api/read-consistency/
- https://rqlite.io/docs/clustering/
