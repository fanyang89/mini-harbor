# Operations

[Home](../README.md) · [Configuration](configuration.md)

## Stop or update

Run these commands from the repository root:

```bash
# Stop the stack, keeping its data volumes.
docker compose down

# Update the source and rebuild.
git pull --ff-only
docker compose up -d --build
```

Back up the persistent volumes before updating. Avoid `docker compose down -v` unless you intend to delete the registry, cache, and metadata volumes.

## Backups

Preserve `registry-data`, `registry-proxy-data`, and `mini-harbor-data`, together with your configuration. Use a consistent backup or storage snapshot; stop writers while taking an offline backup. Verify that the backup can be restored before destructive maintenance.

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
