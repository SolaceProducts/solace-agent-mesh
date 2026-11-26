*(Early Access)* A link agent is a third-party agent that joins the mesh
without touching the broker: it opens one WebSocket to an entrypoint of type
`agent_link` and Agent Mesh lists it, routes tasks to it and calls other
agents on its behalf. It is usually built with the Agent Mesh link SDK
(`sam-link-sdk`). This kind registers the agent; the `agent_link` entrypoint
it connects through is an ordinary `entrypoint` resource (see the
`## type: agent_link` section of `references/entrypoint.md`).

Files live under `linkAgents/`, and the manifest key is `resources.linkAgents`.

```yaml
kind: linkAgent
name: Claims Desk
description: Answers claims questions for the partner.
spec:
  entrypoint: partner-link
  tokenSha256: ${CLAIMS_DESK_TOKEN_SHA256}
  owner: alice@example.com
  team: Claims Operations
```

`name` is 3 to 40 characters and must be unique among all agents, standard
and remote agents included, ignoring case and spaces. A name another agent
holds fails the apply with a 409. `spec` accepts only the keys in the schema
table below: an unknown key, or an unknown key under `wake`, fails the plan
and lists the allowed ones.

### `entrypoint`: the `agent_link` entrypoint, by name

`spec.entrypoint` is the `name:` of an entrypoint of type `agent_link`, either
declared in the same manifest or already on the platform. The plan fails when
the name matches no entrypoint, or matches one of another type. Write the name,
not an id, and write the key as `entrypoint`: the API's `entrypointId` is
rejected as an unknown key.

Declare both in one manifest and one `apply` does everything in order: it
creates the entrypoint, creates the link agent, deploys the entrypoint, then
deploys the link agent.

```yaml
resources:
  entrypoints:
    - partner-link
  linkAgents:
    - Claims Desk
```

A deployed link agent cannot be moved to another entrypoint. Changing
`entrypoint` on one fails the apply with `undeploy the link agent before
moving it to another entrypoint`. Undeploy it first, then apply:

```sh
sam api --target dev -X POST /api/v2/platform/agentDeployments \
  --field agentId=<link agent id> --field action=undeploy
```

### `tokenSha256`: required to create, and never the token

The agent proves who it is with a token. Agent Mesh stores only the token's
SHA-256 digest, and a file carries only the digest: 64 lowercase hexadecimal
characters. `tokenSha256` is required to create a link agent; a plan that
would create one without it fails with `spec.tokenSha256 is required to create
a link agent`. The platform can also generate a token and show it once, but
only to a caller of its API, and a file has no way to receive it. Once the
link agent exists, a file that leaves `tokenSha256` out keeps the stored token,
like any other secret.

Issue the token where the agent runs, then put the digest in the file or in
the variable the file names:

```sh
sam-link-sdk link-token --out claims-desk.link-token
```

The command writes the token to the file, readable by its owner only, prints
the digest, and refuses to overwrite a file that exists. The digest of any
other token is `printf %s "$TOKEN" | sha256sum`; with `$TOKEN` unset that is
the digest of an empty token, which the plan refuses. Every link agent needs a
token of its own: a digest another link agent uses fails the apply.

The platform never returns the digest. Two things follow:

- When the file sets `tokenSha256` and the link agent's other fields match
  the platform, `sam config plan` asks the platform whether it matches the
  stored one, which needs the `agent_builder:*:update` scope. When another
  field changed, the plan shows an update without comparing the digest. A token without it fails the plan with a message that names the
  scope; leave `tokenSha256` out to keep the stored token without the check.
- `sam config pull` leaves `tokenSha256` out. Without it, the pulled files
  still plan against the platform they came from. To create the link agents on
  another platform, add `tokenSha256` to each file first.

**Replacing the token.** Issue a new token to a new file (`link-token` does
not overwrite one), set `tokenSha256` to its digest, and apply. Changing the
digest needs `agent_builder:*:deploy` as well as `agent_builder:*:update`. The
plan shows `update + deploy`, and the new token takes effect when that
deployment completes; the old one stops working at the same moment, so give
the agent the new token file (`SAM_LINK_TOKEN_FILE`). When the deployment
fails, or with `--no-deploy`, the new digest is stored and the agent keeps
accepting the old token (`syncStatus: out_of_sync`) until a later apply
deploys it. For a leaked token, confirm that `apply` deployed it and exited
`0`. While the file sets `tokenSha256`, apply makes that digest the token: a
token replaced outside the file (with `rotateToken`, say) is put back by the
next apply, so update the file with the new digest or leave `tokenSha256`
out once the link agent exists.

