# Connecting to SAM

An agent joins Solace Agent Mesh through the **link**: one WebSocket from the agent to SAM. The agent never touches SAM's
message broker. SAM verifies users, enforces the stop, checks every message, keeps the agent's card and calls other
agents on its behalf. What the agent does when the connection misbehaves is in [operations.md](operations.md); the wire
format is in [link-protocol.md](link-protocol.md).

## What you need

SAM accepts the link through an `agent_link` app, a gateway app that an administrator adds to SAM's configuration (SAM
calls its web-facing apps gateways). You need three things from whoever runs SAM, or do it yourself on a SAM of your own:

- the link's URL, `wss://<sam-host>/gw/<gateway_id>/v1/connect`;
- your agent's name registered in that app, with the SHA-256 digest of its token;
- a restart of SAM after the agent is added, because SAM reads the list when it starts.

## Get a token

```bash
sam-link-sdk link-token --out claims-triage.link-token
```

The command writes the token to the file and prints its digest. SAM knows the agent by its name and the digest, never
the token. It refuses to overwrite an existing file.

## Configure SAM

The `agent_link` app is loaded with SAM's other configuration files (`sam run <files or directories>`). Each agent is
listed by its name, which must be the `AgentConfig.name` the agent joins with, and its token's digest:

```yaml
apps:
  - name: agent_link
    broker:
      # The same broker settings as the deployment's other apps. On a local `sam run --embedded`:
      dev_mode: true
      broker_url: ""
      broker_username: default
      broker_password: default
      broker_vpn: default
    app_config:
      namespace: ${NAMESPACE, sam}
      gateway_id: agent-link
      authorization_service:
        type: none
      gateway_adapter:
        type: agent_link
        adapter_config:
          agents:
            - name: claims-triage
              token_sha256: <the digest sam-link-sdk link-token printed>
              peers: [policy-desk]
```

- `namespace` is the deployment's namespace, and `authorization_service` its own authorization, so each user is checked
  against the agent's and its tools' scopes; `type: none` lets every user reach the agent.
- `gateway_id` is the path segment in the agent's URL: SAM serves the link at `/gw/<gateway_id>/v1/connect`, here
  `/gw/agent-link/v1/connect`.
- `peers` (optional) are the agents on the mesh this agent may call ([delegation.md](delegation.md)); `wake:
  {url, timeout_seconds}` (optional) is for a request-driven agent ([serverless.md](serverless.md)).
- `share_user_profile: true` (optional, per agent) lets the agent see the user's profile in `ctx.request_context`.

An agent's `name` is letters, digits, `-` and `_`, starting with a letter or digit, at most 128 characters less the
suffix SAM lists its card under, and not `agent_<uuid>` or `workflow_<uuid>`, which the platform keeps for the agents
it deploys. Each agent needs its own token: two entries with the same `token_sha256` refuse to start, as does an
unknown key. Every key an agent entry takes:

| Key | Default | What it does |
|---|---|---|
| `name`, `token_sha256` | required | the agent's name and the SHA-256 of its token, in hex |
| `enabled` | `true` | `false` refuses the agent's connections and keeps it off the mesh |
| `display_name` | the `name` | the name SAM shows; the one the agent describes is not used |
| `owner`, `team` | none | who answers for the agent, shown on its card (256 characters at most) |
| `peers` | none | the agents it may call |
| `share_user_profile` | `false` | sends it each task's user profile |
| `wake` | none | `url`, `timeout_seconds` (900), `on_start` (`true`), `headers`, and `token_url`, `client_id`, `client_secret`, `scope` for an OAuth token; URLs must be `https` ([serverless.md](serverless.md)) |

`adapter_config` itself takes `max_frame_bytes` (8 MiB), `max_inflight` (16 tasks a connection holds unacknowledged),
`max_connections_per_agent` (8), `max_connects_per_minute` (60, per agent) and `task_ttl_seconds` (7200).

The `agent_link` app goes into a SAM that already runs its web UI: users reach the agent through it and `sam task send`,
and a SAM with the `agent_link` app alone answers `503 gateway not ready`.

## Point the agent at SAM

`MeshAgent` builds the link from the environment and joins as `config.name`:

```python
from sam_link_sdk import AgentConfig, MeshAgent

agent = MeshAgent(AgentConfig(name="claims-triage", description="Triages claims"), handle)
await agent.run_forever()
```

