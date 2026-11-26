# Inspecting platform state

After `sam config apply`, use `sam api` (see `references/cli-auth.md`) to
confirm what actually landed. This page covers the status fields, how to look
a resource up by name, and which endpoint returns what.

## Status fields — what each one means

A platform resource carries several independent status fields. They answer
different questions; don't conflate them.

| Field | Lives on | Values | Meaning |
|---|---|---|---|
| `deploymentStatus` | agent, link agent, entrypoint, workflow | `deployed`, `not_deployed`, `deploy_failed` | Did the last deploy succeed? |
| `syncStatus` | agent, remote agent, link agent, entrypoint | `in_sync`, `out_of_sync` | Does the **running deployment** match the **stored config**? `out_of_sync` means a config change never made it into a running deployment (e.g. a deploy that failed after the config was saved). |
| `discoveryStatus` | toolset, skill | `pending`, `ready`, `failed` | Has the Secure Tool Runtime finished scanning the tool bundle and learned its schema? A deploy that references a toolset still `pending` succeeds, but that toolset is skipped — its tools are absent until it reaches `ready` and the agent is redeployed. |
| `runtimeStatus` | agent, remote agent, link agent, entrypoint, workflow | `running`, `degraded`, `starting`, `disconnected`, `stopped` | Is the deployed process actually up right now, and is it reporting any active problems? `degraded` means up but with a problem — check the resource's `runtimeStatus.problems` array for detail. |

The trap: an agent can be `deploymentStatus: deployed` and
`runtimeStatus.status: running` yet `syncStatus: out_of_sync` — it's up, but
running *older* config than the YAML you just applied. `runtimeStatus` is an
object (`{status, problems}`, per the table above), not a bare string — a
`select(.runtimeStatus=="running")`-style jq filter never matches; filter on
`.runtimeStatus.status` instead. **`sam config apply` now re-deploys
`out_of_sync` agents, link agents and entrypoints automatically**, and also deploys an
unchanged agent, link agent, workflow or entrypoint whose `deploymentStatus` is
`not_deployed` (such as one an earlier `apply --no-deploy` created) and a
workflow or link agent whose `deploymentStatus` is `deploy_failed` (and `plan` flags them as "out of sync —
will re-deploy"), so you no longer need to POST a manual deploy to recover a
stale or never-deployed resource. If a re-deploy fails, apply exits non-zero.

## Looking up a resource by name

Platform paths address resources by **UUID**, not name —
`GET /api/v2/platform/agents/<name>` returns `400 Invalid path parameter` (`validationDetails.id: ["must be a UUID"]`).
To find a resource by its human name, list and filter client-side:

```sh
# Resolve a name → id
sam api --target dev /api/v2/platform/agents \
  --jq '.data[] | select(.name=="my-agent") | .id'

# Inspect one agent's sync/deploy status by name
sam api --target dev /api/v2/platform/agents \
  --jq '.data[] | select(.name=="my-agent") | {id, deploymentStatus, syncStatus, runtimeStatus}'

# Check whether a custom toolset finished discovery
sam api --target dev '/api/v2/platform/toolsets?origin=custom' \
  --paginate \
  --jq '.data[] | {name, discoveryStatus, discoveryErrors}'
```

## Which endpoint returns what

Two agent-shaped endpoints exist and are easy to confuse:

| Endpoint | Returns | Use when |
|---|---|---|
| `/api/v2/platform/agents` | The **managed** agents — desired + stored config and the status fields above. `PaginatedResponse` envelope. | Verifying what `sam config apply` created/updated, or resolving a name → id. |
| `/api/v1/agentCards` | The **discovered** agent cards published on the broker by running agents (A2A discovery). Different shape — this is the runtime view, not the config view. | Checking what's actually live and discoverable, independent of the config store. |

All `/api/v2/platform/...` list endpoints use the `PaginatedResponse`
envelope (`{data: [...], meta: {pagination: {...}}}`); single-resource GETs
wrap the resource in `{data: {...}}`, so filter one with `--jq '.data.<field>'`.
Walk multi-page lists with `--paginate`.
