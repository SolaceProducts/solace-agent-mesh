---
name: sam-troubleshoot
description: Use when something in Solace Agent Mesh is broken or misbehaving and you need to diagnose it — an agent won't start or won't respond, the entrypoint returns 500s, a deploy times out, tasks hang or produce no output, a tool/connector call fails, broker or LLM connectivity is suspect, or you need to reconstruct exactly what happened during a specific task (the task event log / STIM file). Covers logs, traceID correlation, the sam-doctor pre-flight checks, health endpoints, and per-task event logs. Not for authoring agents/tools/entrypoints/connectors (those skills), nor for operator configuration like SSO/RBAC/secrets/upgrades (sam-operate).
---

# sam-troubleshoot

The diagnosis front door for Solace Agent Mesh. When a user reports a **symptom** — "my agent won't respond", "entrypoint 500s", "deploy times out", "a tool failed silently" — work the evidence, don't guess. This is a Go runtime: there is **no `pip`, no community/enterprise edition split, no `sam plugin`, no Python `solace_agent_mesh` package**. Diagnosis is logs + `traceID` correlation + `sam-doctor` + health endpoints + the per-task event log.

## First move: always anchor on `traceID`

Agent Mesh mints one immutable `traceID` (UUIDv7) per user task at the entrypoint, carries it on every broker hop (entrypoint → agent → STR → tools, and across peer delegation), and attaches it to every structured log line. **This is the backbone of every diagnosis.**

1. Turn on JSON logs first so fields are queryable: set `LOG_FORMAT=json` on every component.
2. Grab the failing task's `traceID` from the entrypoint log (or the UI), then follow it everywhere. Match the spelling to the format you turned on in step 1: JSON logs carry it as a field, so filter `jq 'select(.traceID=="<uuid>")'` (or `grep '"traceID":"<uuid>"'`); only text/logfmt output spells it `traceID=<uuid>`. In Datadog Logs use `@traceID:<uuid>`.
3. Identifier slog fields are camelCase: `traceID`, `taskID`, `agentName`, `corrID`/`reqID`, `contextID`, `userID`, `publisherID`. Metric fields are snake_case (`duration_ms`). (`eventSeq`/`publisherId` are A2A wire user-property keys, not log keys — the gap-detector logs the sequence as `lastSeq`/`currentSeq`/`gap`.)

**Silent-failure tell:** if the logs show `event sequence: gap detected on per-publisher stream` (logged at ERROR) or `... duplicate or out-of-order event` (logged at WARN), events were dropped or split mid-stream — usually a broker queue/subscription misconfiguration (e.g. two components sharing a subscription, or duplicate `agent_name`/entrypoint id across pods), not a tool bug. Investigate queue config before the tool.

## The symptom → look-here map

Route by what the user reports. Field detail and commands: [references/diagnose.md](references/diagnose.md).

