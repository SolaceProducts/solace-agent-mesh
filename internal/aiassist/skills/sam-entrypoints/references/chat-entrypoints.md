# Chat entrypoints: Slack, Teams

Field names are real keys for naming and recognition; full YAML shape and validation belong to `sam-declarative-config` (or pull a UI-created entrypoint). The builder UI form is generated from the same schema.

## Slack (GA)

**Socket Mode only** — the entrypoint opens an outbound WebSocket to Slack; no public URL, no Events-API webhook mode exists. Don't offer one.

**Slack-side setup (the user does this at https://api.slack.com):**
1. Create an app, **enable Socket Mode**, generate an app-level token (`xapp-…`) with `connections:write`.
2. Bot token scopes the entrypoint uses: `app_mentions:read`, `channels:history`, `chat:write`, `files:read`, `files:write`, `im:history`, `im:write`, `reactions:write`, `users:read`, `users:read.email`, `users.profile:read`.
3. Event subscriptions: `app_mention`, `message.channels`, `message.im` — delivered over the Socket Mode WebSocket; there is **no Request URL to configure**.
4. Install to workspace → bot token (`xoxb-…`); invite the bot to target channels.

**Agent Mesh-side fields:** `bot_token` (xoxb, required), `app_token` (xapp, required), `default_agent_name` (optional; the create form pre-fills the Orchestrator — see "Agent routing and refusals" for what an unset or unknown value does). Optional knobs exist for the initial status message, markdown correction, and feedback buttons.

**Routing:** `@AgentName` mention, else `default_agent_name` (Teams routes the same way) — see "Agent routing and refusals" below for the exact rule. A Slack thread maps to one Agent Mesh session, so follow-ups in-thread keep context; DMs work the same way. Replies stream back into the thread; artifacts upload as files. `!help` lists the agents the asker can actually invoke, by display name. **Silent no-response usually means the bot wasn't invited to the channel** or an event subscription is missing — check those before the tokens.

## Microsoft Teams (GA)

**Azure-side:** an Azure Bot Service registration provides `microsoft_app_id` (GUID) and `microsoft_app_password` (client secret); `microsoft_app_tenant_id` only for single-tenant bots. The Bot Framework delivers messages by **webhook**, so the entrypoint needs a public URL — the UI's **networking details** view shows the exact endpoint to register.

**Agent Mesh-side fields:** the three credentials above plus `default_agent_name` (optional, same behaviour as Slack), `initial_status_message`, `enable_typing_indicator`, `max_download_file_size_mb`.

**Channel identity (Slack and Teams).** A 1:1 DM always runs as the asker. A message in a shared surface — a channel or group chat — does **not** by default: it runs as a service account, so an agent answering in front of others cannot use the asker's personal access. That service account is the one named by `run_as`, and an unset `run_as` means the built-in `system:default` (not the asker). Set `use_user_identity_in_channels: true` to opt out and run channel messages as the real asker; `run_as` is then ignored. The legacy `run_as: "channel-user"` still loads but is deprecated, mapped to the Orchestrator-only `system:channel` with a WARN.

## Agent routing and refusals (Slack, Teams)

One rule serves both; there is no per-channel routing config.

- A mention matches an agent's broker name **or** its display name, and only among agents the message's run-as identity is allowed to invoke. In a DM that is the sender; in a channel it is the service account described in "Channel identity" above, not the sender's own access. An agent that identity cannot use is treated as though it were not there.
- A mention **at the start of the message** is the sender naming a target. If it matches nothing they can use, the message is refused rather than falling back to `default_agent_name` — answering as some other agent than the one named is worse than an error. A leading typo therefore now gets a refusal. Trailing `.`, `-` and `_` are read as sentence punctuation, so `@Reporter. what's the status` still reaches `Reporter`.
- A mention **later in the message** routes if it matches a usable agent and otherwise falls through to `default_agent_name`, so a stray `@` in prose is not an error.
- No mention: `default_agent_name`. Set it explicitly — left unset, the fallback is the alphabetically first discovered agent, which changes as agents come and go. A name that matches no running agent is not silently replaced: the sender gets a reply that the entrypoint is misconfigured, and the entrypoint logs a WARN.

Refusal wording is deliberate, so don't "improve" it per surface:

- A refusal never contains an agent's broker instance name (`agent_<uuid>` for anything platform-deployed) and never a raw Go error.
- "That agent exists but you're denied" and "no such agent" produce the **same** message. Differing replies would let anyone enumerate the mesh's agents from a chat client one mention at a time. Operators get the distinction from the entrypoint's logs; the sender never does.
- A permission boundary reads differently from a retryable fault, so a sender who can never succeed is not told to try again.
- Because the surface's identity is an email, a refusal may also offer a WebUI sign-in, since signing in once links that email to a Agent Mesh user.

## Common to both

- Tokens/secrets are masked after save; `sam config pull` exports them as `${VAR}` placeholders.
- The entrypoint identifies the human (Slack/Teams profile email) — that identity is what RBAC and audit see, except where a service account carries authorization instead — a channel message by default (see "Channel identity" above) — and the human is then kept for audit only; identity/permission depth → `sam-operate`.
- Verify by sending a real message and watching the reply; auth failures surface in the entrypoint's logs and deployment status.
