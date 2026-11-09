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

## rqlite PoC upgrade boundary

This revision replaces the embedded SQLite/Ent organization store with rqlite.
`DATABASE_PATH` is no longer used. Existing SQLite organization records are **not
automatically imported**; keep the old database and its backup. Use a fresh test
deployment for this PoC. An existing installation needs a separately validated
data import before switching; the registry image data itself is unaffected.

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
