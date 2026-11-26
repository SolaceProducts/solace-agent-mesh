# Security and governance

What the agent enforces, what SAM enforces, and what neither can. SAM verifies users' tokens, decides
who may use the agent, enforces the stop and checks every message ([agent-link.md](agent-link.md)); the agent never
sees a user's token and never touches SAM's broker.

| Who | Does |
|---|---|
| SAM | Verifies users, checks the agent's and each tool's scopes, enforces the stop, redacts and caps what the agent reports, records spend. |
| You | Declare the scopes (`with_scopes`, `required_scopes`), keep model and tool credentials out of prompts and logs, and decide which tools need approval. |
| The SDK | Refuses a tool call the user may not make, asks for approval, redacts what it reports, writes the audit trail, and fails closed when SAM cannot be reached. |

## Required scopes (RBAC)

```python
from sam_link_sdk import AgentConfig
from sam_link_sdk import ToolInfo

config = AgentConfig(
    name="claims-triage",
    description="Triages claims",
    required_scopes=["claims:_:read"],
    tools=[ToolInfo("route_claim", "Route a claim", required_scopes=["tool:claims:route"])],
)
```

With a framework adapter, declare a tool's scopes on the tool, and build the card from the guarded tools:

```python
from sam_link_sdk.langchain import tool_infos, with_scopes  # the same two names in every framework adapter

TOOLS = with_scopes([lookup_policy, route_claim], {"route_claim": ["tool:claims:route"]})
config = AgentConfig(name="claims-triage", description="Triages claims", tools=tool_infos(TOOLS))
```

`with_scopes` is how an adapter's tools are given scopes (`tool_infos` takes none of its own), so a scope cannot reach
the card through `tool_infos` on a tool that does not check it. A name that is none of the tools is a `ValueError`, so a
misspelt name cannot leave the tool unguarded. Scopes declared any other way are enforced by the adapters' hooks only,
not by the tool itself: a `ToolInfo` written by hand (as above), the tools of an MCP server or another toolset that
resolves them at run time (ADK's MCP toolset, say), and Hermes' tools, which it builds inside the agent and declares
with `tool_infos(agent_tools, scopes={...})` ([frameworks/hermes.md](frameworks/hermes.md)). Those hold in the runs the
adapter follows. A nested run the adapter can detect but not follow is refused before it starts (an ADK
`AgentTool(include_plugins=False)`, a Strands agent with `background_tasks`, an Agent Framework
agent behind a tool that is not a plain `Agent`). A run that your own code starts inside a tool's body (a nested
`Runner`, graph or crew) cannot be detected: there hook-only scopes are not enforced, so keep such tools out of it, or
give it tools guarded with `with_scopes`.

- **Published and enforced by SAM:** `required_scopes` is described to SAM, which puts it on the card
  (`sam/required-scopes`), hides the agent from users without those scopes, and refuses their tasks before they are
  delivered. Who the user is, and that they may use the agent, is SAM's decision; a task carries the user's id
  (`ctx.user_id`) and nothing of their token.
