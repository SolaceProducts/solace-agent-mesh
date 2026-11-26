# CrewAI

`sam_link_sdk.crewai` kicks off a crew for a mesh task and reports its steps from CrewAI's event bus. Tested with crewai
1.15.27; a newer version is checked for the hooks the adapter uses when it is imported.

## Install

```bash
pip install "sam-link-sdk[crewai]"
```

CrewAI's `LLM` needs the provider extra for native providers (for Anthropic: `crewai[anthropic]`).

## Minimal agent

This is the whole agent, `examples/minimal_crewai.py`. Set `OPENAI_API_KEY` (and `OPENAI_BASE_URL` for an OpenAI-compatible endpoint) and the SAM variables from the [quick start](../../README.md#quick-start), then run it with `python minimal_crewai.py`:

```python
import asyncio
import logging

from crewai import LLM, Agent, Crew, Task
from crewai.tools import tool

from sam_link_sdk import AgentConfig, MeshAgent, TaskContext
from sam_link_sdk.crewai import run_agent


@tool("lookup_policy")
def lookup_policy(policy_id: str) -> str:
    """Look up an insurance policy by id."""
    return f"{policy_id}: active, covers water damage"


def build_model() -> LLM:
    return LLM(model="openai/gpt-4o-mini")


async def main() -> None:
    logging.basicConfig(level=logging.INFO)
    model = build_model()

    def build(ctx: TaskContext) -> Crew:
        triager = Agent(
            role="Claims triager",
            goal="Answer questions about insurance claims",
            backstory="You triage insurance claims.",
            llm=model,
            tools=[lookup_policy],
        )
        task = Task(
            description="Handle this request: {request}",
            expected_output="One or two sentences.",
            agent=triager,
        )
        return Crew(agents=[triager], tasks=[task])

    async def handle(ctx: TaskContext) -> str:
        return await run_agent(ctx, build)

    config = AgentConfig(name="claims-triage", description="Triages insurance claims")
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
```

CrewAI sends anonymous telemetry unless you set `CREWAI_DISABLE_TELEMETRY=true`, `CREWAI_TRACING_ENABLED=false` and `OTEL_SDK_DISABLED=true`; the claims-triage example sets them.

`run_agent` takes `build(ctx)`, which builds the crew for the task, as Strands and Hermes take theirs, so each task
gets its own: a kickoff fills the crew's task templates in place, so two tasks sharing a crew could each run on the
other's request. A `Crew` object itself is refused with a `TypeError`. Keyword arguments: `input` (default: the task's
text), `memory` (a `ConversationMemory`: a crew keeps no conversation between kickoffs, so the
earlier turns go in the inputs as `history`), `include_request_context` (adds `request_context` to the inputs),
`max_model_calls` (10: set as `max_iter` on each agent of the crew, which CrewAI counts per agent; an agent that reaches
it answers with one more model call and the run goes on), `stream_text` (default True, see Streaming) and
`kickoff_inputs` (the kickoff inputs, with `request` set to the input for task templates that use `{request}`, unless
they set it). The crew's tools belong in `build(ctx)`.

CrewAI fills in only the placeholders the crew's templates name. Put `{history}` or `{request_context}` in a task
description, expected output, role, goal or backstory to choose where they go; when no template names one that has
content, `run_agent` puts it at the head of the crew's first task itself, so `memory=` and `include_request_context=True`
always reach the model.

`examples/claims_triage_crewai.py` is the claims-triage agent. It turns off CrewAI's own telemetry and trace export
(`CREWAI_DISABLE_TELEMETRY`, `CREWAI_TRACING_ENABLED`, `OTEL_SDK_DISABLED`) so task content stays in, and keeps the
conversation with `memory=`.

## Steps

`run_agent` registers a listener once on CrewAI's process-wide event bus, and its tool-call hooks once
(`crewai.hooks`), and routes each event and call to the mesh task that started the kickoff, so concurrent crews stay
separate. There is no `step_reporter` to attach.

- **Ordering:** CrewAI runs event handlers on a thread pool, so events can arrive out of order, however late a
  handler thread is. The listener re-sequences them by CrewAI's `emission_sequence` before reporting, and the crew's
  thread waits (`crewai_event_bus.flush`, at most 5 s) before each model call and tool hook until the handlers of the
  events emitted so far have delivered theirs, so a late event is never reported behind a later one.
