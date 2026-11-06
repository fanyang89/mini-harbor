# Mini Harbor

A compact management console for a self-hosted CNCF Distribution Registry. The stack uses `registry:3`, a Go API, and a React + TypeScript + Vite UI built with shadcn/ui components.

## Features

- Browse repositories and paginated tag catalogs
- Inspect OCI and Docker manifests, sizes, platforms, and image metadata
- Create a tag from an existing manifest
- Overwrite a tag with another manifest
- Delete one tag without affecting other tags
- Delete a manifest and every tag pointing to its digest
- Copy ready-to-run `docker pull` commands
- Browse the console and read its catalog without signing in
- Authenticate management operations with OpenID Connect and PKCE
- Optionally restrict access by an OIDC group claim
- Create, edit, and delete repository organizations
- Persist organization metadata with SQLite and Ent
- Cache images from configurable upstream registries by repository prefix
- Persist registry data in a Docker volume

Image layers are created with the standard Registry push flow. The Web UI creates and updates tags by copying an existing manifest in the same repository.

## Start

```bash
docker compose up -d --build
```

Open <http://localhost:8080>. The Registry API is available at `localhost:5000`.

Push an image:

```bash
docker pull hello-world
docker tag hello-world localhost:5000/hello-world:latest
docker push localhost:5000/hello-world:latest
```

Refresh the Web UI after the push.

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `WEB_PORT` | `8080` | Web console host port |
| `REGISTRY_PORT` | `5000` | Registry host port |
| `REGISTRY_PUBLIC_URL` | `localhost:5000` | Registry address shown in pull commands |
| `REGISTRY_HTTP_SECRET` | Local default | Registry upload state signing secret |
| `DOCKER_HUB_REMOTE_URL` | `https://registry-1.docker.io` | Upstream or mirror used by the default `docker.io` proxy |
| `PROXY_HTTP_PROXY` | Empty | HTTP proxy used for upstream registry requests |
| `PROXY_HTTPS_PROXY` | Empty | HTTPS proxy used for upstream registry requests |
| `PROXY_NO_PROXY` | `localhost,127.0.0.1,registry` | Hosts that bypass the network proxy |
| `DATABASE_PATH` | `/var/lib/mini-harbor/mini-harbor.db` | SQLite database path in the Web container |
| `IMAGE_USAGE_ROOT` | `/var/lib/mini-harbor/image-usage` | Shared directory for manifest pull timestamps |
| `OIDC_ISSUER` | Empty | OIDC issuer URL; setting it enables authentication |
| `OIDC_CLIENT_ID` | Empty | OIDC client ID |
| `OIDC_CLIENT_SECRET` | Empty | OIDC client secret, if required by the provider |
| `OIDC_REDIRECT_URL` | Empty | Absolute callback URL, ending in `/auth/callback` |
| `OIDC_SCOPES` | `openid,profile,email` | Comma-separated OIDC scopes |
| `OIDC_GROUPS_CLAIM` | `groups` | Claim containing the user's groups |
| `OIDC_ALLOWED_GROUPS` | Empty | Comma-separated allowed groups; empty allows every authenticated user |
| `OIDC_SESSION_SECRET` | Empty | Secret of at least 32 bytes used to encrypt session cookies |
| `OIDC_COOKIE_SECURE` | Auto | Whether cookies require HTTPS; defaults to the callback URL scheme |

Example:

```bash
REGISTRY_PUBLIC_URL=registry.example.com docker compose up -d --build
```

Registry storage and deletion settings are in `registry/config.yml`. Registry data is stored in `registry-data`, proxy cache data in `registry-proxy-data`, and organization metadata and pull timestamps in `mini-harbor-data`. Successful manifest `GET` and `HEAD` requests through the gateway update the last-pulled time.

### Prefix proxies

The Registry port routes configured repository prefixes through isolated Distribution pull-through caches. The default mapping pulls Docker Hub images with the `docker.io` prefix:

```bash
docker pull localhost:5000/docker.io/library/alpine:latest
```

Official Docker Hub images require the `library/` namespace. Proxy namespaces are read-only; repositories without a configured prefix continue to use the writable local Registry.

Add or change upstreams in `registry/proxies.yml`:

```yaml
proxies:
  - prefix: docker.io
    remote_url: https://docker-mirror.example.com
    priority: 10
    ttl: 168h
  - prefix: docker.io
    remote_url: https://registry-1.docker.io
    priority: 100
    ttl: 168h
```

