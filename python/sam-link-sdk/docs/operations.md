# How the agent behaves on the link

What the agent does when SAM stops it, the connection drops, or a task goes wrong. One behaviour per bullet, with the
knob where there is one. Setup is in [agent-link.md](agent-link.md).

## SAM decides who may do what

- **Identity is SAM's.** SAM verifies each user's token and never forwards it. A task carries the user's id and the tool
  scopes SAM granted them; the agent verifies nothing itself.
- **Per-tool scopes are what SAM granted.** SAM checks each user against the agent's `required_scopes` before it
  delivers a task, and sends the tool scopes the user holds with it. `ctx.permitted_tools` and `ctx.check_tool`, and
  with them every adapter's `run_agent`, give a tool only to a user granted all of its scopes, compared exactly. A task
  without a granted list grants none. SAM checks at most 64 distinct tool scopes per agent; more is refused at start-up
  (`TooManyToolScopes`). So is a description over 4096 bytes, more than 32 skills, or a skill whose name is over 256
  bytes or whose description is over 1024 (`DescriptionTooLong`): SAM would refuse it and give the agent no task.
- **Peers.** The agent may call only the peers configured for it in SAM, and only for a user who may invoke them. A call
  SAM refuses fails at once with `SAM refused the peer call: <reason>`.
- **Prompts on channels that cannot show them.** A task from a channel that cannot prompt its user (MCP, a webhook, the
  event mesh) has `ctx.can_prompt_user` false, and `ask_user` and `request_approval` come back cancelled at once.
- **The user's profile is withheld** unless the administrator sets `share_user_profile: true` for the agent.
- **SAM redacts, caps and totals.** The agent reports what happened (a model call and its tokens, a tool's arguments and
  result). SAM redacts credentials, caps strings at 16 KiB and totals the task's token usage. Usage that cannot reach
  SAM (reported after the answer, or unsent when the agent disconnects) is logged as lost at WARNING.
- **Limits.** SAM announces what it accepts when the agent connects (name lengths, tool calls per model call, questions
  and options per prompt, tool scopes, and more). The SDK stays within them: it cuts text that is only shown, and raises
  `PromptTooLarge` to the handler for a prompt with more questions, options or approval arguments than the link carries.
  `SAM_LINK_MAX_FRAME_BYTES` is the largest message the agent accepts.

## The card

- On every connection the agent describes itself first and only then asks for tasks. SAM refuses a description that is
  too large or names too many tool scopes; the agent logs that at ERROR and gets no tasks until SAM accepts one.
  `start()` does not wait for a verdict, so SAM may list the agent a moment after it returns.
- The agent describes itself again when its description changes and every 5 minutes, so a card SAM lost comes back.
- It retires (leaves the listing) only when it leaves for good: `agent.stop()` or `agent.retire()`. Stopping one replica
  leaves the others listed.
- A request-driven agent stays listed while it sleeps. After SAM restarts it is not listed until it announces again
  (`agent.announce()`) or a wake describes it ([serverless.md](serverless.md)).

## SAM's stop

No SAM release sends a stop yet: until one does, the only way an agent gets stopped is the offline grace below.

- A stopped agent gets no new tasks, running tasks end with a failed answer, `agent.check()` raises `AgentStopped`, and
  `on_stop_change` listeners are called, until SAM releases it. `agent.allowed` says whether SAM lets the agent run.
- The stop is saved under `state_dir` (default `~/.sam_link_sdk`), so a restarted agent starts stopped until SAM says
  otherwise. A save that fails is logged at ERROR once, reported as the health problem `stop_state_unsaved`, and retried
  every 5 s.
- The agent fails closed. Once connected, a link that stays down longer than `offline_grace_seconds` (900) stops the
  agent (reason `disconnected`) until it reconnects and learns SAM's state.

## Connecting and reconnecting

- **The first connect is retried** with backoff for up to `offline_grace_seconds` before `start()` raises `LinkError`;
  with 0 it tries once. A refused token or another protocol fails at once. A request-driven agent tries once. Trying
  once still means trying again past an HTTP 503, as below.
- **A refused token ends the agent.** After `LinkConfig.max_auth_refusals` (10) refusals in a row, `run_forever()` stops
  the agent gracefully and raises `LinkAuthRefused`, so a supervisor sees a non-zero exit. A connection that stayed up
  for 60 s starts the count again.
- **Reconnects.** A dropped connection is reopened with backoff and jitter (at most `max_connects_per_minute`, 20), the
  agent describes itself, then asks for its tasks again. A task still running answers through the new connection.
