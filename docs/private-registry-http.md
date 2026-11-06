# Quick LAN deployment over HTTP

[Choose a deployment route](private-registry.md)

This route needs no domain, hosts entry, or certificate. Traffic is unencrypted, so use it only on a trusted network. Do not send registry passwords or sensitive images over this route; use [internal-CA HTTPS](private-registry-ca.md) when confidentiality is required.

## 1. Start the server

Complete the [server and network preparation](private-registry.md#prepare-the-server). Merge into `.env`:

```dotenv
LAN_IP=192.168.1.20
REGISTRY_PORT=5000
WEB_PORT=8080
```

```bash
docker compose -f compose.yml -f docs/examples/lan/compose.http.yml pull
docker compose -f compose.yml -f docs/examples/lan/compose.http.yml up -d --no-build
```

Open http://192.168.1.20:8080. The Registry address is `192.168.1.20:5000`. The override binds both ports to that LAN IP and sets the address shown in the console.

## 2. Configure each Docker client

Configure the **Docker daemon that performs the pull/push**, including remote daemon hosts. Merge the following entry into its existing configuration; preserve other keys and existing registry entries:

```json
{
  "insecure-registries": ["192.168.1.20:5000"]
}
```

- **Linux Engine:** edit `/etc/docker/daemon.json`, validate with `sudo dockerd --validate --config-file=/etc/docker/daemon.json`, then run `sudo systemctl restart docker`.
- **Rootless Linux:** edit `~/.config/docker/daemon.json` (or `$XDG_CONFIG_HOME/docker/daemon.json`), then run `systemctl --user restart docker`.
- **Docker Desktop:** merge the JSON in **Settings → Docker Engine**, then **Apply & restart**.

Restarting Docker may interrupt running containers. Verify the exact `IP:port` appears under `Insecure Registries` in `docker info`. Do not add a subnet or `http://` prefix.

## 3. Verify

```bash
curl --fail --show-error http://192.168.1.20:5000/v2/
docker pull hello-world
docker tag hello-world 192.168.1.20:5000/demo/hello-world:latest
docker push 192.168.1.20:5000/demo/hello-world:latest
docker pull 192.168.1.20:5000/demo/hello-world:latest
docker pull 192.168.1.20:5000/docker.io/library/alpine:latest
```

Expect `{}` from the first request, then refresh the console to see the new image. No `docker login` is needed for this default unauthenticated stack.

When moving to HTTPS, remove this insecure-registry entry, configure hostname resolution and certificate trust, restart Docker, and retag images with the new registry address. If switching from the HTTPS Compose route to this one, remove the old Caddy container with `up -d --no-build --remove-orphans` using these same HTTP `-f` arguments; keep its data volume if you intend to reuse its CA.

Source: https://docs.docker.com/reference/cli/dockerd/#insecure-registries
