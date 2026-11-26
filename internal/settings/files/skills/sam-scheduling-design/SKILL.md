---
name: sam-scheduling-design
description: Design guidance for Solace Agent Mesh Scheduled Tasks — when to attach the scheduling_tools group to a builder agent versus pointing the user at the no-code Scheduled Tasks page.
---

Design guidance for time-based triggering in Solace Agent Mesh. Scheduled Tasks are SAM's own scheduler — not an OS cron job — that fires a one-time, cron, or interval invocation of an agent or workflow with a given message.

For **tool schema details** see `sam-tool-schema` (the `scheduling_tools` builtin-group entry).

## What Is a Scheduled Task?

A Scheduled Task is a durable row the platform's scheduler engine fires on a `one_time` timestamp, a `cron` expression, or a fixed `interval`. When it fires, it delivers a message to a target **agent or workflow** as a fresh task, under the identity that created the schedule. The target must already be a registered component at fire time — self-targeting (an agent scheduling itself) is the common case and always works once the agent is deployed.

There are exactly **two ways** a Scheduled Task gets created — the Builder only participates in the first:

1. **The `scheduling_tools` builtin-group tool**, attached to an agent's `tools:` list. Once deployed, the agent can create, list, update, delete, and enable/disable its own scheduled tasks conversationally, in response to what the user asks in chat (e.g. "remind me every weekday at 9am"). **This is what the Builder can configure.**
2. **The platform's Scheduled Tasks page** — a separate, no-code UI where a user schedules an *already-deployed* agent or workflow directly, without that component needing any special tools. **The Builder does not create this** — it is configured after deployment, outside the Builder conversation.

The Builder's only lever is #1: deciding whether the agent it is generating should carry the `scheduling_tools` group. There is no `type: scheduled_task` build-manifest component and no separate config artifact — it is just an entry in the agent's `tools:` list, same as any other builtin-group.

## When to Attach `scheduling_tools`

Attach the group when the user wants the **agent itself** to set up, change, or manage recurrence conversationally — the schedule isn't fixed once at build time, it's something the user (or the agent, autonomously) creates or adjusts during normal use:

- "Remind me every day at 9am to..."
- "Check on this again in an hour" / "follow up if I haven't replied by tomorrow"
- "Escalate if the ticket hasn't been resolved in 2 hours"
- "Run this report every Monday morning" (when the report *agent* should own scheduling itself, e.g. because the user might later ask it to change the cadence in chat)
- The user answers the Phase 1 "who or what kicks this off" question with "a schedule" **and** also wants ongoing conversational control over that schedule (create/pause/reschedule/cancel by asking the agent)

**Do not** attach it, and instead tell the user to use the platform's Scheduled Tasks page once the build is deployed, when:

- The schedule is fixed and administrative — "run this every night at 2am, nobody needs to change it in chat." A tool the agent never has a reason to call just adds unused capability surface.
- The user is really asking about a **reactive** trigger, not a time-based one — see "Scheduling vs. Event Mesh Entrypoint" below.

When in doubt, ask: "once this is running, should people be able to set or change the schedule just by asking the agent, or is a fixed schedule set up once enough?" The first answer means attach the tool; the second means point to the Scheduled Tasks page.

## Scheduling vs. Event Mesh Entrypoint

Both answer "what kicks this off automatically," but for different triggers:

- **Time** ("every day at 9am", "every 15 minutes", "on this exact date") → Scheduled Task. Use `scheduling_tools` when the agent should manage its own recurrence; otherwise point to the Scheduled Tasks page.
- **An external event** ("when a broker message arrives", "react to an incoming order", "listen for IoT data") → an **Event Mesh Entrypoint**, not scheduling. Load `sam-event-mesh-design`.

Don't conflate a periodic poll of an external system ("check this API every hour") with an event mesh entrypoint — that's still time-based triggering (`scheduling_tools`, with an `interval` schedule), because nothing external is pushing the event; the agent is initiating the check on its own timer.

## Adding the Tool

In the agent config's `tools:` list, alongside any other builtin groups:

```yaml
tools:
  - tool_type: builtin-group
    group_name: scheduling_tools
```

This grants the full lifecycle — `schedule_task`, `list_scheduled_tasks`, `update_scheduled_task`, `delete_scheduled_task`, `set_scheduled_task_enabled` — not just creation. The agent can review, change, pause, or delete tasks it created, not only make new ones.

## Instruction Guidance for a Self-Scheduling Agent

When an agent carries `scheduling_tools`, its instruction should say plainly when to use it — the LLM won't infer scheduling intent from generic instructions alone. For example:

```
When the user asks you to do something on a recurring basis, or to follow up
or remind them later, use schedule_task to set it up rather than trying to
remember it yourself. Use list_scheduled_tasks if they ask what's currently
scheduled, and update_scheduled_task / delete_scheduled_task /
set_scheduled_task_enabled to change, cancel, or pause an existing one.
```

## Target Naming and Create-Time Validation

For an `agent` target, `target_name` is checked against the live agent registry when the schedule is **created**, not deferred to when it fires — an unregistered agent name is rejected up front. (A `workflow` target skips this check entirely and is not validated up front.) In practice: an agent scheduling **itself** is always safe once it has been deployed and received at least one task. Scheduling a **different** sibling component from the same build only succeeds once that sibling is actually deployed and registered — being listed in the same manifest is not enough by itself.

The `schedule_task` tool normally surfaces a rejected target name back to the calling agent as a real tool error (it waits up to 15 seconds for the entrypoint to accept or reject the request), so a bad target name in the common case is not a silent failure — the agent finds out and can tell the user. The one case worth flagging in a generated agent's instruction: if the tool ever reports it "could not confirm" whether a schedule was created, that's a genuine ambiguous outcome (a broker/timeout edge case, not a rejection) — the agent should tell the user to check their scheduled tasks rather than assuming success or failure.

## Build Manifest

No manifest changes are needed for scheduling — it's not a component type. If it's useful for UI display, list `schedule_task` (or the other scheduling tool names) in the agent's `tools:` array in the manifest entry, the same way other notable tools are listed. Use `filename:`, not `artifact:`, for the config artifact reference — `artifact:` is not a recognized manifest field and is silently dropped, which then fails validation on the *missing* `filename` instead:

```yaml
components:
  - name: DailyStandupBot
    type: agent
    filename: daily_standup_bot.yaml
    tools:
      - schedule_task
```

## Prerequisites

Two runtime conditions determine whether `scheduling_tools` actually works once deployed, worth knowing when troubleshooting or setting expectations:

- The platform's Web UI (HTTP SSE) entrypoint must have a database configured — the scheduled-task service only initializes when one is available. This is true of most deployments already; it isn't something the Builder configures.
- `interval` schedules have a 60-second minimum. A request like "check every 30 seconds" isn't achievable via a schedule; suggest an interval of 60 seconds or more, or reconsider whether it truly needs to be that frequent.
