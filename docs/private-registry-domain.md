# Internal HTTPS with your own domain

[Choose a deployment route](private-registry.md)

This route uses a publicly trusted certificate obtained with **DNS-01**. The registry stays on your LAN: no public inbound port is required. Caddy needs outbound access for certificate issuance and renewal. Certificate transparency logs reveal the certificate hostname.

## 1. Configure DNS and credentials

Complete the [server and network preparation](private-registry.md#prepare-the-server). Pick a hostname under a domain you control, such as `registry.example.com`. Configure your internal DNS to resolve it to `192.168.1.20`, or add a hosts entry on each client. The public DNS zone must be able to publish ACME TXT records; it does not need a public A/AAAA record for the registry.

The runnable example uses Cloudflare authoritative DNS. Create a scoped API token with **Zone:DNS:Edit** and **Zone:Zone:Read**, restricted to your domain's zone. Keep it in `.env`, never in the Caddyfile or Git. If a Cloudflare A record is used, use DNS-only mode rather than its public HTTP proxy.

Merge into `.env`:

```dotenv
LAN_IP=192.168.1.20
REGISTRY_HOST=registry.example.com
CADDY_IMAGE=mini-harbor-caddy-cloudflare:local
CADDY_CONFIG=./docs/examples/lan/Caddyfile.dns
CF_API_TOKEN=replace-with-your-scoped-token
```

## 2. Build the DNS-enabled proxy and start

The official Caddy image does not include the Cloudflare DNS module. Build the supplied proxy image locally; the application itself is still pulled prebuilt:

```bash
docker build -f docs/examples/lan/Dockerfile.caddy -t mini-harbor-caddy-cloudflare:local .
docker compose -f compose.yml -f docs/examples/lan/compose.https.yml pull registry registry-gateway web
docker compose -f compose.yml -f docs/examples/lan/compose.https.yml up -d --no-build
docker compose -f compose.yml -f docs/examples/lan/compose.https.yml logs --tail=100 caddy
```

Wait for successful certificate issuance. Caddy renews certificates automatically while its data volume, DNS credentials, and outbound access remain available. For a different DNS provider, replace the module in `Dockerfile.caddy` and its `tls` configuration using that provider's documentation.

If the proxy build cannot reach `proxy.golang.org`, add `--build-arg GOPROXY=https://goproxy.cn,direct` to the `docker build` command, or use your organization's Go module proxy. Keep Go checksum verification enabled.

## 3. Verify from a LAN client

```bash
curl --fail --show-error https://registry.example.com/v2/
docker pull hello-world
docker tag hello-world registry.example.com/demo/hello-world:latest
docker push registry.example.com/demo/hello-world:latest
docker pull registry.example.com/demo/hello-world:latest
docker pull registry.example.com/docker.io/library/alpine:latest
```

The first request should return `{}`. Do not use `curl -k` or Docker `insecure-registries`: clients should trust the public certificate normally. Open https://registry.example.com for the console.

For [OIDC](authentication.md), set `OIDC_REDIRECT_URL=https://registry.example.com/auth/callback` and register that exact URL with your provider. The override sets the address shown in pull commands to `REGISTRY_HOST`.

Sources: https://caddyserver.com/docs/automatic-https#dns-challenge and https://github.com/caddy-dns/cloudflare