| Symptom | Most likely causes | First look |
|---|---|---|
| Agent/entrypoint won't start (exits, crashloops) | YAML parse error, missing required key, broker/DB/LLM unreachable at startup, port already bound | startup stderr (`kubectl logs --previous` on crashloop); run `sam-doctor` |
| Agent deployed but won't respond; task hangs | broker connectivity, topic-namespace mismatch between entrypoint and agent, agent never discovered, LLM auth/egress | follow `traceID` from the entrypoint; check broker connect + agent-card discovery |
| Entrypoint returns 500 | downstream error mid-request; persistence write failure | grep the response `errorId` in logs, then its `traceID` for the causal chain |
| Deploy times out | broker not reachable, agent not registering its card, image/secret issues | `sam-doctor`; `kubectl describe pod`; broker connect logs |
| Tool / connector / MCP call fails | STR sandbox error, MCP/OpenAPI auth (401/403), an "SSRF protection" refusal of a private address (the Platform service blocks it unless `SAM_PLATFORM_ALLOW_PRIVATE_MCP` is `true`, a process-wide setting to recommend only when connector users are trusted), dial timeout, tool timeout, toolset stuck `pending`/`failed` | the per-task event log (STIM) + the tool's audit log line; check toolset `discoveryStatus`; for an "SSRF protection" refusal, the Platform service log (link-local and metadata addresses stay blocked even with the opt-out) |
| Requests refused: "…usage budget is used up until <date> UTC…", webhook `429` with `Retry-After` | a spent Agent Mesh budget ("this deployment's usage budget") or user budget ("your usage budget", or a named service account's); "usage budgets can't be checked at the moment" is not a spent budget but an Entrypoint Executor still waiting for the budget state after a restart or month rollover | `sam-operate`'s budgets reference; the **Budgets** page and the user's **Budget** column on **User Management** |
| "Some events are missing" / partial output | per-publisher stream gap (see above) | grep `event sequence` |

## Run `sam-doctor` for connectivity/config preflight

`sam-doctor` validates the environment **before** (or independent of) a running Agent Mesh: broker connectivity + auth, LLM endpoint + key, database reachability + auth, object storage, TLS certificates, OIDC discovery, and (local/wheel only) runtime version + port availability. Run it as a CLI (`sam doctor`) or it runs automatically as the Helm pre-install/pre-upgrade hook. `SAM_DOCTOR_CONTEXT=helm|wheel|local` selects the check set; **unset defaults to `local`** and still runs (a misspelled value is a hard error). Skip individual checks with `SAM_DOCTOR_SKIP_CHECKS=<names>`. Full check list + invocation: [references/diagnose.md](references/diagnose.md).

## Reconstruct a specific task: the task event log (STIM)

When the question is "what exactly did this task do — every LLM call, tool call, artifact op, and where it went wrong", that's the **task event log** (STIM file). It must be enabled, then downloaded per task, then read. Full how-to: [references/task-event-logs.md](references/task-event-logs.md). In short: requires a SQL `session_service` **and** `task_logging.enabled: true` on the entrypoint; download per task via `GET /api/v1/tasks/{taskId}` (a `.stim` file) or it auto-saves under the task output dir on `sam task send`; read the `.stim` YAML directly.

## Hard rules (each counters an observed failure)

- **Never invent diagnostics.** Baselines fabricated kubectl label selectors, health-endpoint paths/ports, env-var names, and doc URLs. Don't. The verified names live in the references; if a name isn't there, say you'd confirm it rather than emit a plausible-looking guess a customer will paste and fail on.
- **`sam-doctor` is the purpose-built preflight — lead with it** for connectivity/config symptoms instead of generic `kubectl`/`curl` flailing.
- **Distinguish the two health surfaces.** The entrypoint's request-path `/health` is a shallow "HTTP listener is up" check (always cheap, not diagnostic). The dedicated workload **health server** exposes `/health` (component health, 503 when unhealthy) and `/ready` (readiness) on a separate port. Point Kubernetes probes at the workload health server, not the shallow one. Ports + override flag: [references/diagnose.md](references/diagnose.md).
- **traceID before tools.** A "tool failed silently" is often a dropped event (`event sequence` gap), not a tool error — confirm the stream is intact before blaming the tool.
- **Deployed-environment logs: filter, don't page.** On a cluster, query your log aggregator on the `traceID` field (every component emits it as a structured field) and scope by namespace and service, rather than paging through raw pod logs one at a time.
- **Distinct from operator config.** "How do I turn on SSO / configure RBAC / rotate a secret / upgrade" is `sam-operate`, not troubleshooting. This skill diagnoses; it doesn't configure.

## References

| Topic | File |
|---|---|
| traceID workflow, slog fields, sam-doctor checks, health endpoints/ports, failure-mode table, per-mode log access | [references/diagnose.md](references/diagnose.md) |
| Task event logs (STIM): enable, locate, download, read, event schema | [references/task-event-logs.md](references/task-event-logs.md) |