- **Per-tool scopes:** a tool's `ToolInfo.required_scopes` are enforced as SAM enforces a tool's scopes.
  - **The rule: a scoped tool checks the task's grants where it runs, and refuses outside a mesh task.** A tool given
    scopes with `with_scopes` is the framework's own tool type with the check in front of its body. Wherever it is
    called from (the adapter's run, a nested run no hook sees, a thread, a background run, an agent a tool builds), it
    finds the mesh task it runs for (`sam_link_sdk.current_task()`, which every `run_agent` sets, and which the framework
    carries into the asyncio tasks and threads it starts with the context) and runs only when that task's user holds the
    scopes, the agent's card declares them, and the task has been neither stopped nor answered (work a handler leaves
    running after it returns is refused: no guarded tool runs, nobody is asked and no peer is called; nothing a finished
    run does reaches a later delivery of the task; a step of a call already under way when the answer went out never
    reaches SAM, since the link holds no answered task, and a call the adapter refuses reports nothing). With no
    current task it refuses: a scoped tool never runs outside a mesh task, and a framework path that drops the context
    is refused, not trusted. Approval wrappers (`with_approval` in LangChain, OpenAI Agents, CrewAI and ADK) hold the
    same way: outside the task they serve they ask nobody and refuse.
  - **A card that declares a scoped tool is the agent's statement of its tools.** When `AgentConfig.tools` lists at
    least one tool with `required_scopes`, a run that offers or calls a tool whose name the card does not list is
    refused as a denied tool is (the model is told `Access denied`, the audit trail records `tool access denied`), and
    an ERROR naming the tool and the names the card declares is logged once per task and tool. A tool the card lists
    without scopes stays allowed, and an agent whose card lists no scoped tool is unchanged. This closes the gap where
    a framework offers a scoped tool under a name the card does not know (a renamed tool, an MCP prefix, `tool_infos`
    built from another list) and it would run for any user: list every tool the framework offers, or build the card
    from the same list. The tools the SDK adds are exempt, because they are on no card: `ask_user_question` and the
    `peer_<name>` tools `mesh_agent_tools` builds (a peer is checked by SAM and the agent allowlist).
  - **What the hooks add.** The adapter's `run_agent` (or its `step_reporter`) also keeps such a tool away from the
    model where the framework allows, and refuses a call to it before the tool is reached: the model is told `Access
    denied: you do not have the scopes required to use <tool>.`, the call shows as its start and a failed result
    carrying that text, and the audit trail records `tool access denied` (once per tool and run) and the call as
    failed, once, however many checks the call met. Hiding is never what enforces the rule
    ([frameworks/README.md](frameworks/README.md#a-tool-the-user-may-not-use) says how a refusal ends).
  - **Tools that cannot carry the check** (an MCP server's tools, a toolset that resolves its tools at run time, Hermes'
    registry tools) are enforced by the hooks only: nested runs the adapter can follow are covered as the first run, and
    one it can detect but not follow is refused before it starts (`NestedRunRefused`); a run your own code starts inside
    a tool's body is not detected, and such tools are not checked there. What each adapter covers in a nested run,
    guarantee by guarantee (scopes, approvals, the stop, steps, tokens), is in
    [contributing/adapters.md](contributing/adapters.md#nested-runs).
  - The OpenTelemetry adapter reads spans after the fact and cannot refuse a call; its `run_agent` logs a warning when
    the agent declares tool scopes, and a tool guarded with a framework adapter's `with_scopes` still refuses itself
    under it. Outside the adapters, give the model `ctx.permitted_tools(tools)` and refuse a call with
    `ctx.check_tool(name)`.
- **Which scopes the user holds: only what SAM granted.** SAM verifies the user's token, checks the user
  against the scopes the agent's tools describe, and sends with each task the ones the user holds
  (`grantedToolScopes`, `IncomingTask.granted_tool_scopes`). A tool is permitted only when every one of its scopes was
  granted, matched exactly (a granted `tool:claims:*` is the scope the agent named, not a grant of `tool:claims:pay`),
  and a task that carries no granted list grants nothing: the rule fails closed. The adapters, `ctx.permitted_tools` and `ctx.check_tool` all apply it. SAM checks
  at most 64 distinct tool scopes per agent, counted over every description since it started, so keep them fixed; the
  SDK refuses at start-up a configuration with more.
- **`AgentConfig.tools` is the one declaration.** It is both what the agent describes to SAM (each tool's
  `requiredScopes`, so SAM knows which scopes to check the user against) and what the agent enforces, so the two
  cannot disagree. A tool missing from it has no required scopes anywhere. A tool declared with scopes that SAM did not
  grant (because the user lacks them, or the task carried no granted list) is refused, never run.
- **Tool scopes are enforced by the agent's own code** (the link does not see a tool call before it runs). SAM cannot
  require it of a third-party agent. The agent's own `required_scopes` are SAM's to enforce, before a task is
  delivered.
- **The user's profile**, from the profile provider configured on SAM's Platform service, reaches the agent only
  when the administrator sets `share_user_profile: true` for it; otherwise, or when no profile provider is
  configured, `ctx.request_context.user_profile` is empty.

## Stopping the agent (SAM's stop)

The SDK always honours a stop SAM sends over the link. This release of SAM has no API or console control that sends
one yet, so until it does an agent is never stopped this way; the code below needs no change when it arrives.

```python
agent = MeshAgent(config, handle, transport=transport)


@agent.guard  # your own entry points stop too, not only the mesh
def answer_from_my_api(question): ...


agent.on_stop_change(lambda allowed, reason: my_server.pause() if not allowed else my_server.resume())
```

Mesh tasks stop on their own in every framework. For runs outside the mesh (your own API, a notebook, a job), add the
framework's `stop_guard(agent)` where you build or run the agent; each raises `AgentStopped` at the start of a run and
before every model and tool call:

| Framework | Guard |
|---|---|
| LangChain / LangGraph | `graph.with_config(callbacks=[stop_guard(agent)])` |
| Google ADK | `Runner(..., plugins=[stop_guard(agent)])`; ADK wraps it in `RuntimeError`, cause `AgentStopped` |
| OpenAI Agents SDK | `Runner.run(my_agent, text, hooks=stop_guard(agent))` |
| CrewAI | `stop_guard(agent, crew).kickoff()`; checks at kickoff and in each crew agent's LLM call |
| Strands Agents | `Agent(..., hooks=[stop_guard(agent)])`; raises `AgentStopped` itself, unwrapped |
| Microsoft Agent Framework | `Agent(..., middleware=stop_guard(agent))`; in function middleware the stop is an `AgentStopped` that is also a `MiddlewareFailure`, because the framework turns any other exception there into a tool error and carries on |
| Hermes Agent | `guard = stop_guard(agent)` once at start-up, `guard.watch(hermes_agent)` per agent: refuses every tool call and gateway message while stopped and interrupts watched agents |

A running mesh task stops at once in LangChain, LangGraph, ADK, Strands, Agent Framework, Hermes and the OpenAI Agents
SDK (its asyncio task is cancelled, including an async tool in flight). CrewAI runs in a worker thread that cannot be
interrupted and swallows exceptions from step callbacks and tools, so `run_agent` checks the task in each crew agent's
LLM call: the crew stops at its next model call, after the step in progress finishes. The same check stops a crew whose
task was cancelled or hit a limit.

- **Stop:** SAM sends the stop (`control`) and refuses the agent's tasks itself. The agent stops its running
  tasks (each answered as failed), refuses new tasks, and `agent.check()`, `agent.guard` and the stop guards raise
  `AgentStopped` in every code path that uses them. SAM keeps the card down while the agent is stopped. A stop or
  release that changes whether the agent may run is written to the audit trail (`agent stopped by SAM`,
  `agent released by SAM`, with SAM's reason); one that repeats what the agent already holds writes nothing.
- **Kept across restarts:** the stop is saved under `state_dir` (default `~/.sam_link_sdk`) before it takes effect, in a
  file named after SAM's host, port and path and the agent's name, so a restarted agent starts stopped until the
  link says otherwise. Every `welcome` says whether SAM has the agent stopped.
- **Fail closed:** once connected, a link that stays down longer than `offline_grace_seconds` (900) stops the agent
  (reason `disconnected`) until it reconnects. A request-driven agent learns the state each time it connects, and saves
  none of its own.
- **`exit_on_stop=True`** also ends `run_forever`, so a process that only serves the mesh exits.
- **Limits:** it is cooperative. Code that skips these checks, or does not run the SDK, keeps running, and so does
  anything the agent holds outside SAM (its own model keys, tools and APIs). A model call or synchronous tool already
  under way finishes before the stop takes effect. SAM's hard stop is revoking the agent's link token (SAM's
  configuration) and its use of SAM's models.

## Credential redaction

Tool arguments, tool results and the arguments inside model responses are redacted by the adapters before they are
reported, with a port of SAM's redactor (`sam_link_sdk.redact`), and again by SAM with SAM's own before
anything is published: values under credential names (`password`, `api_key`, `accessToken`
...), `name=value` assignments, Authorization headers, Bearer tokens, JWTs, private keys, vendor token prefixes,
webhook URLs, and URL passwords and credential query parameters become `<REDACTED>`. Names stay visible, and counts
like `max_tokens` are left alone. Tests compare the port with the Go redactor's own output
(`tests/fixtures/go_redact_cases.json`). The model's answer text and the final answer are not redacted, as in SAM.
Arguments shown in an approval prompt are redacted too. A peer's answer reaches the agent redacted by SAM: its text,
its error message and every string of its data.

## Audit trail

SAM audits access decisions and tool runs inside the agents it runs; an SDK agent writes the same kinds of records for
what it decides itself (`sam_link_sdk.audit`): agent delegation granted/denied (one record when a `ctx.call_agent` ends
with SAM's decision, naming the peer and never the payload: granted when SAM carried the call and the peer's answer
came back, denied when SAM refused it as not allowed; a call that never left the agent, one SAM could not carry, and
one that timed out or was cancelled write no record), agent stopped or released by SAM (one record when a stop or
release changes whether the agent may run, with SAM's reason), tool access denied (a tool's `required_scopes`), tool
approval granted/refused, task limit reached, and tool execution succeeded/failed with its duration. Each carries a
per-process increasing `seq`, the user, task, agent and tool names, and never any prompt, tool argument, result or
credential. Authentication and access to the agent are SAM's decisions, audited by SAM before a task is delivered. By
default each record is one JSON line on the `sam_link_sdk.audit` logger; pass `AgentConfig(audit_sink=fn)` to receive
`AuditEvent` objects instead (for a SIEM or file). A tool counts as failed when its result is just `{"error": ...}`,
which is how every adapter reports a failed or refused tool. SAM agents write their audit records to their own
process log too, and the deployment's log pipeline collects them; ship this agent's log (or point `audit_sink` at the
same destination) to keep its records next to theirs. They are written by the agent's own code.

## Tool credentials

SAM's per-user OAuth for tools (each user connects their own account; SAM stores, refreshes and injects the token)
applies only to tools SAM runs. An SDK agent's own tools use credentials it holds itself, usually a shared service
account, unless it runs its own OAuth flow (`ctx.user_id` is the user SAM verified). To act with the user's own
credentials, delegate to a SAM agent that has the tool (`mesh_agent_tools`): SAM supplies that user's credentials and
this agent never sees them.

## The link token

The link token is the agent's credential: whoever holds it can join the mesh as the agent. The SDK sends it in the
`Authorization` header of the WebSocket upgrade, to SAM's address only, and keeps it out of its logs (the
websockets client's handshake headers are logged redacted) and out of `LinkConfig`'s repr.

- **Encrypted, or not sent.** A `wss://` (`https://`) address is verified against the system's CAs, or the
  `SAM_LINK_TRUST_STORE_PATH` ones; there is no switch that turns verification off. A plain `ws://` (`http://`) address
  is taken for this machine's loopback only. To another host the agent refuses to connect (a `LinkError` naming the host)
  unless `SAM_LINK_ALLOW_INSECURE=1` (`LinkConfig.allow_insecure`) says the network is trusted, and then it logs a
  warning that the token is not encrypted.
- **No redirects off the address.** An answer to the upgrade that redirects to another scheme, host or port is refused
  and the agent retries its own address with backoff: the WebSocket client would otherwise follow it and send the same
  `Authorization` header to wherever it pointed.
- **Written privately.** `sam-link-sdk link-token --out <file>` creates the file owner-only (0600) and refuses a path that
  already exists, a link included, so the token never lands in a file someone else placed. Keep the token file, and
  `SAM_LINK_TOKEN`, out of images and source control; a rotated token is picked up from `SAM_LINK_TOKEN_FILE` on the next
  connection.

## Logging

The SDK logs ids, counts and lengths only. It never logs prompts, completions, tool arguments or results, token values
or credentials.
