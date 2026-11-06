# Proxy caching

[Home](../README.md) · [Configuration](configuration.md)

The Registry port routes configured repository prefixes through isolated Distribution pull-through caches. The default configuration includes Docker Hub and GHCR.

```bash
docker pull localhost:5000/docker.io/library/alpine:latest
```

Official Docker Hub images require the `library/` namespace. Proxy namespaces are read-only; repositories without a configured prefix continue to use the writable local Registry.

## Configure upstreams

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

Each `remote_url` must be an HTTP or HTTPS registry root. Environment variables such as `${DOCKER_HUB_REMOTE_URL}` are expanded when the gateway loads the file. Set `ttl: 0` to disable cache expiration.

After editing the proxy file:

```bash
docker compose restart registry-gateway
```

## Private upstreams

Optional `username` and `password` fields enable private upstream access. Anyone who can reach the Registry port can pull cached private content, so protect the port before configuring upstream credentials. OIDC sign-in for the web console does not protect this port.

## Outbound network proxy

Set `PROXY_HTTP_PROXY` and `PROXY_HTTPS_PROXY`, and use `PROXY_NO_PROXY` for destinations that bypass the proxy. Apply environment changes with `docker compose up -d`.

The hostname `host.docker.internal` resolves to the Docker host; a proxy running there must accept connections from the Docker bridge network.
