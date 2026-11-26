# CLI authentication reference

`sam auth login` is the interactive way to authenticate the CLI to an Agent Mesh
platform. It performs an OAuth 2.0 Authorization Code flow with PKCE, using
a kernel-assigned loopback redirect (RFC 8252), and caches the resulting
Agent Mesh access token at `$XDG_CONFIG_HOME/sam/auth/<target>.json` (file mode `0600`,
parent dir `0700`). On Linux that resolves to `~/.config/sam/auth/`, on macOS
to `~/Library/Application Support/sam/auth/`.

Subsequent `sam config apply` / `sam config plan` / `sam config pull` /
`sam eval run` invocations read the cached token transparently when the
manifest's target declares `auth: { type: oauth }`.

## Manifest target shape

```yaml
kind: manifest
name: my-app
target:
  name: dev                                  # cache key (defaults to URL host)
  url:  https://platform.dev.example.com
  auth:
    type: oauth
resources:
  agents:
    - my-agent
```

`target.name` is the cache key. Two manifests pointing at the same platform
but using different `name` values produce two independent
`$XDG_CONFIG_HOME/sam/auth/<name>.json` caches — useful for per-persona logins. When
`target.name` is absent, the URL host is used as the implicit cache key.
When `target.url` is absent, `target.name` also supplies the address: the
running desktop app for `desktop`, otherwise the URL cached by
`sam auth login <name>`. This holds for every command that accepts
`-m/--manifest` (`sam config`, `sam eval`, `sam api-key`, `sam task send`,
`sam api`), and `--url` / `--target` replace only the address, never the
manifest's `auth` block.

## First-time login

`sam auth login` takes a single optional positional argument and is
HTTPS-only — `http://` URLs are rejected and bare hostnames are always
upgraded to `https://`.

```sh
sam auth login platform.dev.example.com         # https implied; cache = the host
sam auth login dev --url https://platform...    # first-time named login
sam auth login https://platform.dev.example.com # explicit https URL
```

## Refresh of an existing login

After the first login, subsequent invocations need no flags — pass the
positional that names the target you want to re-authenticate, or omit it
entirely when there's exactly one cached target:

```sh
sam auth login platform.dev.example.com         # refresh by hostname
sam auth login dev                              # refresh existing "dev" cache
sam auth login                                  # refresh the only cached target
```

If multiple caches exist and you don't pass a positional, the CLI lists
the candidates and exits — pass one of them as the positional.

## Auto-login on apply

`sam config apply` and `sam config plan` resolve the manifest's target
through the same cache. When the cache is empty and stdin/stderr are
attached to a real terminal, the CLI prompts you to authenticate inline:

```
$ sam config apply -m manifests/dev.yaml
Not logged in to dev (https://platform.dev.example.com). Open browser to authenticate? [Y/n]
```

Answering `y` (or hitting Return) runs the same loopback PKCE flow that
`sam auth login` does and continues with the apply. Answering `n` or
declining exits non-zero with a not-logged-in error.

When stdin or stderr is not a terminal, or `CI` is set to a truthy
value, the prompt never fires — the CLI immediately prints
`run sam auth login` and exits non-zero.

Pass `--no-interactive` to apply/plan to force the non-TTY behaviour even
when running in a terminal — useful for scripts that need apply/plan to
fail fast when the OAuth cache is empty.

`SAM_PLATFORM_TOKEN` continues to short-circuit auth resolution entirely:
when the env var is set, neither the cache nor the prompt are consulted.

## CLI subcommands

