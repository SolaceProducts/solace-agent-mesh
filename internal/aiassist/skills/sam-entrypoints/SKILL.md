---
name: sam-entrypoints
description: Use when making Solace Agent Mesh agents reachable from outside — chatting with agents from Slack or Microsoft Teams, exposing agents as MCP tools to Claude Code/Cursor/other MCP clients, triggering agents or workflows from Solace event mesh topics, receiving inbound HTTP webhooks from GitHub or CI pipelines, letting an agent built outside Agent Mesh (LangGraph, CrewAI, custom code, the link SDK) join the mesh over an agent link, or securing an entrypoint with OAuth/OIDC (Keycloak, dynamic client registration, PKCE) — and when creating, deploying, or troubleshooting entrypoints. Not for the agent reaching OUT to external systems (sam-connectors no-code, sam-tools-and-skills custom code), authoring the agent itself (sam-author-agent), or the built-in web chat UI, which needs no entrypoint setup.
---

# sam-entrypoints

This skill creates and manages Agent Mesh **entrypoints** — the inbound front doors that let external users, systems, and events reach your agents. **Scope check:** if the *agent* reaches out (posts to Slack, queries a database, publishes to the mesh), that is a *connector* → `sam-connectors` (or custom code → `sam-tools-and-skills`). The same external system often exists on both sides — direction decides. The built-in web chat UI is not an entrypoint you create; it ships with the product.

## What an entrypoint is

A platform-managed resource: name, optional **slug**, type, and type-specific config. The slug is **opt-in and set only at creation** — 3–63 chars of `[a-z0-9-]`, must start with a letter and end with a letter or digit (no trailing hyphen), unique across entrypoints, and not one of the reserved routing prefixes `oauth`, `api`, `well-known`, `gw`, `health`. There is no rename path, and the two surfaces fail differently: `PATCH /entrypoints/{id}` **rejects** a `slug` key outright (400, unknown field), while through declarative config a changed `spec.slug` **fails the plan** with "slug is immutable"; an omitted slug leaves the live value alone. To change one, delete and recreate the entrypoint, then re-register any external URL. It sets the URL path — an HTTP-bearing entrypoint lives at `/gw/<slug>/…` on the entrypoint proxy. **If you omit the slug, the entrypoint is addressed by its generated UUID (`/gw/<uuid>/`) — the slug is deliberately NOT derived from the name.** So choose a slug at creation whenever the URL is externally registered or handed to clients (IdP redirect allowlists, Meta/Teams webhooks, MCP clients); an immutable, readable slug is what keeps those URLs stable. Create-or-update can deploy in the same step; deleting auto-undeploys first. Deployment status (`deployed` / `not_deployed` / `deploy_failed`) and runtime status are visible in the UI.

## Picking the type

| The outside thing reaching in | Type | Availability |
|---|---|---|
| People chatting from Slack channels/DMs | `slack` | GA — **Socket Mode only** (no public URL needed) |
| People chatting from Microsoft Teams | `teams` | GA — Bot Framework webhook (needs public URL) |
| Broker events triggering an agent or workflow | `event_mesh` | GA |
| MCP clients (Claude Code, Cursor…) calling your agents as tools | `mcp` | Early access |
| Inbound HTTP requests from external systems (GitHub, CI pipelines) — fire-and-forget, no session, no response back | `webhook` | GA |
| Agents built outside Agent Mesh joining the mesh over one WebSocket each | `agent_link` | Early access |

Say the maturity plainly — never present an early-access type as GA. The web chat UI (`httpsse`) is built-in infrastructure, not a type you pick.

## Paths, in teaching order

1. **Builder UI** (default). Create the entrypoint in the Builder UI: pick the type, fill the schema-driven form, toggle deploy-on-save. The UI also gives you a **YAML preview** (secrets redacted), and **networking details** (the public webhook URL inbound types need). Lead with this — it is the source of truth for which fields exist.
2. **Declarative config** — an entrypoint is its own resource kind, authored via the **`sam-declarative-config`** skill (`sam config plan` / `apply`). **That skill is the only source of full YAML — this skill names types and field names only.** `sam config pull` on a UI-created entrypoint is the fastest schema oracle.

   **Two fields are config-apply-only and camelCase on every type except `agent_link`, which has neither**: `systemPurpose` and `responseFormat` (the `snake_case` spellings still work; the first one holding a **string** wins, so `systemPurpose: ""` clears a legacy value — but a bare `systemPurpose:` is YAML null, falls through, and leaves the stored value alone). They tell agents what this surface is for and how to shape replies, stamped onto every task the entrypoint submits and picked up by agents with `inject_system_purpose` / `inject_response_format`. Keep each under 4000 bytes (a budget, not a guardrail: only `webhook`/`mcp`/`event_mesh`/`google_chat` enforce it, never on the `snake_case` spelling, and the limit counts bytes rather than characters). **This is the one place both oracles above fail** — no create form sets them, so the UI never shows them and a pull of a UI-created entrypoint never carries them. Two carve-outs: the web UI entrypoint's pair is overridden instance-wide by the manifest's `platform.webuiSettings`, and `event_mesh` has a separate per-rule `event_rules[].responseFormat` that is appended to the prompt as literal text rather than stamped as metadata — not interchangeable.
