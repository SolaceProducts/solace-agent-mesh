---
name: sam-a2a-agent-schema
description: JSON Schema for a Solace Agent Mesh remote A2A agent (proxy) — fields, agent-card discovery, auth config, and validation rules for connecting an external A2A agent into the mesh.
---

Configuration schema for a **remote A2A agent** (a.k.a. A2A proxy). A remote agent
connects an external A2A-over-HTTP agent — one hosted outside this Solace Agent Mesh deployment —
into the mesh through a proxy. The proxy translates between the external agent's
A2A/HTTPS protocol and Agent Mesh's internal A2A/Solace protocol.

For **design guidance** (when to build one vs an MCP connector or a native agent) see
`sam-a2a-agent-design`.

## Schema

```json
{{SCHEMA:a2a-agent}}
```

---

## Format

A2A agent artifacts use **JSON** (not YAML) and mirror the `CreateRemoteAgentRequest`
body the platform accepts at `POST /api/v2/platform/remoteAgents`:

```json
{
  "name": "WeatherProxy",
  "description": "Proxies the external weather forecasting agent into the mesh.",
  "agentUrl": "http://ec2-1-2-3-4.compute.amazonaws.com:9000",
  "agentCardLocation": "well_known",
  "agentAuthConfig": { "type": "bearer", "token": "<<__SAM_REQUIRED__>>" }
}
```

**MIME type**: `application/vnd.sam-a2a-agent-config+json`

**Key rules:**
- `name` is required, 3-40 chars, and must be unique among A2A agents.
- `agentUrl` is required and must start with `http://` or `https://` (max 2048 chars).
- `agentCardLocation` is required — one of `well_known`, `custom`, `none`.
- `agentAuthConfig` is required when you are creating the agent — always present, even
  when it needs no auth (`{ "type": "none" }`).
- Use `<<__SAM_REQUIRED__>>` for every secret (bearer token, API key, OAuth client secret).
- **When editing an agent that already exists, leave `agentAuthConfig` as the bare
  `{ "type": "none" }` boilerplate unless you are deliberately changing the credential.**
  The schema above lists it in `required`, so a config without it fails validation —
  omitting it is not an option the tooling allows. It does not need to be: that bare form
  is exactly what this skill tells you to emit, so the save cannot read it as a statement
  about auth and preserves whatever credential is stored.
  What you must not do is write a `<<__SAM_REQUIRED__>>` placeholder or a real secret you
  cannot see — a stored credential reads back redacted, and either of those replaces a
  working one configured elsewhere.
  To genuinely remove auth, set the **top-level** `agentAuthType: "none"`. Removal has to
  be stated there precisely because nothing emits that field by habit, whereas the config
  block's `{ "type": "none" }` is boilerplate.

---

## Agent card discovery (`agentCardLocation`)

The agent card describes the remote agent's name, description, skills, and I/O modes.
How the proxy obtains it:

| Value | Behaviour | Extra fields |
|-------|-----------|--------------|
| `well_known` | Fetch from `{agentUrl}/.well-known/agent-card.json` | none |
| `custom` | Fetch from an explicit URL | `agentCardUrl` **required** |
| `none` | No card published — describe the agent inline | `skills` **required** |

Prefer `well_known` — most A2A agents publish a card there. Only use `none` when the
remote agent has no discoverable card; then supply `skills`, `inputModes`, and
`outputModes` so Solace Agent Mesh can advertise the proxied agent.

---

## Authentication

Auth is a **discriminated object** with a `type` field. There are two independent auth
surfaces:

- **`agentAuthConfig`** (required always; leave it as `{ "type": "none" }` on an edit that
  is not about auth) — credentials sent on every *task request* to the agent.
- **`agentCardAuthConfig`** (optional) — credentials used to *fetch the card*.

Both use the same shape. Pick `type` and fill the matching fields; put secrets behind
`<<__SAM_REQUIRED__>>`:

| `type` | Fields |
|--------|--------|
| `none` | `{ "type": "none" }` |
| `bearer` | `{ "type": "bearer", "token": "<<__SAM_REQUIRED__>>" }` |
| `apikey` | `{ "type": "apikey", "apiKey": "<<__SAM_REQUIRED__>>", "headerName": "X-API-Key" }` |
| `basic` | `{ "type": "basic", "username": "...", "password": "<<__SAM_REQUIRED__>>" }` |
| `oauth2` | `{ "type": "oauth2", "tokenEndpoint": "...", "clientId": "...", "clientSecret": "<<__SAM_REQUIRED__>>", "scopes": ["..."] }` |

On a **create**, always include `agentAuthConfig` — use `{ "type": "none" }` when the agent
is unauthenticated. On an **edit**, omit it unless you are changing the credential: a
stored secret reads back redacted, so `{ "type": "none" }` alone is treated as boilerplate
and ignored rather than risk wiping auth someone configured elsewhere.

To genuinely **remove** an existing agent's auth, say so explicitly by setting the
top-level `agentAuthType` as well:

```json
{ "agentAuthType": "none", "agentAuthConfig": { "type": "none" } }
```

---

## Other fields

| Field | Type | Notes |
|-------|------|-------|
| `description` | string | Human description (≤1000 chars). |
| `agentCardHeaders` | object | Extra headers sent when fetching the card. |
| `taskHeaders` | object | Extra headers sent on every task request. |
| `requestTimeoutSeconds` | integer | 10-600; defaults to 120 when omitted. |
| `inputModes` | string[] | e.g. `["text"]`. Only needed when `agentCardLocation: none`. |
| `outputModes` | string[] | e.g. `["text", "file"]`. Only needed when `agentCardLocation: none`. |
| `protocolVersion` | string | A2A version to speak: `auto` (default, the highest both sides support), `v1_0` or `v0_3`. Pin only when the user asks for a version. |
| `trustedCardJwksUrls` | string[] | Up to 10 https URLs (http only on localhost) of JWK Sets whose keys may sign the agent card. A signed card must then verify against them. Not allowed with `agentCardLocation: none`. |
| `requireSignedCard` | boolean | Refuse an unsigned card and send no task until a signed one is accepted. Needs `trustedCardJwksUrls`. Defaults to false. |

---

## Examples

**Well-known card, bearer auth on tasks:**
```json
{
  "name": "ResearchProxy",
  "description": "External research agent exposed through the mesh.",
  "agentUrl": "https://research.partner.example.com",
  "agentCardLocation": "well_known",
  "agentAuthConfig": { "type": "bearer", "token": "<<__SAM_REQUIRED__>>" }
}
```

**Custom card URL, no auth:**
```json
{
  "name": "PublicForecastProxy",
  "description": "Public forecasting agent with a non-standard card path.",
  "agentUrl": "https://forecast.example.com",
  "agentCardLocation": "custom",
  "agentCardUrl": "https://forecast.example.com/a2a/card",
  "agentAuthConfig": { "type": "none" }
}
```

**No card — skills described inline:**
```json
{
  "name": "LegacyProxy",
  "description": "Legacy A2A agent that does not publish a card.",
  "agentUrl": "http://legacy.internal:9000",
  "agentCardLocation": "none",
  "agentAuthConfig": { "type": "none" },
  "skills": [
    { "name": "Summarize", "description": "Summarizes documents." }
  ],
  "inputModes": ["text"],
  "outputModes": ["text"]
}
```

---

## Placeholder convention

Use `<<__SAM_REQUIRED__>>` for every secret (tokens, API keys, client secrets) and for
any required value not provided in the conversation. The UI detects the placeholder and
prompts for the real value before saving.