| Variable | Meaning |
| --- | --- |
| `SAM_LINK_URL` | The connect URL. `wss://` (or `https://`) for a SAM served over TLS. A plain `ws://` (`http://`) address is accepted only for this machine, unless `SAM_LINK_ALLOW_INSECURE` is set. |
| `SAM_LINK_TOKEN_FILE` | A file holding the token. It is read again on every connection attempt, so a rotated token needs no restart. |
| `SAM_LINK_TOKEN` | The token itself, for a secret manager: `export SAM_LINK_TOKEN=$(cat claims-triage.link-token)`, not the file's path. Set this or `SAM_LINK_TOKEN_FILE`, not both: with both set the agent refuses to start. |
| `SAM_LINK_TRUST_STORE_PATH` | Optional: a CA file or a directory of PEM CA certificates, for SAM behind a private certificate. |
| `SAM_LINK_MAX_FRAME_BYTES` | Optional: SAM's `max_frame_bytes`, when it is not the default 8 MiB. |
| `SAM_LINK_ALLOW_INSECURE` | Optional: `1` lets the agent send its token over plain `ws://` to a host other than this machine, on a network you trust (a cluster's own, TLS ended at a proxy). The agent logs one warning that the token is not encrypted. |

To build the link in code (a token file path computed at start, say), pass it as `transport`:

```python
from sam_link_sdk.link import LinkConfig, LinkTransport

config = AgentConfig(name="claims-triage", description="Triages claims")
link = LinkConfig(url="wss://sam.example.com/gw/agent-link/v1/connect", agent=config.name, token=token)
agent = MeshAgent(config, handle, transport=LinkTransport(link))
```

`LinkConfig.agent` is the name the link connects as and `AgentConfig.name` the name the agent serves as. They must be the
same: `start()` raises `ValueError` naming both when they differ. `token` here is the token string you read yourself.

## Check that it joined

The agent logs through Python's `logging`. Without a logging configuration only warnings and errors show, so call
`logging.basicConfig(level=logging.INFO)` (the examples do). A good start logs `agent link open agent=<name> ...
protocol=2` and then `agent started name=<name>`; `joined Agent Mesh as <name>` says it joined. Then confirm SAM lists it:

```bash
curl -s https://<sam-host>/api/v1/agentCards | jq '.data[].name'
sam task send "Hello" --agent claims-triage --url https://<sam-host>
```

## When the agent does not join

The messages call SAM Agent Mesh. A few address and token mistakes fail at start with a message that names the fix.

| The log says | Cause |
| --- | --- |
| `ValueError: the agent link needs SAM_LINK_URL; SAM_LINK_URL has the form wss://<sam-host>/gw/<gateway_id>/v1/connect` (or `SAM_LINK_TOKEN or SAM_LINK_TOKEN_FILE`) | The variable is not set in the agent's environment. |
| `ValueError: SAM_LINK_TOKEN and SAM_LINK_TOKEN_FILE are both set` | The agent cannot know which token you mean: unset one. |
| `the agent link needs SAM_LINK_TOKEN (SAM_LINK_TOKEN_FILE <path> is empty or cannot be read)` | The token file's path is wrong, or the file is empty. |
| `Agent Mesh refused the agent's token: HTTP 401 refusals_in_a_row=1 limit=10`, then `LinkAuthRefused` listing what to check | SAM does not know this name with this token. Check that the agent's name is the link agent's `instanceName` (a link agent registered on the platform) or matches an entry in SAM's `agent_link` configuration, that `SAM_LINK_TOKEN` holds the token and not the file's path (`SAM_LINK_TOKEN_FILE` takes a path), that the digest SAM holds is this token's, and that the link agent is deployed, or SAM was restarted after the agent was added to its configuration. |
| `LinkConfigError: Agent Mesh answered HTTP 404 at <url>` | The URL's path is wrong: another `gateway_id`, or SAM has no `agent_link` entry. The agent exits at once on its first connection; after it has connected once, a 404 is retried like any outage. |
| `LinkConfigError: cannot open a TLS connection to <url>` | A `wss://` URL for a SAM served over plain HTTP (use `ws://` on this machine; anywhere else needs `SAM_LINK_ALLOW_INSECURE=1`), or a private certificate (`SAM_LINK_TRUST_STORE_PATH`). The agent exits at once on its first connection. |
| `agent link connect failed err=cannot open the agent link: ConnectionRefusedError` (or `OSError`), repeated | Nothing listens at the URL's host and port. A SAM on your own machine serves the link on its web UI's port, 8800 by default. |
| `agent link connect failed err=Agent Mesh refused the agent link: HTTP 403`, repeated | SAM is waiting for its first sign-in: open SAM's web UI, sign in and accept the licence. |
| `agent link reconnect refused: too many connections; backing off`, then `LinkRateLimited` if it never gets through | SAM limits how often one agent may connect in a minute (HTTP 429), so several processes are joining as the same agent, or one is restarting in a loop. |
| `still trying to reach <host> (<last error>), giving up in <n> s` | The agent cannot reach SAM and keeps trying; said at most once a minute. |
| `still trying to reach <host> (Agent Mesh refused the agent link: HTTP 503)`, or `LinkUnavailable` from a wake, `announce()` or `retire()` | No SAM replica serves agent links: SAM has just started or is changing leader (a few seconds), the load balancer pins the agent to one replica (remove session affinity), or only some of the namespace's gateway processes run the `agent_link` app (all of them must). |
| `LinkProtocolMismatch` | SAM speaks another protocol version than `sam-agent-link.v2`, the only one the SDK offers. |

A refused token, another protocol, and a path or TLS setting that cannot be right (`LinkConfigError`) fail at once. A connection that cannot be opened is tried again with backoff for
`offline_grace_seconds` (900 s) before `start()` raises `LinkError`. With 0 the first failure raises, except an HTTP
503, which is tried again for up to 20 s ([operations.md](operations.md), "Several SAM replicas").