### Deployment

A link agent deploys like an agent. `apply` deploys one it creates or
updates, and also an unchanged one that is `out_of_sync`, `not_deployed`, or
whose last deployment failed; `plan` marks those `out of sync — will
re-deploy`. `--no-deploy` skips it.

The entrypoint has to be deployed first. When it is not, the link agent's
deployment fails with `the link agent's entrypoint "<name>" is not deployed;
deploy the entrypoint first`. That happens when the entrypoint's own
deployment failed in the same apply, or when the entrypoint is not in this
manifest and nobody deployed it. Get the entrypoint deployed and apply again.

A deployed link agent whose process is not connected reads
`deploymentStatus: deployed` and `runtimeStatus.status: disconnected`. That is
its normal state until the agent connects, not a failure.

### What the agent needs to connect

The platform chooses the agent's name on the mesh, so read it after the first
apply:

```sh
sam api --target dev /api/v2/platform/linkAgents --paginate \
  --jq '.data[] | select(.name=="Claims Desk") | {instanceName, connectUrl, connectPath, deploymentStatus}'
```

- `instanceName` (`agent_` followed by the link agent's id with underscores)
  is the name the agent must join as: the SDK's `SAM_AGENT_NAME`, or
  `AgentConfig.name`.
- `connectUrl` is the WebSocket address, the SDK's `SAM_LINK_URL`. When it is
  `null`, the platform does not know its public address: join `connectPath`
  to the host the web UI is served from, with `wss://` in place of `https://`
  (`ws://` in place of `http://`).
- The token file from `link-token` is the SDK's `SAM_LINK_TOKEN_FILE`. The
  SDK also accepts the token itself in `SAM_LINK_TOKEN`, but not both.

The `instanceName` belongs to the resource, not to its `name`. Editing the
description, owner or token keeps it. Renaming the file's `name:` does not: a
new name plans a create, and the old link agent becomes a delete that needs
`--prune`, so the new one gets a new id and a new `instanceName`. The create
needs `tokenSha256` in the file. A rename that keeps the old link agent's
digest fails the create unless the apply runs with
`--prune`, because the old link agent still holds the digest; `--prune`
deletes it before the create. Without `--prune`, issue the agent a new token
and put its digest in the file. The old link agent then stays on the
platform until an `apply --prune` deletes it, and if it is deployed, its
token keeps working until then.

### `peers`: the agents this one may call

`peers` lists agents by their name on the mesh, not by the `name:` of a
resource file:

- for an agent the platform deploys (an `agent` resource, a link agent), its
  `instanceName`;
- for an agent that runs from a runtime configuration file, the name
  configured there.

Each peer is letters, digits, `-` and `_`, starting with a letter or digit.
A link agent cannot list its own `instanceName`, or a peer twice. To read an
`agent` resource's `instanceName`, list `/api/v2/platform/agents` with
`--paginate` (that list leaves link agents out; read theirs from
`/api/v2/platform/linkAgents`).

A peer does not have to exist yet: the apply stores the list as written and
the agent can call a peer once it is on the mesh. Because an agent's
`instanceName` is assigned when it is created, declare the agent, apply, read
its `instanceName` with `sam api`, and then add it to `peers`. A peer that is
the `name:` of a platform agent or link agent fails the plan, since the link agent cannot
call it by that name: the error names the link agent, the peer and the
agent's `instanceName` to write instead, or, for an agent the same apply
creates, says to apply without the peer first. A peer that matches no
platform agent passes, because an agent from a runtime configuration file is
not on the platform's list.

### `wake`: an agent that is started on demand

Leave `wake` out for an agent that stays connected. Set it for one on a
request-driven runtime: when a task arrives and the agent is not connected,
Agent Mesh calls `wake.url` and the agent connects and takes the task.

```yaml
kind: linkAgent
name: Claims Foundry
description: Claims agent hosted on a request-driven runtime.
spec:
  entrypoint: partner-link
  tokenSha256: ${CLAIMS_FOUNDRY_TOKEN_SHA256}
  wake:
    url: https://runtime.example.com/invocations
    tokenUrl: https://idp.example.com/oauth2/token
    clientId: claims-client
    clientSecret: ${CLAIMS_FOUNDRY_WAKE_SECRET}
    scope: https://runtime.example.com/.default
    timeoutSeconds: 600
```

- `url` is required and must be `https` (plain `http` is accepted for
  `localhost` and loopback addresses only). The same rule applies to
  `tokenUrl`. Neither may carry a user name or a password. `url` may carry
  one query parameter, `api-version`, once, whose value is 1 to 64 letters,
  digits, `.`, `_` or `-` starting with a letter or digit, such as
  `2025-05-01` or `v1` (Azure AI Foundry needs it); `tokenUrl` may carry no
  query string. Put the `url`'s credentials in `headers` and the
  `tokenUrl`'s in `clientId` and `clientSecret`. A reserved IP address (cloud metadata,
  link-local, unspecified, broadcast or multicast) fails the apply. A private or loopback address is accepted, but the
  entrypoint only calls it when its Entrypoint Executor runs with
  `SAM_AGENT_LINK_ALLOW_PRIVATE_WAKE=true`.
