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
requires a new cursor. Each request reads live state, not a pinned snapshot:
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

Pagination limits browser payloads, not the Registry metadata work needed for
global sorting/search. Each image connection request currently scans catalog
metadata. A fixed four-worker pool and the shared four-query semaphore per Web
replica bound repository-image reads. Aliases and sibling fields share one
request-local catalog scan; no persistent index or cross-request cache is used.
Client cancellation propagates to queued and active Registry work. A dependency
failure fails the connection instead of publishing an incomplete global result;
stale 404 catalog entries are treated as empty repositories.

## Rolling deployment

Deploy the new backend with the previous HTML index to every replica before
activating the new frontend. Retain both generations of hashed assets. Verify
GraphQL queries on every backend before the second phase. After activation,
roll back only to a backend that still supports `/graphql`, since open browser
tabs can keep using the new client. The production build wrapper lives in the
infrastructure repository's `kubernetes/mini-harbor/web-build/` directory.
