# The agent link protocol

This is the wire contract of the link, for the curious and for anyone writing a client. It is implemented by SAM's `agent_link` app (its "entrypoint" below) and by
`sam_link_sdk.link.LinkTransport` (the frames are built and read in `sam_link_sdk/link/v2.py`). This document is the client's
contract, checked against the entrypoint's code: where they disagree, the entrypoint wins and this document is the
bug. [How the SDK speaks the protocol](#how-the-sdk-speaks-the-protocol) and the [known gaps](#known-gaps) close it.

## Why

The agent says **what happened** ("a model call finished with these tokens"), and the entrypoint builds every SAM
message from it with SAM-Go's own code: status events, the final answer, approval and question screens, peer requests,
the agent card, spend records. So:

- The entrypoint redacts credentials, caps sizes, attributes spend and totals token usage. None of these depend on
  the agent's code.
- An SDK in any language needs a WebSocket client and this document, and nothing from SAM-Go's source.

## Connecting

`GET /gw/{gateway_id}/v1/connect`, upgraded to a WebSocket, with `Authorization: Bearer <link token>`,
`Sam-Agent: <agent name>` and `Sec-WebSocket-Protocol: sam-agent-link.v2`. A wrong name or token is refused before the
upgrade with HTTP 401. The entrypoint selects `sam-agent-link.v2` when it is offered and echoes it; a connection that
does not offer it is upgraded without a subprotocol and closed at once with 4400 and the reason
`subprotocol sam-agent-link.v2 required`. `welcome.protocol` is `2`. The subprotocol's name and the number are wire
facts, kept from the protocol's history.

Every message is a JSON object in a text frame with a `type`; all field names are camelCase. Unknown fields are
ignored both ways; an unknown frame `type` or event `kind` is answered with `error` code `unknown_type` and otherwise
ignored. The entrypoint pings every 20 s and drops a connection that sends nothing for 60 s. Close codes: 1009 message
too large, 1012 reconnect (the replica no longer serves agent links and another does, or the agent was deployed again
with new settings), 1013 too slow to keep up, 4400 protocol error, 4401 the token the connection was accepted with no
longer works (the agent was removed or its token was replaced), 4408 idle, 4409 too many connections for the agent.

A SAM with several gateway replicas serves agent links on one of them, its leader. Every other replica answers the
upgrade with HTTP 503 and `Retry-After: 1` before it looks at the agent's name or token, and so does every replica for
the few seconds after a start or a change of leader. A replica that stops leading closes its agent connections with
1012.

## Frames from the entrypoint

The wire spells `canceled` with one l in every field, as A2A's task states do: a task's or peer call's `state` and a
prompt answer's `status` alike. (The SDK's own outcomes keep `cancelled`.)

