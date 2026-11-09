# Operations

[Home](../README.md) · [Configuration](configuration.md)

## Stop or update

Run these commands from the repository root:

```bash
# Stop the stack, keeping its data volumes.
docker compose down

# Update the configuration and pull published images.
git pull --ff-only
docker compose pull
docker compose up -d --no-build
```

Back up the persistent volumes before updating. Avoid `docker compose down -v` unless you intend to delete the registry, cache, and metadata volumes.

`latest` follows tested builds of `main`. Pin `MINI_HARBOR_IMAGE` to `ghcr.io/fanyang89/mini-harbor:sha-<full-commit-sha>` or a digest when you need a fixed application version. Update that value when upgrading. If you build from source, use `MINI_HARBOR_IMAGE=mini-harbor:local docker compose up -d --build` instead of pulling the application image.

## Backups

Preserve `registry-data`, `registry-proxy-data`, `mini-harbor-data`, and `rqlite-data`, together with your configuration. Use a consistent backup or storage snapshot; stop writers and the database while taking an offline backup. Verify that the backup can be restored before destructive maintenance. For online database backups, use the rqlite backup API: https://rqlite.io/docs/guides/backup/

## Migrate legacy SQLite organizations

The rqlite revision replaces the embedded SQLite/Ent organization store.
`DATABASE_PATH` is no longer used. Import existing organizations with
the standalone `mini-harbor-migrate` executable (`cmd/migrate`). It is included
in the application image and does not require Python or a SQLite CLI. The pure-Go
SQLite driver is linked only into the migration executable, not the web server.
The importer preserves names, descriptions, and timestamp instants at nanosecond
precision. It reads the source without SQL writes and retains the original file.
Only organization metadata is imported; registry images and usage files stay in
their existing volumes.

For the default local Docker Compose deployment:

1. Stop the **old** web service before replacing its container. Copy the entire
   metadata directory, including any `mini-harbor.db-wal` and `-shm` companions.
   Do not copy only the main database while WAL data may exist. Keep organization
   management writes paused until verification finishes.

   ```sh
   docker compose stop web
   mkdir -p data/legacy-sqlite
   docker compose cp web:/var/lib/mini-harbor/. ./data/legacy-sqlite/
   ```

   For a customized old `DATABASE_PATH`, copy that database and its companions
   instead. A consistent SQLite backup is also accepted. A live source is read
   as one snapshot including committed WAL records, but later source writes are
   not replicated: stop the old writer before the final import.

2. With the updated checkout, build the image containing the migrator and start
   rqlite. The migrator initializes a fresh target during import; the web service
   can remain stopped throughout migration.

   ```sh
   docker compose build web
   docker compose up -d rqlite
   ```

3. Preview the source/target comparison, then import:

   ```sh
   docker compose run --rm --no-deps \
     -v "$PWD/data/legacy-sqlite:/legacy:ro" \
     --entrypoint mini-harbor-migrate web \
     --source /legacy/mini-harbor.db --dry-run

   docker compose run --rm --no-deps \
     -v "$PWD/data/legacy-sqlite:/legacy:ro" \
     --entrypoint mini-harbor-migrate web \
     --source /legacy/mini-harbor.db
   ```

   For an existing rqlite cluster, use comma-separated node URLs, or set
   `RQLITE_URLS`. All URLs must belong to the same cluster. No
   authentication credentials are configured by this tool.

4. After the JSON report contains `"verified": true`, resume the web service.
   Check the organization list in the UI and retain the source backup.

   ```sh
   docker compose up -d rqlite web
   ```

For host-side use, build the independent entry point and specify reachable node
URLs (the standard Compose configuration does not publish rqlite's port):

```sh
mkdir -p data
CGO_ENABLED=0 go build -o ./data/mini-harbor-migrate ./cmd/migrate
./data/mini-harbor-migrate --source /path/to/mini-harbor.db \
  --rqlite-urls http://node1:4001,http://node2:4001,http://node3:4001 --dry-run
```

The preview reports `source`, `new`, `identical`, `conflicts`, and whether the
target needs schema initialization (`initialize`). Import uses one data
transaction: identical rows are accepted, unrelated target rows are preserved,
and same-name rows with different content cause failure without overwriting
them. A transaction-level guard also rejects conflicts introduced after preview.
There is no force/overwrite option. Fix the source or reconcile the target
deliberately before retrying.

If the write response is lost, the tool does **not** resend it. Run `--dry-run`
again: an already-committed import will report identical rows, and rerunning the
import is safe when those rows still match. A successful import is followed by
a linearizable read-back comparison of all source records. Dry runs issue no
target writes, including on a fresh database. A real import initializes a fresh
target using the application's versioned migrations before writing records.
Existing targets must have supported schema version 1. `--timeout` bounds the
whole operation (default `2m`). Exit code 0 means success, 1 means migration
failure, and 2 means invalid arguments. The source is never deleted.

An API error saying the write outcome is unknown means the request may have
committed. Reload the organization before deciding whether to retry. Metadata
failover does not provide HA for registry storage, gateway caches, usage records,
or cleanup coordination.

## Registry garbage collection

Deleting a tag only removes that reference. Deleting by digest removes the manifest and all tags that reference it. Unreferenced blobs remain on disk until Registry garbage collection runs.

Confirm that a recent, restorable backup or storage snapshot exists before continuing. Stop all registry writers and run Distribution garbage collection against the registry's storage volume during a maintenance window. Start with a dry run and review its output before deleting blobs.

Do not use `--delete-untagged`. Digest-only manifests, multi-architecture child manifests, and OCI artifacts may rely on untagged manifests. Ensure garbage collection has finished before allowing writes again.

## Metrics

The gateway exposes Prometheus metrics at http://localhost:5000/metrics. The endpoint is unauthenticated, so protect it at the network layer when source metadata is sensitive. It includes Go and process metrics plus:

| Metric | Description |
| --- | --- |
| `mini_harbor_registry_requests_total` | Requests by route, prefix, method, and status |
| `mini_harbor_registry_request_duration_seconds` | Request duration histogram |
| `mini_harbor_registry_requests_in_flight` | Current requests by route and prefix |
| `mini_harbor_registry_proxy_fallbacks_total` | Source fallbacks by reason |
| `mini_harbor_registry_proxy_backend_up` | Local proxy backend process state |
| `mini_harbor_registry_proxy_source_info` | Configured proxy source metadata |

For observed CPU and memory consumption, see [resource usage](resource-usage.md).
