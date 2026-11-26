# Runtime behaviour

How a `MeshAgent` behaves on the mesh, beyond what the handler sees ([handlers.md](handlers.md)).

- **Steps:** each step reaches the agent as a step event (`ctx.emit_event`; the adapters build them through
  `sam_link_sdk._adapter`) and the link sends it on as an `event` frame. The adapters redact tool arguments, tool
  results and the arguments inside model responses before they report them, and SAM redacts them again
  with SAM's own redactor before anything is published ([security.md](security.md)).
- **Tool arguments and results on the wire:** every string inside them is cut to 16 KB with a `…[truncated N bytes]`
  marker, as SAM agents do, so a large tool result cannot push a step over the link's frame limit. The agent's own
  framework still sees the full result, and the SDK does not move large results into artifacts the way SAM agents do.
- **Model attribution:** every model step names its model, and SAM adds the model family and provider from
  the model id when it builds SAM's messages.
- **Cost:** SAM's evidence store prices each model response from its usage (model, input, cached and output tokens) with
  a rate card. SDK agents send the same usage, so their
  calls are priced like SAM agents'. A model the card does not list stays unpriced, and so does a call with cached
  tokens on a provider that charges cache writes when the framework does not report the writes: the SDK sends them when
  it has them (`cacheWrite` on the link), and otherwise leaves them out, which SAM reads as unknown rather than zero.
  SAM records each model call's tokens in SAM's cost ledger; `AgentConfig.spend_model_keys` maps a model
  name to the SAM model configuration whose prices cost it. An SDK agent's hard
  limits are `max_llm_calls` and `task_timeout_seconds`.
- **Usage the adapter could not read:** each adapter reads its framework's usage by field name, so a framework or
  provider release that renames a field would leave the call's tokens at zero in SAM's usage and cost records with
  nothing failing. When a model response produced text or tool calls, its usage was read as having no input or no
  output tokens, and the usage the framework handed over still holds a token count (a field named for tokens, above
  zero), the agent logs one WARNING per adapter and process, `token usage unread: ...`, naming the adapter, the model
  and the usage's field names (never their values). The call is still recorded as read: the SDK makes no count up. A
  usage that counts nothing (a local or scripted model, a stream that carried no usage) is no reason to warn, and so is
  a framework that turns a renamed provider field into its own zero (ADK through LiteLLM, Hermes): nothing is left to
  read there. The usage fields each adapter reads from a typed framework structure are also in its drift check
  (`FrameworkDrift`, [frameworks/README.md](frameworks/README.md)).
- **The description:** `AgentConfig(tools=[ToolInfo(...)], input_schema=..., output_schema=..., welcome_message=...,
  suggestions=[Suggestion(...)])` is described to SAM, which publishes the `sam/tools`, `sam/schemas` and `sam/welcome`
  card extensions from it. The web UI shows the tools and welcome screen, and a workflow takes a node's schemas from the
  card. The tool list is your declaration: the mesh does not check it against what the agent really runs. Each
  adapter's `tool_infos(tools)` builds it from the framework's tools, with the scopes `with_scopes` gave them (Hermes:
  `tool_infos(tools, scopes=...)`).
- **Keepalive:** a task that has been silent for 120 s gets a progress update (`keepalive_seconds`), so SAM's
  300 s idle timer never fires. A task waiting for its user gets none. A report that fails is logged at ERROR with the
  task id and does not stop the keepalive for other tasks.
- **Shutdown:** on Ctrl-C or SIGTERM the agent first retires (SAM takes it off the mesh, so no new work is routed here),
  then lets running tasks finish for up to `shutdown_grace_seconds` (default 20 s, as SAM agents do), then abandons
  what is left. An answer that waits for SAM to deliver its task again (after a reconnect) waits for that for the
  grace too, so a stop can take the whole of `shutdown_grace_seconds` with no task running; what still waits when it
  ends is sent as it is before the agent disconnects. An abandoned task is not acknowledged, so SAM delivers it
  again, to another replica or a later connection, which repeats its model and tool calls. Tasks that arrive while
  draining are not started and go back the same way. The whole shutdown takes at most about
  `shutdown_grace_seconds` plus 8 s: the retire gets 2 s (it does not wait for a link that is down to come back),
  the held answers sent at the end of the grace get at most 1 s beyond it (none when the link is down), and tasks
  already stopped and answers still going out share 5 s after the drain. With the default that is 28 s, inside
  Kubernetes' default `terminationGracePeriodSeconds` of 30; raise the pod's when you raise `shutdown_grace_seconds`. A run left behind may still be going after that: a
  framework's run in a worker thread (CrewAI, Hermes, any blocking call) cannot be cancelled, and a handler can swallow
  its cancel, and Python waits for either before the process ends. So `run_forever()` ends the process itself once the
  agent has stopped, if it left a run behind: exit code 0 (1 when the link gave up on a refused token), logging flushed,
  `atexit` handlers not run. `run_forever(exit_when_stuck=False)` keeps the process for an app that runs other work
  beside the agent.
- **Redelivery:** SAM delivers a task from the agent's queue, which SAM creates with a redelivery limit
  of 3. A task the agent gives back (its run was cancelled from outside the agent, or the core could not take
  it) goes back on that queue; an agent process gives a task back at most once and answers it failed the second time
  ([operations.md](operations.md
