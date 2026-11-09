# Management GraphQL API

The Web console uses `/graphql` for organization and image queries, pagination,
tag operations, and cleanup. The checked-in schema is in
`internal/httpapi/schema.graphql` and `internal/httpapi/mutations.graphql`.
Docker/OCI `/v2/`, OIDC login/callback/logout, and the session-status endpoint
`/api/auth/me` remain separate protocols. Existing REST management endpoints
remain available for older clients and rolling-update compatibility.

## Transport and authentication

- Read-only queries use `GET /graphql?query=...&variables=...&operationName=...`.
  Variables are a JSON object. Reads remain public, as with existing REST reads.
  The GET schema has no mutations, including through aliases or fragments.
- Queries and mutations may use `POST /graphql` with `Content-Type:
  application/json` and `{ "query": "...", "variables": {}, "operationName": "..." }`.
  When OIDC is enabled, all POST requests require a session and pass the same
  same-origin checks as REST writes. With OIDC disabled, writes remain anonymous.
- GraphQL validation and resolver failures appear in `errors`; clients must
  inspect this array even after HTTP 200. Application errors include the former
  REST status in `extensions.status`. Transport/auth failures use HTTP status.
- Responses use `Cache-Control: no-store`. Request bodies are limited to 64 KiB,
  query documents to 16 KiB, URL query strings to 32 KiB, nesting depth to 12,
  and execution to 25 seconds. JSON request batching is not supported.

## Image connection

```graphql
query Images($after: String!, $filter: ImageFilter!) {
  status { online registry }
  images(first: 50, after: $after, filter: $filter, sort: SIZE, direction: DESC) {
    nodes { repository tag digest size created lastUsed architecture os }
    totalCount
    pageInfo { endCursor hasNextPage }
  }
}
```

Start with `{"after":"","filter":{"search":"busybox"}}`. Pass `endCursor`
as `after` for the next page. `first` must be 1–100. Search matches repository,
tag, digest, architecture, and OS, case-insensitively; a trailing `/` selects a
repository prefix. `filter.organization` selects an exact organization prefix.
Sort is `LAST_USED`, `CREATED`, or `SIZE`; direction is `ASC` or `DESC`.
Missing timestamps sort last in either direction. Repository, tag, and digest
break ties deterministically. Byte counts use the `Long` output scalar instead
of GraphQL's 32-bit `Int`.

Cursors are stateless keyset positions bound to the normalized filter and sort.
They work across Web replicas without sticky sessions. Changing filter or sort
requires a new cursor. Requests may use a short-lived shared catalog cache,
not a cursor-pinned snapshot:
concurrent pushes, deletions, or usage updates can move images between pages.
Refresh to restart; the Web console deduplicates displayed repository/tag pairs.

`repositories` also provides a connection with `first`, `after`, and `search`.
`organizations` returns organization metadata and repository counts without
loading image tags. `image(repository:, reference:)` retrieves one manifest.

## Writes

Mutations include `createOrganization`, `updateOrganization`,
`deleteOrganization`, `putTag`, `deleteTag`, `deleteManifest`,
`previewImageGC`, and `executeImageGC`. Existing validation, non-empty
organization protection, digest-only manifest deletion, and shared cleanup-plan
semantics remain in effect. Cleanup execution requires the preview's `planId`
and the exact confirmation string `DELETE`.

## Cost and cancellation

Completed catalog scans are cached in shared rqlite for 30 seconds after scan
completion. The server checks freshness every five seconds and refreshes expired
snapshots in the background, including while the console is idle. A shared
60-second owner lease permits only one background scan across Web replicas;
scans are canceled after 25 seconds and on shutdown. While the worker is active,
browsing can use an expired snapshot for up to two additional minutes. Thus
normal requests do not wait for every periodic refresh. Missing, invalidated,
or older snapshots still require a synchronous scan. Metadata and last-used
timestamps lag by refresh cadence and scan duration; failures may extend that
lag to the stale bound. Send `Cache-Control: no-cache` to force a new scan; the Web
Refresh button does this. Management tag/manifest writes and cleanup execution
invalidate the shared cache, including legacy REST writes. Generation fencing
prevents an older in-flight scan from republishing after invalidation.

The cache is disposable, capped at 4 MiB per registry, and stored in a separate
`catalog_cache_v1` table, with leases in `catalog_refresh_v1`, without changing the durable schema version. Older
Gateway binaries remain compatible. Cache read failures fall back to live
scans; failed or canceled scans are never cached. Cache invalidation failures
are logged and bounded by normal expiry. Cleanup preview/execution always read
live Registry and usage state, never cached browsing data.

Cache misses still scan catalog metadata. A fixed four-worker pool and the
shared four-query semaphore per Web replica bound repository-image reads.
Concurrent misses on one replica wait for its in-flight scan; foreground cold
misses on different replicas can independently scan. Aliases share the same
request-local result. Client cancellation stops queued and active Registry work;
other waiting clients can retry after a canceled leader. Dependency failures
fail the connection rather than publishing incomplete results; stale 404 catalog
entries are empty repositories. The cold-scan latency remains a separate limit.
`Server-Timing` reports catalog-read duration and `shared-cache`, `stale-cache`,
or `live-scan` so browser timings can distinguish backend work from transport.

## Rolling deployment

Deploy the new backend with the previous HTML index to every replica before
activating the new frontend. Retain both generations of hashed assets. Verify
GraphQL queries on every backend before the second phase. After activation,
roll back only to a backend that still supports `/graphql`, since open browser
tabs can keep using the new client. The production build wrapper lives in the
infrastructure repository's `kubernetes/mini-harbor/web-build/` directory.