- **Call ids:** CrewAI tool events carry no call id (CrewAI 1.15 knows it while running the call but does not put it on
  the event). A tool's result pairs with its start through CrewAI's own `started_event_id`, which follows each parallel
  call on its thread. A start takes the call with the same tool name and arguments from the crew agent's last
  completion (with native tool calling, the provider's ids), else the first remaining call to that tool; identical
  calls pair in start order. A new completion drops the agent's calls CrewAI never ran.
- **Text with tool calls:** CrewAI's completion event carries the model's tool calls in place of its text, so text sent
  with tool calls is not in the step (it can be streamed, see Streaming).
- **Declared failures:** a tool that returns CrewAI's `ToolFailure` shows as failed, with the text the agent got.
- **Guardrails:** a task guardrail that still rejects the answer after its retries fails the task with "Stopped: a
  guardrail of the crew blocked its answer." (not `internal error`); neither the guardrail's words nor the answer are in
  it. Such a task's text is not streamed.
- **Failed model calls** are logged; the exception the kickoff raises is classified by the agent when it is a provider
  client library's error.
- **Nested runs:** a hierarchical crew's manager (built at kickoff from `manager_llm`, or a `manager_agent`) and the
  coworkers it delegates to run in the same kickoff: their steps, tokens, scope checks and stop are the task's. A crew a
  tool kicks off runs on that tool's thread with the context CrewAI copies there, so the process-wide event bus and
  hooks report and check it as the task's too. Approvals are the wrapper `with_approval` returns: they reach a nested
  crew only when it is given those same tools; a crew given the plain tool runs it unasked. Per guarantee:

| Nested run | Scopes | Approvals | Stop | Steps and tokens |
|---|---|---|---|---|
| A hierarchical crew's manager and coworkers; a crew a tool kicks off | refused (tool hook and tool) | asked where the crew has the `with_approval` tools | ends at its next model call | reported |
| A crew on a thread without the context | refused by the tool (`with_scopes`) | the `with_approval` tool refuses | a scoped tool refuses | not reported |

- **Per-tool scopes:** a call to a tool the user lacks the scopes for is refused by a before-tool-call hook, which
  CrewAI runs in every crew agent on both of its tool-calling paths (native and text); the tool does not run, and the
  model gets SAM's refusal (an after-tool-call hook puts it in place of CrewAI's "blocked by hook" text). CrewAI emits
  tool events for a blocked call on one path and none on the other, so the hook reports the refusal with an event of
  its own, the same steps as in every adapter. With native tool calling the model is not offered the tool either:
  each crew agent's LLM call leaves it out of the tools it sends ([security.md](../security.md#required-scopes-rbac)).

## Scopes on the tool

Declare a tool's scopes on the tool, and build the card from the guarded tools:

```python
from sam_link_sdk.crewai import tool_infos, with_scopes

TOOLS = with_scopes([lookup_policy, route_claim], {"route_claim": ["tool:claims:route"]})  # crew tools
config = AgentConfig(name="claims-triage", description="Triages claims", tools=tool_infos(TOOLS))
```

The guarded tool is the framework's own tool type with the check in front of its body. Wherever it is called from, it
finds the mesh task it runs for (`sam_link_sdk.current_task()`: `run_agent` sets it, the mesh agent runs your handler with
it, and the framework carries it into the asyncio tasks and threads it starts with the context) and runs only when that
task's user holds the scopes, the card declares them, and the task has been neither stopped nor answered; outside a mesh
task it refuses. The step reporter still hides the tool and refuses a call to it first, reported and audited once; the
tool's own check is what holds where no hook sees the run ([security.md](../security.md#required-scopes-rbac)).
`tool_infos(tools)` reads the scopes off the guarded tools; a name that is none of the tools is a `ValueError`. A
refusal is the tool's result, `{"error": ...}`, as a refused approval is.

**Caching.** CrewAI reads a crew's tool-result cache (`Crew(cache=True)`, the default) before it calls a tool, so a
cached result would answer a later call without the tool's check. The tools `with_scopes` and `with_approval` return are
never cached (`cache_function` is always false), whatever the tool they wrap allowed: a guarded tool's result, or its
refusal, never answers another call. They carry the tool's `result_as_answer`, `result_schema`, `tool_failure_policy`,
`env_vars` and `args_schema`, but not its usage limit: CrewAI claims a use before it calls a tool, so the wrapped tool
keeps `max_usage_count` and claims it itself, only for a call that runs (a refused call, a denied approval, a stopped or
answered task spend none of it). Do not give the same crew an unguarded tool of the same name: CrewAI's cache is keyed
by name, and that tool's results are cached.

**Async kickoffs.** CrewAI runs a sync tool's async call (`ainvoke`, the ReAct path of an async kickoff) on an
executor thread without the caller's context; the guarded tools run it on one that carries it, so they find their task.
CrewAI's async native tool calling runs a tool on the event loop's own thread, where an approval or a question cannot
wait for the user (the prompt goes to that loop): there `with_approval` refuses at once and `ask_user_tool` answers that
it cannot ask, rather than block the loop.

## Approvals and questions

CrewAI runs tools synchronously on the crew's worker thread, so these tools hand the prompt to the mesh task's event
loop and block that thread until the user answers (the crew is paused meanwhile). Build them in the handler and give
them to the crew built for that task:

```python
from sam_link_sdk.crewai import ask_user_tool, run_agent, with_approval


def build(ctx):
    return build_crew([*with_approval(ctx, my_tools, names=["route_claim"]), ask_user_tool(ctx)])


async def handle(ctx):
    return await run_agent(ctx, build)
```

