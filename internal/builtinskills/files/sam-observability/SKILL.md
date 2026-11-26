---
name: sam-observability
description: Reference for interpreting Solace Agent Mesh observability data — the task/agent/execution data model, what each metric means, how to read health, common failure modes, and Datadog query patterns. Load when you need to interpret a metric or judge whether a value is healthy.
tags:
  - observability
  - admin
  - sam
---

# Solace Agent Mesh Observability Reference

This skill is the reference the admin observability agent uses to interpret the
data its tools return. It explains the data model, what the numbers mean, how to
judge health, and how to read failures.

## The data model

- **Task** — a unit of work submitted into the mesh (via an entrypoint or a
  scheduled task). Has an id, owning `userId`, `status`, `startDate`/`endDate`,
  token usage, and an `executionMode` (`foreground` or `background`). A task's
  `kind` is normally `task`; rows with `kind=compaction-cost` are internal
  accounting and are excluded from the health/usage reports.
- **Status** — terminal: `completed` (success), `failed` / `error` / `timeout`
  (counted as failures), `cancelled` (user-initiated stop, NOT a failure).
  Non-terminal: `running`, `pending`. A NULL status shows as `unknown`.
- **Agent** — a deployed worker addressable over A2A. A task is routed to a
  target agent via a `…/agent/request/<agentName>` broker topic; that topic is
  how per-agent attribution is derived (best-effort).
- **Gateway** — the ingress that submits tasks and owns the operational store
  (tasks, task events, feedback, scheduled tasks).
- **Scheduled task** — a cron/interval definition that fires executions against
  a target agent; each run is a `ScheduledTaskExecution` with its own status.
- **Feedback** — a per-task user rating (e.g. `up`/`down`) with an optional
  comment.

A single task threads one `traceID` through every hop — use
`correlate_task_with_logs` to pull exactly the log lines emitted during a task.

## What the numbers mean

- **successRate / errorRate** — fractions of all tasks in the window.
  successRate = `completed / total`; errorRate = `(failed+error+timeout)/total`.
  Always state the denominator and window — a 50% error rate over 4 tasks is
  noise; over 4,000 it is an incident.
- **avgDurationMs / p50 / p95** — computed over *completed* tasks only (tasks
  with an end time). p95 is the latency the slowest 5% experience; watch it more
  than the average, which hides tails.
- **activeBackgroundCount** — background tasks still running (no end time),
  independent of the window. A steadily climbing value suggests stuck tasks.
- **Token usage** — input/output/cached token sums per day or per user. This is
  usage, not cost. **Cost is not in the task store** — it lives in the OTel
  `gen_ai.cost.total` metric (Prometheus / Datadog). For spend, report tokens
  and point the operator at the metrics dashboards rather than estimating.
- **Agent performance** — invocationCount, errorCount, successRate, avg/p95 per
  agent. Attribution is best-effort from routing events; tool-level metrics are
  not here (they live in the OTel `operation.duration` metric).
- **Scheduled-task health** — enabled vs disabled counts, execution outcomes by
  status, and currently-failing tasks (non-zero `consecutiveFailureCount`) with
  their last error. A rising consecutive-failure count is the early warning.

## How to read health

Scan with the status emoji and these rough bands (tune to the deployment's
baseline — compare a window against the prior equivalent period):

- ✅ healthy — errorRate near baseline, p95 within normal range, no climbing
  background count, no scheduled tasks failing repeatedly.
- ⚠️ degraded — errorRate or p95 elevated vs baseline, a few new failures, a
  scheduled task with 1–2 consecutive failures.
- ❌ failing — a specific agent/task class erroring, or a scheduled task failing
  every run.
- 🚨 critical — broad error spike, p95 multiples of baseline, or many tasks
  stuck (activeBackgroundCount climbing without completion).

## Common failure modes

| Mode | How it shows up | How to confirm |
|---|---|---|
| LLM/provider error (rate limit, timeout) | `error`/`timeout` status, bursty | Datadog logs for the agent around the task time; provider 429/5xx |
| Tool error | task `error` after a tool step | `correlate_task_with_logs` on the traceID; look for the tool's error line |
| Config error | consistent failure for one agent right after a deploy | per-agent errorRate spike from a point in time; deploy correlation |
| A2A delegation failure | orchestrator/coordinator tasks fail mid-plan | logs showing a peer request with no response |
| User/input error | isolated failures, varied agents | request text shows malformed/unsupported input |
| Transient infra | brief spike that recovers on its own | error window bounded; no code/deploy change |

## Datadog query patterns

- The Datadog tools automatically scope queries to this deployment's
  `cluster_name` and `kube_namespace` — you only supply the query body.
- Error sweep: `datadog_get_errors` (optionally `service: gateway` / `awe`).
- Task correlation: `correlate_task_with_logs` with the task's `traceID` (it
  builds `@traceID:<id>`). Widen the window for older tasks.
- Free-form: `datadog_query_logs` with a Datadog query like
  `status:error service:awe @agentName:search-agent`.
- Cross-reference the `using-datadog` knowledge for the deployment's exact
  service/namespace names and how slog fields are parsed.
