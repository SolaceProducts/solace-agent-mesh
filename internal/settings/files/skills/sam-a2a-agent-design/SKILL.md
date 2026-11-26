---
name: sam-a2a-agent-design
description: Design guidance for Solace Agent Mesh remote A2A agents (proxies) — when to connect an external agent vs build an MCP connector or a native agent, agent-card discovery, and auth.
---

Design guidance for connecting an **external A2A (agent-to-agent) agent** into the mesh through a proxy.

For the **config schema** see `sam-a2a-agent-schema`.

## What is a remote A2A agent (proxy)?

A proxy connects an external **A2A-over-HTTP agent** — an agent already running outside
this Solace Agent Mesh deployment — into the mesh. It translates between the external agent's A2A/HTTPS
protocol and Agent Mesh's internal A2A/Solace protocol, fetches and publishes the agent's card
to mesh discovery, and forwards tasks with the configured authentication.

Use a proxy when the user wants to integrate an agent hosted elsewhere — a third-party
agent, an agent owned by another team or organization, or an agent running on separate
infrastructure (e.g. an EC2 host, a partner's cloud).

## When to use each — DO NOT confuse these

This is the single most important decision. A bare agent URL most often means an **A2A
proxy**, NOT an MCP connector.

**Remote A2A agent (proxy)** — the user mentions:
- "connect to an external/remote/third-party agent"
- "use the A2A proxy" / "proxy this agent" / "an agent hosted at <URL>"
- an agent that *talks* (accepts tasks and replies), reachable at a base URL
- "another Solace Agent Mesh/A2A agent running somewhere else"

→ Build a **remote A2A agent** (`type: a2a_agent`). Load `sam-a2a-agent-schema`.

**MCP connector** — the user mentions:
- "MCP server", "tool server", "remote tools"
- a server that exposes *tools/functions* for an agent to call (GitHub MCP, Stripe MCP…)
- `streamable-http` or `sse` MCP transport

→ Build an **MCP connector** (`type: connector`). Load `sam-connector-design`.

**Native Solace Agent Mesh agent** — the user wants a brand-new agent built and run *inside* this
deployment (its own instruction, model, tools).

→ Build an **agent** (`type: agent`). Load `sam-agent-design`.

> Decision rule: if the endpoint is *an agent you send tasks to*, it's an A2A proxy. If
> the endpoint *exposes tools/functions* to one of your agents, it's an MCP connector.
> A2A (JSON-RPC over HTTP + agent-card discovery) and MCP are different protocols — an
> MCP connector will NOT work against an A2A agent, and vice versa. When the user says
> "A2A proxy" or "external agent" and gives a base URL, build a remote A2A agent.

## What to gather (Phase 1)

- **Agent URL** — the base URL of the external agent (required).
- **Agent card location** — does it publish a card at `/.well-known/agent-card.json`
  (`well_known`, the common case), at a custom path (`custom` + the URL), or not at all
  (`none` — then ask what skills/capabilities it offers)?
- **Authentication** — does fetching the card and/or sending tasks require auth? Which
  scheme (none, bearer token, API key, basic, OAuth2)? Ask about the *scheme*, not the
  secret values — those use `<<__SAM_REQUIRED__>>`, which blocks build and test until replaced.
- **Timeout** — only ask if the user expects long-running tasks (default is 120s).
- **A2A version and card signing** — leave `protocolVersion` at `auto` unless the user
  names a version (an agent that misbehaves on A2A 1.0 can be pinned to `v0_3`). Only if
  the user says the agent's card is signed, ask for the JWK Set URL(s) that hold its
  signing keys (`trustedCardJwksUrls`) and whether an unsigned card must be refused
  (`requireSignedCard`).

**Ask the user for the card location and auth scheme (unless they already stated them) —
do not infer them, and do not copy them from another remote A2A agent that already
exists.** Different agents
legitimately use different card locations and auth types, so an existing one is not a
template; `platform_get_component_config` is for avoiding duplicate builds, not for lifting
another agent's auth/card pattern. These two choices set the config's structure (card
location's conditional fields + the discriminated auth object), so guessing wrong yields
an invalid, undeployable config.

A single deployment can run many proxied agents; each remote A2A agent is one external
endpoint.

## Builder workflow

1. Identify the A2A-proxy need during Discovery and confirm it's an external agent, not
   a tool server (MCP) or a new native agent.
2. Generate the remote-agent config artifact (JSON, see `sam-a2a-agent-schema`) with
   `<<__SAM_REQUIRED__>>` for every secret.
3. **Name the outstanding secrets after generating** — do not collect them through
   `ask_user_question`, whose field is not masked. Leave each one as
   `<<__SAM_REQUIRED__>>` and say in your next message which fields are still missing.
   The route open right now — and always — is the user telling you the value in the
   chat, which you write into the artifact with `artifact_search_and_replace_regex`.
   The agent's own page becomes available once it has been saved to the platform: the
   plan-approval flow saves it before it blocks the deploy, a manual Build & Activate
   blocks before saving. The agent can't be deployed until any required auth is
   filled in.
4. A remote A2A agent has no instruction/model/tools to design — its behaviour lives in
   the external agent. Keep the build focused on the connection details.