3. **Raw runtime YAML** — escape hatch only (debugging, parity). Never the headline. Its keys are **not** the authoring field names, and the mapping is per-field rather than a case rule: some are case flips (`enableAuth` → `enable_auth`), some are renames (`messageFormat` → `payload_format`), some gain a prefix (Slack `bot_token` → `slack_bot_token`), some pass through unchanged (`feedback_enabled`). The block they live in varies too — `gateway_adapter.adapter_config` for Slack/Teams/MCP, but top-level `event_mesh_broker_config` / `event_handlers` for event mesh, which has no `adapter_config` at all.

   **Authoring casing is also per-type — do not infer it.** `webhook` and `mcp` author in **camelCase** (`routePath`, `targetType`, `targetAgentName`, `enableAuth`, `oauthDispatcherUrl`); `slack` and `teams` author in **snake_case** (`bot_token`, `microsoft_app_id`); `event_mesh` is **mixed** — snake_case broker fields (`broker_url`, `broker_vpn`, `tls_skip_verify`, `event_rules`) wrapping camelCase rule fields (`targetAgent`, `promptTemplate`, `messageFormat`, `acknowledgmentPolicy`). Never guess: `sam config pull` on a UI-created entrypoint, or the UI's own schema-driven form, is the only reliable oracle.

## Routing inbound traffic to an agent

Each type has one routing mechanism — don't invent others: Slack/Teams route by an inline `@AgentName` mention, else `default_agent_name`; the event-mesh entrypoint routes per **event rule** (target agent *or* workflow); the webhook entrypoint routes to one pre-configured target chosen by `targetType` (`agent` | `workflow`) plus the matching `targetAgentName` / `targetWorkflowName` — exactly one target name, never both, and callers cannot override it; the MCP entrypoint routes by which tool the client called. Chat threads map to Agent Mesh sessions automatically — webhook requests are sessionless and never do. The `agent_link` entrypoint routes nothing in: it hosts third-party agents, which then receive tasks like any agent on the mesh.

## Hard rules (each counters an observed failure)

- **Collect the slug at creation** — it is opt-in and immutable-after-create, and it is deliberately *not* derived from the name. Omit it and the URL is an opaque UUID (`/gw/<uuid>`). This matters most for **MCP**, where the slug *is* the endpoint clients connect to (`/gw/<slug>`): ask the user for a slug before creating an MCP entrypoint — never let it default to a UUID that clients would have to hardcode.
- **Go product, Go patterns.** Entrypoints are first-class platform resources with a real UI — never `sam plugin add`, never `sam-slack`/`sam-event-mesh-gateway` plugins, never hand-built `apps:`/`app_config:` files as the taught path.
- **Never write entrypoint YAML from memory** — not even "illustrative, verify the keys" sketches; users paste them. UI, `sam-declarative-config`, or `sam config pull`. Under time pressure that *is* the fast path — urgency never licenses memory-YAML.
- **Slack is Socket Mode only.** Two tokens (`xoxb-` bot + `xapp-` app-level), outbound WebSocket, no public endpoint, no Events-API/webhook alternative to offer.
- **MCP three-way split** — exposing your agents to MCP clients = MCP *entrypoint* (here); consuming a remote MCP server across agents = `mcp` *connector* (`sam-connectors`); one-agent inline MCP tool = `sam-author-agent`. Prefer the connector for anything reusable. State which you picked and why.
- **Slack and event mesh exist on both sides** — messages/events coming in = *entrypoint* (here); agent posting or publishing = *connector* (`sam-connectors`). A mixed ask ("team chats with it AND it posts alerts") is two resources, one per direction. Filtering/multi-step logic on a triggered flow belongs in a workflow (`sam-author-agent`) — an event rule can target a workflow directly.
- **A webhook entrypoint alone does nothing observable.** It returns `202 Accepted` before the target runs and never returns the response, so the *only* visible outcome is what the target agent does through its own tools and connectors — posting the review comment back on the GitHub pull request, opening the ticket, sending the Slack message. Whenever a user asks for a webhook, confirm the target has an outbound capability (and credentials) for the system they expect to see the result in; a webhook pointed at a tool-less agent is a silent no-op. There is no dedup either, so warn that a redelivered event repeats that action.
- **An `agent_link` entrypoint is two resources.** The entrypoint carries only limits; each third-party agent is a separate **link agent** that names it. Never put an `agents` list in a platform entrypoint's `values`, and never hand the agent the link agent's display name to connect with: it joins as the `instanceName` the platform assigns. Facts in [references/agent-link-entrypoint.md](references/agent-link-entrypoint.md).
- **Secrets hygiene** as in `sam-connectors`: pasted tokens are exposed (advise rotation), `${VAR}` from then on, never echo literals.
- **Don't guess auth behavior.** OAuth/RBAC specifics for the MCP entrypoint are in [references/mcp-entrypoint.md](references/mcp-entrypoint.md) — facts there only; entrypoint login/RBAC beyond it → `sam-operate`.

## References

| Topic | File |
|---|---|
| Slack and Teams entrypoints — fields, external prerequisites, routing | [references/chat-entrypoints.md](references/chat-entrypoints.md) |
| Event-mesh entrypoint — broker connection, event rules, outputs, acks | [references/event-mesh-entrypoint.md](references/event-mesh-entrypoint.md) |
| MCP entrypoint — exposing agents, tool naming, four auth modes, OAuth/Keycloak, client setup | [references/mcp-entrypoint.md](references/mcp-entrypoint.md) |
| Agent link entrypoint — third-party agents joining the mesh, link agents, tokens, platform-managed and runtime-file paths | [references/agent-link-entrypoint.md](references/agent-link-entrypoint.md) |
