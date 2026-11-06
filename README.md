# Mini Harbor

**Your own container registry, with a lightweight web console and fewer services to run.**

Mini Harbor is for homelabs, personal servers, and small teams that want to store, browse, and cache container images without operating a full Harbor installation. Push images with Docker, manage them in your browser, and cache upstream images locally.

## Why Mini Harbor?

- **Around 52 MiB in a running homelab deployment.** Our 24-hour observation averaged 52.3 MiB across the registry, gateway, and web/API containers. SQLite and a Go backend keep the stack lean, with no separate database server, Redis, or Node.js runtime. Measurement details below.
- **Easy to self-host.** Start with Docker Compose; images, cache data, and metadata live in persistent volumes.
- **Useful image management.** Browse repositories, inspect sizes and platforms, copy pull commands, create or replace tags, and preview stale-image cleanup.
- **Built-in pull-through caching.** Cache Docker Hub and GHCR images by prefix, with configurable upstream priorities and fallback.
- **Simple sign-in.** Optional OpenID Connect login and group restrictions for management operations, with anonymous browsing.
- **A familiar workflow.** Standard Docker push/pull, OCI and Docker manifests, organization namespaces, and an English/Chinese console.

### Measured resource usage

A running k3s deployment observed over the 24 hours ending **2026-11-06 11:22 UTC**:

| Metric | Registry + gateway + web/API |
| --- | --- |
| Average memory working set | **52.3 MiB** |
| Highest sampled memory working set | **60.8 MiB** |
| Average CPU usage | **6.1 millicores** (about **0.6% of one CPU core**) |

These figures come from existing monitoring data, with no synthetic load added: summed container working-set memory and summed 5-minute CPU rates, evaluated at 5-minute intervals over 24 hours. The memory summary includes 287 available samples; the sampled peak can miss shorter spikes. Cluster services, ingress, authentication infrastructure, and disk storage are excluded.

This deployment runs a pre-public-release build, not the latest public commit. The figures describe that deployment's observed workload, not a minimum resource requirement or a capacity benchmark. Usage varies with traffic, image catalog size, and the number of cache sources.

### A smaller stack than Harbor

Mini Harbor focuses on image storage, browsing, and caching rather than Harbor's broader platform features.

**Around 52 MiB observed for Mini Harbor; Harbor documents a 4 GB minimum and recommends 8 GB of memory for deployment.**

| Resource | Mini Harbor: observed 24-hour average | Harbor 2.15: minimum configuration | Harbor 2.15: recommended configuration |
| --- | --- | --- | --- |
| Memory | **52.3 MiB** | **4 GB** | **8 GB** |
| CPU | **0.0061 cores** | **2 CPUs** | **4 CPUs** |

Mini Harbor's column measures application container usage in the deployment described above. Harbor's columns are published deployment resource requirements, **not measured usage**. This gives sizing context, not a like-for-like performance benchmark or a measured percentage saving.

Source: Harbor 2.15 installation prerequisites, accessed 2026-11-06: https://goharbor.io/docs/2.15.0/install-config/installation-prereqs/

**3 containers instead of 9 — one-third the container count of Harbor's standalone stack.** This comparison uses Harbor 2.15.0 with bundled PostgreSQL and Redis, without the optional scanner or metrics exporter. Container count is not a RAM or CPU usage ratio.

| Runtime component | Mini Harbor | Harbor |
| --- | --- | --- |
| Metadata storage | Embedded SQLite | PostgreSQL service |
| Redis | Not required | Part of the standard stack |
| Web console | Static files served by the Go backend | Separate portal service |
| Background job service | No separate job service | Jobservice |
| Vulnerability scanning and replication | Not included | Available |

The default Compose stack has **three containers**: the registry, the cache gateway, and the web/API service. The gateway also runs a Distribution cache process for each configured upstream source. Go and Node.js build tools are used during the image build, not at runtime.

Fewer always-on services keep the stack lean. No running Harbor deployment was available in the observed cluster for a like-for-like RAM or CPU comparison.

Harbor comparison source: https://github.com/goharbor/harbor/blob/v2.15.0/make/photon/prepare/templates/docker_compose/docker-compose.yml.jinja

Choose Mini Harbor for a focused registry with a UI and cache. Choose Harbor when you need features such as project-level RBAC, integrated vulnerability scanning, or replication policies. Mini Harbor is an independent project, not an official Harbor distribution.

## Quick start

You need Git, Docker, and Docker Compose. The build runs inside Docker; you do not need Go or Node.js installed on the host.

The default configuration is for a trusted local network. Management operations are unauthenticated until OIDC is enabled; the Registry port needs its own access controls for use outside that network.

```bash
git clone https://github.com/fanyang89/mini-harbor.git
cd mini-harbor
docker compose up -d --build
```

Open http://localhost:8080 to browse your images. Push and pull through `localhost:5000`.

### Push your first image

```bash
docker pull hello-world
docker tag hello-world localhost:5000/hello-world:latest
docker push localhost:5000/hello-world:latest
```

Refresh the console to see the image, inspect its tags, and copy a pull command. New image content is uploaded with `docker push`; the console creates or replaces tags by copying an existing manifest in the same repository.

### Cache an upstream image

```bash
docker pull localhost:5000/docker.io/library/alpine:latest
```

Use `docker.io/` or `ghcr.io/` before the upstream image name. Docker Hub official images also need `library/`. Cached content is kept in a persistent volume for subsequent pulls.

### Use another port or hostname

Set these values in a `.env` file beside `compose.yml`:

```dotenv
WEB_PORT=8080
REGISTRY_PORT=5000
REGISTRY_PUBLIC_URL=registry.example.com:5000
```

`REGISTRY_PUBLIC_URL` changes the address shown in pull commands; it does not configure DNS, TLS, or authentication. For access from another machine, use your host's address instead of `localhost`. Docker requires HTTPS for remote registries unless you explicitly configure that address as an insecure registry for trusted-network use.

### Stop or update

```bash
# Stop the stack, keeping its data volumes.
docker compose down

# Update the source and rebuild.
git pull --ff-only
docker compose up -d --build
```

Back up the persistent volumes before updating. Avoid `docker compose down -v` unless you intend to delete the registry, cache, and metadata volumes.

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

Use organizations to group repositories in the console. A repository belongs to an organization when its name starts with `org-name/`:

```bash
docker tag hello-world localhost:5000/platform/hello-world:latest
docker push localhost:5000/platform/hello-world:latest
```

Organization names are immutable. An organization can only be deleted after every repository under its namespace has been removed. Organizations group images; they do not provide per-organization access control.

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

## Security

OIDC is disabled until `OIDC_ISSUER` is set, so the default stack is intended for a trusted local network. Do not expose it directly to the internet. For production, enable OIDC, use HTTPS, keep the Registry port private or separately authenticated, restrict the catalog endpoint, and back up the registry volume.

Deleting a tag only removes that reference. Deleting by digest removes the manifest and all tags that reference it. Unreferenced blobs remain on disk until Registry garbage collection is run during a read-only maintenance window.

## Registry garbage collection

Confirm that a recent, restorable backup or storage snapshot exists before continuing. Stop all registry writers and run Distribution garbage collection against the registry's storage volume during a maintenance window. Start with a dry run and review its output before deleting blobs.

Do not use `--delete-untagged`. Digest-only manifests, multi-architecture child manifests, and OCI artifacts may rely on untagged manifests.

## Development

For local development and test instructions, see `docs/development.md`.

## License

MIT. See `LICENSE`.
