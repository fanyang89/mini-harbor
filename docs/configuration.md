# Configuration

[Home](../README.md) · [Getting started](getting-started.md)

Set Compose variables in `.env` beside `compose.yml`, then apply changes with `docker compose up -d --no-build`. Keep credentials out of version control.

| Variable | Default | Description |
| --- | --- | --- |
| `MINI_HARBOR_IMAGE` | `ghcr.io/fanyang89/mini-harbor:latest` | Application image; accepts a commit tag or digest for pinning |
| `WEB_PORT` | `8080` | Web console host port |
| `REGISTRY_PORT` | `5000` | Registry host port |
| `REGISTRY_PUBLIC_URL` | `localhost:5000` | Registry address shown in pull commands |
| `REGISTRY_HTTP_SECRET` | Local default | Registry upload state signing secret; replace for production |
| `DOCKER_HUB_REMOTE_URL` | `https://registry-1.docker.io` | Upstream or mirror used by the default `docker.io` proxy |
| `PROXY_HTTP_PROXY` | Empty | HTTP proxy used for upstream registry requests |
| `PROXY_HTTPS_PROXY` | Empty | HTTPS proxy used for upstream registry requests |
| `PROXY_NO_PROXY` | `localhost,127.0.0.1,registry` | Hosts that bypass the network proxy |
| `RQLITE_URLS` | `http://rqlite:4001` | Comma-separated HTTP(S) origins for nodes in one rqlite cluster |
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

`IMAGE_USAGE_ROOT` is set directly in `compose.yml`; change its environment and volume mounts there. `RQLITE_URLS` can be overridden in `.env`. Every URL must refer to the same database cluster and have a stable address. The client uses linearizable reads and probes for a quorum-ready endpoint before writes. An uncertain submitted write is not automatically retried.

The default deployment runs one rqlite node. Three voting nodes on separate hosts are needed to tolerate one host failure. The current client targets a trusted private rqlite network; database authentication and custom TLS credentials are not configured by this PoC. Do not expose its API publicly.

Example:

```bash
REGISTRY_PUBLIC_URL=registry.example.com docker compose up -d --no-build
```

## Persistent storage

| Volume | Contents |
| --- | --- |
| `registry-data` | Local registry images and blobs |
| `registry-proxy-data` | Pull-through cache data |
| `mini-harbor-data` | Pull timestamps (and any retained legacy SQLite file) |
| `rqlite-data` | Organization metadata, schema versions, and Raft state |

Registry storage and deletion settings are in `registry/config.yml`. Proxy mappings are in `registry/proxies.yml`.

See [proxy caching](proxy-cache.md), [authentication](authentication.md), and [operations](operations.md) for setup and maintenance details.