`with_approval(ctx, tools, *, names=None, message="")`; `names` must be a list of names. A refusal is the tool's
result, `{"error": ...}` as JSON text.

## Peers

`mesh_agent_tools(ctx, agents=None)` in the handler, given to the crew built for that task:
`run_agent(ctx, lambda ctx: build_crew(extra_tools=mesh_agent_tools(ctx)))`. CrewAI does not tell a tool its tool-call id,
so the peer's steps nest under this agent rather than under the specific tool call. CrewAI also turns the tool's name
into a valid identifier, so the model sees the peer's tool with underscores where the agent's name has hyphens
(`peer_claims_desk`, not `peer-claims-desk`).

## Streaming

`run_agent` streams the crew's answer by default (`stream_text=True`): it turns on streaming for the model calls made
for the crew's last task, for that call only (CrewAI's `call_stream_override`, so an LLM shared with other tasks keeps
its own setting), and sends their text through `ctx.emit_text` from CrewAI's `LLMStreamChunkEvent`, which CrewAI hands
to its handlers on the crew's thread as the model writes. Pass `stream_text=False` to send the answer whole when the run
ends; a model you built with `stream=True` then still streams, but its text is not sent.

Only the answer streams:

- **The last task's calls only.** The earlier tasks of a multi-agent crew, and the coworkers a hierarchical crew's
  manager delegates to (CrewAI gives each delegation a task of its own), are not streamed: their text is not the
  answer.
- **Text, not tool calls.** CrewAI does not say while a call streams whether it is the final answer; that is known only
  when the response ends without a tool call. A call offered tools (native tool calling) streams its text as written
  and stops at its first tool-call chunk. A model that writes a sentence before a tool call in the same response (Claude
  often does) has that sentence streamed: nothing tells it from an answer until the response ends, and holding every
  call's text until then would not stream at all.
- **CrewAI's ReAct form.** A call offered no tools (an agent without tools, or text tool calling) is parsed by CrewAI:
  a response that starts `Thought:`, `Action:` or `Final Answer:` is a tool call or a final answer, whose answer is the
  text after `Final Answer:`, stripped; any other response is the answer as written. The text is held while its start
  could still be one of those words, then streams whole, from after `Final Answer:`, or not at all.
- **Answers CrewAI rewrites are sent whole.** A last task with a guardrail or a structured output (`output_pydantic`,
  `output_json`, `response_model`) is not streamed, since CrewAI may retry or change the answer after the model wrote
  it.

A task whose text did not stream (a tool's `result_as_answer`, an answer CrewAI rewrote) gets its answer whole when the
run ends, as with `stream_text=False`. The e2e suite checks that the claims crew's answer arrives as several text
updates that join to the answer, and that a crew of two streams only its second agent's answer, written plainly or in
the ReAct form.

## Stop

A cancelled or stopped mesh task stops its steps at once. CrewAI cannot interrupt a running `kickoff` and swallows
exceptions from step callbacks and tools, so `run_agent` checks the task in each crew agent's LLM `call` and `acall` (a streamed call goes through
`call`): the crew stops at its next model call, after the step in progress finishes. The same check stops a crew whose task hit a limit.
CrewAI's own before-LLM-call hook cannot do this: it ends a kickoff only with CrewAI's `HookAborted`, and swallows any
other exception a hook raises and makes the call anyway, so `AgentStopped` or `TaskHalted` could not come out of it.

For crews run outside the mesh, `stop_guard(agent, crew)` patches the crew (CrewAI has no hook object to attach) so it
raises `AgentStopped` at kickoff and at its next model call while SAM has stopped the agent, and returns it:
`stop_guard(mesh_agent, Crew(...)).kickoff()`.

## Files

No adapter reads `ctx.files` or attaches files for you ([README](README.md#limits-tools-and-files)): read `ctx.files` in the handler and pass what the model should see as `input=`. To return a file, give the model a tool that calls `ctx.attach`, a tool in the crew `build(ctx)` makes:

```python
from crewai.tools import tool


@tool("attach_report")
def attach_report(name: str, text: str) -> str:
    """Attach a text file to the answer."""
    ctx.attach(name, text.encode(), "text/plain")
    return "attached"
```

The file reaches the user with the answer.

## What is not supported

- `step_reporter`: CrewAI's event bus and hooks are process-wide; `run_agent` registers them and routes them to the
  task it runs.
- Hiding a tool the user lacks the scopes for with text (ReAct) tool calling, where the tools are written into the
  prompt: the call is still refused. Build the crew with `ctx.permitted_tools(tools)` to leave it out there too.
- Interrupting a crew mid-step: a running tool or model call finishes (see Stop); at a per-task limit the task is
  answered and the crew stops at its next model call.
- Reasoning streaming: CrewAI's events carry no reasoning.
