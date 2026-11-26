# SSO / OIDC for the entrypoint

The Agent Mesh entrypoint implements **native, in-process OIDC**. There is no `oauth2-proxy` sidecar and no `oauth2_config.yaml` (those are Python Agent Mesh). You configure a **providers catalog** in the entrypoint runtime YAML; the entrypoint owns the login and callback routes itself (callback is `/api/v1/auth/callback`).

## The providers catalog

A top-level `providers:` block maps named IdPs to their config. Each entry:

```yaml
providers:
  keycloak:                                  # the catalog key = the provider name
    issuer: "${OIDC_ISSUER}"                 # required — e.g. https://kc.example.com/realms/myrealm
    client_id: "${OIDC_CLIENT_ID}"           # required
    client_secret: "${OIDC_CLIENT_SECRET}"   # required (env-sourced)
    redirect_uri: "${OIDC_REDIRECT_URI, }"   # optional — derived from frontend_server_url if omitted
    scopes: "${OIDC_SCOPES:-openid email profile offline_access}"   # optional (defaults shown); a list or a space/comma-separated string
    ca_cert_path: "${OIDC_CA_CERT_PATH, }"   # optional — PEM bundle for a private-CA IdP
    insecure_skip_verify: false              # optional — DEV/LAB ONLY; disables TLS verification
    dev_mode: false                          # optional — DEV ONLY; allows http://localhost login
```

YAML keys stay snake_case (operator-written YAML convention). Secrets are `${VAR}` references, never literals.

**Extra scopes for the group claim.** Some IdPs return the group claim only when login requests a dedicated scope, commonly `groups`. Set `OIDC_SCOPES` to the full list — it **replaces** the defaults, so keep `openid` — e.g. `OIDC_SCOPES="openid email profile offline_access groups"`. The shipped catalog reads it from Agent Mesh 2.569.0 (an earlier image silently ignores it). On Helm, use chart 2.4.1 or later (chart 2.4.0 has the key but installs 2.568.3, which ignores it) and set the `sam.oauthProvider.oidc.scopes` list (the chart renders it as `OIDC_SCOPES` and rejects a list without `openid`); on a chart without that key, set `OIDC_SCOPES` under `environmentVariables`, never both. A `scopes` value that is neither a list nor a string, or yields no scopes, falls back to the defaults with a WARN; a `scopes` value (list or string) without `openid` is used as written but WARN-logged: with no id_token, login either fails at the userinfo call (IdPs such as Keycloak require `openid` there) or loses id_token-only claims such as groups. List elements split on whitespace like the wire form, so `["openid email"]` equals `["openid", "email"]`. Only catalogs whose `scopes` reads `${OIDC_SCOPES:-…}` (the shipped one) honour the variable; a hand-written catalog with a literal list ignores it.

## Selecting the active provider

- **One entry** in the catalog → auto-selected.
- **More than one** → the entrypoint's `app_config` must set `external_auth_provider` to a catalog key; a missing/mismatched name fails at startup with an error listing the available providers.
- `external_auth_callback_uri` (per-app) overrides the entry's `redirect_uri`, which overrides the value derived from `frontend_server_url` + `/api/v1/auth/callback`.
- A catalog entry missing `issuer` or `client_id` is **dropped with a WARN, not a hard failure** — a mistyped entry silently disappears and login just won't offer that provider. If a provider "isn't there," check the entrypoint logs for the drop warning before anything else.

## Session secret

OIDC requires a signing key for the session/state cookie: `session_secret_key` in YAML or `SESSION_SECRET_KEY` env (generate with `openssl rand -hex 32`). `dev_mode: true` drops the cookie's `Secure` flag so plain `http://localhost` works for local testing — never in production.

## Session lifetime

`sam_access_token` in the entrypoint `app_config` controls how long a login lasts:

```yaml
sam_access_token:
  ttl_seconds: 3600            # one access token's lifetime (default 1 h)
  idle_timeout_seconds: 14400  # sign out after this much inactivity (default 4 h; 0 disables)
```

The UI renews the access token in the background, so `ttl_seconds` alone never ends a session. `idle_timeout_seconds` does: the UI logs out a tab with no keyboard or pointer activity for the window, and the entrypoint refuses to renew any token issued longer ago than the window (tokens are also capped to it). The shipped entrypoint config reads the value from `SESSION_IDLE_TIMEOUT_SECONDS`. The idle timeout ends the Agent Mesh session only — an IdP session that is still valid signs the user back in on the next **Sign in** without a prompt; use the IdP's sign-in-frequency policy for that. The `sam` CLI's cached login follows the same window and asks for `sam auth login` again once refused.

## Self-signed / private-CA IdPs

Two fields control TLS on the entrypoint's calls to the IdP (discovery, token exchange, userinfo, refresh):

- `ca_cert_path` — path to a PEM file with the CA bundle. Use this for a private-CA IdP; it keeps verification on. (Mounted file path — fine to name; it's the customer's own file.)
- `insecure_skip_verify: true` — disables hostname/chain checks entirely. **Dev/lab only** — it exposes tokens to MITM on the IdP connection.

## IdP-side setup (provider-agnostic)

Create a **confidential** client, set its valid redirect URI to your entrypoint's `<external-base>/api/v1/auth/callback`, and configure the claim you'll map to roles (commonly `groups`) — that claim feeds the RBAC `idp_claims` provider (see [rbac.md](rbac.md)).

## Docs note

There **is** a published **Enabling Single Sign-On (SSO)** page on the docs site, and it describes the Go architecture — in-process OIDC login with no separate auth proxy — covering IdP registration, redirect URIs, the provider catalog and the login flow. Point operators there, and treat this reference as the verified config surface for shapes that page doesn't spell out. Don't link the older Python SSO material, and don't invent a doc URL. The shipped enterprise entrypoint example config (which the customer receives with their distribution) is the working template to copy from.
