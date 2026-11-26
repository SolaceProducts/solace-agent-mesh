# Testing

Three suites, from fastest to most real.

| Suite | Marker | Needs | Run |
| --- | --- | --- | --- |
| Unit | none | nothing | `uv run pytest -q` |
| Contract | `contract` | a SAM-Go build or checkout | `SAM_GO_DIR=<checkout> uv run --group contract pytest -m contract` |
| End to end | `e2e` | a SAM-Go build or checkout | `SAM_GO_BIN=<sam> uv run pytest -m e2e tests/e2e` |
| Soak | `soak` | a SAM-Go build or checkout | `SAM_GO_BIN=<sam> SAM_SOAK_SECONDS=600 uv run pytest -m soak tests/e2e` |

The default run excludes `contract`, `e2e` and `soak`, and `-m e2e` excludes `soak`.

Every run sets `HOME` to a temporary directory of its own before any test module is imported, so SAM's stop saved
under the default `state_dir` (`~/.sam_link_sdk`), a framework's own storage (CrewAI's under `Library/Application
Support`) and anything else kept under `HOME` stay out of yours. A run that writes under the real `~/.sam_link_sdk` anyway
fails at its end and names the files (`tests/conftest.py`).

## Lint

Ruff checks the Python under the SDK's directory, with the rules set in `[tool.ruff]` in `pyproject.toml`: pyflakes and pycodestyle errors, import order, bugbear, pyupgrade to Python 3.11, commented-out code (`ERA`) and ruff's own rules. The `Lint` job in CI runs it on every pull request. Run it before you push:

```
uv run --only-group lint ruff format .
uv run --only-group lint ruff check --fix .
```

The first formats the code (the same style as Black, at 120 characters), the second applies the lint fixes ruff is sure of and lists the rest. CI fails on code `ruff format` would change. Silence a finding on one line with `# noqa: <code>` only when the code is right as it is, such as an exception raised inside `except` on purpose to test its chaining. A string or comment longer than the line is left whole.

## Type check

mypy checks the types of `src/sam_link_sdk`, with the settings in `[tool.mypy]` in `pyproject.toml`: bodies of unannotated functions are checked too, and an unused `# type: ignore` or a redundant cast is an error. It is not strict mode, and `tests/` is not checked. It needs the framework packages installed to check the adapters, so it runs with the `dev` group. The `Type check` job in CI runs it on every pull request, on Python 3.11. Run it before you push:

```
uv run --group dev --group typecheck mypy
```

Fix a finding with the right annotation or a narrowing check. Where the code reaches a framework's private names or an untyped object, `Any` at that boundary is fine. Silence a finding on one line with `# type: ignore[<code>]` only when the code is right as it is, and always name the code. A package that ships no types is named in a `[[tool.mypy.overrides]]` entry with `ignore_missing_imports`, not silenced globally.

## Code comments

There are no code comments. The repository's `commentcheck` enforces the rule (`make lint-comments`), and
`tests/test_comments.py` applies the same rule to the SDK, so a pull request fails in the SDK's own tests too.

What counts as a comment: any `#` comment in a `.py` file and in a `.sh` or `.bash` file, and any `--` or `/* */`
comment in a `.sql` file. Docstrings are not comments. The checker skips paths with a `testdata`, `vendor`,
`node_modules` or `examples` directory, and a top-level `webui` or `public`, so the example agents under `examples/`
are not held to it; the rest of the code is, `tests/` and `ci/` included.

What it allows: a shebang on the first line, and tool directives: `# noqa`, `# type:`, `# pyright:`, `# mypy:`,
`# pylint:`, `# fmt:`, `# isort:` and `# ruff:` in Python, an encoding line (`# -*- coding: utf-8 -*-`), and
`# shellcheck` in shell. Anything else is a violation, including `# pragma: no cover`, `# nosec`, a licence header and
a comment after code. For a `# pragma: no cover`, use the coverage configuration (`exclude_also`, `omit`) instead.

Where the information goes instead:

- A reason a value, a limit or a branch is what it is: the docstring of the function or class, or, for a module-level
  constant or a dataclass field, a string literal on the line after it (an attribute docstring). Write it as a
  sentence.
- A reason a test exists or sets something up: the test's docstring, or a name that says it.
- Something a contributor must know about the build or CI: this directory.
- What the code already says, a banner, or commented-out code: nothing. Delete it.

A docstring that a framework reads as a tool description (a function handed to a framework as a tool) is the model's
prompt: do not put contributor notes in it.

## Public API snapshot

