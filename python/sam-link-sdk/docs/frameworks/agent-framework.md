# Microsoft Agent Framework

`sam_link_sdk.agent_framework` runs an `agent_framework.Agent` on a mesh task. Tested with agent-framework-core 1.22.0;
a newer version is checked for the hooks the adapter uses when it is imported.

## Install

```bash
pip install "sam-link-sdk[agent-framework]"     # agent-framework-core and agent-framework-openai
```

## Minimal agent

The agent keeps no conversation unless it is given a session, and `run()` takes middleware and tools for that run only,
so one agent serves every task. `run_agent` passes this task's step reporter; you pass the tools and middleware that
depend on the task:

This is the whole agent, `examples/minimal_agent_framework.py`. Set `OPENAI_API_KEY` (and `OPENAI_BASE_URL` for an OpenAI-compatible endpoint) and the SAM variables from the [quick start](../../README.md#quick-start), then run it with `python minimal_agent_framework.py`:

```python
import asyncio
import logging

from agent_framework import Agent, tool
from agent_framework.openai import OpenAIChatCompletionClient

from sam_link_sdk import AgentConfig, MeshAgent, TaskContext
from sam_link_sdk.agent_framework import run_agent


@tool(approval_mode="never_require")
def lookup_policy(policy_id: str) -> str:
    """Look up an insurance policy by id."""
    return f"{policy_id}: active, covers water damage"


def build_model() -> OpenAIChatCompletionClient:
    return OpenAIChatCompletionClient(model="gpt-4o-mini")


async def main() -> None:
    logging.basicConfig(level=logging.INFO)
    triage = Agent(build_model(), "You triage insurance claims.", name="claims-triage", tools=[lookup_policy])

    async def handle(ctx: TaskContext) -> str:
        return await run_agent(ctx, triage)

    config = AgentConfig(name="claims-triage", description="Triages insurance claims")
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
```

Keyword arguments: `input`, `memory` (a `ConversationMemory`), `include_request_context` (sent as a system message),
`stream_text` (default True, see Streaming), and `middleware` and `extra_tools`, added to the agent's own for this run
only. There is no `max_model_calls`: Agent Framework's limit (`function_invocation_configuration["max_iterations"]`)
belongs to the chat client, which every task shares, so set it where you build the client; at the limit the agent
makes one more model call without tools and the task completes with that answer. A tool the user lacks the scopes for is left out of every model request of
the run (a denial is audited once), and a call to it anyway is refused as in every adapter
([security.md](../security.md#required-scopes-rbac)).
`examples/claims_triage_agent_framework.py` is the claims-triage agent; it uses `OpenAIChatCompletionClient` with any
OpenAI-compatible endpoint, so no OpenAI key is needed.

## Steps

`step_reporter(ctx)` returns the middleware that reports one task's steps: chat middleware wraps each model call and
function middleware each tool call. Pass it first in a run's middleware (`run_agent` does).

- **Pairing:** tool steps pair with the model's call by call id (the function context's `metadata["call_id"]`, which
  the framework's own security middleware also reads), including parallel calls.
- **Token usage** from the chat response's `usage_details` (input, output, cache read and reasoning; cache writes only where the client puts them in the usage). The core
  does not say whether a client's input count holds the cache tokens; where the reported total shows it does not,
  they are added ([handlers.md](../handlers.md#steps) has `Usage`'s convention).
- **Order:** SAM's middleware come before yours, so SAM sees a tool call before an approval middleware asks about it.
- **Failures:** a tool that raises is reported as a failed step with the tool's own error. The framework tells the model
  only `Error: Function failed.` unless the framework's function invocation setting `include_detailed_errors` is on, so without it the model
  does not see why the tool failed. A failed model call is recorded with its category for the task's error reply.
- **Reasoning** (with `stream_thinking`): the response's reasoning content. `OpenAIChatCompletionClient`, which the
  example uses, drops the `reasoning_content` an OpenAI-compatible endpoint sends; only reasoning a client puts in the
  response (the Responses client's) is streamed.
- **Nested runs:** an agent used as a tool (`inner.as_tool()`) runs with its own middleware only. The step reporter
  routes every such agent to the task for as long as the run lasts: those among the run's tools when it starts, at any
  depth, and those a model call is given (a context provider may add tools for one run). Its (streamed) model calls
  and tool calls are reported, checked and asked for approval (`run_agent`'s `with_approval` middleware). The router
  does nothing for the run's own top-level agent and is taken off the agent when the last run that needs it ends, so an
  agent used as a tool and later run on its own is reported once. Only a plain `Agent` (or `RawAgent`) is routed: any
  other agent behind a tool (a `WorkflowAgent`, whose agents run inside its workflow, a functional workflow agent, a
  custom `BaseAgent`), or one whose agent cannot be found (including a tool that holds an agent but is not shaped like
  `as_tool()` makes it), refuses the run (`NestedRunRefused`). Per guarantee:

| Nested run | Scopes | Approvals | Stop | Steps and tokens |
|---|---|---|---|---|
| `Agent.as_tool()` agents, at any depth, those a context provider adds included | refused (middleware and tool) | asked | stopped | reported |
| Any other agent behind a tool | refused before the run (or at the model call that is given it) | | | |
| An agent a tool's code runs | refused by the tool (`with_scopes`) | not asked (approval is middleware) | a scoped tool refuses; the task's cancel reaches it when the tool awaits it | not reported |

## Scopes on the tool

Declare a tool's scopes on the tool, and build the card from the guarded tools:

```python
from sam_link_sdk.agent_framework import tool_infos, with_scopes

TOOLS = with_scopes([lookup_policy, route_claim], {"route_claim": ["tool:claims:route"]})  # @tool function tools
config = AgentConfig(name="claims-triage", description="Triages claims", tools=tool_infos(TOOLS))
```

The guarded tool is the framework's own tool type with the check in front of its body. Wherever it is called from, it
finds the mesh task it runs for (`sam_link_sdk.current_task()`: `run_agent` sets it, the mesh agent runs your handler with
it, and the framework carries it into the asyncio tasks and threads it starts with the context) and runs only when that
task's user holds the scopes, the card declares them, and the task has been neither stopped nor answered; outside a mesh
task it refuses. The step reporter still hides the tool and refuses a call to it first, reported and audited once; the
tool's own check is what holds where no hook sees the run ([security.md](../security.md#required-scopes-rbac)).
`tool_infos(tools)` reads the scopes off the guarded tools; a name that is none of the tools is a `ValueError`. A
refusal is the call's result, `{"error": ...}`.

## Approvals and questions

Approval is function middleware over the run's calls; asking is a tool. Give both to the run. `with_approval(ctx, tools,
*, names=None, message="")` covers the given `tools` (those in `names`, each among `tools`, else a ValueError), as the
other adapters wrap them:

```python
from sam_link_sdk.agent_framework import ask_user_tool, run_agent, with_approval


async def handle(ctx):
    return await run_agent(
        ctx,
        agent,
        extra_tools=[ask_user_tool(ctx)],
        middleware=[with_approval(ctx, [lookup_policy, route_claim], names=["route_claim"])],
    )
```

SAM's approval asks the user and waits inside the run. A refused call is reported as a failed step; the model is told
why.

The framework's own flag needs no `with_approval`: `@tool(approval_mode="always_require")` makes the run end with a
`function_approval_request` (`response.user_input_requests`), and `run_agent` asks the user through Agent Mesh for each,
then runs again with the answers (`to_function_approval_response`) until none is left. The framework only honours an
answer to a request it remembers, so `run_agent` gives each run a session of its own (the SDK's `memory` still keeps
the conversation; the session lives for the one run). A refused call (denied, cancelled, timed out, a channel that
cannot show a prompt, a stopped task) does not run and the model is told with the same text as `with_approval`'s; the
framework's own "rejected by user" text is never used, because the answer is always sent as an approval and decided
by the SDK's middleware. An agent run as a tool (`Agent.as_tool()`) is covered too, at any depth: the framework does
not hand its approval requests up (the agent tool fails with "cannot continue because its sub-agent requested
approval"), so the router `run_agent` puts on that agent for the run asks the user through Agent Mesh and runs the agent
again, in its own session, with the answers. A refusal is told to that agent's model in the same words.

## Peers

`mesh_agent_tools(ctx, agents=None)`, given to the run: `run_agent(ctx, agent, extra_tools=mesh_agent_tools(ctx))`. The
peer's steps nest under the model's tool call.

## Streaming

`run_agent(..., stream_text=True)` runs the agent with `stream=True` and sends each update's text to the chat as it
arrives; the run's response is the stream's final response. Pass `stream_text=False` to send the answer whole when the
run ends.

## Stop

Mesh tasks stop on their own: a cancel, a limit or SAM's stop cancels the asyncio task, including an async tool in
flight. For agents run outside the mesh, `stop_guard(agent)` returns middleware that raises `AgentStopped` when an
agent runs and before every model and tool call: `Agent(client, instructions, middleware=stop_guard(mesh_agent))`. In
function middleware the stop is an `AgentStopped` that is also a `MiddlewareFailure`, because
the framework turns any other exception there into a tool error and carries on.

## Files

No adapter reads `ctx.files` or attaches files for you ([README](README.md#limits-tools-and-files)): read `ctx.files` in the handler and pass what the model should see as `input=`. To return a file, give the model a tool that calls `ctx.attach`, `run_agent(ctx, agent, extra_tools=[attach_report])`:

```python
from agent_framework import tool


@tool
def attach_report(name: str, text: str) -> str:
    """Attach a text file to the answer."""
    ctx.attach(name, text.encode(), "text/plain")
    return "attached"
```

The file reaches the user with the answer.

## What is not supported

- Streaming runs of your own: `run_agent` streams the agent itself (see Streaming); it returns the answer, not the stream.
