---
name: sam-scheduling
description: Create, list, change, pause, resume or delete scheduled tasks that run an agent or workflow later, once or on a cron or interval schedule. Use for reminders, follow-ups, escalations and recurring work.
tags:
  - scheduling
  - timers
  - cron
  - automation
  - reminders
tools:
  - tool_type: builtin-group
    group_name: scheduling_tools
---

# Scheduling

A scheduled task runs a **workflow or agent** later, once or on a schedule. It fires as a fresh task under the user's identity, so it can do only what they are allowed to do, and it does not block the current turn.

| Tool | Use it to |
|---|---|
| `schedule_task` | Create a task. |
| `list_scheduled_tasks` | List the user's tasks and their **ids**. |
| `update_scheduled_task` | Change a task's schedule, message, name, description or timezone. |
| `set_scheduled_task_enabled` | Pause (`enabled=false`) or resume (`enabled=true`) a task. Use this, not `update_scheduled_task`, to turn a task on or off. |
| `delete_scheduled_task` | Remove a task permanently. |

The last three take a `task_id`. When the user names a task ("my market summary"), call `list_scheduled_tasks` to find its id. Never guess an id.

## `schedule_task`

- `target_name` (required): the agent or workflow to run.
- `target_type`: `agent` (default) or `workflow`.
- `schedule_type` (required) and `schedule` (required):

  | `schedule_type` | `schedule` | Example | Fires |
  |---|---|---|---|
  | `one_time` | ISO 8601 timestamp | `2026-12-31T15:30:00Z` | once; a past time is skipped |
  | `cron` | 5-field cron | `0 9 * * 1-5` | on each match |
  | `interval` | duration, one unit (`15m`, `36h`, `7d`; not `1d12h`), at least 60s | `24h` | right away, then every interval |

- `message` (required): the instruction the target gets when it fires.
- `name`: optional label, generated if left out.
- `timezone`: IANA zone for `cron` and `one_time` (e.g. `America/New_York`), UTC by default. `interval` counts from creation.
- `timeout_seconds`: limit on the fired run, 3600 by default.

```
schedule_task(target_name="market-summary", target_type="workflow",
  schedule_type="cron", schedule="0 9 * * 1-5", timezone="America/New_York",
  message="Generate today's market summary and post it to #markets.")
```

## Confirm in the conversation

- Before creating or updating, restate the schedule in one plain sentence ("every weekday at 9am Eastern, MarketAgent posts a summary") so the user can catch a wrong time or target.
- Never invent details the user didn't give. If they say "daily" with no time, ask, or pick a default and say so.
- Before deleting, always confirm, and offer pausing as the reversible option.
- One call is enough. Don't repeat a schedule call to make sure.

Don't use scheduling to wait inside the current task, for timing under a minute, or instead of answering now.

## Where results go

Web: the user's inbox and chat. Slack and Teams: sent to the user there, if they have messaged the bot on that channel before. WhatsApp can't deliver later results, so tell a WhatsApp user the run works but the results will be in the web inbox.