`tests/test_public_api.py` compares the names each public module exports, each callable's parameters (names, kinds,
defaults) and each class's public instance attributes (a dataclass's fields and every `self.<name> =` in its methods), to `tests/fixtures/public_api.json`, and fails on any difference. It imports every adapter, so it runs only
with the framework extras installed, as the unit job has them (`uv run --locked`); it does not skip. After an intended
change, regenerate the snapshot with `SAM_LINK_SDK_UPDATE_API_SNAPSHOT=1 uv run --locked pytest tests/test_public_api.py`
and commit it with the CHANGELOG entry (CONTRIBUTING.md, "Versioning and deprecation").

## Unit tests

`uv run pytest -q` runs the unit tests in about two minutes. They stand in for SAM: `tests/agent_rig.py`'s
`RecordingTransport` records what a real `MeshAgent` tells SAM and lets a test act as SAM (deliver a task, cancel it,
answer a prompt, stop the agent), and `tests/test_link.py`'s `LinkServer` is an in-process agent link entrypoint for
what goes over the wire. So they show that the SDK does what its authors meant. They can't show that SAM agrees.

`uv run pytest -q --cov=sam_link_sdk` also measures line and branch coverage of `sam_link_sdk` and fails under the floor in
`[tool.coverage.report]` in `pyproject.toml` (90%; the suite covers 92%). CI measures it on Python 3.14, so new code comes
with the tests that run it.

