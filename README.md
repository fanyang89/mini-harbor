# Mini Harbor

**A lightweight container registry with a web UI and pull-through cache.**

Store, browse, and cache container images on your homelab or small-team server. Standard Docker push/pull, a compact Go backend, and SQLite — no separate database server or Redis.

![Mini Harbor image catalog with search, tags, sizes, and cleanup actions](docs/images/console.png)

*Actual console rendered with example data.*

## Why Mini Harbor?

- **Small footprint:** 52.3 MiB average memory across all three application containers in an observed k3s deployment. See [resource usage](docs/resource-usage.md) for measurement scope and Harbor sizing context.
- **Easy to run:** three containers, Docker Compose, persistent data volumes.
- **Image management:** inspect manifests and platforms, copy pull commands, manage tags, and preview stale-image cleanup.
- **Pull-through caching:** Docker Hub, GHCR, configurable upstream priorities, and fallback.
- **Simple access:** optional OIDC login and group restrictions for management, with anonymous browsing.
- **English and Chinese UI:** organization filters and a familiar Docker workflow.

## Deploy with Docker Compose

The public image **`ghcr.io/fanyang89/mini-harbor:latest`** supports Linux **amd64** and **arm64**. No registry login, Go, Node.js, or local image build is required.

Install Git, Docker, and Docker Compose. Clone the repository for its Compose file and registry configuration, then pull and start the published images:

```bash
git clone --depth 1 https://github.com/fanyang89/mini-harbor.git
cd mini-harbor
docker compose pull
docker compose up -d --no-build
```

Open http://localhost:8080. The stack runs the registry, cache gateway, and web console, with data stored in persistent Docker volumes.

Push your first image:

```bash
docker pull hello-world
docker tag hello-world localhost:5000/hello-world:latest
docker push localhost:5000/hello-world:latest
```

The defaults are for a trusted local network. Before exposing the service, configure [authentication and network access](docs/authentication.md).

`latest` follows tested builds of `main`. For a fixed version, set `MINI_HARBOR_IMAGE` in `.env` to a `ghcr.io/fanyang89/mini-harbor:sha-<full-commit-sha>` tag or image digest. See [getting started](docs/getting-started.md) for port settings and [operations](docs/operations.md) for updates.

## Documentation

| Guide | What it covers |
| --- | --- |
| [Getting started](docs/getting-started.md) | Installation, first push, hostnames, and ports |
| [Using the console](docs/usage.md) | Image details, tags, organizations, and screenshots |
| [Proxy caching](docs/proxy-cache.md) | Upstreams, fallback, credentials, and outbound proxies |
| [Configuration](docs/configuration.md) | Environment variables and persistent storage |
| [Authentication](docs/authentication.md) | OIDC, group restrictions, and Registry access |
| [Operations](docs/operations.md) | Updates, backups, garbage collection, and metrics |
| [Resource usage](docs/resource-usage.md) | Observed footprint and comparison with Harbor |
| [Development](docs/development.md) | Local development and checks |

Mini Harbor is an independent project, not an official Harbor distribution. It focuses on image storage, browsing, and caching; it does not include project-level RBAC, vulnerability scanning, or replication policies.

## License

MIT. See [LICENSE](LICENSE).
