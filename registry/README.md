# Bundled Distribution registry

The application image builds Distribution 3.1.1 from upstream commit
`9a8d98b679740cd514aa7e7d84d23d442a5ef54c`, with a checksum-pinned archive.
`_distribution/` is a source overlay compiled and tested inside that upstream
module, not the Mini Harbor module. Go's `./...` pattern ignores underscore
directories; `task test:registry` exercises it explicitly, and `task test` includes
that check. Set `DISTRIBUTION_ARCHIVE` to reuse a downloaded archive; its checksum
is always verified.

## Ordered, batched S3 metadata walks

Garage returns an empty result for Distribution's initial `StartAfter=registry`
when that marker lies outside the requested prefix. Omitting that marker alone
is insufficient: raw S3 keys order `foo-bar/` before `foo/`, while Distribution's
catalog/tag pagination uses component-wise directory order. Paginated results
can therefore omit names.

The upstream `WalkFallback` directory walker is correct but performs serial
List/HEAD/Stat requests for each entry. This caused approximately 18-second
catalog and organization-count requests with 188 repositories in the HA cluster.

The overlay instead:

1. Fetches object metadata through paginated `ListObjectsV2`, without StartAfter.
2. Infers directories and sorts all entries using the Registry's component-wise
   ordering, including parents before their children.
3. Applies the pagination hint and callback skip/stop rules in that order.

This needs one S3 request per 1,000 objects, not per directory or file. Metadata
is fetched anew on each request: there is no stale cache used for cleanup or
organization deletion. A failed page publishes no partial callback results.
No object contents, credentials, schema or storage layout change.

Temporary metadata is bounded at 100,000 inferred entries. Larger walks use the
existing correct directory walker before any callback runs; those requests can
still be slow. The cap applies to files plus directories, not only objects.
Directory-marker objects are retained as directories. SkipDir matches a path
boundary, so skipping `foo` never skips `foo-bar`.

The standalone `registry:3` service in the filesystem-backed Compose example
is unchanged. S3 deployments must use the patched `/bin/registry` build target
or `/usr/local/bin/registry` in the application image to benefit from the overlay.

## Read-only acceptance — 2026-11-09

Two isolated local Registry instances used the same read-only Garage bucket key,
the same endpoint and the same 188-repository dataset. Production routing and
objects were unchanged. Three full-catalog requests per instance measured:

| Implementation | Samples (seconds) | Median |
| --- | --- | --- |
| Previous directory walker | 19.188, 19.717, 18.706 | 19.188 s |
| Ordered bulk metadata walker | 0.350, 0.397, 0.383 | 0.383 s |

The catalog responses were identical, with approximately 50x lower median
latency. Paginated candidate reads preserved all 186 frozen repositories and
357 frozen tags across 151 repositories; the two additional repositories are
HA validation fixtures. All 125 frozen live image/OCI manifest digests matched.
These are Registry measurements, not browser page timings. The separate final
probe of the unchanged production organization API returned HTTP 502; it was
not part of the candidate instance and is not claimed as successful acceptance.

`task test` passed backend tests, overlay race tests, frontend type checks and
the frontend build. The Docker `distribution` target also built successfully.
The subsequent approved production rollout used image `registry-fb1cbdd`, digest
`sha256:c286511decef3bad2cbba9589f7c73a1cf9f7e0ec97d715ff07a429521fb8cff`.
All three Registry replicas became Ready. Five HTTPS samples per endpoint
returned HTTP 200, with medians of 1.177 seconds for the catalog, 1.079 seconds
for the organization API and 0.014 seconds for homepage HTML. These include the
production ingress and S3 proxy path; they are not full browser-render timings.
Post-rollout pagination and all 125 manifest checks passed. The earlier 502 did
not recur in those organization samples.