`tests/test_fail_closed.py` shows that a guard fails closed in every adapter. Each adapter's real framework runs a
scripted model as the handler of a real task, and the tests assert on the tool's body, not on the steps reported: it
does not run when the user denies the approval, when the approval prompt times out or fails, when the user lacks the
tool's scope (CrewAI there; the other adapters in `test_adapter_conformance.py`'s `denied_tool` scenario), or when the
task was stopped while the model was answering. A framework release that kept a hook's name but stopped honouring it
(Strands' `cancel_tool`, Agent Framework's middleware that skips `call_next`, Hermes' `{"action": "block"}`) fails here.
Hermes runs at its hook boundary, so these cells run in the unit job; `tests/test_hermes.py` runs the real agent. The
module is in `ADAPTER_MODULES`, so the skip guard fails the run if none of it passes.

## Contract tests

The contract tests run SDK agents against a real SAM-Go. They take about a minute.

Point them at SAM-Go in one of two ways:

- `SAM_GO_BIN=/path/to/sam`: a built `sam` binary.
- `SAM_GO_DIR=../..`: the Solace Agent Mesh checkout this directory is in. The tests build it with `go build -tags nosolace ./cmd/sam`
  (as SAM-Go's `make build-backend`) into `.contract-cache/sam-go/<commit>/` in this directory, and reuse that
  build until the checkout changes. They never write inside the checkout.

With neither set, every contract test is skipped and says why, unless `SAM_CONTRACT_REQUIRED=1` (CI sets it): then a
SAM that cannot be found or built fails the run. A SAM that does not speak the agent link protocol
(`sam-agent-link.v2`) fails the run with the reason.

### What a run does

1. Boots one SAM for the session: `sam run --embedded` with the in-process dev broker, SQLite, the web gateway, the
   platform service and the agent link entrypoint. Ports are chosen free at run time. The data directory, SAM home
   and `HOME` are under pytest's temporary directory. The environment is built from scratch, so none of your own
   keys reach SAM. RBAC is on without an identity provider: the trust manager signs each task's user token, the
   user is `alice@example.com`, and the roles come from a YAML file the test writes. No model key is needed.
2. Issues a link token per agent with `sam-link-sdk link-token` and configures SAM with the digests.
3. For each scenario, connects scripted agents (no model) through `MeshAgent` over `LinkTransport` (`driver_link.py`,
   which reports raw step events, so redaction and caps seen in SAM's records are SAM's own). It then works SAM from
   the outside, as a user or an administrator would: the HTTP API (`/api/v1/message:stream`,
   `/api/v1/tasks/{id}/events`, `/api/v1/agentCards`, `/api/v1/platform/...`) and `sam task send` for prompts and
   workflow-node tasks.
4. Stops SAM with SIGTERM, then SIGKILL after 15 seconds. It signals only the process it started.

When a test fails, the report includes the end of SAM's log, with tokens and secrets removed.

### What it proves

The suite checks these against SAM's own records:

- an agent is listed with its card (description, tools, its declared owner and framework, SAM's registered owner)
  and is delisted when it leaves
- model and tool steps reach the task's events with matching call ids, and token usage reaches the final task
- streamed text reaches the user in order
- a cancel stops the agent and the task ends canceled
- an approval prompt reaches the user, and both approval and denial reach the agent
- a peer call reaches the peer, and the peer's steps nest under the caller's task
- a peer outside the caller's allowlist is refused by the SDK, and by SAM when the call is sent anyway
- a wrong or borrowed link token is refused
- a handler exception becomes the JSON-RPC error SAM's own agents send (-32603 `internal error`), without the
  exception text
- an offer without `sam-agent-link.v2` is closed 4400; a `consume` before `describe` is refused with
  `error.frame` `consume`, which is how the SDK tells it from a refused `describe` (both have no `ref`); a
  `consume` sent again after an accepted `describe` is accepted; and the SDK describes first on a fresh connection
  and after a reconnect
- of two connections of one agent, one leaving keeps the agent listed; the last one leaving delists it
- a description SAM refuses (tools naming more than 64 scopes over two describes) is followed by the `consume` the
  SDK sends right after it, which SAM refuses too; `start()` succeeds with the refusal logged,
  and SAM delivers that connection no task
- a refused `describe` is followed by a refused `consume`, each naming itself in `error.frame`
- a cancel sent while the agent is disconnected reaches it on reconnect and stops the task (`canceled`)
- a tool's required scope that the user holds is granted to the agent and one the user lacks is not (the test user
  holds every catalog scope but one tool scope; the harness's single user cannot show a second user)
- spend is recorded per model call, for the agent and the user
- tokens recorded for one model are never billed under another
- an answer with an empty file, a signal-shaped `data` object or SAM's own task keys in `metadata` still completes the
  task
- values JSON cannot carry (NaN, infinities, an invalid file name, an infinite prompt timeout) reach SAM as strict JSON
- credentials in tool arguments, tool results and a model's tool calls never reach SAM's records, and an oversized
  tool result string is capped at 16 KiB
- a questions prompt reaches the user, and the answers reach the agent; a multi-select question is shown as one and its
  choices come back joined
- a prompt with more questions than the link takes is stopped in the agent (nothing is sent), and the welcome
  announces the limits the SDK keeps
- a workflow-node task (`sam task send --si-input-schema`) reaches the agent as one, and its structured output reaches
  the final task

A test marked `xfail(strict=True)` is a known SDK or SAM bug, and its reason says where. It starts failing when the
bug is fixed. Then remove the mark.

### What it doesn't prove

- Other topologies: separate gateway, platform and agent processes, a real Solace broker, Postgres, more than one
  gateway replica.
- Real models and the framework adapters. The agents are scripted.
- A protocol mismatch, load, and reconnects other than SAM restarting. The e2e suite runs request-driven agents that
  SAM wakes (`wake:`, `tests/e2e/test_core_behaviour.py` and `test_hosted_runtimes.py`); a stopped one not being woken
  is untried. It restarts SAM
  to drop the link and to rotate or revoke a token, so SAM-Go's own revocation (close code 4401 on an open link) and a
  token swapped without a restart are untried, and so is a shutdown whose answer is slow to leave on a live link. The
  unit tests cover the SDK's side of them against an in-process entrypoint (`tests/test_link.py`,
  `test_link_transport.py`, `test_link_protocol.py`, `test_serverless_templates.py`).
- A prompt on a task that cannot prompt its user (`canPromptUser` false): the harness has only the web gateway, which
  always can. The unit tests cover it against an in-process entrypoint.
- A real workflow: the workflow-node scenario submits the node's request itself, so its input arrives by reference
  (the web gateway stores the uploaded file), never inline as a workflow sends it.
- SAM's stop. No SAM build has an agent stop API yet, so the three scenarios that stop an agent from SAM (it gets no
  new tasks and works again once released; SAM delists and relists it; a task waiting for a slot is answered) skip,
  and `ci/allowed-skips-sam.txt` names them. When the API lands, point `stop_agent` in `tests/contract/sam_client.py`
  at it and remove those entries.

### Adding a scenario

1. If the scenario needs a new agent, add a role to `ROLES` in `tests/contract/roster.py`. The value is the roles it
   may call as peers. SAM reads its link configuration once, at boot, so every agent must be listed there.
2. Write the test in `tests/contract/test_link_contract.py`. Take the `mesh` fixture, start agents with
   `mesh.start(role, handler, ...)`, and drive SAM through `mesh.client`. A handler is an `async def` that takes a
   `ScriptTask` (`tests/contract/driver.py`). It uses only what the `ScriptTask` offers (`model_call`, `tool_call`,
   `stream`, `request_approval`, `call_peer`, `wait_cancelled`), never SDK internals.
3. Assert on what SAM reports (`mesh.client.wait_done(task_id)` returns the task's status, answer and events), not on
   what the agent believes. Bound every wait.
4. If the scenario needs a step that `ScriptTask` lacks, add it to the interface (`driver.py`) and to `driver_link.py`.

## End-to-end tests

The end-to-end tests run each framework's claims-triage example (`examples/claims_triage_<framework>.py`) as its own
process, unchanged, on a real SAM-Go. The only stand-in is the model: a scripted OpenAI-compatible server
(`tests/e2e/model_script.py`, around `tests/hermes_fake_llm.py`) that the example reaches over HTTP through its
framework's own client. A CI shard (a quarter of the frameworks) takes about two minutes.

They find SAM-Go the way the contract tests do (`SAM_GO_BIN` or `SAM_GO_DIR`), and need the dev group (`uv sync`),
which holds every framework. A SAM or a framework that cannot be found skips the tests and says why, unless
`SAM_E2E_REQUIRED=1` or `SAM_CONTRACT_REQUIRED=1` is set: then it fails the run. That holds for Hermes too, though it
cannot join the dev group: it needs Python 3.14 and a checkout (`examples/claims_triage_hermes.py`), so its tests need
`SAM_E2E_HERMES_PYTHON` to name an interpreter that has Hermes installed (the suite puts this SDK on its path).
`ci/install_hermes.sh <commit> <dir>` makes one: a venv at `<dir>/venv`, with the checkout's own `uv.lock` installed
with hashes and the checkout installed editable. CI pins the commit in `compat/pins.json`. Hermes is not on PyPI
and needs Python 3.14, so it cannot join this project's environment; its dependencies come from the `uv.lock` of the
checkout, with their hashes, so a release published after that lock was written is never pulled in. The checkout is
installed editable because that is the only way its `setup.py` allows. The interpreter is `<dir>/venv/bin/python`.

`SAM_E2E_FRAMEWORK=<id>` (an id from `FRAMEWORKS` in `tests/e2e/conftest.py`, such as `langgraph`) runs only that
framework's tests; the compatibility workflow uses it to test one framework installed alone.

`SAM_E2E_SHARD=<n>/<of>` runs one slice of the frameworks, each slice with its own SAM-Go, so the suite can run in
parallel jobs. CI runs four (`1/4` to `4/4`), each taking about two minutes; run in parallel, they finish in the
time of the slowest. Hermes is in none of those slices: `SAM_E2E_SHARD=hermes` runs it alone, in a job of its own,
since only that job installs it. The tests that are not per framework (core behaviour, robustness, the hosted runtimes)
run in `SAM_E2E_SHARD=core`. Within a file, the tests run one framework at a time, so each framework's examples start
once per file.

### What a run does

1. Boots one SAM for the session with `tests/contract/sam_server.py`, with the agents of every framework on its link
   entrypoint, named `e2e-<framework>-<role>`. The roles are in `tests/e2e/conftest.py` (`Framework.roles`): every
   framework has `plain`, `approval`, `blocked`, `stop` and `peer`, and each adds the ones its example supports
   (`whole`, `limited`, `handled`, `native`, `nested`, `reply`).
2. For each framework, starts its examples at once, each with a scripted model of its own and an environment
   built from scratch (`PATH`, a `HOME` under pytest's temporary directory, the link settings and the `CLAIMS_*`
   settings). The approval agent asks before `route_claim` (`CLAIMS_APPROVAL_TOOLS`) and may ask the user a question.
   The blocked agent requires a scope for `route_claim` that the test user lacks (`CLAIMS_ROUTE_SCOPE`). The peer
   agent may call the plain and approval agents (its entrypoint allowlist) but lists only the plain one
   (`CLAIMS_PEER_AGENTS`). The undeclared agent's card leaves out `lookup_policy`, which its framework still offers
   (`CLAIMS_UNDECLARED_TOOL`): that tool must be refused. The stop agent has a short fraud screen and a low model call limit. The others set a
   behaviour of their own: the whole agent turns streaming off, the limited agent has a low framework loop limit and an
   input guardrail word, the handled agent handles tool errors in its tool, the native agent uses the framework's own
   approval, and the nested agent (OpenAI Agents) runs the triage agent as a tool of an intake agent (`Agent.as_tool`),
   with a low model call limit and route_claim requiring a scope the user lacks, and the reply agent is a crew of two
   whose second agent writes the answer (CrewAI). The rest require a scope the user holds.
   The examples stop with SIGTERM, then SIGKILL after 15 seconds, when the framework's last test is done.
3. Works SAM as a user would (the HTTP API, and `sam task send` for approvals), and asserts only on what SAM recorded
   about the task and on what the model received.

A turn the model answers after a tool call names the call ids whose results the request must carry. A request
without them gets an HTTP 500, and the test fails on it, so a framework that stops handing tool results back to its
model is caught. When a test fails, the report includes the end of each example's log and of SAM's log, with the link
token and the model key removed.

### What it proves

For every framework, with the example's own code, against SAM's records and what the model received:

- `tests/e2e/test_examples_on_agent_mesh.py`: the tool path. An allowed tool runs once, its step and result are in
  SAM's record and the answer reaches the user. A tool that needs a scope the user lacks never runs and the refusal is
  visible. An approved tool waits for the user and runs only after the answer; a denied one never runs and the model
  is told it was not approved. A task cancelled while its model call is in flight ends canceled. A credential in a
  tool's arguments and result reaches no SAM record. (LangGraph's prebuilt `ToolNode` is never given a tool the user
  lacks the scope for, so it answers "not a valid tool" itself and records no step; the model was not offered the
  tool. Hermes also leaves the tool out of the run, and its own dispatcher says the tool does not exist.)
- `tests/e2e/test_adapter_promises.py`: what an adapter tells SAM and the model. Parallel tool calls, the model name
  and token usage on each step (each input, output and reasoning count as the provider reported it, and the task's
  totals, for a streamed and a whole response), a usage whose keys a provider renamed (recorded as none, with one
  `token usage unread` warning in the agent's log, pinned per framework), a failing tool or model call (a tool that raises a model client's error is not reported as a model failure, and a model call that fails after a tool ran still is), the cuts on long arguments and results, the agent card's
  tools and scopes, questions to the user, a cancel while a tool runs, the model call and loop limits (`run_agent`'s `max_model_calls` in LangChain, OpenAI Agents, ADK and Hermes, and Strands' `max_tokens`, named, not `internal error`), a CrewAI task guardrail that keeps rejecting an answer, an
  input guardrail, an output guardrail whose blocked answer never reaches the user's chat, conversation memory across a
  chat, ADK's `user:` state reaching the user's next chat and not a second user's, and streaming versus whole answers (for CrewAI, that a crew of two streams only its last agent's answer, from
  text CrewAI parses in its ReAct form too; for Hermes, that the text a response writes after its tool call is not
  streamed, and that a task's tool runs while another task in the same process waits on an approval for a call with
  the same id, each task's steps paired by their own ids; the harness has one user, so the two tasks are two of its
  conversations). For OpenAI Agents, an agent run as a tool (`Agent.as_tool`): its model and
  tool calls are steps of the task between the agent tool's start and result, its answer is the tool's result, its
  model calls count toward `max_llm_calls`, and its tool the user lacks the scope for is not offered and is refused.
