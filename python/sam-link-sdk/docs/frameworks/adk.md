# Google Agent Development Kit (ADK)

`sam_link_sdk.adk` runs an ADK `Runner`'s agent on a mesh task. Tested with google-adk 2.14.1; a newer version is
checked for the hooks the adapter uses when it is imported.

## Install

```bash
pip install "sam-link-sdk[adk]"     # google-adk and litellm
```

## Minimal agent

ADK builds an agent and its `Runner` once, not per task, and takes plugins per `Runner`. Add the step reporter where
the `Runner` is built, and run each task with `run_agent`:

This is the whole agent, `examples/minimal_adk.py`. Set `OPENAI_API_KEY` (and `OPENAI_BASE_URL` for an OpenAI-compatible endpoint) and the SAM variables from the [quick start](../../README.md#quick-start), then run it with `python minimal_adk.py`:

```python
import asyncio
import logging

from google.adk.agents import LlmAgent
from google.adk.models.lite_llm import LiteLlm
from google.adk.runners import InMemoryRunner

from sam_link_sdk import AgentConfig, MeshAgent, TaskContext
from sam_link_sdk.adk import run_agent, step_reporter


def lookup_policy(policy_id: str) -> dict:
    """Look up an insurance policy by id."""
    return {"policy_id": policy_id, "status": "active", "covers": "water damage"}


def build_model() -> LiteLlm:
    return LiteLlm(model="openai/gpt-4o-mini")


async def main() -> None:
    logging.basicConfig(level=logging.INFO)
    triage = LlmAgent(
        name="claims_triage",
        model=build_model(),
        instruction="You triage insurance claims.",
        tools=[lookup_policy],
    )
    runner = InMemoryRunner(agent=triage, app_name="claims_triage", plugins=[step_reporter()])

    async def handle(ctx: TaskContext) -> str:
        return await run_agent(ctx, runner)

    config = AgentConfig(name="claims-triage", description="Triages insurance claims")
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
```

`run_agent` raises `ValueError` for a Runner without the step reporter: without it the mesh sees no steps and nothing
refuses a tool the user lacks the scopes for. Keyword arguments: `input`, `include_request_context` (added to each model
request's instructions), `max_model_calls` (10: set as the `run_config`'s `max_llm_calls`, so a run that reaches it fails
the task saying so), `stream_text` (default True, see Streaming) and `run_config` (passed to `run_async`; its
`max_llm_calls` is replaced by `max_model_calls`). ADK keeps the conversation in its session service, so there is no
`memory`. The SAM session is the ADK session id, so follow-up turns in one chat keep the conversation in ADK's
session service; a run-based task (a peer call or a workflow node) gets its own session, deleted when it finishes. The
SAM user is the ADK user (`ctx.user_id`), so what ADK keeps per user, `user:` state and its memory service, is each
user's own and never reaches another user's chats (the example keeps the policy a user last looked up in
`user:last_policy`). `examples/claims_triage_adk.py` is the claims-triage agent; it uses ADK's `LiteLlm`,
so the SAM `.env` model string works as it is.

## Steps

`step_reporter()` returns a `SamLinkPlugin`. The step events map directly from what ADK hands over (`role: "model"`,
`function_call`, `usage_metadata`).

- **Routing:** the plugin belongs to the Runner, not to one run. `run_agent` binds the mesh task, so one Runner
  serves concurrent tasks.
- **Call ids:** ADK assigns `adk-...` function-call ids after the model callback. The model's response is reported from
  the model event, so its ids match the tool callbacks whether or not the model supplies ids, for parallel calls
  too.
- **Token usage** from `usage_metadata`: prompt (which holds the cached content) and tool-use prompt tokens as input,
  candidates and, from Gemini itself, thoughts tokens as output, and through LiteLLM the cache writes ADK puts on the
  response. LiteLLM reads a provider's `prompt_tokens` and `completion_tokens` with a default of 0 and recomputes the
  total, so a provider that renamed them reaches the adapter as a usage of zeros: SAM records no usage for the call,
  and the adapter cannot tell it from a provider that reports none, so it logs no `token usage unread` warning
  ([behaviour.md](../behaviour.md)).
- **Errors:** an exception inside an ADK plugin aborts the run, so the plugin swallows its own failures. A tool
  exception is reported as a failed step, then ADK raises it out of the run (no second model call); the agent answers
  the task with an error.
- **Failed model calls:** the exception the run raises is classified by the agent when it is a provider client
  library's error.
- **Reasoning** (with `stream_thinking`): the model's thought parts.
- **Per-tool scopes:** a tool the user lacks the scopes for is taken out of each model request's function
  declarations, so the model is not offered it; ADK still knows the tool, so a call to it anyway reaches the plugin,
  which refuses it as every adapter does (the tool does not run; [security.md](../security.md#required-scopes-rbac)).
- **Nested runs:** every agent the Runner runs is covered (steps, tokens, scopes, approvals, the stop), sub-agents
  included, and agents used as tools (`AgentTool`), whose nested run inherits the Runner's plugins by default. An
  `AgentTool(include_plugins=False)` runs its agent with none of them, whatever that agent's tools are (an MCP toolset
  included), so `run_agent` refuses to run an agent that can reach one (`NestedRunRefused`), among its tools or inside
  `with_approval`, and one a toolset hands over at run time ends the run at the call (the plugin sees through the
  adapter's own wrappers). Per guarantee:

| Nested run | Scopes | Approvals | Stop | Steps and tokens |
|---|---|---|---|---|
| Sub-agents, `AgentTool`s with the Runner's plugins | refused (plugin and tool) | asked | stopped | reported |
| `AgentTool(include_plugins=False)` | refused before the run (or at the call, from a toolset) | | | |
| A Runner a tool's code starts without the plugin | refused by the tool (`with_scopes`); an MCP toolset's tools are not | the `with_approval` tool asks the current task's user, and refuses outside a task | a scoped tool refuses | not reported |

## Scopes on the tool

Declare a tool's scopes on the tool, and build the card from the guarded tools:

```python
from sam_link_sdk.adk import tool_infos, with_scopes

TOOLS = with_scopes([lookup_policy, route_claim], {"route_claim": ["tool:claims:route"]})  # functions or ADK tools
agent = LlmAgent(
    name="claims_triage", model=model, instruction="Triage claims.", tools=with_approval(TOOLS, names=["route_claim"])
)
config = AgentConfig(name="claims-triage", description="Triages claims", tools=tool_infos(TOOLS))
```

The guarded tool is the framework's own tool type with the check in front of its body. Wherever it is called from, it
finds the mesh task it runs for (`sam_link_sdk.current_task()`: `run_agent` sets it, the mesh agent runs your handler with
it, and the framework carries it into the asyncio tasks and threads it starts with the context) and runs only when that
task's user holds the scopes, the card declares them, and the task has been neither stopped nor answered; outside a mesh
task it refuses. The step reporter still hides the tool and refuses a call to it first, reported and audited once; the
tool's own check is what holds where no hook sees the run ([security.md](../security.md#required-scopes-rbac)).
`tool_infos(tools)` reads the scopes off the guarded tools; a name that is none of the tools is a `ValueError`. A
refusal is the call's error result. A toolset (an MCP toolset) among the tools is passed on unchanged: its tools resolve
at run time and cannot be given scopes here, so declare their scopes in the card (`ToolInfo`) and the plugin enforces
them, by the hooks only; naming one of them in `with_scopes` is a `ValueError` that says so. `with_approval` likewise
leaves a toolset outside its approval: name the tools to approve (with no names and a toolset it is a `ValueError`, so
the toolset's tools are never silently left unapproved).

## Approvals and questions

The tools that act for a task are toolsets ADK asks for tools on every run; they act for the task `run_agent` is
serving, so they take no task context:

```python
from google.adk.agents import LlmAgent
from sam_link_sdk.adk import ask_user_tool, with_approval

agent = LlmAgent(
    name="claims_triage",
    model=model,
    instruction=INSTRUCTIONS,
    tools=[*with_approval([lookup_policy, route_claim], names=["route_claim"]), ask_user_tool()],
)
```

`with_approval(tools, *, names=None, message="")` returns a one-toolset list and `ask_user_tool()` a toolset. A
refused call, and asking outside a mesh task, give the model an `{"error": ...}` result. An unanswered question is the
text `The user did not answer (...)`, as in the other adapters.

ADK's own flag needs no `with_approval`: `FunctionTool(route_claim, require_confirmation=True)` makes the run end with
an `adk_request_confirmation` call, and `run_agent` asks the user through Agent Mesh for each, then runs again with a
`FunctionResponse` carrying the answer, until none is left. The step reporter reports the call once, when it runs or is
refused, not when ADK holds it. A refused call (denied, cancelled, timed out, a channel that cannot show a prompt, a
stopped task) does not run and the model is told with the same text as `with_approval`'s: the answer is always sent
to ADK as a confirmation and decided by the step reporter, because ADK words its own rejection ("This tool call is
rejected.") in fixed text. A tool that asks for confirmation from inside its body (`tool_context.request_confirmation`)
is asked the same way, but its held call shows as a failed step first.

## Peers

`mesh_agent_tools(agents=None)` returns a one-toolset list, added once where the agent is built:
`LlmAgent(..., tools=[lookup_policy, *mesh_agent_tools()])`. On each run it offers the peers the current task's user may
invoke. The peer's steps nest under the model's tool call. ADK hands the model a tool's return wrapped, so the peer's answer
reaches it as `{"result": ...}`.

## Streaming

`run_agent(..., stream_text=True)` runs with `RunConfig(streaming_mode=StreamingMode.SSE)`, merged into the `run_config`
you pass (one that already sets a streaming mode keeps it), and sends the text of each partial event to the chat. The
steps ignore partial events and report each model response once, from its final event. Pass `stream_text=False` to
send the answer whole when the run ends.

## Stop

Mesh tasks stop on their own. For runs outside the mesh, `stop_guard(agent)` returns a plugin (`StopPlugin`) that
raises `AgentStopped` when a run starts and before every model and tool call. Add it to every Runner that runs the
agent: `Runner(agent=..., plugins=[step_reporter(), stop_guard(mesh_agent)])`. ADK wraps a plugin's exception in
`RuntimeError("Error in plugin ...")`; the `AgentStopped` is its `__cause__`.

## Files

No adapter reads `ctx.files` or attaches files for you ([README](README.md#limits-tools-and-files)): read `ctx.files` in the handler and pass what the model should see as `input=`. To return a file, give the model a tool that calls `ctx.attach`, a plain function in the agent's `tools` (ADK builds the agent once, so it finds the task with `sam_link_sdk.current_task()`):

```python
def attach_report(name: str, text: str) -> str:
    """Attach a text file to the answer."""
    current_task().attach(name, text.encode(), "text/plain")
    return "attached"
```

The file reaches the user with the answer.

## What is not supported

- `memory=`: ADK's session service keeps the conversation.
