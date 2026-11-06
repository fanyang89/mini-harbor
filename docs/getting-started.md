# Getting started

For a registry shared with other LAN machines, start with the [internal-only deployment guide](private-registry.md), including HTTPS options with or without your own domain.

[Home](../README.md) · [Configuration](configuration.md)

## Install

You need Git, Docker, and Docker Compose. Prebuilt images support Linux `amd64` and `arm64`; Go and Node.js are not required on the host.

```bash
git clone https://github.com/fanyang89/mini-harbor.git
cd mini-harbor
docker compose pull
docker compose up -d --no-build
```

Open http://localhost:8080 to browse your images. Push and pull through `localhost:5000`.

The application image is `ghcr.io/fanyang89/mini-harbor:latest`. Each published main-branch commit also has a `sha-<full-commit-sha>` tag. To pin a version, set `MINI_HARBOR_IMAGE` in `.env` to that tag or an image digest.

To build from source instead:

```bash
MINI_HARBOR_IMAGE=mini-harbor:local docker compose up -d --build
```

The default configuration is for a trusted local network. Management operations are unauthenticated until OIDC is enabled; the Registry port needs separate access controls. See [authentication](authentication.md) before exposing the service.

## Push your first image

```bash
docker pull hello-world
docker tag hello-world localhost:5000/hello-world:latest
docker push localhost:5000/hello-world:latest
```

Refresh the console to see the image, inspect its tags, and copy a pull command. New image content is uploaded with `docker push`; the console creates or replaces tags by copying an existing manifest in the same repository.

## Cache an upstream image

```bash
docker pull localhost:5000/docker.io/library/alpine:latest
```

Use `docker.io/` or `ghcr.io/` before the upstream image name. Docker Hub official images also need `library/`. Cached content is kept in a persistent volume for subsequent pulls. See [proxy caching](proxy-cache.md) to configure upstreams.

## Use another port or hostname

Set these values in a `.env` file beside `compose.yml`, then run `docker compose up -d --no-build` again:

```dotenv
WEB_PORT=8080
REGISTRY_PORT=5000
REGISTRY_PUBLIC_URL=registry.example.com:5000
```

`REGISTRY_PUBLIC_URL` changes the address shown in pull commands; it does not configure DNS, TLS, or authentication. For access from another machine, use your host's address instead of `localhost`. Docker requires HTTPS for remote registries unless you explicitly configure that address as an insecure registry for trusted-network use.

For stopping, updating, and backing up the stack, see [operations](operations.md).