- `tests/e2e/test_peer_delegation.py`: a peer call goes through SAM, the peer's answer comes back as the tool result, and
  an agent the entrypoint allows but `AgentConfig.peers` does not list is not offered. For Hermes, whose peer tool is registered once per process,
  a later task's peer call still goes out as that task.
- `tests/e2e/test_core_behaviour.py`: SDK core behaviour on a SAM of its own with short gateway timers, on one
  framework: a silent task stays alive past the gateway's idle timeout, an agent stopped mid-task finishes it within
  its grace, and a task past its timeout fails saying so. A handler without a framework (`tests/e2e/robust_agent.py`)
  reports a model call as `ModelFinished(usage=Usage(...))`, and SAM records its tokens with the call, cache reads and
  writes included.
- `tests/e2e/test_robustness.py`: SDK robustness on a SAM of its own that the tests may restart, with a minimal agent
  (`tests/e2e/robust_agent.py`) that sets up what the examples cannot: a handler that ignores its cancel, the link's
  timers and a token file. An agent sent SIGTERM while a task outlasts its grace gives the task its grace, leaves the
  mesh and exits zero; one sent SIGTERM with the link down and a run that ignores its cancel still stops within the
  grace plus 8 s, and so does one whose run blocks a worker thread, as a framework's synchronous run does. An agent whose `LinkConfig.agent` names another agent than its `AgentConfig.name` exits non-zero at start with a `ValueError` naming both and never joins the mesh. An agent started with `SAM_LINK_TOKEN_FILE` reconnects with the rotated token after its link drops and
  serves another task; one whose token SAM no longer accepts stops after `max_auth_refusals` with `LinkAuthRefused`
  and a failing exit. A task whose run ignores its cancel at the task timeout is answered failed with the limit's
  message within the cancel deadline, and the next task gets the freed slot. A handler that swallows the cancel and
  returns is still answered failed with the limit's message, one that returns `None` fails the task, and a
  `ModelFailed` step followed by an error gives SAM's error its category.
