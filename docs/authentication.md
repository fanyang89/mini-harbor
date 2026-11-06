# Authentication and access

[Home](../README.md) · [Configuration](configuration.md)

## Access model

OIDC is disabled until `OIDC_ISSUER` is set. The default stack is intended for a trusted local network.

- The web console and catalog API are publicly readable.
- With OIDC enabled, management operations require an authenticated user and any configured group membership.
- OIDC does not authenticate `docker pull` or `docker push` against the Registry port.
- Organizations do not provide per-organization access control.

For production, use HTTPS, enable OIDC, and keep the Registry port private or configure a separate Distribution token service. Restrict console/catalog access at the network or reverse-proxy layer if image names and metadata should be private. Protect the gateway's unauthenticated `/metrics` endpoint as needed.

## Configure OpenID Connect

Register this callback URL with your identity provider:

```text
https://registry.example.com/auth/callback
```

Set the required values in `.env` beside `compose.yml`:

```dotenv
OIDC_ISSUER=https://identity.example.com
OIDC_CLIENT_ID=mini-harbor
OIDC_CLIENT_SECRET=replace-with-client-secret
OIDC_REDIRECT_URL=https://registry.example.com/auth/callback
OIDC_SESSION_SECRET=replace-with-at-least-32-random-bytes
OIDC_ALLOWED_GROUPS=registry-admins
```

Generate a session secret with `openssl rand -base64 32`. Supply a client secret if your provider requires one. Ensure the provider includes the group claim used by your access rule; adjust `OIDC_SCOPES` and `OIDC_GROUPS_CLAIM` as needed. An empty `OIDC_ALLOWED_GROUPS` allows every authenticated user.

Apply the settings:

```bash
docker compose up -d --no-build
```

The login flow uses Authorization Code, PKCE, state, and nonce validation. Sessions are encrypted in HttpOnly, SameSite cookies and expire after eight hours. Secure cookies default to the callback URL scheme; use HTTPS for production.
