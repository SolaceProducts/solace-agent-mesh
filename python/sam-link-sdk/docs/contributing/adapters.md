# Adapter guarantees

What every framework adapter promises and how each framework's shape changes it. This is for contributors and for
anyone who needs the exact behaviour; a user choosing a framework needs [the framework index](../frameworks/README.md).

## What each framework supports

The arguments differ where the framework's shape does: an ADK agent and its toolsets are built once with the Runner,
not per task (no `ctx`); OpenAI Agents' hooks see neither a tool's failure nor a way to refuse a call, so its step
reporter prepares the agent it reports on (`agent`, run as `hooks.agent`); Hermes builds its tools inside the agent, so
its approvals are a policy for `run_agent` (no `ctx`, no `extra_tools`); CrewAI has no hook object, so `stop_guard` patches
the crew.

| | LangChain / LangGraph | OpenAI Agents | ADK | CrewAI | Strands | Agent Framework | Hermes |
|---|---|---|---|---|---|---|---|
| `run_agent(ctx, ...)` | `model, tools` (the agent's whole list) | `agent` | `runner` (refused without the step reporter) | `build` | `build` | `agent` | `build` |
| `step_reporter` | `(ctx)` | `(ctx, agent, *, run_config=None)` | `()` | none: the event bus and hooks are process-wide | `(ctx)` | `(ctx)` | none: the hooks are process-wide |
| `stop_guard` | `(agent)` | `(agent)` | `(agent)` | `(agent, crew)` | `(agent)` | `(agent)` | `(agent)` |
| `with_approval` | `(ctx, tools, *, names, message)` | `(ctx, tools, *, names, message)` | `(tools, *, names, message)` | `(ctx, tools, *, names, message)` | `(ctx, tools, *, names, message)`, a hook | `(ctx, tools, *, names, message)`, middleware | `(*, names, message)`, for `approval=` |
| `ask_user_tool` | `(ctx)` | `(ctx)` | `()` | `(ctx)` | `(ctx)` | `(ctx)` | none: Hermes' `clarify` tool |
| `mesh_agent_tools` | `(ctx, agents)` | `(ctx, agents)` | `(agents)` | `(ctx, agents)` | `(ctx, agents)` | `(ctx, agents)` | `(ctx, agents)` |
| `with_scopes(tools, scopes)` guards | any LangChain tool | function tools (a hosted tool is a `ValueError`) | functions and ADK tools | crew tools | Strands tools (`@tool`) | function tools (`@tool`) | none: `tool_infos(scopes=...)`, and the hook enforces |
| Peer steps nest under the tool call | yes | yes | yes | no: CrewAI gives a tool no call id | yes | yes | yes |
| Request context in the prompt | system prompt | instructions | each model request | `request_context` kickoff input | system prompt | a system message | system prompt |
| Conversation memory | `memory=` or a checkpointer | `memory=` | ADK session per chat | `memory=` (the transcript as `{history}`) | `memory=` | `memory=` | `memory=` (Hermes' own is off) |
| A call to a tool the user lacks scopes for | refused (a graph's default `ToolNode` then ends the run) | refused (an MCP server's tool ends the run) | refused | refused | refused | refused | refused |
| Nested runs | [below](#nested-runs) | [below](#nested-runs) | [below](#nested-runs) | [below](#nested-runs) | [below](#nested-runs) | [below](#nested-runs) | [below](#nested-runs) |
| That tool offered to the model | no in `run_agent`; in your graph unless built with `ctx.permitted_tools` | no, in every agent it hands off to | no | no with native tool calling; yes with text (ReAct) tool calling | yes unless `build` uses `ctx.permitted_tools` | no | no |
| Failed tool shown as failed | yes | yes | yes (then ADK raises it) | yes | yes | yes | yes |
| Failed model call's category (a run that ends on a tool's own error is never one) | recorded | from the raised exception | from the raised exception | from the raised exception | recorded | recorded | recorded |
| Reasoning stream (`stream_thinking`) | thinking blocks, `reasoning_content` (not through `ChatOpenAI`) | reasoning summaries | thought parts | not available | reasoning blocks | reasoning content (not through the Chat Completions client) | reasoning text |
| Per-task limits | stop at the limit | stop at the limit | stop at the limit | answered at the limit, crew stops at its next model call | none of its own | answered at the limit (one more model call without tools) | stop at the limit |
| Lowest supported | langchain-core 1.6.9, langgraph 1.2.14 | openai-agents 0.20.0 | google-adk 2.14.1 | crewai 1.15.27 | strands-agents 1.61.1 | agent-framework-core 1.22.0 | checkout `b65e078c` on Python 3.14 (installed by hand; CI checks the hook boundary only) |

A tool the framework offers under a name the agent's card does not list is refused too, once the card lists a scoped tool
([security.md](../security.md#required-scopes-rbac)), except `ask_user_question` and the `peer_<name>` tools.

A scoped tool checks the task's grants where it runs, and refuses outside a mesh task
([security.md](../security.md#required-scopes-rbac)): that holds whatever runs the tool. The hooks add the rest, and a
refused call is the same in every adapter, judged by the scopes SAM granted the task's user (the agent link sends them
with each task): the tool does not run, the model is told `Access denied: you do not have the scopes required to use
<tool>.`, the call shows as its start and a failed result carrying that text, and
the audit trail records `tool access denied` (once per tool and run) and the call as failed. The OpenTelemetry adapter
is the one exception: spans record what already happened, so it cannot refuse a call (a tool guarded with `with_scopes`
still refuses itself), and its `run_agent` logs a warning when the agent declares tool scopes. One difference in the
text the model sees: a LangGraph `ToolNode` built with `handle_tool_errors=True` wraps the refusal in its own template
(`Error: ToolAccessDenied('Access denied: ...')\n Please fix your mistakes.`); build it with `handle_tool_errors=lambda
error: str(error)` to give the model the refusal as it is.

A tool's outcome follows one rule in every adapter: a call failed when the framework says so (the tool raised, or the
framework marked the result failed) or when its result is `{"error": ...}` and nothing else; a result that only has an
`error` key among others is a success.

`tests/test_adapter_conformance.py` drives every adapter, and the OpenTelemetry one, through the same scenarios
(including the refusal above, and, through one real nested shape per adapter, the same refusal and the nested run's
steps and tokens: `nested_denied_tool`, `nested_steps_and_usage`), and encodes each legitimate difference with its
reason; `tests/test_scope_guard.py` calls every adapter's scoped and approval-guarded tools through paths no hook sees
(outside any task, from a thread with no context, from a fresh agent with no reporter) and runs the nesting shapes
above; `tests/test_fail_closed.py` runs each adapter's real framework with a scripted model and checks, on the tool's
body itself, that a guarded tool does not run when its approval is denied, times out or fails, or when the task was
stopped (and, for CrewAI, when the user lacks the scope), so a framework that stops honouring a guard's hook fails a
test; `tests/test_adapter_surface.py` checks the names above, and that the removed ones are gone.

## Nested runs

A run that starts another run (an agent run as a tool, a handoff, a sub-agent, a delegated subagent) is one of three
kinds, per framework:

- **Followed:** the adapter reaches the nested run with its hooks, so it is covered as the first run is.
- **Refused before it starts** (`NestedRunRefused`): the framework would run it where no hook can reach it, and it could
  reach tools that cannot carry the check themselves (an MCP server's, a toolset's).
- **Unseen:** a run the adapter cannot know of (an agent a tool's own code builds and runs, a run on a thread that lost
  the context). There a scoped tool still refuses a user without its scopes, and refuses outside a mesh task, by its own
  check; the rest depends on the guarantee, as below. Steps and tokens of an unseen run are never reported.

Each cell says what holds for that guarantee; "refused by the tool" means the tool's own check (`with_scopes`).

| Framework | Nested run | Scopes | Approvals | Stop | Steps and tokens |
|---|---|---|---|---|---|
| LangChain / LangGraph | followed: subgraphs, and runnables a tool runs with the run config (an approved tool's included) | refused (step reporter and tool) | asked (`with_approval`, or a LangGraph `interrupt()` through `run_graph`) | ends at the next model or tool call, on any thread | reported |
| | unseen: a runnable run without the callbacks, a thread without the context | refused by the tool | the `with_approval` tool asks for its own task, and refuses outside it | a scoped tool refuses; the run's model calls go on | not reported |
| OpenAI Agents | followed: agents handed off to, directly or through `handoff(...)`, and agents run as tools (`Agent.as_tool`) at any depth, behind `with_scopes` or `with_approval` too | refused (hooks and tool) | asked (`with_approval`, or the tool's own `needs_approval`) | the task's cancel ends the run; an agent tool's model and tool calls check the stop first | reported (an agent tool's steps between its call's start and result, not nested under it) |
| | refused: an agent tool whose agent cannot be found the way `Agent.as_tool` keeps it | | | | |
| | unseen: a `Runner` a tool's code starts | refused by the tool | the `with_approval` tool asks for its own task, and refuses outside it | a scoped tool refuses; the task's cancel reaches it when the tool awaits it | not reported |
| ADK | followed: sub-agents, `AgentTool`s with the Runner's plugins (the default) | refused (plugin and tool) | asked (`with_approval`, or the tool's own `require_confirmation`) | stopped | reported |
| | refused: `AgentTool(include_plugins=False)` anywhere among the tools, inside `with_approval` included (one a toolset hands over at run time ends the run at the call) | | | | |
| | unseen: a Runner a tool's code starts without the plugin | refused by the tool; a toolset's tools (MCP) are not | the `with_approval` tool asks the current task's user, and refuses outside a task | a scoped tool refuses | not reported |
| CrewAI | followed: a hierarchical crew's manager and coworkers; a crew a tool kicks off | refused (process-wide tool hook, and tool) | asked where that crew has the `with_approval` tools (the wrapper is the tool); a crew given the plain tool runs it unasked | ends at its next model call | reported (process-wide event bus) |
| | unseen: a crew on a thread without the context | refused by the tool | the `with_approval` tool refuses | a scoped tool refuses | not reported |
| Strands | followed: agents given as tools (`tools=[inner]`, `inner.as_tool()`), at any depth | refused (hooks and tool) | asked (the outer agent's `with_approval` hooks, or Strands' own interrupts) | ends at the next model or tool call | reported |
| | refused: an agent with `background_tasks`, outer or nested | | | | |
| | unseen: an agent a tool's code builds and runs | refused by the tool | not asked: approval is a hook, so keep approval-only tools out of such an agent | a scoped tool refuses; the task's cancel reaches it when the tool awaits it | not reported |
| Agent Framework | followed: `Agent.as_tool()` agents at any depth, those a context provider adds for one run included | refused (middleware and tool) | asked (`run_agent`'s `with_approval` middleware, or the tool's own `approval_mode="always_require"`) | stopped | reported |
| | refused: any agent behind a tool that is not a plain `Agent` (a `WorkflowAgent`, a functional workflow agent, a custom `BaseAgent`), or whose agent cannot be found | | | | |
| | unseen: an agent a tool's code runs | refused by the tool | not asked: approval is middleware | a scoped tool refuses; the task's cancel reaches it when the tool awaits it | not reported |
| Hermes | followed: subagents `delegate_task` starts, while the parent's turn lasts (in the background too) | refused (hook) | asked (`approval=`) | interrupted with the parent | reported |
| | a background subagent after the parent's turn ends | every tool call refused | every tool call refused | interrupted when the turn ends | not reported: the task has ended |
| | unseen: an agent a tool starts outside `delegate_task` | refused while any mesh run is open; after that it is not SAM's (Hermes' registry tools cannot carry the check) | the same | not reached | not reported |

Work a handler leaves running after its task is answered, or after SAM stops the agent: its guarded tools refuse in
every adapter. The hooks of LangChain, OpenAI Agents, ADK, CrewAI, Strands and Agent Framework also refuse its next
model and tool calls; Hermes' hooks refuse its tool calls (a Hermes hook cannot refuse a model call; the run is closed
and its subagents interrupted); the OpenTelemetry adapter refuses nothing, but its `run_agent` does not start for a
finished task. No guarded tool runs, nobody is asked and no peer is called; nothing a finished run does reaches a later
delivery of the task; a step of a call already under way when the answer went out never reaches SAM (the agent link
holds no answered task), and a call the adapter refuses reports nothing.

The OpenTelemetry adapter only reads spans: it reports the nested spans it is handed and refuses nothing, but a tool
guarded with a framework adapter's `with_scopes` refuses itself there too.

## Caches in front of a tool

A cache that answers a tool call without running the tool also skips the tool's own check. What each framework has
in front of a tool's body (checked against the tested versions; `tests/test_tool_caches.py`):

| Framework | In front of a tool's body | What sam-link-sdk does |
|---|---|---|
| CrewAI | the crew's tool-result cache (`Crew(cache=True)`, the default), read before a tool is called, keyed by tool name and arguments | `with_scopes` and `with_approval` tools are never cached (their `cache_function` is always false), so a guarded tool's result, or its refusal, never answers a later call, another user's included |
| LangGraph | a node's `cache_policy` (with `compile(cache=...)`) answers a hit without running the node | nothing it can do: give a `ToolNode` that holds scoped or approval-guarded tools no `cache_policy` |
| OpenAI Agents | within one run, a tool call item already seen (the same call) is not run again | nothing needed: no result is reused across runs |
| Strands | its tool-stage middleware may answer from a cache | nothing ships with one, and the API is private (`strands._middleware`); a middleware you add that answers from a cache skips the tool's check |
| Hermes | duplicate tool calls in one model response are dropped | nothing needed: no result is reused |
| ADK, Agent Framework, LangChain | caches of declarations and schemas only | nothing needed |

## What a refusal spends

A call our wrappers or hooks refuse (scopes, a denied approval, a stopped or answered task) must not spend what belongs
to a call that runs. In the tested versions (`tests/test_refusal_budgets.py`):

| Framework | Counter a refused call meets | What it spends |
|---|---|---|
| CrewAI | the tool's `max_usage_count`, claimed before the tool runs | nothing: the wrappers do not carry it; the wrapped tool claims it only for a call that runs |
| CrewAI | `tool_failure_policy` (counts a tool's `ToolFailure`) | nothing: a refusal is an `{"error": ...}` result, not a `ToolFailure` |
| Agent Framework | the tool's `max_invocations` and `max_invocation_exceptions`, counted in its `__call__` | nothing: the scope check sits before that call; the middleware refuses before it |
| OpenAI Agents | the adapter's failure tracking | nothing: it marks calls that raised; a refusal is reported failed by its result |
| every framework | the run's model turns or iterations (`max_model_calls`, `max_iter`, `recursion_limit`, `max_llm_calls`, `max_iterations`, `max_function_calls`) | the model's next turn, as for any failed call: it counts the model's requests, not the tool's |
| LangChain, ADK, Strands, Hermes | no per-tool limit | nothing |

## When a hook stops a run

A stop reaches a run through our hooks, guards and callbacks: the next one raises `TaskHalted`. Each framework passes
that on its own way (`tests/test_halt_chain.py`, a run stopped inside a tool, through each real `run_agent` and through
a real agent):

| Framework | What it does with what our code raises | What ends the run |
|---|---|---|
| LangChain | a callback's exception (`raise_error`) ends the call bare | the next model or tool callback |
| OpenAI Agents | a run hook's exception ends the run bare | the next model or tool hook |
| ADK | wraps a plugin's exception in `RuntimeError("Error in plugin ...")` | the next model or tool callback |
| CrewAI | the guarded LLM's `TaskHalted` ends the kickoff bare; its before-LLM hook can only abort with its own `HookAborted`; a refused tool call is a result the crew goes on from | the next model call |
| Strands | wraps a hook's exception in `EventLoopException` | the next model or tool hook |
| Agent Framework | middleware's exception ends the run bare | the next model call or tool call |
| Hermes | a hook blocks a tool call (the model sees the refusal) but cannot refuse a model call; an interrupted turn is reported failed | the hooks interrupt the agent (`hard_interrupt`), and its turn ends |
| OpenTelemetry adapter | whatever the framework it reads does | the framework's own end |

Every adapter's `run_agent` ends in the bare `TaskHalted` of its task (`_adapter.halts_bare`): found anywhere in the
raised exception's `__cause__`/`__context__` chain or an exception group, or, when the framework swallowed it (one of
our hooks or guards met the stop during the run, and the run went on), read from the task once the run ends. A stop that
nothing in the run met leaves a completed answer alone. The agent answers a bare `TaskHalted` with the stop's message,
not as a crash.

## The order of an approval

The invariant: a tool behind an approval never runs unless the user approved that call
(`tests/test_approval_invariant.py`). A check before the prompt only refuses, with a result built here, for a fact that
is certain and that no legitimate call can get past; anything else is asked, even when the call may then fail. It never
hands the call to the tool to refuse it. The checks before the prompt:

| Framework | Refused before the prompt | Why it is certain |
|---|---|---|
| LangChain | stopped or answered; scopes | the task's own state; the user's grants |
| OpenAI Agents | stopped or answered; scopes | as above |
| ADK | stopped or answered; scopes; outside a mesh task | as above |
| CrewAI | stopped or answered; scopes; the spent `max_usage_count` (CrewAI's own words) of a tool that runs through CrewAI's own claim, `@tool` functions included; the event loop's own thread (it could not wait for the answer) | `BaseTool.run` and `Tool.run` claim a use before the body and cannot run a spent tool; a tool that claims its own way is asked |
| Strands | the step reporter's hook first: stopped or answered; scopes | as above |
| Agent Framework | the step reporter's middleware first: stopped or answered; scopes; the stock function tool's spent `max_invocations` or `max_invocation_exceptions` (its own words) | a FunctionTool whose call is Agent Framework's own cannot run once spent; another class is asked |
| Hermes | stopped or answered; scopes | as above |

After the prompt the same check runs again: an approval that arrives after SAM stopped the agent, the task was cancelled
or it was answered runs nothing (a crew's or Hermes' thread, which a stop does not cancel, waits on it; a task's scopes
do not change, so they need no second look). That is the rule for every wait the SDK puts before a guarded body.
Arguments are never judged before the prompt: whether a tool can read them is the tool's business (a hand-built tool
reads anything), so the user sees them as the model gave them.

## Notes

- **Versions and API drift.** Each adapter is tested with the version in the table (the OpenTelemetry adapter with
  opentelemetry-sdk 1.45.2, semantic conventions 0.66b2), which is the extra's lower bound in `pyproject.toml`. The
  extras set no upper bound: a newer version installs, and is checked for the hooks the adapter uses when it is
  imported. Each adapter checks, when it is imported, that every hook, event and field it depends on still
  exists (the usage fields it reads from a typed structure included: OpenAI Agents' `Usage`, ADK's and Gemini's usage
  metadata, Agent Framework's `UsageDetails`, Strands' `Usage`, LangChain's `UsageMetadata`), and otherwise raises `FrameworkDrift` (exported from `sam_link_sdk`) naming the framework, the installed version
  and the missing symbols, instead of running and reporting nothing. Members that exist only on instances (Agent
  Framework's middleware contexts, Strands' tool registry, OpenAI Agents' failure invoker, Hermes' plugin manager and
  agent) are checked on first use, and Agent Framework's and OpenAI Agents' `as_tool()` shapes by building a throwaway agent on the first
  lookup. LangGraph's `GraphInterrupt` is checked at import when LangGraph is installed. The check finds removed
  hooks, not changed behaviour; the conformance tests find that, so run them after moving a framework version. A
  usage field renamed where no type declares it (CrewAI's and Hermes' usage dicts, a provider's own fields) is not
  caught at import; an agent that reads a usage holding token counts as none logs a warning
  ([behaviour.md](../behaviour.md)), and the end-to-end suite pins every count.
- **Framework internals still used.** Hermes: its plugin manager's hook registry and the agent's `_persist_disabled`
  switch ([hermes.md](../frameworks/hermes.md)). CrewAI: `run_agent` and `stop_guard` stop a crew by wrapping each crew agent's LLM
  `call` and `acall` (and `run_agent` leaves denied tools out of those calls' tools; a streamed call goes through
  `call`); CrewAI 1.15's public before-LLM-call hook ends
  a kickoff only with its own `HookAborted` and swallows any other exception, so `AgentStopped` or `TaskHalted` cannot
  come out of it. OpenAI Agents: an `Agent.as_tool` tool is recognised by its private `_agent_instance` and
  `_is_agent_tool` fields, and run on the task by running its function (`_invoke_tool_impl`) over a closure whose
  `self`, `hooks` and `run_config` are swapped for SAM's (the SDK keeps them nowhere else); `with_scopes` and
  `with_approval` rebuild a tool's invoker from `_FailureHandlingFunctionToolInvoker`'s `_invoke_tool_impl` and
  `_on_handled_error`. Agent Framework: the agent behind `as_tool()` is found by its `_agent_wrapper` function and
  that function's closure. Strands: nested agents are read from `tool_registry.registry` and each tool's `agent`. ADK:
  `check_require_confirmation` and `adk_request_confirmation`'s name from `google.adk.utils._function_call_names`.
  LangChain: a LangGraph interrupt is recognised by the name `GraphInterrupt`. LangChain: a tool started
  without a tool call id takes the oldest pending call id for its name. Agent Framework: the call id comes from the
  function context's `metadata["call_id"]`.
- **Approvals fail closed.** A refused or timed-out approval does not run the tool. The model gets an error result
  (`The user did not approve this call to <tool> (denied), so it was not run.`), the audit trail records `tool approval
  refused` and the run as failed, and the task carries on, so the model can explain or choose something else. Where
  the approval is a wrapper around the tool (LangChain, OpenAI Agents, CrewAI, ADK), the wrapper asks nobody and
  refuses outside the mesh task it serves; where it is a hook, middleware or policy (Strands, Agent Framework, Hermes),
  it applies to the runs the adapter follows ([Nested runs](#nested-runs)).
- **CrewAI** runs tools on a worker thread; the approval and ask-user tools wait for the answer from there.
- **Steps are events.** The adapters build each step as the core's step event (`ctx.emit_event`), redacted and capped
  ([behaviour.md](../behaviour.md)), through one shared module (`sam_link_sdk._adapter`). They import only the core, never
  a transport: the agent's transport (the agent link) sends the events on.