- **Several SAM replicas.** One gateway replica serves agent links and the others answer HTTP 503, so a plain load
  balancer is enough, and one that pins an agent to a replica (session affinity) must be changed: a pinned agent may
  never reach the replica that serves. The agent tries any 503 again after its `Retry-After` (about a second),
  without backing off. A serving agent does so up to 30 times in a reconnect before it falls back to its back-off. A
  connection made with no grace (a wake, `announce()`, `retire()`, or `offline_grace_seconds: 0`) does so up to 15
  times within 20 s. SAM asks for 1 s between attempts and the agent adds up to half as much again, so that comes to
  14 to 16 attempts (13 to 15 of them retries) when each refusal comes back promptly: enough to reach the serving
  replica behind a round-robin balancer of about a dozen replicas, and to cover the seconds a SAM that has just
  started needs. A connection that SAM closes with 1012 before its welcome (the replica reached stopped serving agent
  links as the agent joined) counts as a 503 too. Past that it raises `LinkUnavailable`, so an outage
  costs a serverless invocation about 20 s. When the serving replica changes, SAM closes the agent's connections
  (1012) and the agent reconnects the same way. A task still running then is delivered again with
  `redelivered: true`, on whichever replica serves next. The run under way goes on: it sends nothing for the task
  until that delivery arrives (its steps are dropped, a prompt, a peer call and the answer wait), then answers
  through it, once. If the delivery does not come within 90 s the run stops waiting and sends on the new
  connection all the same, with a WARNING that names the task. SAM takes its frames if it still holds the task and
  no other connection of the agent that is still open has it, and the task is answered late. If SAM closed the
  task, or a second instance of the agent runs it by now, SAM refuses them, the run is stopped unanswered, the usage
  SAM did not take is logged as lost, and a later delivery runs the task again, so make a handler's work safe to
  repeat. The 90 s are longer than the 60 s SAM can take to notice a connection that died without a close, which is
  when it delivers that connection's tasks again. A broker that delivers a request again spares the wait after a
  drop SAM saw, and shortens it to those 60 s at most after one it did not. SAM's
  in-process dev broker and a Solace event broker do, until the request's redelivery limit (3) is spent. The TCP dev
  broker behind `cmd/devbroker` does not, so there an answer that crosses a reconnect is up to 90 s late. Use the
  in-process dev broker or a Solace event broker where agents must ride out a dropped connection. A task the agent
  had acknowledged
  and not answered is failed to its caller at once, with a message to send it again, and a `result` the agent sends
  for it afterwards is refused with `unknown_task`.
- **What survives a drop.** An answer that could not be sent is held (at most 256, 64 MiB; the oldest is dropped
  with a WARNING) and goes out, with the usage that had not been sent, when SAM delivers its task again on a new
  connection. An agent that is stopping takes no tasks any more, so it sends a held answer as soon as a connection
  opens. When it stops on a connection that waits for its tasks to be delivered again, the runs and the held answers
  wait there for the delivery for the grace period (`shutdown_grace_seconds`), and what still waits when it ends is
  sent on the connection as it is before the agent disconnects, so the stop takes the grace period when SAM delivers
  nothing again. Open prompts and peer calls stay open.
  When the serving replica stands down (a change of replica, a rolling
  restart), it first answers each open prompt cancelled and ends each open peer call, before it closes the
  connection, so the run asks again once its task is delivered again. A prompt or a peer call that arrives while it
  stands down is answered the same way, not left to wait for its timeout. After a crash of the serving replica nothing
  is sent: an open prompt or peer call of a redelivered task waits for the prompt's own timeout (45 minutes by
  default) or `peer_call_timeout_seconds` (2 hours), and the peer is not cancelled. A cancel sent while the agent
  was disconnected reaches it on the next connection.
- **What does not.** Step events (progress, text, model and tool steps) are dropped while there is no connection.
  Answers, prompts and peer calls wait for it up to `connect_timeout_seconds`; after that a prompt is answered
  cancelled, a peer call fails and an answer is held. On the new connection a run's steps stay dropped, and its
  prompts, peer calls and answer wait, until SAM has delivered its task there again. SAM does that at once after a
  drop it saw, and up to 60 s later after one it did not. A run or a held answer whose task does not come within
  90 s stops waiting and is sent on that connection as it is: SAM takes it if it still holds the task and refuses
  it otherwise. The 90 s count only while the link is up, and every such wait ends when the agent disconnects.
- **A known limit: a connection that died without a close while the agent stops.** An agent that is stopping, or
  whose description SAM refused, sends no `consume` on a new connection, so nothing is delivered again there and it
  does not wait: its runs and held answers are sent at once. If its earlier connection died without a close, SAM
  counts that connection as open for up to 60 s more and holds the tasks for it, so it refuses those frames. The
  run is stopped, its usage is logged as lost, and the WARNING says SAM no longer holds the task or holds it for
  another connection of this agent. SAM gives the request back when it notices the dead connection, and the task
  is then run again, by another instance of the agent or by this one once it is back.
- **The token stays out of logs.** The WebSocket client logs its handshake headers at DEBUG, and the agent redacts the
  token from them.

## When a task goes wrong

- A handler that raises ends the task with `internal error`; the exception's text never leaves the agent.
- A cancel ends the task as `canceled`; a limit or SAM's stop ends it with that message.
- A task whose run is cancelled from outside the agent (a loop shutting down, library code) is given back to SAM
  unanswered, with a WARNING naming it, and SAM delivers it again. A process gives a task back once; the second time it
  answers the task failed, so an outside cancellation costs one redelivery per process.
- A task SAM sends that the agent cannot read is answered failed ("The agent could not read this task, so it did not run
  it.") and acknowledged. A message that is not a JSON object is logged by its type, never its content, and the
  connection carries on.