| Command | Purpose |
|---|---|
| `sam auth login [target-or-url]` | Run the loopback PKCE flow. Positional is a hostname, URL, or cache name. |
| `sam auth login dev --url <url>` | First-time named login: cache key `dev`, platform URL from `--url`. |
| `sam auth logout --url <url> [--target <name>]` | Revoke server-side and delete the local cache. `--url` (or `--manifest <file>`) is required; `--target` overrides the cache key, which otherwise defaults to the URL host (or the manifest's `target.name`). |
| `sam auth status --url <url> [--target <name>]` | Show whether the cache is present, the email claim, and the expiry. Takes the same `--url` / `--manifest` / `--target` flags as `logout`. |
| `sam auth list` | List every cached target with platform URL + email + expiry. |
| `sam auth login --timeout 90s` | Cap how long the loopback listener waits for the browser callback. |
| `sam auth login --manifest <file>` | DEPRECATED: read URL/name from a manifest. Pass the hostname positionally instead. |

## Cache file shape

```json
{
  "platform_url": "https://platform.dev.example.com",
  "issuer":       "https://idp.example.com",
  "access_token":      "...",
  "sam_access_token":  "...",
  "refresh_token":     "...",
  "expires_at":   "2026-05-07T15:30:00Z",
  "obtained_at":  "2026-05-07T14:30:00Z"
}
```

- The directory is created lazily on first `sam auth login`; nothing is
  written before then.
- File mode is always `0600`. Cache writes are atomic (write to a temp file
  in the same directory, then rename) so a crash mid-write never leaves a
  half-written cache.

## Proactive refresh

The CLI proactively refreshes the access token when it has less than five
minutes of life remaining. The refresh call is `POST /api/v1/auth/refresh`
with the cached refresh token in the body and the cached access token as
the bearer; on success the rotated tokens are written back to the cache
atomically.

If the refresh is rejected (the IdP has revoked the refresh token, the
entrypoint's idle session timeout has elapsed since the access token was
issued, or the entrypoint's signing key changed), the cache file is deleted
and the next CLI invocation prints `run sam auth login` so the user can
re-authenticate.

Only a cached login is refreshed. A token from `SAM_PLATFORM_TOKEN` or a
`bearer_token` env var is sent as-is, and a `401` fails the command
instead of retrying with the cache.

## Auth resolution order

When `sam config apply` resolves a target's auth credential, it consults
the following sources in priority order:

1. **`SAM_PLATFORM_TOKEN`** (env var) — highest priority. CI flows continue
   to work without manifest churn even when the manifest declares
   `auth: { type: oauth }`. Set this to an Agent Mesh-minted service token to
   bypass the cache entirely.
2. **`auth: { type: oauth }`** — read from `$XDG_CONFIG_HOME/sam/auth/<target>.json`,
   refreshing if needed. Errors with `run sam auth login` when the cache is
   absent.
3. **`auth: { type: bearer_token, envVar: <NAME> }`** — read from the
   named env var (or the default `SAM_PLATFORM_TOKEN` when `envVar` is
   absent).
4. **No `auth` block** — talk to the platform anonymously (test harness
   only).

The other commands that accept `-m/--manifest` (`sam eval`, `sam api-key`,
`sam task send`, `sam api`) honour the same `auth` block. Two differences:
`SAM_AUTH_TOKEN` also overrides it, and with no `auth` block they fall back
to a `sam auth login` cache entry if one is available: the entry named by
`target.name` first, but only when that login was made for the same origin
(scheme, host and port) as the target URL, then one cached under the URL's
hostname (as `sam auth login <hostname>` creates), else anonymous. So a
manifest's `target.url` or a `--url` override never carries a name-matched
login to another origin. `sam eval` and
`sam api-key` refresh a token found this way on a 401, under that same entry.
A declared `auth` block that can't produce a token, such as an unset
`bearer_token` env var or a missing `oauth` login, fails the command instead
of sending a different identity. These commands also refuse a declared
`auth: {type: oauth}` login when the target has a `name` and that login was
made for a different origin than the target URL, whether the URL comes from
`target.url` or `--url`. `sam config` does not check this, so keep
`target.url` pointed at the platform the login was made for.

## Common failure modes

| Symptom | Cause | Fix |
|---|---|---|
| `oauth login: timed out after 90s waiting for browser callback` | The browser never reached the loopback listener — usually a popup blocker or a corporate proxy stripping the redirect. | Re-run with `--timeout 5m` and check the browser tab; alternatively paste the printed URL into a browser by hand. |
| `oauth login: state mismatch` | An old browser tab from a previous login attempt was reused. | Close all Agent Mesh login tabs and re-run. |
| `target "<name>": its sam auth login was issued for <origin>, not <origin>` | The manifest declares `auth: {type: oauth}` with a `target.name`, but `target.url` or `--url` points at a different platform than that login was made for. | Point `target.url` (or `--url`) at the platform the login was made for, or run `sam auth login --target <name> --url <platform-url>` to log in under that name for the new platform. |
| `tokencache: no cached credentials (target "dev")` | The cache was never populated, or `sam auth logout` removed it. | `sam auth login dev` (or pass the full hostname). |
| `auth requires HTTPS — provide an https:// URL` | Typed `http://...` (or `--url http://...`). | Use the `https://` form; bare hostnames auto-derive HTTPS. |
| `oauth: refresh token denied` | The IdP rotated/revoked the refresh token. | The cache is auto-cleared; just re-run `sam auth login`. |
| `redirect_uri must be a loopback URL` | A network appliance rewrote the redirect to a non-loopback host. | Check for a corporate proxy that intercepts the OAuth flow; the platform allowlist enforces RFC 8252 §7.3. |

## Debugging: `sam api`

`sam api` is a generic authenticated HTTP client for the Agent Mesh entrypoint REST API — think `gh api` for Agent Mesh. It reuses the same `$XDG_CONFIG_HOME/sam/auth/<target>.json` cache and refresh ladder described above, so a developer who has run `sam auth login` once can poke at the entrypoint from the shell without copying tokens around.

Useful when authoring or debugging declarative-config repos: after a `sam config apply`, hit the entrypoint to confirm a resource looks the way the YAML said it should, dump the live shape of a resource you're about to write, or chase down a 4xx that the apply surfaced without telling you which field tripped it.

### Quick reference

```sh
# GET, target resolved from the auth cache
sam api --target dev /api/v2/platform/agents
sam api --target dev /api/v2/platform/agents --jq '.data[].name'

# Walk a paginated list (`PaginatedResponse` envelope; nextPage is the next page number)
sam api --target dev /api/v1/sessions --paginate --jq '.data[].id'

# POST / PATCH / PUT / DELETE — typed body fields
sam api --target dev -X POST   /api/v1/projects --field name=demo --field 'description=ad-hoc'
sam api --target dev -X PATCH  /api/v1/sessions/$SID --field 'name=triage notes'
sam api --target dev -X DELETE /api/v1/sessions/$SID -i

# Full body from a file or stdin
sam api --target dev -X PUT /api/v2/platform/agents/$ID --input agent.json
cat agent.json | sam api --target dev -X PUT /api/v2/platform/agents/$ID --input -

# Inspect status line + headers
sam api --target dev /api/v1/user -i
sam api --target dev /api/v2/platform/agents --verbose   # method, URL, redacted headers, RTT on stderr

# A local desktop or embedded run is served over plain http://. `sam api`
# refuses to send a bearer token to an http:// URL unless you pass --insecure
# (the same flag also skips TLS verification on https:// targets).
sam api --target desktop --insecure /api/v2/platform/agents
```

### Body construction

- `--field key=val` is **typed**: `true`, `false`, `null`, and numeric literals are emitted as JSON literals; everything else is a JSON string.
- `--raw-field key=val` is `--field` but always a JSON string (use it when a literal `"true"` is intended).
- `--field 'key[]=v1' --field 'key[]=v2'` builds `{"key":["v1","v2"]}`.
- `--field 'project.id=abc'` builds `{"project":{"id":"abc"}}`.
- `--input <path | @file | -` is mutually exclusive with `--field`/`--raw-field` and reads a full pre-built JSON body. `-X GET` refuses any body.
- Field names go through verbatim — Agent Mesh enforces **camelCase** request bodies (`agentId`, `pageNumber`); typing `agent_id` will get a 400 from the server, which is the right teaching signal.

### Target resolution

Precedence (highest first): `--url` › `--target <cached>` › `--manifest <file>` › `SAM_WEBUI_URL` env var › single-cache-entry shortcut. `--target` is the usual choice when you've already run `sam auth login`. A manifest resolves `target.url`, else `target.name` (`desktop` or a login name), and its `auth` block picks the token even when `--url` / `--target` replace the URL. A token the manifest selects (its `auth` block, or the login its `target.name` names) is sent only to that target: an absolute URL whose scheme, host, or port differs exits `2` before any request. Use a relative path, or pass the other host as `--url` to send a `bearer_token` credential there deliberately. A named login, whether matched by `target.name` or declared with `auth: {type: oauth}`, is used only on the origin it was issued for.

### Exit codes

- `0` — 2xx success.
- `1` — 4xx (response envelope written to stdout for scripting; one-line `errorId` summary to stderr).
- `2` — 5xx or usage error (e.g. mutually-exclusive flags, malformed `-H`).
- `3` — network / TLS / token-refresh failure, or a target or credential that can't be resolved (for example a `--manifest` that fails to load, or a manifest login made for a different origin).

### Deprecated routes

When the platform marks a route it answered as deprecated, or as returning a deprecated field (the `X-Solace-Api-Deprecated` response header, whose value links to the notice), `sam api`, `sam config`, `sam eval` and `sam api-key` collect it. At the end of the run, one `warning: this run called platform API routes that are deprecated or return deprecated fields: …` line goes to stderr, naming each route once with its notice link. It never goes to stdout or into a `--format json` document, so scripts that parse output are unaffected. The call itself still works.

### When to reach for it vs. the dedicated subcommands

- **Authoring config**: use `sam config plan/apply/pull/migrate` — `sam api` won't reconcile YAML for you.
- **Inspecting what's deployed**: `sam api` is the path of least resistance — it gives you the raw response shape the Agent Mesh UI uses, which is also what the schema docs in this skill describe.
