# Deploy an internal-only registry

[Home](../README.md)

Choose one route. All three use the published Mini Harbor image.

| Your situation | Guide | Client setup |
| --- | --- | --- |
| You own a domain | [HTTPS with a public certificate](private-registry-domain.md) | Internal DNS or hosts entry; no custom CA |
| No domain, but you want HTTPS | [HTTPS with an internal CA](private-registry-ca.md) | Internal DNS or hosts entry, plus trust the CA |
| You want the quickest LAN setup | [HTTP by IP address](private-registry-http.md) | Docker `insecure-registries` setting |

**A hosts entry fixes name resolution, not certificate trust.** HTTPS encrypts traffic; it does not grant or restrict push permissions. By default, anyone who can reach the Registry endpoint can pull, push, and use its enabled delete API. Web OIDC only protects management operations in the web API. These guides assume trusted LAN clients; see [authentication](authentication.md) if you need additional restrictions.

## Prepare the server

Install Docker and **Docker Compose 2.24.4 or newer**. The overrides use `!override` to replace published ports rather than append to them.

```bash
git clone --depth 1 https://github.com/fanyang89/mini-harbor.git
cd mini-harbor
docker compose version
```

Run every command in these guides from this repository root. Examples use `192.168.1.20` as the server's static LAN IPv4 address; replace it with your own. Merge each guide's variables into `.env`, preserving existing secrets and settings. Generate a registry upload signing secret with `openssl rand -hex 32` and set `REGISTRY_HTTP_SECRET` to that value.

## Keep access internal

- Bind to the server's LAN address, not `0.0.0.0`. The supplied overrides require `LAN_IP` explicitly.
- Do not create router port forwards, public reverse-proxy routes, or public tunnels to this service. Check IPv6 exposure too.
- Use router/VLAN or host firewall rules to allow only your trusted client subnet. HTTPS needs TCP 443; HTTP needs TCP 5000 and 8080 by default. A private bind address alone does not block traffic routed to that address.
- Docker-published ports can bypass ordinary UFW rules. Apply rules at a Docker-aware forwarding layer or upstream firewall; verify from a disallowed network. Do not assume a UFW deny rule protects a published container port.

For HTTPS, the topology is:

```text
LAN clients → Caddy :443 → /v2 and /v2/* → registry-gateway:5000
                        → other paths  → web:8080
```

The HTTPS override removes the base stack's published ports 5000 and 8080. Caddy's admin port and the gateway metrics endpoint are not published. Port 80 is intentionally closed: use `https://` explicitly. Caddy adds a fourth container; the published 52.3 MiB observation excludes this proxy.

## Validate and maintain

Each route ends with a push/pull check. Also open the console and verify the pushed image appears. Test from another LAN machine, and confirm access fails from a network that should be blocked.

**Keep using the same `-f` arguments** for `up`, `pull`, `logs`, `cp`, and `down`. Running only the base `docker compose up` later would restore its original published ports. Keep the same project directory/name to reuse data volumes.

For HTTPS updates, pull `registry registry-gateway web`; pull the official Caddy image for the internal-CA route, or rebuild the custom Caddy image for the DNS route. Then run the same `up -d --no-build` command. Back up application volumes and `caddy-data`: it holds certificates and, for the internal-CA route, the CA private keys. Do not use `down -v` unless intentionally deleting that data.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| `no such host` | DNS/hosts must resolve on the Docker daemon host, not just the CLI machine. Docker Desktop, remote Docker, and k3s/containerd may need their own DNS configuration. |
| `x509: certificate signed by unknown authority` | Install the internal CA on the client/daemon host; restart Docker Desktop or Engine as appropriate. Do not add `insecure-registries` to work around a misconfigured HTTPS route. |
| Certificate name mismatch | Use the hostname in `REGISTRY_HOST`, not its IP, in Docker commands. |
| `server gave HTTP response to HTTPS client` | For the HTTP route, configure the exact `IP:port` in the daemon's `insecure-registries`. |
| Upload redirects to HTTP or an internal hostname | Keep `REGISTRY_HTTP_RELATIVEURLS=true` and forward `/v2/*` unchanged. The supplied Caddy configs preserve Host and set forwarded protocol headers. |
| Large uploads fail | Check any additional proxy's body-size limit, buffering, and timeouts. The supplied Caddy configs stream requests without adding a body-size limit or short upload timeout. |
| DNS certificate issuance fails | Check DNS API permissions, public TXT propagation, and outbound access to the DNS API, resolvers, and ACME service. |

References: https://docs.docker.com/reference/compose-file/merge/ and https://docs.docker.com/engine/network/packet-filtering-firewalls/
