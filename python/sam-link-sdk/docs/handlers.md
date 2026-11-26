# Writing a handler

A handler is `async def handle(ctx: TaskContext) -> str | TaskResult`. SAM calls it once per task. The framework
adapters build on `TaskContext`; a handler without a framework uses it directly. This page is what `ctx` offers.

## Input and answer

Three ids name a task, and only one is safe to key your own storage on:

| Id | Means | Use it for |
|---|---|---|
| `ctx.task_id` | One request. | Logging. |
| `ctx.context_id` | SAM's chat id. | Not for storage: anyone who knows it could reach another user's history. |
| `ctx.session_id` | The user plus the chat. Empty for a peer call or a workflow node. | Per-conversation state. |

- `ctx.text` is the user's message and `ctx.user_id` the user SAM verified.
- `ctx.permitted_tools(tools)` keeps the tools SAM granted this user, and `ctx.check_tool(name)` answers for one tool and
  audits a denial ([security.md](security.md#required-scopes-rbac)).
- Return a string, or `TaskResult(text, state="failed")` to report a logical failure (`state` is `"completed"` or
  `"failed"`). Any other return value, `None` from a forgotten `return` included, fails the task and the agent logs a
  `TypeError`. A handler that raises ends the task with `internal error`; `data.token_usage` is attached when tokens
  were used.
- `await ctx.progress("Triaging claim")` reports progress; `await ctx.emit_text(...)` streams text to the chat. If the
  handler streamed no text, the answer is sent once as a status update and again in the final task. SAM's web UI drops
  the final text when text was streamed, so the user does not see the answer twice. `sam task send` prints only
  streamed text.
- Every adapter's `run_agent` streams the model's text through `emit_text` as it is written. Pass `stream_text=False` to
  send the answer whole ([frameworks/README.md](frameworks/README.md#streaming-the-answer)).

## Files

- **In:** not usable yet. `ctx.files` is a list of `InputFile(name, content, *, mime_type, uri)`, but in the current SAM
  an attachment arrives with `content` empty and `uri` set to an `artifact://` URI the agent cannot fetch.
- **Out:** return `TaskResult(text, files=[File(name, content, mime_type=...)])`, or call `ctx.attach(name, content,
  mime_type)` from anywhere in the run. Each file reaches the user with the answer. It is not stored in the user's
  session, so it is not in the Files panel. An empty file is left out, with a note in the answer, since SAM refuses one.
- **Data SAM refuses:** `TaskResult.data` holding an object whose `type` is one SAM acts on (a signal such as a
  redirect), or a `metadata` object naming SAM's own task keys (`trace_id`, `node_id`, `workflow_name` and the like),
  is left out with a WARNING, so the answer still reaches SAM.
- **Size:** each direction carries at most about 7 MB of base64 file data (`max_inline_file_bytes`). Files over the
  budget are left out, and a note says so.
- `examples/claims_intake.py`, a deterministic agent with no model, reports the files it receives and returns a report
  file.

## Steps

The adapters report model and tool steps for you. Without one, report them with `await ctx.emit_event(...)`, which takes
the step events of `sam_link_sdk.events` (`ModelStarted`, `ModelFinished`, `ModelFailed`, `ToolStarted`, `ToolFinished`,
`Progress`; a `ModelFinished` lists the model's `ToolCall`s). Redact what you put in them
(`sam_link_sdk.redact.redact_value`); SAM redacts and caps them again.

Give a model call's tokens with its step, `ModelFinished(model, text, usage=Usage(...))`. `ctx.record_usage(model,
Usage(...))` records tokens no step carries. `Usage`'s convention: `input_tokens` is every input token, the cache reads
(`cached_input_tokens`) and writes (`cache_write_tokens`) included, those two disjoint; `output_tokens` includes the
reasoning tokens. Anthropic and Bedrock count their cache tokens outside the input, so add them.

## Asking the user

A task can pause for the user.

- `await ctx.request_approval("send_email", {"to": ...}, message="...")` asks the user to approve a tool call and returns
  an `ApprovalResult` (`approved`, and `outcome`: approved, denied, cancelled, timed_out or unreadable; anything but an
  explicit approval is not approved). The arguments shown are redacted, and every decision is audited.
- `await ctx.ask_user("Which queue?")`, or `ctx.ask_user([Question("Which queue?", (Option("fast-track"),
  Option("standard")))])`, asks questions and returns a `UserAnswer`. Two or more questions are shown in tabs.
  `Question(..., multi_select=True)` lets the user pick several options.
- **Timeout:** `timeout_seconds` of 0, a negative number or NaN means SAM's default (45 minutes); a larger value,
  `math.inf` included, waits at most a year, and SAM's task TTL still applies.
- **Limits:** at most 4 questions per prompt, 4 options per question and 64 approval arguments. Past these, `ask_user`
  and `request_approval` raise `PromptTooLarge` (a `ValueError`) and nothing is shown, because leaving something out
  would change what the user answers. Long texts are cut instead.

The web UI, Slack and `sam task send --hil-approve` / `--hil-respond` show and answer the prompt, and the wait defaults
to 45 minutes. It also works when the agent runs as a workflow node or is called by another agent: the prompt reaches the
user's own chat. The adapters' `with_approval` and `ask_user_tool` build on these, and so does a framework's own approval
flag ([frameworks/README.md](frameworks/README.md)). `sam task send --hil-deny` answers "cancelled".

## Conversation memory

An agent answers each message fresh unless it keeps history. Pass `memory=ConversationMemory()` to an adapter's
`run_agent` and the chat's earlier turns, keyed by `ctx.session_id`, are replayed before each new message. A handler
without an adapter does the same with `memory.history(ctx.session_id)` and `memory.remember(ctx.session_id, ...)`.
LangGraph's `run_graph` uses the session id as the checkpointer's thread id, and ADK uses it as its session id.

A peer call or a workflow node has an empty `ctx.session_id`: it starts without the chat's history and adds none to it.
Memory is bounded (`max_turns` turns per chat, chats, idle time), lives in this process, and is lost on restart. With
several replicas a follow-up may reach a replica that has not seen the chat, so use a shared checkpointer or store if
that matters. A turn that fails is not remembered. History holds the user's words and is never logged.

## Request context

`ctx.request_context` carries more than the user's words: system purpose, desired response format, user profile,
timezone, project id, and the per-turn conversation context such as a Slack thread. `ctx.metadata` is as SAM sent it.
`ctx.request_context.context_prompt()` renders them as prompt sections, and every adapter's `run_agent(...,
include_request_context=True)` adds them to the prompt. It is opt-in so existing prompts do not change. Conversation
context arrives fenced as untrusted text, so keep it fenced and out of conversation history.

## Per-task limits

A task that starts more than `max_llm_calls` model calls (default 20) or runs longer than `task_timeout_seconds` (default
600) is stopped and answered with a failed task that says which limit it hit; an audit record `task limit reached` is
written. Time spent waiting for a person does not count; time spent waiting for a `max_concurrent_tasks` slot does,
so a task queued behind one that waits for a person fails at its limit rather than waiting as long. `0` turns a limit off. A handler still running 5 s after the
cancel is left behind: the task is answered and its `max_concurrent_tasks` slot is freed.

A framework run that stops on purpose fails the same way, with a message naming what stopped it: `run_agent`'s
`max_model_calls` (default 10), LangGraph's `recursion_limit`, an OpenAI Agents guardrail, a CrewAI guardrail that
still rejects the answer, Strands' `max_tokens`. CrewAI and Agent Framework make one more model call for a final answer at
their limit instead, so their task completes. `max_model_calls` is the framework run's limit and `max_llm_calls` the
task's; the SDK enforces the second whatever the framework.

## Model failures

When a task fails because a model call failed, the error reply carries `llm_error_category` (`auth_failure`,
`rate_limit`, `timeout`, `context_overflow`, `content_filtered`, `unknown`) and `llm_provider`, never the provider's own
message, which can contain prompt text. Errors from the OpenAI, Anthropic, LiteLLM, Google, Bedrock, Cohere, Mistral, Groq
and Ollama client libraries are recognised, also when wrapped. A handler that sees a failed call can record it with
`ctx.record_llm_error(exc)`, or `ctx.record_llm_error("openai.RateLimitError")`. A bug in your own tool is not reported
as a model failure.

## Reasoning stream

`AgentConfig(stream_thinking=True)` sends the model's reasoning to the chat as it is written; the adapters take it from
the framework, and a handler can call `ctx.emit_thinking(text)` and `ctx.end_thinking()`. It is off by default, because
reasoning is model-generated content that is shown in the UI and kept in the task's event log. It is not redacted.
