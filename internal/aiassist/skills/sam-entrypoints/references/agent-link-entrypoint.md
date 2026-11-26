# Agent link entrypoint (early access)

Lets an agent built outside Agent Mesh (LangGraph, CrewAI, your own code) join the mesh as an agent. The agent opens **one WebSocket to the entrypoint** and never gets broker credentials; Agent Mesh lists its card, delivers tasks to it and relays its calls to other agents. It is usually built with the Agent Mesh link SDK (`sam-link-sdk`, Python). **Direction check:** this entrypoint brings *agents* in, not users or events. A chat surface is `slack`/`teams`; MCP clients calling your agents is `mcp`.

Two resources are involved, and they are separate on purpose:

- the **`agent_link` entrypoint**, which only carries connection limits;
- one **link agent** per third-party agent, which names the entrypoint it connects through and holds the agent's token digest, peers and wake settings.

## What the third-party agent needs

| It needs | Where it comes from |
|---|---|
| The connect address | `wss://<public host>/gw/<slug>/v1/connect`. Set a slug on the entrypoint, or the path carries its UUID. |
| Its name on the mesh | The name it is registered under. It sends it when it connects (`AgentConfig.name` in the link SDK). |
| Its token | A secret only the agent holds. Agent Mesh stores the token's SHA-256 digest, never the token. |

`sam-link-sdk link-token --out <file>` issues a token: it writes the token to the file (owner-readable only), prints the digest, and refuses to overwrite an existing file. In the link SDK the address is `SAM_LINK_URL`, the agent's name `SAM_AGENT_NAME`, and the token file `SAM_LINK_TOKEN_FILE` (or the token itself in `SAM_LINK_TOKEN`, never both).

## Path 1: platform-managed (a deployment with the Platform service)

1. **Create and deploy the entrypoint**, type `agent_link`. `values` is optional: `maxFrameBytes`, `maxInflight`, `maxConnectionsPerAgent`, `maxConnectsPerMinute`, `taskTtlSeconds`, each with a default. There is no `agents` value; sending one is rejected.
2. **Register each agent as a link agent**, one of two ways:
   - **Declarative config**: kind `linkAgent`, authored through the `sam-declarative-config` skill (its `references/linkAgent.md`). `spec.entrypoint` names the entrypoint and `spec.tokenSha256` is required to create the link agent; once it exists, leaving `tokenSha256` out keeps the stored token, and `sam config pull` never writes it. A peer that is the display name of a platform agent or link agent fails the plan, which names the `instanceName` to use. One `sam config apply` creates and deploys the entrypoint, then the link agents.
   - **Management API**: `POST /api/v2/platform/linkAgents` with `name` and `entrypointId`. Leave `tokenSha256` out and the response carries a generated `token` once, never again; send `tokenSha256` and no token is generated. Then deploy with `POST /api/v2/platform/agentDeployments` (`agentId`, `action: deploy`).