Repeat a `prefix` to configure multiple upstream sources. Lower `priority` values are tried first; the default is `100`. Priorities and upstream URLs must be unique within a prefix. The gateway tries the next source when a source is unavailable or returns `404`, `429`, or `5xx`. Each source has an isolated persistent cache.

Each `remote_url` must be an HTTP or HTTPS registry root. Optional `username` and `password` fields enable private upstream access. Anyone who can reach the Registry port can pull cached private content, so protect the port before configuring upstream credentials. Environment variables such as `${DOCKER_HUB_REMOTE_URL}` are expanded when the gateway loads the file. Set `ttl: 0` to disable cache expiration, then restart `registry-gateway` after changing the configuration.

For an outbound network proxy, set `PROXY_HTTP_PROXY` and `PROXY_HTTPS_PROXY`. The hostname `host.docker.internal` resolves to the Docker host; a proxy running there must accept connections from the Docker bridge network.

### Metrics

The Registry gateway exposes Prometheus metrics at `http://localhost:5000/metrics`. The endpoint is unauthenticated, so protect it at the network layer when source metadata is sensitive. It includes Go and process metrics plus:

| Metric | Description |
| --- | --- |
| `mini_harbor_registry_requests_total` | Requests by route, prefix, method, and status |
| `mini_harbor_registry_request_duration_seconds` | Request duration histogram |
| `mini_harbor_registry_requests_in_flight` | Current requests by route and prefix |
| `mini_harbor_registry_proxy_fallbacks_total` | Source fallbacks by reason |
| `mini_harbor_registry_proxy_backend_up` | Local proxy backend process state |
| `mini_harbor_registry_proxy_source_info` | Configured proxy source metadata |

### Organizations

Organizations are independent records backed by SQLite. A repository belongs to an organization when its name starts with `org-name/`:

```bash
docker tag hello-world localhost:5000/platform/hello-world:latest
docker push localhost:5000/platform/hello-world:latest
```

Organization names are immutable. An organization can only be deleted after every repository under its namespace has been removed.

### OpenID Connect

Register this callback URL with the identity provider:

```text
https://registry.example.com/auth/callback
```

Set the required values in `.env` before starting the stack:

```dotenv
OIDC_ISSUER=https://identity.example.com
OIDC_CLIENT_ID=mini-harbor
OIDC_CLIENT_SECRET=replace-with-client-secret
OIDC_REDIRECT_URL=https://registry.example.com/auth/callback
OIDC_SESSION_SECRET=replace-with-at-least-32-random-bytes
OIDC_ALLOWED_GROUPS=registry-admins
```

Generate a session secret with `openssl rand -base64 32`. The login flow uses Authorization Code, PKCE, state, and nonce validation. Sessions are encrypted in HttpOnly, SameSite cookies and expire after eight hours.

OIDC protects mutating management API requests. The Web console and its catalog API are publicly readable; authenticated users in an allowed group can manage tags, images, and organizations. OIDC does not authenticate `docker pull` or `docker push` against the Registry port. Keep the Registry on a trusted network or configure a separate Distribution token service before exposing it.

## Development

Requirements: Go 1.26.10 or later, a C compiler for SQLite, Node.js 24, npm, Docker, and Docker Compose. Install Task to use the commands below.

```bash
docker compose up -d registry
npm install --prefix web
task dev
```

Run all checks:

```bash
task test
```

Regenerate Ent code after changing `internal/ent/schema`:

```bash
go generate ./internal/ent
```

## Security

OIDC is disabled until `OIDC_ISSUER` is set, so the default stack is intended for a trusted local network. Do not expose it directly to the internet. For production, enable OIDC, use HTTPS, keep the Registry port private or separately authenticated, restrict the catalog endpoint, and back up the registry volume.

Deleting a tag only removes that reference. Deleting by digest removes the manifest and all tags that reference it. Unreferenced blobs remain on disk until Registry garbage collection is run during a read-only maintenance window.

## Registry garbage collection

Confirm that a recent, restorable backup or storage snapshot exists before continuing. Stop all registry writers and run Distribution garbage collection against the registry's storage volume during a maintenance window. Start with a dry run and review its output before deleting blobs.

Do not use `--delete-untagged`. Digest-only manifests, multi-architecture child manifests, and OCI artifacts may rely on untagged manifests.

## License

MIT. See `LICENSE`.
