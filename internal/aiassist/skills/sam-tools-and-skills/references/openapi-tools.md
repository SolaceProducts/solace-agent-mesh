# OpenAPI: inline tool vs `api` connector

Agent Mesh has **two** ways to call a REST API from an OpenAPI spec. Decide first, state the choice and why.

| | `api` connector (subtype `openapi`) | Inline `tool_type: openapi` tool |
|---|---|---|
| Lives | Platform resource, created once | On one agent's `tools:` list |
| Managed via | Builder UI (Connectors page) / `sam-connectors` | Agent config (builder UI or declarative repo) |
| Reuse | Attach by name to many agents (≤5 connectors/agent) | Re-declared per agent |
| Credentials | Platform-held, edit redeploys attached agents | In the agent's YAML via `${VAR}` |
| Default for | "Our API, several agents need it", UI-first teams | One agent, config-file-first workflows, fine-grained per-agent curation |

**Default to the connector** — it is GA, no-code, and centrally credentialed; hand off to `sam-connectors`. Use the inline tool when the API genuinely belongs to one agent or the team manages agents as declarative YAML. Never recommend wrapping a plain REST API in an MCP server or a dedicated proxy agent when these two exist.

## How the inline tool behaves

Runs **inside AWE in-process** (plain HTTP client — no STR). AWE loads the spec at agent startup, so a `specification_url` must be reachable from the AWE process/pods, and `${VAR}` references resolve from that process's environment. Each spec operation becomes one LLM-callable tool named by its `operationId`. **Operations without an `operationId` are auto-named from their method + path** (e.g. `get_users_by_id`) — they still become tools, but under less predictable names, so curate with `allow_list`/`deny_list`. (The `api` connector curates differently — its create flow has a Select Tools step in the UI; details belong to `sam-connectors`.)

## Field vocabulary (name keys only — full YAML via `sam-declarative-config` or the builder UI)

- **Spec source, exactly one of:** `specification_url` | `specification_file` | `specification` (inline string; add `specification_format: json|yaml` if auto-detect fails).
- `base_url` — overrides the spec's `servers[0].url`.
- `allow_list` / `deny_list` — operationIds to expose/exclude. **Always curate**: tool-per-endpoint on a large spec bloats the agent's context and confuses the model.
- `headers` — static headers; `max_response_size` — response limit in bytes, default 10 MiB, must be a whole number from 1 to 52428800 or the toolset fails to load and the agent starts without its tools (an over-limit success is rejected whole; an over-limit HTTP error returns the status with a partial body). Values earlier releases accepted and ignored — `0`, above 50 MiB, `10MB`, non-numeric — are now errors. `required_scopes` — RBAC scopes.
- `effect` — only `side_effecting`: marks every operation the entry exposes as side-effecting. Without it an operation is `read_only` when its method is GET, HEAD or OPTIONS and `side_effecting` otherwise. `read_only`, or any other value, stops the agent from starting, because an operator cannot declare an operation safer than its method. Set it in a hand-authored runtime `tools:` entry; the platform's declarative agent kind and the Builder cannot store it.
- `auth.type` — `none` | `bearer` (`token`) | `basic` (`username`/`password`) | `apikey` (`name`/`value`/`in`: header (default) or query — cookie-placed API keys are only auto-wired from the spec's own security scheme, not settable here) | `oauth2` | `serviceaccount` (`service_account_json`).
- OAuth2 splits on shape: `authorization_url` present → authorization-code flow (per-user consent, refresh handled); only `token_url` → client-credentials (machine-to-machine, shared per-agent token). Extras: `client_id`, `client_secret`, `scopes`, `use_pkce` (default off), `disable_consent_prompt` (set `true` for Microsoft Entra ID tenants that block user consent; drops `prompt=consent` from the sign-in URL), `audience`, `resource` (RFC 8707 resource indicator, sent on the authorization/token/refresh requests so the IdP binds a matching `aud` claim; MCP OAuth auto-discovery infers it from the server's protected-resource metadata), `token_endpoint_auth_method`, `credential_key` (the per-user token's storage slot; every authorization-code entry without one shares `openapi_oauth2_authorization_code`, so give each OAuth entry in one agent its own), `display_name` (the system's name on the sign-in prompt and in the agent's connection list), and `ca_cert_path` / `insecure_skip_verify` for private-CA token endpoints.

Secrets in these fields are always `${VAR}` references, never literals.