3. **Read what the agent needs** from `GET /api/v2/platform/linkAgents`: `instanceName` is its name on the mesh (`agent_` followed by its id with underscores, assigned by the platform), `connectUrl` the address (`null` when the platform does not know its public address; then join `connectPath` to the web UI's host with `wss://` in place of `https://`, or `ws://` in place of `http://`).

Facts that prevent wrong answers:

- **The link agent's `name` is its display name, not its mesh name.** The agent must connect as `instanceName`. A link agent's name must be unique among all agents.
- **A link agent has its own deploy step**, like any agent, and its entrypoint must be deployed first. Deployed but not connected reads `deploymentStatus: deployed` with `runtimeStatus.status: disconnected`; that is normal until the process connects.
- **Edits take effect at the next deployment.** Changing a deployed link agent stores the change and it reads `syncStatus: out_of_sync` until it is deployed again. `sam config apply` runs that deployment; through the API, send `action: update`.
- **Replacing a token.** Declaratively: issue a new token to a new file, set `tokenSha256` to its digest, apply, and give the agent the new token file. If that deployment fails, or the apply ran with `--no-deploy`, the old token still works until a later apply deploys the link agent. Through the API, `PUT /api/v2/platform/linkAgents/{id}/rotateToken` returns a new token once and redeploys the agent before it answers, so the old token stops working. Check `deployment.status` in that response: when it is `failed`, or `deploymentRefusal` is set, the old token still works until an update deployment succeeds.
- **The wake client secret and wake header values are write-only.** Responses carry `<REDACTED>`; a replace that sends `<REDACTED>` keeps the stored value only while the wake URL (and, for the secret, the token URL) is unchanged. The token digest is never returned at all.
- **Scopes**: link agents use the agent builder scopes (`agent_builder:*:create`, `agent_builder:*:read` / `:update` / `:delete` / `:deploy`). There is no separate category. `rotateToken` needs both `agent_builder:*:update` and `agent_builder:*:deploy`, and so does a replace (`PUT /api/v2/platform/linkAgents/{id}`) that changes `tokenSha256`; a replace that leaves it out or sends the stored digest needs `agent_builder:*:update` alone. An API key can call these routes except `rotateToken`, which needs a signed-in user; a pipeline holding both scopes replaces a token by changing `tokenSha256`.
- **Removing**: a deployed link agent is undeployed when it is deleted. The entrypoint cannot be undeployed while link agents are deployed on it, nor deleted while any link agent names it. A platform-managed entrypoint that cannot start one of its agents still serves the others and retries that one on its own (5 s, doubling to 15 min); the agent's token is answered 503 meanwhile.

## Path 2: runtime configuration file (a deployment started from config files)

The entrypoint is an app whose `gateway_adapter.type` is `agent_link`, and the agents are listed in the same file under `adapter_config.agents`. Keys are snake_case here: `name` and `token_sha256` (required), `enabled`, `display_name`, `owner`, `team`, `peers`, `share_user_profile`, and `wake` (`url`, `token_url`, `client_id`, `client_secret`, `scope`, `headers`, `timeout_seconds`, `on_start`). The limits (`max_frame_bytes`, `max_inflight`, `max_connections_per_agent`, `max_connects_per_minute`, `task_ttl_seconds`) sit beside `agents`.

- The agent joins as the `name` configured there and connects to `/gw/<gateway_id>/v1/connect`.
- Names of the form `agent_<uuid>` and `workflow_<uuid>` are reserved for what the platform deploys.
- The list is read when the process starts: adding or changing an agent means a restart, which drops the open tasks of every link agent in that app.
- An unknown key, or two agents with the same `token_sha256`, stops the app from starting.

Do not mix the paths: an entrypoint created on the platform takes its agents from link agents only.

## Peers, wake and what the agent may see

- **`peers`** lists the agents this agent may call, by their name on the mesh (for a platform-deployed agent, its `instanceName`). A call is also checked against the user's right to invoke that peer. A peer need not exist yet.
- **The other direction is the mesh's own rule.** A link agent's card is a card like any other: an agent whose allow list admits it, the built-in Orchestrator included, offers it to its model and delegates to it. To keep one agent from calling it, use that agent's deny list (`sam-author-agent`, peer delegation).
- **`wake`** is for an agent on a request-driven runtime: when a task arrives and the agent is not connected, Agent Mesh calls the wake URL (with an OAuth client-credentials token when `tokenUrl`, `clientId` and `clientSecret` are set) and the agent connects and takes the task. Wake URLs must be `https` (`http` only for a loopback host). On a platform-managed entrypoint (path 1) they must also carry no user name or password, and no query parameter other than one version-shaped `api-version` on the wake URL (none on `tokenUrl`; send credentials in `headers`), the wake call ignores the proxy settings, and a private or loopback wake address is only called when the Entrypoint Executor runs with `SAM_AGENT_LINK_ALLOW_PRIVATE_WAKE=true`. An entrypoint configured in a runtime file (path 2) checks only the scheme: it does not guard the address and its wake calls use the process's proxy settings. Leave `wake` out for an agent that stays connected.
- **The user's profile is withheld** from a link agent unless `shareUserProfile` (`share_user_profile`) is `true`. The profile comes from the profile provider configured on the Platform service, not from the identity provider; without one, a task carries only the user's ID. Users' tokens are never sent.

## Several entrypoint replicas

Only one replica, the leader, serves link agents' connections. The others answer HTTP 503 with `Retry-After`, which the link SDK retries about a second later, so a plain load balancer with no session affinity is enough. When the leader changes, connections close and the agents reconnect.

## When an agent does not join

| Symptom | Check |
|---|---|
| The agent's token is refused (HTTP 401) | The agent joins as the registered name (`instanceName` on path 1), the digest is that token's, the link agent is deployed, and a replaced token was deployed. |
| HTTP 403 with the right name and token | The link agent has `enabled: false`. |
| HTTP 404 at the connect address | The slug or `gateway_id` in the path, and that the entrypoint is deployed. |
| Deploying a link agent fails with `the link agent's entrypoint "<name>" is not deployed` | Deploy the entrypoint first. |
| Connections refused with HTTP 429 | More than `maxConnectsPerMinute` connections from one agent: several processes joining as the same agent, or one restarting in a loop. |
