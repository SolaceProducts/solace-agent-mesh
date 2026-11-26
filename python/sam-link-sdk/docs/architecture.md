# SDK architecture

How the SDK is built, for the curious: nothing here is needed to write an agent. The link (`sam_link_sdk/link/`) speaks
one protocol, whose wire contract is [link-protocol.md](link-protocol.md).

## Layers

```
handler(ctx)  +  framework adapters (sam_link_sdk.langchain, .strands, .adk, ...)
        │  TaskContext: emit steps, ask the user, call peers, record usage      (framework-neutral, no SAM shapes)
        ▼
core: MeshAgent                                                                  sam_link_sdk/agent.py, tasks.py, ...
  task registry, running the handler, limits, concurrency, stop (check/guard),
  usage totals, audit, metrics, serverless entry points
        │  Transport (semantic): tasks in; events, prompts, results, peer calls, description out
        ▼
LinkTransport                                                                    sam_link_sdk/link/
  the agent link protocol: the core's calls as frames, frame for frame, to SAM's agent_link entrypoint
  (SAM's broker, its envelopes, token checks, cards and spend records stay behind the entrypoint)
```

## The core

`MeshAgent` (`sam_link_sdk/agent.py`) owns a few collaborators, each built with the things it uses, and holds none of their
state itself:

| Collaborator | Module | Does |
| --- | --- | --- |
| `_TaskRunner` | `tasks.py` | The task registry, running the handler, per-task limits, concurrency, cancel, the final outcome, drain and abandon at shutdown, the keepalive. Reads SAM's stop from `StopState`. |
| `_StopController` | `stop.py` | SAM's stop: `check`, `guard`, the listeners, the saved stop and its retry, the offline grace timer, and the reaction to a change (stop the running tasks through the runner, tell the mesh and the listeners). It holds a `StopState` (the pure state machine). |
| `_WakeHandler` | `serverless.py` | `handle_wake`, `announce` and `retire`, for an agent a runtime starts per request. |
| `_MeshHost` | `agent.py` | The one object the transport is bound to (`TransportHost`): `on_task`, `on_cancel`, `on_control`, `on_connection`, `wants_events`, `on_link_failure`, `description`, `draining`, `stop_reason`. It hands each call to the collaborator that serves it. |

Smaller shared pieces: `_Lifecycle` (`agent.py`: stopping, stopped, serving ended, the exit event, a link failure),
`Health` (`_health.py`: the open health problems the card reports) and `TrackedTasks` (`_util.py`: spawned answers and
announcements a disconnect waits for).

`TaskContext` (`context.py`) is what a handler holds. The core's state for the same task is its `_run`, a `_TaskRun`
(`_run.py`): the run state (`RunState`: running, halted, cancelled; a task is answered separately), the channel to the
transport, the usage, the model and tool timings, the limits and the tool scopes (`_ToolPolicy`). The runner and the
adapters use `ctx._run`; a handler uses `TaskContext`, which delegates to it.

## The `Transport` interface

`sam_link_sdk/_model.py` (its step events are in `sam_link_sdk/events.py`). Internal: the agent link is the only transport, and the interface is not an extension point
(a transport decides which tool scopes SAM granted each user). Its public part is the step events a handler reports
(`ModelStarted`, `ModelFinished`, `ModelFailed`, `ToolStarted`, `ToolFinished`, `ToolCall`, `Progress`). Semantic: it names tasks, events, prompts, peers, never topics or message properties. The
data types mirror [link-protocol.md](link-protocol.md) one to one (`IncomingTask`, `StepEvent` kinds, `Prompt`,
`PromptAnswer`, `TaskOutcome`, `PeerCall`, `PeerResult`, `PeerInfo`, `AgentDescription`).

A transport:

- delivers `IncomingTask`s, cancels and prompt answers to the core through callbacks, and takes `ack`/`nack`;
- sends a task's step events, prompts and its final outcome;
- carries peer calls and returns their result;
- publishes the agent's description and removal, and reports the agent's peers;
- reports SAM's stop and release (`on_control`), says whether it leaves the listing to SAM during a stop
  (`_manages_listing`), and may name the key SAM's stop is saved under (`_stop_key`).

The interface's methods have underscore names: they are the seam between the core and the link, not something a user
calls. A user builds `LinkTransport(LinkConfig(...))` and passes it to `MeshAgent`; that constructor and its `config`
attribute are all `LinkTransport` offers.

Each task carries the tool scopes SAM granted its user (`IncomingTask.granted_tool_scopes`), and no granted list
means none are granted, whatever the transport: a tool that requires scopes runs only when every one of them was
granted. Who the user is beyond their id, and whether they may use the agent at all, is decided by SAM before the task
is delivered.

What is the transport's job and not the core's: everything about *how* SAM is told. On the agent link the entrypoint
verifies users, checks scopes on the task, builds SAM's messages, approval screens, cards and spend records, and
`LinkTransport` is a thin frame codec.

What is the core's job: accepting a task, running the handler, limits, concurrency, stop semantics for local work,
usage accounting, memory, audit and metrics. The core imports only the standard library and itself
(`tests/test_layering.py` checks it); it never imports `sam_link_sdk.link`.

## What the core promises

- `MeshAgent(config, handler)` joins through the link built from the environment; `transport=` takes a `LinkTransport`
  built in code.
- The link speaks one protocol (subprotocol `sam-agent-link.v2`).
- `TaskContext` is built by `MeshAgent` with its channel; the adapters report `ctx.emit_event` step events.
- `import sam_link_sdk` loads nothing beyond the standard library; the link adds `websockets`.

## Packaging

| Install | Gets |
| --- | --- |
| `sam-link-sdk` | core + `LinkTransport` + the `sam-link-sdk` CLI (depends on `websockets` only) |
| `sam-link-sdk[langchain]`, `[langgraph]`, `[strands]`, ... | one framework adapter each |
| `sam-link-sdk[otel]` | OpenTelemetry metrics and the generic span adapter |
| `sam-link-sdk[foundry]`, `[agentcore]` | the request-driven runtime entry points |