| type | fields | meaning |
| --- | --- | --- |
| `welcome` | `protocol` (2), `agent`, `connection`, `maxInflight`, `limits` (below), `enabled` | first, once. All fields are always present. `enabled` is false when SAM has stopped the agent, and a `control` with `enabled: false` follows. Answers held for the agent (below) follow right after it (and the `control`) |
| `task` | `delivery`, `taskId`, `redelivered`, `task` (below) | a new task for the agent. Acknowledge with `ack` once it is answered (or `nack` to give it back). `redelivered` is present only when true. A task the connection did not acknowledge before it closed goes back to the queue and is delivered again, possibly to another connection of the agent; a task already answered comes again only when its result did not land (it reached a replica that no longer held the task, or was lost with the connection), so the agent sends the result again, then the `ack`. A task whose frame would be larger than `maxFrameBytes` never reaches the agent: its caller gets a JSON-RPC error (`Request too large: ...`) and it is acknowledged, not delivered again |
| `cancel` | `taskId` | the user or caller cancelled the task. The agent stops it and sends `result` with `state: "canceled"`. It has no `delivery` and is not acknowledged. The task's open prompts and peer calls are cancelled at once (after this frame). A cancel that finds no connection (a disconnect, or a stop) is held like an answer (below), so it may arrive on the next connection, for a task the agent is still running or has already answered |
| `prompt_answer` | `taskId`, `requestId`, `status` (`answered`, `canceled`, `timed_out`), `approved` (approvals only), `answers` (questions only, when answered), `truncated` (optional) | the answer to the agent's `prompt`. Sent to the connection that asked; when it has gone, to the one holding the task, else any connection of the agent; with none at all it is held (below). `approved` is true only for an answered approval the user approved. An answer is taken only from the task's own user. An "Other" choice arrives as what the user typed, a multi-select joined with `, `, any other choice as its `label` (as shown: cut and redacted, below). `truncated: true` says the answers were cut with SAM's marker `…[truncated N bytes]` to fit `maxFrameBytes`. A prompt is answered `cancelled` when its task ends (result, expiry) or is cancelled |
| `peer_result` | `subtaskId`, `state` (`completed`, `failed`, `canceled`, `rejected`), `text`, `data` (optional), `error` `{code, message}` (optional; the peer's JSON-RPC code), `truncated` (optional) | the outcome of the agent's `peer_call`. `data` is the first data part of the peer's reply that is not a SAM notice (a signal, an artifact save). `text`, `error.message` and every string in `data` are redacted with SAM's redactor and cut at `maxStringBytes` with SAM's marker. `truncated: true` says something was cut: a string at `maxStringBytes` (`data` kept), or a frame over `maxFrameBytes` (`data` dropped, `text` or `error.message` cut further). A peer's JSON-RPC error is `state: failed` with `error`. A call the agent cancelled, or whose task ended, expired or was cancelled, ends `canceled`; a peer silent for `task_ttl_seconds` ends `failed`. Routed and held as `prompt_answer` is |
| `peers` | `peers`: `[{name, displayName, description, skills: [{id, name, description}], onDemand}]` | the agent's configured peers that are on the mesh now. Sent after `welcome`, and again when the list changes. Always the full list, never sent twice unchanged |
| `control` | `enabled`, `reason` (`stopped_by_sam`, `released_by_sam`) | SAM stopped or released the agent. While stopped the entrypoint refuses the agent's tasks and peer calls itself and keeps its card down |
| `error` | `code`, `message`, `ref`, `requestId` | a frame from the agent was refused; the connection stays open (below) |

`welcome.limits`:

| field | value | bounds |
| --- | --- | --- |
| `maxFrameBytes` | `max_frame_bytes` (default 8388608) | any message, either way. The entrypoint never sends a larger one: a `peer_result` or `prompt_answer` is cut to fit (`truncated`), and a larger `task` is refused to its caller |
| `maxFrameValues` | 16384 | JSON values in one frame: every object, array, string (object keys included), number, `true`, `false` and `null` (`limit`, "a message holds at most 16384 JSON values") |
| `maxStringBytes` | 16384 | every string of tool arguments and results, after redaction (longer ones are cut, not refused) |
| `maxIdBytes` | 128 | `requestId`, `subtaskId`, an event's `callId`, a tool call's `id`, a `peer_call`'s `callId` |
| `maxNameBytes` | 256 | an event's `model`, `pricingKey`, `provider` and tool `name`, a tool call's `name`, an approval's `tool` |
| `maxToolCallsPerModelCall` | 128 | `toolCalls` of one `model_finished` |
| `maxModelsPerTask` | 32 | distinct `model` values one task's events name |
| `maxOpenPeerCalls` | 1024 | open peer calls per agent, across its connections, held results included |
| `maxOpenPrompts` | 1024 | open prompts per agent, across its connections, held answers included |
| `maxToolScopes` | 64 | distinct `requiredScopes` the agent's tools have named, over every `describe` since the entrypoint started, one refused for another reason included |
| `maxFailureMessageBytes` | 2048 | a failed `result`'s `error.message` (longer ones are cut, not refused) |
| `maxPromptQuestions` | 4 | `questions` of one `prompt` (`limit`) |
| `maxPromptOptions` | 4 | `options` of one question (`limit`) |
| `maxPromptTextBytes` | 2048 | a question's `question`, an option's `label` and `description` (`bad_message`) |
| `maxQuestionHeaderChars` | 12 | a question's `header`, in characters (`bad_message`) |
| `maxApprovalArgs` | 64 | members of an approval's `args` object (`limit`) |
| `maxApprovalArgsBytes` | 65536 | an approval's `args` as sent (`limit`) |
| `maxDescriptionBytes` | 4096 | a `describe`'s `description` (`limit`). The SDK refuses a longer one at start-up (`DescriptionTooLong`) |
| `maxSkills` | 32 | a `describe`'s `skills` (`limit`, as above) |
| `maxSkillDescriptionBytes` | 1024 | a skill's `description`; its `name` is held to `maxNameBytes` (`limit`, as above) |
| `taskTtlSeconds` | `task_ttl_seconds` (default 7200) | how long a task stays open without an answer (below) |
| `maxCallDepth` | 10 | peer calls nested under one user request; past it a `peer_call` is refused `forbidden` ("call depth limit reached") |
| `maxConnectionsPerAgent` | `max_connections_per_agent` (default 8) | open connections of one agent; one more is closed at once with 4409 |
| `maxToolPayloadBytes` | 262144 | one event's `args`, `result` and `toolCalls[].args` together, as sent (`limit`, "an event's tool arguments and results are at most 262144 bytes", `ref` the task id) |

`error.code` is one of `unknown_task` (not an open task of this agent, or one bound to another connection of the
agent that is still open, which the message says), `bad_message`, `forbidden`, `unknown_type`,
`unknown_delivery`, `limit`, `publish_failed` (SAM could not deliver the message; the agent may send it again),
`peer_unavailable` (the `peer_call` names an agent that is not on the mesh; the call fails at once).
`error.ref` is the `taskId` of a refused `event`, `prompt` or `result`; the `subtaskId` of a refused `peer_call` or
`peer_cancel` (empty when the `subtaskId` itself is unusable); the delivery number of a refused `ack` or `nack`; and
empty for `describe`, `consume` and an unknown type; a `ref` longer than 128 bytes is not echoed. A refused
`describe` or `consume` also says which it was in `frame` (`"describe"` or `"consume"`). A refused `prompt`
carries the `taskId` as `ref` and echoes its `requestId` when that is valid; its checks run in this order: the
`requestId` and the prompt's shape (`bad_message` or `limit`, among them "an approval's message goes in
approval.message" for a top-level `message` beside an approval), then an unknown task, `forbidden`, a duplicate
`requestId`, and room under `maxOpenPrompts`. A prompt whose screen cannot be built is refused with "the prompt could
not be shown".

### The `task` object

```json
{
  "contextId": "chat or session id; empty for none",
  "text": "the user's words (text parts joined with \n)",
  "files": [{"name": "a.pdf", "mimeType": "application/pdf", "bytes": "<base64>", "uri": "artifact://..."}],
  "input": {"any": "JSON"},
  "workflow": {"name": "claims-intake", "nodeId": "coverage"},
  "sessionBehavior": "session",
  "user": {"id": "alice@example.com"},
  "callDepth": 0,
  "trigger": "interactive",
  "traceId": "0192...",
  "calledBy": "orchestrator-agent-name or empty",
  "canPromptUser": true,
  "grantedToolScopes": ["tool:claims:pay"],
  "requestContext": {
    "systemPurpose": "", "responseFormat": "", "projectId": "", "conversationContext": "",
    "userProfile": "", "userTimezone": ""
  },
  "metadata": {}
}
```

- `files` is always present (possibly empty); a file has `bytes`, `uri` or both. A file a user uploads through the
  web gateway arrives by `uri`.
- `input` and `workflow` are present only when a SAM workflow runs the agent as a node. `text` is then the node's
  instruction, or empty. `input` is set only when the workflow sent the input inline as JSON.
- `sessionBehavior` is `"run"` for a peer call or a workflow node (no chat history), else `"session"`.
- `user.id` is the request's user id. The entrypoint has already verified the user's token and the agent's
  `requiredScopes` before delivering the task.
- `canPromptUser` says whether the task's channel can show a prompt and return its answer: false for a task without a
  status stream and for one from an entrypoint that cannot (MCP, webhook, event mesh). A prompt on such a task is
  answered `canceled` at once.
- `grantedToolScopes` (always present) are the `requiredScopes` of the agent's own described tools that the task's
  user holds, each checked as the agent's own required scopes are. The agent learns only yes or no for scopes it
  named itself.
- `requestContext.userProfile` is empty unless the administrator set `share_user_profile: true` for the agent.
- `metadata` is the request message's metadata as sent; prefer the named fields.
- The agent never receives the user's token, scopes, the raw user configuration or any broker address.

## Frames from the agent

| type | fields | meaning |
| --- | --- | --- |
| `describe` | `agent` (below) | the agent's card. The entrypoint builds and publishes it, then republishes it every 60 s while the agent has a connection to that replica that has not retired. Send again to change it: a description that changes nothing the mesh sees publishes nothing, and a changed one sent within 10 s of the last publish is published when the 10 s are up (only a publish made at once can answer `publish_failed`). While SAM has the agent stopped the description is checked and kept, and published on release. An `agent` object over 256 KiB is refused |
| `consume` | | start delivering tasks to this connection. Refused with `bad_message` ("send describe before consume: ...") until the entrypoint holds a description of the agent; one an earlier connection gave this replica counts |
| `retire` | | this connection is leaving for good (see [Card presence](#card-presence)) |
| `ack` / `nack` | `delivery` | settle a delivered task: `ack` once it is answered, `nack` to give it back to the queue (the SDK nacks a task whose run was cancelled from outside it, or that its core failed to take, once per task per process, by a memory of its last 256 give-backs kept an hour each; the second time it answers the task failed) |
| `event` | `taskId`, `event` (below) | something happened in a delivered, open task. Still taken after a `cancel` |
| `prompt` | `taskId`, `requestId`, `timeoutSeconds`, and exactly one of `approval` `{tool, args, message}` or `questions` `[{question, header, options: [{label, description}], multiSelect}]` with an optional top-level `message` shown above them | ask the task's user; answered with one `prompt_answer`. `requestId` is unique among the agent's open prompts, has no `/`, `*`, `>` or whitespace. `timeoutSeconds` absent or not positive means 45 minutes, capped at `task_ttl_seconds`. `approval.args` is an object or null. `approval.message` is shown as written (never a template), redacted and cut at 1024 bytes; a `message` with `approval` is `bad_message`. An option answers with its `label`; a `multiSelect` question lets the user pick several, answered as their labels joined with `, `. Refused with `forbidden` on a cancelled task |
| `result` | `taskId`, `state` (`completed`, `failed`, `canceled`), `text`, `files` `[{name, mimeType, bytes}]`, `data`, `error` | the final answer; closes the task. `error` `{category, provider}` is a failed model call: the JSON-RPC error -32603 with SAM's category and provider. `error` `{message}` (no `category`) is any other failure: the JSON-RPC error -32603 SAM's own agents send when their handler fails, with the message redacted and cut at `maxFailureMessageBytes` (`internal error` when empty). `failed` without `error` is a failed Task carrying `text`. On `publish_failed` the task stays open and the agent may send it again |
| `peer_call` | `parentTaskId`, `subtaskId`, `peer`, `text` (not empty), `callId` (optional: the model's tool-call id) | ask a configured peer on behalf of the open task `parentTaskId`. `subtaskId` is chosen as `requestId` is. Refused with `forbidden` on a cancelled task |
| `peer_cancel` | `subtaskId` | stop an open `peer_call` |

### The `agent` object of `describe`

```json
{
  "displayName": "Claims Assistant", "description": "Handles insurance claims", "version": "1.0.0",
  "skills": [{"id": "claims", "name": "Claims", "description": "..."}],
  "tools": [{"name": "pay_claim", "description": "...", "requiredScopes": ["tool:claims:pay"]}],
  "requiredScopes": [],
  "inputSchema": null, "outputSchema": null,
  "welcomeMessage": "", "suggestions": [{"title": "...", "prompt": "...", "autoSend": false}],
  "peers": ["policy-desk"],
  "streaming": true,
  "declared": {
    "framework": "Strands Agents", "owner": "", "team": "",
    "sdk": {"name": "sam-link-sdk", "version": "0.1.0"},
    "dataAccess": [{"system": "Claims DB", "classification": "confidential", "access": "read-write"}],
    "mcpServers": [{"name": "claims-db", "url": "https://..."}]
  },
  "health": [{"id": "db", "severity": "degraded", "detail": "..."}]
}
```

The agent's name is the authenticated one, never a field. `declared` is the agent's own statement (SAM's
`sam/provenance`, whose `origin` is `sdk.name`, else `agent-link`); the administrator's registered owner and team are
added by the entrypoint (`sam/registration`), and the card is marked on-demand (`sam/on-demand`) exactly when the
entrypoint wakes the agent. `requiredScopes` is enforced by the entrypoint before a task is delivered, from the
agent's first `describe` on, and outlives `retire` and a stop. A tool's `requiredScopes` is shown on the card and
checked for each task's user into `grantedToolScopes`; enforcing it is the agent's, since the entrypoint does not see a
tool call before it runs. The distinct tool scopes an agent names are limited to `maxToolScopes` over every `describe`
since the entrypoint started (over that, `describe` is refused with `limit`), so describing again with other scopes
cannot probe which of many scopes a user holds. `autoSend` sends a suggestion at once instead of only filling the chat
input.

### Events (`event.kind`)

| kind | fields | notes |
| --- | --- | --- |
| `model_started` | `model` | a model call began |
| `model_finished` | `model`; optional `text`, `toolCalls` `[{id, name, args}]`, `usage` `{input, output, cachedInput, cacheWrite, reasoning}`, `pricingKey` | a model call returned. One spend record per `model_finished` with `usage`; two or more `toolCalls` share a parallel group |
| `model_failed` | `model`, `category` (`auth_failure`, `rate_limit`, `timeout`, `context_overflow`, `content_filtered`, `unknown`), `provider` | a model call failed. Publishes nothing of its own; only `result.error` ends the task as a model error |
| `tool_started` | `callId`, `name`, `args` | `args` is a JSON object or null |
| `tool_finished` | `callId`, `name`, `result` (any JSON), `failed` (bool) | a failed call is published in the shape SAM gives a failed tool result |
| `progress` | `text` | a short status line ("Looking up the policy") |
| `text` | `text` | a piece of the answer, streamed to the user |
| `thinking` | `text`, `done` | the model's reasoning, when the agent chooses to show it |

`model` is required on the model events, `callId` and `name` on the tool events and on each of `toolCalls`. Token
counts cannot be negative. Names and ids over `maxNameBytes` and `maxIdBytes` are refused with `bad_message` (`ref`
the task id); more than `maxToolCallsPerModelCall` tool calls, or more than `maxModelsPerTask` distinct models in a
task, with `limit`. An event for a task that is not open is refused with `unknown_task`. A refused `model_finished`
records no usage.

`usage` follows SAM's `llm.Usage` convention: `input` is every input token, cached ones included; `cachedInput` is the
part of `input` read from or written to the provider's cache; `cacheWrite` is the part of `cachedInput` written to it,
absent when the provider does not report cache writes separately (SAM then records them as unknown, not zero).
Anthropic's `input_tokens: 100, cache_read_input_tokens: 800, cache_creation_input_tokens: 50` is reported as
`input: 950, cachedInput: 850, cacheWrite: 50`; OpenAI's `prompt_tokens: 950` with `cached_tokens: 800` as
`input: 950, cachedInput: 800`. `pricingKey` names the price to use for the call and is the agent's claim.

## What the entrypoint does

- **Builds every SAM message itself**: the task's status events and signals, the final task or error, the approval
  and question screens, peer requests, the agent card. It numbers events, stamps the agent's name, and adds model
  family and provider from the model id.
- **Redacts** credential-looking values in tool arguments, tool results, a model's tool-call arguments and approval
  arguments with SAM-Go's own redactor, and caps every string in them at `maxStringBytes`, before anything is
  published or stored. An approval's `message` and `tool`, a question prompt's `message`, each question's `question`
  and `header`, each option's `label` and `description`, and a failure's `error.message` go through it too. A
  redacted option label is what its answer returns, so a label that looks like a credential does not come back as
  written.
- **Spend and totals.** One spend record per `model_finished` with `usage`, attributed from the delivered task. The
  final task's token usage is the sum of the task's `model_finished` usage.
- **Peers.** Forwards a peer's progress into the parent task's event stream (so the activity view nests it), relays a
  peer's own prompt to the user and the answer back to the peer, and sends the agent only `peer_result`.
- **Prompts.** A prompt still open after `timeoutSeconds` is answered `timed_out`; one on a task whose
  `canPromptUser` is false is answered `canceled` at once. The prompt reaches the user only on the task's own channel
  (the link does not fan it out to notifications).

### Card presence

Whether the agent is listed is the entrypoint's decision, from what every connection of the agent on this replica has
said:

- `describe` lists the agent (unless SAM has it stopped), at most once per 10 s, and starts republishing while a
  connection that has not retired is open. A connection that opens while the replica holds a description lists the
  agent again at once, even before it describes; except after the agent's last staying connection retired, when only
  a `describe` lists it again.
- `retire` marks its own connection as leaving: the removal card is published only when no other open connection of
  the agent stays un-retired, and only when a card was published since the last removal. A retired connection does not
  keep the card listed. The description is kept; a later `describe` from any connection lists the agent again.
- A stop publishes the removal card and pauses the republishing; a release publishes the held description again. The
  agent sends nothing for either.
- A connection that drops without `retire` leaves the card to expire with its heartbeats.

### Task lifecycle

A task is open from delivery until its `result` is published or `task_ttl_seconds` pass (each answer to one of its
prompts restarts the clock). It is bound to the connection it was last delivered on: SAM takes its `event`,
`prompt`, `result` and `peer_call` frames only from that connection while it is open, and refuses them from any
other with `unknown_task`. Once that connection is gone, another connection of the agent that sends for the task
takes it over. When it ends its open prompts
are answered `canceled` and its open peer calls cancelled (`peer_result` `canceled`); later frames for it are refused
with `unknown_task`. When the user or caller cancels it, the agent gets `cancel` and at once the same cancellations;
the task stays open for the agent's events and `result`, but a new `prompt` or `peer_call` is refused with
`forbidden`.

### Answers while the agent is away

A `prompt_answer` or `peer_result` that finds no connection of the agent (a short disconnect) is held, counted against
`maxOpenPrompts` or `maxOpenPeerCalls`; a `cancel` is held the same way, counted against neither. Held frames are sent
to the agent's next connection to the same replica after its `welcome` (and any `control`), in the order they were
held; other frames (`peers`, answers that arrive meanwhile) may come between them. What is held is bounded at 16 MiB
per agent: past it a frame is held cut to 64 KiB (`truncated`). A held frame is dropped when its task ends. A gateway restart, or a change of the
replica that serves agent links, loses it.

## What stays in the agent

Running the handler, per-task limits (model-call count, time limit), concurrency, conversation memory, its own audit
log and metrics, the framework adapters, enforcing `grantedToolScopes` before a tool runs, and stopping local work
when `control` says stopped. Redacting on the agent side too is allowed (defence in depth) and not required.

## How the SDK speaks the protocol

`sam_link_sdk.link.LinkTransport` is the core's `Transport` over these frames.

- **Protocol mismatch.** The agent offers `sam-agent-link.v2` only. A 4400 before the welcome, or a welcome of another
  protocol (`protocol` not 2, another subprotocol, or no `enabled`), is logged at ERROR and `open()` raises
  `LinkProtocolMismatch`; nothing that connection sent is acted on. On a reconnect the agent keeps trying with backoff
  and logs the mismatch at ERROR each time.
- **Identity and scopes.** A task carries `user.id` only. The agent's `required_scopes` and every tool's
  `required_scopes` go into `describe`, sent before `consume`. `grantedToolScopes` reaches `TaskContext`:
  `permitted_tools` and `check_tool` permit a tool only when every one of its required scopes was
  granted, compared exactly (a granted `tool:claims:*` is the scope a tool named, not a grant of `tool:claims:pay`);
  a task without `grantedToolScopes` grants none, and every adapter's `run_agent` refuses the call and audits the
  refusal. A configuration whose tools name more than `maxToolScopes` distinct scopes
  is refused at start-up (`TooManyToolScopes`), before connecting and again against the welcome's limit. Tool scopes
  come from the configuration, so they never vary between describes.
- **Prompts.** `TaskContext.can_prompt_user` is the task's `canPromptUser` (and, on any transport, whether the task
  has a status stream); when it is false `ask_user` and `request_approval` come back cancelled at once and no
  `prompt` is sent. A question prompt's `message` goes in the top-level `message`, an approval's in
  `approval.message`. A refused prompt is matched by the error's `requestId` and comes back cancelled at once. More
  open prompts than `maxOpenPrompts` are not sent and come back cancelled. `Question(multi_select=True)` sends
  `multiSelect`; its answer is the chosen options' values (or labels) joined with `, `.
- **Prompt limits**, kept before the prompt is sent, each either cut or refused to the code that asked:
  - more than `maxPromptQuestions` questions, or more than `maxPromptOptions` options in one question: `ask_user`
    raises `PromptTooLarge` (a `ValueError`) and nothing is sent. Dropping a question or an option would change what
    the user answers, so the handler decides (ask in two prompts, offer fewer options). The adapters' ask-user tools
    tell the model the question was not asked and why, so it can ask again within the limit;
  - a question's text and an option's label or description over `maxPromptTextBytes`: cut with SAM's marker
    `…[truncated N bytes]` (display text only); a header over `maxQuestionHeaderChars` characters: cut to them. An
    answer naming a cut or redacted label (SAM's redactor, ported in `sam_link_sdk.redact`, says how SAM shows it) comes
    back as the option's value, or the label the handler offered;
  - approval arguments: more than `maxApprovalArgs` members raises `PromptTooLarge` from `request_approval`; over
    `maxApprovalArgsBytes`, their strings are cut with SAM's marker (as the approval screen cuts them anyway), down to
    256 bytes each, and only when that is not enough does `request_approval` raise `PromptTooLarge`. An argument is
    never left out, since the user approves what they see.
- **Steps and usage.** Each `StepEvent` is one `event`. Tool arguments and results go out as the handler reported
  them; the entrypoint redacts and caps them. A model call's usage is what the task recorded for it
  (the `usage` of the `ModelFinished` step every framework adapter reports, which records it as it is emitted): the next
  `model_finished` carries it (a recording for the same model first), with `pricingKey` from
  `AgentConfig.spend_model_keys`, and is taken off the task only once that frame went out. `Usage` counts cache
  reads (`cached_input_tokens`) and writes (`cache_write_tokens`) apart, both within `input_tokens`; on the wire
  `cachedInput` is their sum and `cacheWrite` the writes, from `Usage._buckets()`, the same four buckets SAM's own
  agents' spend events carry (a report that breaks the nesting is clamped as SAM-Go clamps it). A `model_finished` too large for the link is dropped with a
  warning, and its usage still goes out on a `model_finished` of its own. Usage no `model_finished` carried goes out
  as one of its own before the task's `result`; usage reported after that is logged as lost at WARNING with the task
  and the token counts. The entrypoint records spend from `model_finished`.
- **Limits.** Every frame is built within the welcome's limits, so a task never has a step refused mid-way:
  - a model, tool, provider, pricing key or approval tool name over `maxNameBytes` is cut to it (names only label
    what happened);
  - a call id or tool-call id over `maxIdBytes` is replaced by `h-` and its SHA-256, the same for every use of the id,
    so a call's start, finish and model response still pair up;
  - a `model_finished` lists at most `maxToolCallsPerModelCall` tool calls, the first ones (each call is still
    reported by its own `tool_started` and `tool_finished`);
  - a task names at most `maxModelsPerTask` distinct models: one place is kept for `unknown`, under which any model
    past the others is reported; its usage is priced by the model's configured `spend_model_keys` entry, else its own
    name goes as the `pricingKey` (a key that matches no model configuration is filed unpriced, as `unknown` would
    be, never refused);
  - a tool step without a call id or a tool name is dropped with a warning;
  - more open peer calls than `maxOpenPeerCalls` fail at once without being sent;
  - a result's `error.message` is redacted and cut to `maxFailureMessageBytes`;
  - one event's tool arguments, result and tool-call arguments over `maxToolPayloadBytes` together, or over
    `maxFrameValues` with the rest of the frame: every string in them is cut with SAM's marker, shorter and shorter
    down to 256 bytes; failing that (many small values), each argument object becomes `{"_truncated": <its JSON
    text, cut>}` and each result its JSON text, cut, so the step is still reported, with a warning;
  - any other frame over `maxFrameValues` is treated as over `maxFrameBytes`: a result drops its data, a prompt or
    peer call is not sent.
- **Results.** A handler that raises ends the task with `error` `{message}` (the generic `internal error`: the
  exception's text is never sent). A failed model call keeps `category` and `provider`. A handler's own
  `TaskResult(state="failed")`, and a task stopped at a limit or by SAM, is a failed Task with its text. A result over
  `maxFrameValues` loses its structured data first (it holds the values). One over `maxFrameBytes` loses its largest
  files one by one until it fits, then its data, then has its text cut; the text names the files left out.
- **Delivery.** A task is acknowledged once its result is sent (also when it could not be, so it is not run twice). A
  task that arrives while the agent drains is left unacknowledged for another replica; a running task delivered again
  is held and acknowledged with the run, also while the agent drains; an answered one gets its result again (held
  for 10 minutes) and is acknowledged, without the usage SAM already took, since SAM records spend from every usage
  frame it takes and would record it twice. An answered task delivered again while the agent drains is taken as
  well, for it is work the agent already took: its held result is sent again and the delivery acknowledged, or the
  delivery only acknowledged when nothing is held. A run whose task SAM
  refuses as unknown after a reconnect is stopped and acknowledges nothing (see One replica's memory under Known
  gaps). A call depth over `AgentConfig.max_call_depth`, or a task that arrives while SAM has the agent stopped, is
  answered `failed` without running the handler.
- **Describe before consume.** On every connection the agent describes itself first, then consumes, back to back.
  What the dropped connection could not carry of an answer (the task's remaining usage, then its result) goes out
  when the entrypoint delivers that task again, and at once on a connection that takes no tasks. The entrypoint gives a
  connection no tasks while its latest `describe` is refused, so the agent is never served under an older description
  than the one it just sent. The entrypoint answers a refused `describe` at once and an accepted one not at all, so
  the agent never has two outstanding: a `describe` goes out no sooner than 1 s after the previous one on the same
  connection (one asked for sooner waits, then sends the description as it is by then), and a refusal counts for the
  latest `describe` sent. That holds while the entrypoint's refusal comes back within the 1 s spacing (a round trip
  under a second); over a slower link a refusal can arrive after the next `describe` went out, and is then counted for
  that one (the next new connection describes again). A refused `describe` (`error.frame` `describe`) is logged at
  ERROR with SAM's message, and not sent again on that connection until
  the description changes; each new connection describes once more, and the same refusal message is logged at ERROR
  again only after 10 minutes (DEBUG in between). The `consume` refusal that follows it (`error.frame` `consume`) is
  expected and only logged at DEBUG. `start()` does not wait for a verdict (the entrypoint sends none for an accepted
  `describe`): an agent whose description is refused stays connected and takes no tasks. A wake whose description is
  refused ends with `LinkError` at once instead of idling. On a connection that was already taking tasks, a refusal
  stops deliveries there (the entrypoint's doing); the connection stays, running tasks finish, and the next
  description that changes goes out with a `consume` again (an entrypoint that delivers again as soon as a `describe`
  is accepted takes the repeated `consume` harmlessly). A `consume` refused for want of any description is logged at
  ERROR and retried once on that connection after describing again. A `describe` SAM could not publish
  (`publish_failed`) is not a refusal: the entrypoint keeps it, tasks still come, and the agent sends it again so the
  card is published: after 1 s, then 2, 4, and so on up to 30 s while publishing keeps failing (a new connection or a
  changed description starts again at 1 s).
- **Reconnects and held answers.** The agent reconnects with exponential back-off and jitter, and never opens more
  than `LinkConfig.max_connects_per_minute` (20) connections in a minute, whatever drops them, well under the
  entrypoint's `max_connects_per_minute` (60). A connection answered with HTTP 503 (a replica that does not serve
  agent links) is tried again after its `Retry-After`, 1 s when it names none, held between 0.1 and 5 s and jittered
  up by at most half: the back-off does not grow and the attempt is not counted against the pace, since the entrypoint
  refuses it before it counts anything. This holds for a 503 from anything on the way, a proxy's included. After 30
  such answers in one reconnect the agent backs off as for any other failure. A connection opened with no grace (a
  wake, `announce()`, `retire()`, a start with `offline_grace_seconds: 0`), which fails at once on any other error,
  tries a 503 again the same way up to 15 times within 20 s, then raises `LinkUnavailable`. A connection closed with
  1012 is reopened like any dropped one, 0.25 to 0.5 s later with the default
  back-off. A connection the entrypoint refuses with HTTP 429 (too many connections
  in a minute) is retried after 7.5 to 15 s (a 15 s back-off, jittered down by up to half), the back-off doubling up
  to a minute, with one WARNING per back-off period; at start-up it fails `start()` with `LinkRateLimited`. A
  request-driven agent opens one connection per wake, announce or retire, and the entrypoint wakes it at most once per
  30 s. Open prompts and peer calls stay open across a reconnect. An answer the entrypoint held is read after the new
  connection's describe, and reaches its waiting prompt or peer call; a held frame that arrives twice is ignored. A
  prompt waits no longer than its own timeout plus 5 s; a peer call no longer than
  `LinkConfig.peer_call_timeout_seconds` (7200 s, the entrypoint's default `task_ttl_seconds`, which a gateway restart
  forgets), nor the task's own time limit; one given up is cancelled at the entrypoint (`peer_cancel`). A prompt or
  peer call cancelled because its task ended or was cancelled ends quietly. A frame that is not a JSON object is
  dropped; one the agent fails to handle is logged at ERROR by its type, never its content; a reader that fails for
  any other reason is logged and the agent reconnects.
- **Cut answers.** A `prompt_answer` or `peer_result` with `truncated: true` reaches the handler as it came, and is
  logged at WARNING once; `PeerResult.truncated` (and `PromptAnswer.truncated` on the transport) says so.
- **Held cancels.** A `cancel` held across a disconnect or a stop may arrive after the next `welcome`, among other
  frames: for a task still running it cancels it as any `cancel` does; for one already answered it is logged and
  ignored. An entrypoint may instead answer a task cancelled while the agent was away itself, never deliver it again
  and drop its held `cancel`; the run still here then stops at its next step (`unknown_task`, under Errors).
- **Work after the answer.** A handler may leave work running (an asyncio task, a framework's thread) after it
  returns. Once its run no longer holds the task (answered, cancelled, given up at shutdown or a wake's end, or the
  same task id delivered again), that work asks nobody, calls no peer and runs no guarded tool: `ctx.check_running()`
  raises `TaskHalted` (its `reason` says why), `ask_user` and `request_approval` come back cancelled and
  `call_agent` comes back `error`, with one WARNING per run (DEBUG after it). Nothing it does reaches a later delivery
  of the same task id: no step, no text, no prompt, no peer call, no usage. The agent link sends none of its late steps. The task is marked answered before the answer goes out.
  SAM's stop reaches such work too: the task reads the agent's stop live, and `check_running()` then raises
  an exception that is both a `TaskHalted` and an `AgentStopped`; raised out of a handler, bare or wrapped by a
  framework (as the cause or context of the framework's own exception, or in an exception group), it answers the task
  with the stop's message, as a stop does; another task's `TaskHalted` is an ordinary handler failure of this one. A
  task that reaches the core while SAM has the agent stopped is answered `failed` without running.
- **A stop after the handler returned.** A task whose handler has returned is answered with its own result, even when
  SAM's stop is read for the first time at that moment (the offline grace running out); only a
  stop the agent has already applied (`on_control`) before that answer is claimed, while the result's text is still
  streaming, answers it with the stop's message instead.
- **A stop before the first step.** A task stopped (by SAM or a limit) before its run took a step is answered `failed`
  with the stop's text like any other; a task abandoned at shutdown or by a wake cut short is forgotten without an
  answer, so SAM's next delivery of it runs it.
- **Errors.** An `error` whose `requestId` is an open prompt cancels that prompt; one whose `ref` is an open peer call
  fails it at once (`SAM refused the peer call: ...`); a `publish_failed` for a result sends it again (three tries);
  a `bad_message` or `limit` for a result the agent sent answers the task once more, `failed` with `The agent's answer
  could not be delivered: <SAM's message>`, and that answer is not sent again if SAM refuses it too; an `unknown_task`
  for a result the agent sent (the task expired, or the gateway restarted) is logged at WARNING; an
  `unknown_task` for a step of a task still running here and delivered on the current connection (the entrypoint
  closed it: it expired) stops the run, as a `cancel` would; one for a task whose run still waits, after a reconnect,
  to have it delivered again changes nothing (see One replica's memory under Known gaps). An `error` that names
  nothing the agent can place (no `ref`, no `frame`: a `ref` too long to echo, an unknown type) is logged at WARNING
  with its code and message, and changes nothing else.
- **Strict JSON.** Every frame is strict JSON: NaN and the infinities go out as `null`, and a string that is not valid
  Unicode (a lone surrogate) has the bad characters replaced with U+FFFD.
- **Acknowledgements.** A delivery number belongs to its connection: an `ack` that waited out a reconnect is not sent
  on the new connection (the entrypoint delivers the task there again, and the agent acknowledges that copy).
- **Waiting for the user.** While a task waits for its user, the core sends no keepalive "Still working" for it: a
  SAM agent sends nothing then, and a working-state event would put the task back from input-required and restart
  SAM's idle timeout while the question is open.
- **Stop.** `welcome.enabled` and `control` reach the core, which keeps the stop under `state_dir` (in
  `<host>_<port>_<path>__<agent>.stop.json`) and fails closed after `offline_grace_seconds`
  offline. The stop is written before it takes effect, so a crash in between leaves the
  agent stopped. A write that fails (a read-only directory) does not hold the stop back: it is logged at ERROR once,
  reported as the health problem `stop_state_unsaved`, and tried again every 5 s and on every link event until it
  succeeds; a restart before then starts released, until the welcome says otherwise. Each welcome's state and its
  contact are applied before anything else happens on that connection, and every drop (re)arms the offline grace, so a
  connection that drops right after its welcome still stops the agent once the outage passes the grace
  (`sam_link_sdk.stop.StopState` documents the state machine). The card is the entrypoint's: the agent describes itself
  and consumes whether or not it is stopped, never retires for a stop and sends nothing for a release. A release's
  `reason` reaches the `on_stop_change` listeners.
- **Description.** Sent first on every connection, whenever it changes (checked every 5 s, for health problems), and
  every 5 minutes anyway. The entrypoint publishes nothing for an unchanged one and at most one card per 10 s, so the
  periodic describe costs no publish; describing on every connection is what lists the agent again after its last
  staying connection retired. `retire` goes out only when the agent leaves for good: `MeshAgent.stop()` (another
  replica's connection keeps the card) and `MeshAgent.retire()`. An option's `value` is mapped back from its label. A
  health problem's `since` is not carried.
- **Request-driven agents.** `handle_wake`: connect, `describe`, wait for `peers`, `consume`, serve, disconnect. A
  wake the runtime cuts short (its time limit) abandons the tasks still running before it disconnects: they are not
  acknowledged, so SAM delivers them again, and nothing of them keeps running without a connection.
  `announce`: connect, `describe`, disconnect. `retire`: connect, `retire`, disconnect. The entrypoint keeps a
  sleeping agent's description in memory: after a gateway restart the agent is not listed until it announces again
  or a wake describes it.
- **Frame size.** The largest frame either way is the welcome's `maxFrameBytes`, at most the configured
  `max_frame_bytes`. The inbound limit is set when the WebSocket opens, before the welcome; it is kept, and an
  entrypoint announcing more is reported at WARNING when the agent connects.

## Known gaps

- **Ids the agent does not choose.** A task id is SAM's, and a peer on SAM's broker chooses its own subtask ids: they
  can exceed 128 bytes, and then a refusal of a frame naming them carries no `ref`. Such a refusal cannot be placed;
  the SDK logs it and changes nothing.
- **Tool calls the link does not see.** `grantedToolScopes` is enforced by the agent's own code; SAM cannot require
  it of a third-party agent.
- **One replica's memory.** Task bindings, open prompts and peer calls, held answers and the described card live in
  the memory of the replica that serves agent links. A gateway restart, or a change of the serving replica, loses
  them, and the agent describes itself again on its new connection. A task whose delivery the agent had not
  acknowledged, which with this SDK is every task still running, is delivered again with `redelivered: true` once
  the agent takes tasks on its new connection, by whichever replica serves it. SAM forgets the earlier run only when
  the serving replica changes or restarts: the task's steps, prompts and peer calls are gone there, and to SAM the
  task starts from its request again. A replica that keeps serving, and that the agent only reconnects to, still
  holds the task and gives its request back to the broker to deliver again. Whether the request comes again is the
  broker's doing. The in-process dev broker and a Solace event broker deliver it again, until the request's
  redelivery limit (3) is spent. The TCP dev broker behind `cmd/devbroker` never does. An agent's work should be
  safe to repeat.

  The SDK keeps a run still in progress going. After a reconnect on which the agent takes tasks, the run sends
  nothing for its task until the task has been delivered on the new connection or the wait for that ran out
  (below). Until then every frame a run sends for its task (a step, a prompt, a peer call, its answer) goes out
  only on the connection the task was last delivered on. A `peer_cancel`, and a result sent again after
  `publish_failed`, go out on whichever connection is current.
  The run's steps are dropped meanwhile, as they are while the link
  is down, since they are progress and SAM would refuse them; a prompt, a peer call and the answer (the remaining
  usage, then the result) wait, since the run or its caller depends on each. An answer made while the link was down
  waits the same way. A refusal with `unknown_task` in that time is no cancel. When the delivery arrives the SDK
  attaches it to the run, what waited goes out, and the task gets the run's one answer. A `cancel` for the task
  cancels the run at any point, and its `canceled` result waits for the delivery too.

  The wait lasts 90 s from the reconnect. SAM delivers a task again once it has taken the request back from the
  connection that dropped. It does so at once when it saw that connection close, and otherwise only when it notices
  by itself that the connection is gone, up to 60 s later, so the wait is longer than that. A task is not
  delivered again when SAM closed it while the agent was away, when it went to another instance of the agent, when
  its redelivery limit is spent, and whenever SAM's broker cannot deliver a request again (the TCP dev broker).
  When the 90 s are over the SDK stops waiting, with a WARNING: the run sends on the new connection as it is, and an
  answer held from before the reconnect goes out there. With a Solace broker's redelivery limit spent, and on the
  TCP dev broker, SAM still holds the task and takes those frames, so the task is answered, up to 90 s later than
  it would have been. SAM takes them only while the task is not bound to another connection that is still open.
  Where SAM no longer holds the task it refuses them with `unknown_task`, and so it does where a second instance of
  the agent runs the task by now, and on the in-process dev broker, where SAM fails a task at its next sweep once the
  redelivery limit is spent. A run still going is then stopped at its first refused step, prompt or peer
  call and answers nothing, and a delivery that comes later starts its task afresh (one that comes
  while the run is being stopped is given back with a `nack`). A refused answer is logged, and its result stays
  held for a delivery that comes later. Either way the usage SAM did not take is logged as lost, once. While a run
  sends on a connection its task was not delivered on, the usage of its model calls goes with its answer instead of
  with its steps, so none of it is lost unseen. One gap stays: while the replica that serves stands down, it drops
  every `event` it reads unread, one carrying usage included. The send succeeds, the agent is told nothing, and
  SAM records no spend for that step. Run SAM on its in-process dev broker or a Solace event broker where
  agents must ride out a dropped connection without that delay. The 90 s run only while the link is up. When the link
  drops again the wait ends, and what waited waits for the link as any frame does (`connect_timeout_seconds`): past
  that a prompt is answered cancelled, a peer call fails, and the answer is held until SAM delivers its task on a
  later connection, where the 90 s start over. When the agent disconnects (a stop, the end of a wake) every wait
  ends and nothing is answered for a task that was still waiting.

  A connection on which the agent takes no tasks gets no delivery, so nothing waits on it: an agent that is stopping
  has retired and sends no `consume`, an agent whose description does not fit the limits the connection announces
  sends none either, and SAM delivers nothing while it refuses the agent's description. A run still
  going then sends on that connection as it is, and SAM takes its frames if it still holds the task. A task SAM
  delivers again while the agent drains, on a connection it took tasks on before it began to stop, still reaches its
  run or its held answer. `retire` only takes the card down, so the waits go on while the agent drains: a task SAM
  gives back is still delivered on a connection the agent took tasks on, and the run, or the answer made meanwhile or
  held, takes that delivery first. At the end of the grace period (`shutdown_grace_seconds`), `stop()` ends what still
  waits: the runs' frames, their answers included, and the answers held for a delivery go out on the current
  connection as they are, as they do when the 90 s run out, and only then does the agent disconnect, so an answer is
  not lost with the connection. SAM takes them if it still holds the task, and refuses them with `unknown_task`
  otherwise. So a stop whose answers wait for a delivery that does not come takes the grace period, and answers
  once, with no task running. The held answers are sent within one more second once the grace has run out; one the
  link refuses while it stays up does not stop the others, and a link that drops meanwhile ends the send. If the
  link is down at that moment nothing can be sent, and the
  disconnect reports the held answers lost. The answer to a cancel that arrives during the drain is not waited for.
  All of this holds after any reconnect, not only a change of replica. A prompt or a
  peer call the run had open before the reconnect is not re-sent. When the replica that serves stands down (a
  change of the serving replica, a rolling restart), it ends them first: the prompt is answered `canceled` and the
  peer call ends `canceled` with `peer request cancelled`, both written before it closes the connection with 1012,
  so the run asks again, or goes on, once its task is delivered again. A `prompt` or a `peer_call` that reaches the
  replica after it began to stand down, and before it closes the connection, is answered the same way (the prompt and
  the peer call `canceled`) and not run. A `result`, a `retire` and an `event` that reach it then are
  dropped without an answer: a result is sent again when its task is delivered again, and a `retire` is moot because
  the replica forgets the agent's card as it stands down. SAM logs a dropped result and a dropped event that carried
  usage at WARN, with the task's id and the event's token counts, and counts them in its stand-down summary, so an
  operator can tie a repeated run or missing spend to the stand-down. A frame that reaches it in the instant between the last
  write and the close is dropped unanswered. When the replica crashes instead, nothing is
  sent: see One replica's memory below. A task
  the agent had acknowledged and not answered is no longer known to SAM either, so its `result` is refused with
  `unknown_task`: when the serving replica changes while the old one still runs (a rolling restart, a broker
  reconnect), the old one fails it to its caller at once, with a message to send it again; when the gateway process
  dies instead, nothing answers the caller before its own timeout. Held answers are lost too: a user's answer to a
  prompt of the earlier run is dropped, and a peer's answer is not relayed. After a crash of the serving replica
  (no stand-down) an open prompt or peer call of a redelivered task is not closed: the run waits for the prompt's
  own timeout (45 minutes by default) or for `peer_call_timeout_seconds` (2 hours), and the peer is not cancelled.
- **Late usage.** Usage reported after the task's `result` cannot be recorded: the task is closed. The SDK logs it as
  lost.
