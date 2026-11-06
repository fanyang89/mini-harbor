# Internal HTTPS without owning a domain

[Choose a deployment route](private-registry.md)

Use `registry.home.arpa`, a hosts entry or internal DNS, and Caddy's internal CA. No domain purchase or public certificate service is needed. Each client must trust the CA.

## 1. Configure the server and name resolution

Complete the [server and network preparation](private-registry.md#prepare-the-server). Merge into `.env`:

```dotenv
LAN_IP=192.168.1.20
REGISTRY_HOST=registry.home.arpa
CADDY_IMAGE=caddy:2-alpine
CADDY_CONFIG=./docs/examples/lan/Caddyfile.internal
```

On each client, add this line to `/etc/hosts` (Linux/macOS) or `C:\Windows\System32\drivers\etc\hosts` (Windows, administrator privileges):

```text
192.168.1.20 registry.home.arpa
```

Alternatively, configure an internal DNS record. Docker Desktop and remote Docker daemons must also resolve the name; if a host-only entry is not visible to the daemon, use internal DNS. Use this hostname, not the IP address, in HTTPS and Docker commands.

## 2. Start and export the CA certificate

```bash
docker compose -f compose.yml -f docs/examples/lan/compose.https.yml pull
docker compose -f compose.yml -f docs/examples/lan/compose.https.yml up -d --no-build
docker compose -f compose.yml -f docs/examples/lan/compose.https.yml logs --tail=100 caddy
docker compose -f compose.yml -f docs/examples/lan/compose.https.yml cp caddy:/data/caddy/pki/authorities/local/root.crt ./mini-harbor-root.crt
openssl x509 -in mini-harbor-root.crt -noout -fingerprint -sha256
```

Wait for Caddy to create its CA before copying the certificate. Transfer **only `root.crt`** to clients over a trusted channel and verify its fingerprint with the server administrator. Do not distribute the CA private key. Caddy automatically renews leaf certificates; preserve `caddy-data` so clients do not need to trust a new CA after recreation.

## 3. Trust the CA on clients

### Linux Docker Engine

On the Docker daemon host, using the exported certificate:

```bash
sudo mkdir -p /etc/docker/certs.d/registry.home.arpa
sudo cp mini-harbor-root.crt /etc/docker/certs.d/registry.home.arpa/ca.crt
```

Use `.crt`, not `.cert`. Because the image reference omits a port (default HTTPS 443), the directory name has no `:443`. For rootless Docker, use `$XDG_CONFIG_HOME/docker/certs.d/registry.home.arpa` (default `~/.config/docker/certs.d/registry.home.arpa`).

For browser and system tools on Debian/Ubuntu:

```bash
sudo cp mini-harbor-root.crt /usr/local/share/ca-certificates/mini-harbor.crt
sudo update-ca-certificates
```

On Fedora/RHEL, copy it to `/etc/pki/ca-trust/source/anchors/mini-harbor.crt` and run `sudo update-ca-trust`. Restart the browser; browsers with a separate certificate store may need an explicit CA import. Restart Docker if it still uses the old trust settings (`sudo systemctl restart docker`; this can interrupt containers). For rootless Engine use `systemctl --user restart docker`.

### Docker Desktop on macOS

Import the certificate into **Keychain Access → System → Certificates**. Open it and set **Trust → Always Trust**. Restart Docker Desktop and the browser.

### Docker Desktop on Windows (Linux containers)

Import the certificate into **Local Computer → Trusted Root Certification Authorities** using the Windows Certificates console. Restart Docker Desktop and the browser. WSL distributions or separately running daemons may require their own CA and DNS setup.

## 4. Verify from a LAN client

```bash
curl --fail --show-error https://registry.home.arpa/v2/
docker pull hello-world
docker tag hello-world registry.home.arpa/demo/hello-world:latest
docker push registry.home.arpa/demo/hello-world:latest
docker pull registry.home.arpa/demo/hello-world:latest
docker pull registry.home.arpa/docker.io/library/alpine:latest
```

Expect `{}` from `/v2/` and a working console at https://registry.home.arpa. For a direct CA check before installing system trust, use `curl --cacert mini-harbor-root.crt https://registry.home.arpa/v2/`. Do not disable verification with `-k` or `insecure-registries` for this route.

If enabling [OIDC](authentication.md), register `https://registry.home.arpa/auth/callback` with the provider and set `OIDC_REDIRECT_URL` accordingly. The provider must permit this callback hostname.

Sources: https://docs.docker.com/engine/security/certificates/ and https://docs.docker.com/engine/network/ca-certs/