- `tokenUrl`, `clientId` and `clientSecret` go together: with them Agent Mesh
  gets an OAuth access token (client credentials grant) and sends it with
  each wake call. Leave all three out to call `url` without a token.
- `clientSecret` is a secret. Write it as a `${VAR}` placeholder. The
  platform returns it redacted, `sam config pull` writes
  `${LINKAGENT_<NAME>_CLIENTSECRET}`, and `plan` detects a rotated value the
  same way it detects a changed digest.
- `headers` is a map of extra HTTP headers sent with each wake call (32 at
  most). Header values are secrets like `clientSecret`: write each as a
  `${VAR}` placeholder. The platform returns them redacted, `sam config pull`
  writes `${LINKAGENT_<NAME>_<HEADER>}` (the link agent and header names in
  capitals, each run of other characters as one `_`, so `X-Api-Key` on
  `Claims Desk` becomes `LINKAGENT_CLAIMS_DESK_X_API_KEY`), and `plan`
  detects a changed value. Names may not differ only in case, may not be
  `Host`, `Content-Length`, `Transfer-Encoding` or `Content-Type`, and may
  not include `Authorization` when `tokenUrl` is set.
- `timeoutSeconds` is 1 to 86400 and defaults to 900. `onStart` defaults to
  `true`: the agent is also woken when its entrypoint starts, so it describes
  itself and is listed again. Set `onStart: false` to turn that off.

Removing the `wake` block from the file removes the stored wake settings,
the client secret included.

### Other settings

- `enabled: false` keeps the link agent registered and deployed, refuses its
  connections and stops it being woken. Leave it out for the default, `true`.
- `owner` and `team` (256 bytes at most each) say who answers for the
  agent and are shown on its card.
- `shareUserProfile: true` sends the user's profile to the agent with each
  task. The profile comes from the Platform service's profile provider (set
  under `platform.profileProvider` in the manifest), not from the identity
  provider. Without one, or when its lookup finds no profile, a task carries
  only the user's ID. The default, `false`, withholds it from a third-party
  agent.

Every optional key left out of the file is cleared or reset to its default at
the next apply. The file is the whole link agent, not a patch. Two keys are
exceptions: `tokenSha256` left out keeps the stored token, and
`wake.clientSecret` left out keeps the stored secret while neither `wake.url`
nor `wake.tokenUrl` changes.
Change either address and the file must set `clientSecret` again, or the
apply fails.

### Naming a link agent from a workflow or an entrypoint

A link agent can be the target of a workflow's agent node (`agent_name`) and
of an entrypoint (`default_agent_name`, a webhook's `targetAgentName`, an
event rule's `targetAgent`). Write its `name:`, or its `instanceName` once it
exists. `sam config plan` accepts a link agent declared in the same manifest
or already on the platform, and fails on a name that matches no agent and no
link agent.

### Removing a link agent

Drop it from the manifest and apply with `--prune`. The platform undeploys a
deployed link agent before it deletes it. An `agent_link` entrypoint cannot be
deleted while a link agent still names it, so `apply --prune` deletes link
agents before entrypoints, and removing both in one change works. It also
updates link agents before it deletes entrypoints, so moving an undeployed
link agent to a new entrypoint and dropping the old one works in one apply.