- `tests/e2e/test_link_security.py`: where the agent sends its link token, shown by the minimal agent as its own
  process against a plain HTTP server standing where SAM would be (SAM-Go never redirects the link and runs on the
  loopback here). An address that redirects to another port never gets the token there, and the agent keeps trying its
  own; a plain `ws://` address off this machine is refused at start-up with a failing exit, and with
  `SAM_LINK_ALLOW_INSECURE=1` the agent connects and warns once that its token is not encrypted. The same server
  answers 404, a TLS handshake and 401 to show that a wrong path and `wss://` to plain HTTP exit within seconds naming
  the fix, that a refused token lists what to check, that setting both token variables or no URL is refused with a
  message, and (503) that the agent keeps trying and says so once a minute.
- `tests/e2e/test_hosted_runtimes.py`: the request-driven runtime wrappers on a SAM of their own. A minimal agent
  (`tests/e2e/hosted_agent.py`) runs behind `sam_link_sdk.foundry` and `sam_link_sdk.agentcore` inside each runtime's own
  server (Foundry's invocations host, AgentCore's app), as its own process, and SAM wakes it through its `wake` URL.
  For both runtimes: an agent announced with `main.py announce` is listed on-demand, and the agent built for a wake
  serves the task; a wake whose link token is refused fails to SAM (SAM logs the wake refused with HTTP 500), the task
  waits, and once the token file holds the right token the next wake serves both tasks; a runtime without
  `SAM_LINK_URL` refuses the wake (Foundry 400, AgentCore 500) without building an agent; a wake that outlasts SAM's
  call to the runtime (the wake's `timeout_seconds`) still serves its task, with no second wake; and a task cancelled
  during a wake ends canceled while the wake answers the runtime once its queue is quiet.
- `tests/e2e/test_load.py`: one agent process under load, on a SAM of its own, with a minimal agent
  (`tests/e2e/load_agent.py`, max_concurrent_tasks 8). A round sends 200 tasks at once (`SAM_LOAD_TASKS`): answers,
  tool calls, streamed answers, failures, and tasks the test cancels while they run. Each task carries a token of its
  own that its answer and steps echo. Over ten rounds (`SAM_LOAD_ROUNDS`): every task ends in SAM in the state its
  kind calls for, a completed one with its own answer, each with one final answer, and no task's record holds another
  task's token; no handler runs twice and never more than 8 at once; the agent's in-flight count (what it reports as
  `sam.agent.active_executions`) is back to 0 after each round; and from the end of the second round to the end of the
  last, the agent process holds at most 500 more objects the garbage collector tracks and 1,000 more allocated memory
  blocks (which count strings too; the agent reports both), and, measured from outside with psutil, 10 MB more RSS, 3
  more open files and sockets and 2 more threads. It prints each round's tasks per second and those measures. It
  takes about 30 s. Its soak test (the `soak` marker) runs the same rounds for `SAM_SOAK_SECONDS` (60 by
  default) and restarts SAM once half of a round has ended, every `SAM_SOAK_RESTART_SECONDS` (30), so the link drops
  with tasks in flight; it prints the same per round, with the growth since the round before, and writes the report to
  `SAM_SOAK_REPORT` when set. A restarted SAM has forgotten the tasks that were open: it refuses the agent's answers to
  them, and its gateway's idle timer (short on this SAM) fails them; a task whose answer SAM recorded just before it
  stopped is ended once more by that timer, so its record holds two finals. The soak counts those as lost or ended
  again by SAM; every other check holds across the restart. It measures growth from the round after the first
  restart, as the agent's first reconnect allocates about 700 objects once.

The OpenTelemetry example (`examples/claims_triage_otel.py`) runs the scenarios its agents can: Spans record a step
after it happened, so it cannot hold a tool for approval, refuse one for want of a scope, or stop a run.

### What it doesn't prove

- Where the token goes with SAM itself at the other end. `test_link_security.py` stands a plain HTTP server in
  for SAM: SAM-Go cannot be made to redirect, and the off-machine address it uses (TEST-NET-1) answers nothing, so the
  opt-in case shows the warning and the attempt, not a served task over plain `ws://`.
- A second user with other roles. The second user (`X-SAM-User-ID`, honoured with login off; `SamClient(user=...)`,
  the e2e `second_user` fixture) has the same roles, so "a user who lacks the scope" is the same user with an agent
  that requires a scope it lacks. Nor that a user who knows another user's chat id does not reach that chat's history
  in the agent: SAM refuses the message first (`session not found`). The unit tests cover the SDK's keying by user
  (`ctx.session_id`, `tests/test_tool_scopes.py`).
- Who redacted a credential. SAM-Go redacts step events too, with the same `<REDACTED>` marker, so the last scenario
  passes with the SDK's redaction turned off. The unit tests cover the SDK's own (`tests/test_redact.py`).
- A second wake of one request-driven agent. A request that reaches the agent's queue after a wake's connection has
  left is given back to the queue by the link, and the dev broker cannot redeliver it, so the task is lost. For the
  same reason, a runtime that cancels the invocation when SAM stops waiting is not tried: the local runtime servers
  never cancel a handler. The unit tests cover AgentCore's side (`tests/test_invocation.py`). SAM stopping the agent
  during a wake needs the agent stop API, which the pinned SAM-Go lacks, and a doorbell with a malformed body cannot
  come from SAM, which always sends `{}`.
- That a task abandoned past `shutdown_grace_seconds` is delivered again. The embedded SAM's dev broker turns a
  nack into a no-op ("does not support redelivery"), so the task is never redelivered and ends on the gateway's idle
  timer. The unit tests (`tests/test_shutdown.py`) cover the SDK's side.
- That every renamed usage field is noticed. The scripted model renames `prompt_tokens` and `completion_tokens`; ADK
  (through LiteLLM) and Hermes turn that into a usage of zeros, which the adapter cannot tell from a provider that
  reports none, so their agents record no usage and do not warn, and Strands fails the model call. The test pins each.
  A renamed field inside a typed framework structure fails the drift check instead (`tests/test_framework_drift.py`).
- That the web UI shows the guardrail's failure and no blocked text. The output guardrail scenario checks what SAM
  stored (the task's status, its error and its text updates), not the chat as the browser draws it.
- An OpenAI Agents agent tool behind `with_scopes` or `with_approval`, one with hooks or a run config of its own, or an
  agent tool's agent that hands off or holds a tool for the SDK's own approval: the example's agent tool is a plain one.
  The unit tests cover them (`tests/test_unwrap.py`, `tests/test_adapter_conformance.py`).
- SAM stopping an example agent: the SAM build under test has no agent stop API, so neither the Hermes kill switch
  guard (`stop_guard`) nor a peer call SAM refuses because its peer is stopped is tried. `tests/test_hermes.py` drives
  the guard with a stand-in for the agent's kill switch.
- Hermes streaming text a model writes before its tool call in the same response: the adapter sends it, since nothing
  tells it from the answer until the call arrives, and no scenario scripts it.
- That a task open when SAM restarts is answered. SAM forgets it (`test_load.py`'s soak counts it as lost), and an
  answer the agent sends while SAM shuts down, before the link drops, is lost without a word: the link has no
  acknowledgement of a result. The agent cannot tell, and the task ends on SAM's idle timer. A restarted SAM may also
  end a task it already recorded as answered, giving it two finals.
- Load on more than one agent process, on a framework adapter (the load agent is scripted), or on a SAM with Postgres
  and a real broker. The load test's RSS bound catches only a large leak, as macOS moves a process's RSS by several MB
  either way; the object and block counts are the precise ones. A leak outside the Python heap (a C extension's own memory) shows only in RSS.
- Real models, other topologies, and everything listed for the contract tests.
- What CrewAI streams when a model writes text before a tool call in the same response (the scripted turns are text or
  tool calls): that text reaches the chat, as docs/frameworks/crewai.md says. Nor that a CrewAI last task with a
  guardrail or a structured output is sent whole, or what a hierarchical crew streams: the example crews have neither.

## CI

The SDK lives in the Solace Agent Mesh repository, and its contract and end-to-end tests run against the SAM built
from the same tree, so a change to the agent link and a change to the SDK are tested together.
`.github/workflows/python-link-sdk-ci.yml` at the repository root runs every job on main nightly, and on a pull
request only the jobs its files can break, so a pull request outside the SDK and the agent link runs none:

| A pull request that changes | Runs |
|---|---|
| any file under this directory | `lint`, `typecheck`, `unit` on Python 3.11 and 3.14 |
| the SDK's source, examples, contract or end-to-end tests (anything but docs, Markdown, the unit test files and `ci/allowed-skips.txt`), or `internal/entrypoint/agentlink/` | `build-sam`, `contract`, `e2e` |
| `src/`, `pyproject.toml` or `uv.lock` | `install`, `package` |
| `pyproject.toml` or `uv.lock` | `floor` |
| the workflow itself | everything, as nightly |

The jobs:

- ruff and the format check (`lint`) and mypy on `src/sam_link_sdk` on Python 3.11 (`typecheck`)
- the unit tests, then the skip guard (below); 3.14 also measures coverage against the floor in
  `[tool.coverage.report]`. Nightly they also run on 3.12 and 3.13
- the unit tests at the lowest supported versions (`floor`): every direct dependency resolved to the lowest release its
  bound in `pyproject.toml` allows (`uv lock --resolution lowest-direct`), so code that needs a newer framework than the
  bound says fails. Nightly it also catches code that outgrew a bound without touching the dependencies
- `install`: a plain `sam-link-sdk` install brings only `sam-link-sdk` and `websockets`, `sam_link_sdk`, its agent link
  and its CLI import with no extras, and `sam-link-sdk --help` runs; then each extra alone in a clean environment, with
  an import of what it is for
- a build of the wheel and the sdist, an install of the wheel alone into a fresh environment, and, from outside the
  repository, an import of `sam_link_sdk` and `sam_link_sdk.link`, `sam-link-sdk --help`, and a check that the wheel
  ships `sam_link_sdk/py.typed` and no `tests/` (`package`)
- `build-sam`: `go build -tags nosolace ./cmd/sam` from the same commit, handed on as an artifact
- the contract tests against that binary (`SAM_GO_BIN`, `SAM_CONTRACT_REQUIRED`)
- the end-to-end tests in four framework shards, `core` for the tests that are not per framework (the short load run
  among them), and `hermes`, which installs Hermes at the commit in `compat/pins.json` into its own Python 3.14 venv
  (`ci/install_hermes.sh`) and also runs `tests/test_hermes.py` with that interpreter
- `floor-cells`, when the SDK's code or dependencies change: each framework installed alone, every direct dependency at
  its lowest allowed release, on Python 3.11, running its unit tests and its end-to-end tests
  (`.github/actions/link-sdk-compat-cell`, the cells in `compat/frameworks.json`)
- `sam-releases`, likewise: the contract tests against the oldest SAM release the SDK supports
  (`sam_oldest_supported` in `compat/pins.json`) and the newest SAM release, each built from its tag and cached
- `released-contract`: the contract tests of the newest SDK release on PyPI, from its `sam-link-sdk-v<version>` tag,
  with that release installed from PyPI, against the SAM of the pull request. It runs with the contract tests, which
  includes a pull request that changes SAM code a link agent depends on (the `runtime` paths in `changes`), so a SAM
  change cannot break the SDK users have installed
- `frozen-files`, on every pull request: one the maintenance bot (`sam-automation-app[bot]`) opened or committed to
  fails when it changes a file in `ci/frozen-paths.txt` or the `[tool]` settings of `pyproject.toml`
  (`ci/check_frozen.py`)
- `Link SDK CI`, which fails when a job above failed or was cancelled, and passes when each one succeeded or was skipped
  because the pull request cannot break it. It is the one check of this workflow branch protection should require
- with the Monday run, or one started by hand, the soak test (`soak`): one agent under load for ten minutes while SAM
  restarts every 30 s, with its report in the job summary. Ten minutes would double the slowest pull request job, so
  it is not part of `Link SDK CI`

Nothing runs on a push to main. The nightly run tests main as a whole, so a change elsewhere in SAM that breaks the
link shows the next morning even when no pull request touched these paths. A newer push to a pull request cancels its
older run.

To run the contract and end-to-end tests locally against the SAM of your checkout, from this directory:

```sh
SAM_GO_DIR=../.. uv run --group contract pytest -m contract
SAM_GO_DIR=../.. uv run pytest -m e2e tests/e2e
```

### The skip guard

Most framework tests begin with `pytest.importorskip`, so a framework that stops importing would skip its tests and
leave the run green with fewer tests. After the unit tests, `ci/check_skips.py` reads pytest's JUnit report and fails
the job on any skip that `ci/allowed-skips.txt` does not list, and on an adapter test module (`ADAPTER_MODULES` in the
script) in which no test passed. Each allowlist entry is a test id prefix and text from the skip reason, under a
comment that says why. Today it lists the Hermes unit tests (the unit job does not install Hermes; the Hermes e2e job runs them) and one
CrewAI case that cannot exist. Each compatibility cell runs it with `--cell <name>`, which also allows a skip for a
module that cell does not install (below), and the contract
and e2e jobs run it with `ci/allowed-skips-sam.txt` and `--any-modules`, since they hold no adapter test modules. To
run it locally:

```sh
uv run pytest -q --junitxml=/tmp/junit.xml && python3 ci/check_skips.py /tmp/junit.xml ci/allowed-skips.txt
```

### New framework releases

`.github/workflows/python-link-sdk-compat.yml` runs every day: each framework that published a release in the last
seven days, before Dependabot proposes it, installed alone at that release, unit and end to end. It is not part of
`Link SDK CI`: a new framework release never blocks a pull request. [framework-releases.md](framework-releases.md)
describes it.
