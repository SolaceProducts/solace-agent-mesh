---
name: sam-scheduled-task
description: IMPORTANT - Load this skill whenever the user wants to create, view, modify, or delete a scheduled task (anything that should run on a schedule, recur, or be triggered later). Provides a redirect button that takes the user to the Scheduled Tasks page, where they can review a preview and make manual edits before saving.
tags:
  - scheduled-tasks
  - redirect
tools:
  - tool_type: builtin
    tool_name: scheduled_task_redirect
---

# Redirect to Scheduled Tasks

You now have access to the `scheduled_task_redirect` tool, which sends the user to the **Scheduled Tasks** page. Scheduled tasks are Solace Agent Mesh tasks that run automatically on a schedule (cron, interval, or one-time) — for example, *"send me a market summary every Monday at 9am"*.

The destination page renders a **preview panel** (next run times, agent target, message body) and lets the user manually edit any field before saving. Because the user always reviews and confirms on the destination page, you do NOT need to gather every detail or ask for confirmation in chat — your job is to recognize the intent and redirect promptly.

## Modes

The tool's `mode` argument picks the destination experience:

| Mode | When to use | Required args |
|------|-------------|---------------|
| `create` | User wants to schedule something new | `context` |
| `view` | User wants to see what's currently scheduled | `context` |
| `edit` | User wants to change an existing task | `context`, `task_id` |
| `delete` | User wants to remove an existing task | `context`, `task_id` |

For `edit` and `delete`, `task_id` is the existing scheduled task's ID. If you don't know it (the user named the task informally and there's no `task_id` in conversation), use `mode=view` so the user can pick the task themselves on the destination page — do NOT guess.

## How to Use This Tool

**Act quickly.** Write a brief preview message (described below), then call `scheduled_task_redirect` immediately. No multi-turn back-and-forth, no clarifying questions.

1. Recognize scheduling intent: words like *every Monday, every morning, weekly, daily, at 9am, on the 1st, recurring, schedule, remind me, run automatically, in two hours, tomorrow at...* — anything that says "run this later or on a schedule".
2. Pick the mode (create / view / edit / delete).
3. **Write the preview message** (see "Preview Message Pattern" below) — this prepares the user for the destination page.
4. Call the tool with:
   - **`context`** (required): a one-to-two sentence summary of what the user wants. Echo only what the user actually said — do NOT invent times, days, frequencies, or other scheduling details they did not provide. For `create` this becomes the prompt pre-filled into the destination AI builder (*"Schedule a market summary report. The user mentioned Monday mornings."*). For `edit` describe the change (*"Move the run time from Monday to Tuesday."*). For `delete` name the task. For `view` a short framing line is enough.
   - **`mode`** (required): one of `create`, `view`, `edit`, `delete`.
   - **`task_id`** (required for `edit` and `delete`): the scheduled task's ID.
   - **`artifacts`** (optional): filenames uploaded in this conversation that are relevant to the scheduled task; they will be copied to the destination session.
5. The user sees the preview message followed by a button that navigates to the Scheduled Tasks page with context pre-loaded.

## Preview Message Pattern

Before calling the tool, write a single short message that **names the Scheduled Tasks page up front** and lists what the user will be able to do there. The page name MUST appear before you describe what the user can do, otherwise "you'll be able to..." is hanging.

**Template:**

```
I'll take you to the **Scheduled Tasks** page where you can:

- Review and adjust the schedule{{ if user-mentioned-timing }} ({{user-mentioned-timing}}){{ end }}
- Customize {{what-the-task-does (paraphrasing what the user said)}}
- See a preview of when the task will run
- Make any final edits before saving
```

Keep it tight — 3–4 bullets, one sentence each. Personalize the bullets with details the user already provided in this conversation; do NOT add details they did not provide.

**Examples:**

User says *"send me a market summary every Monday at 9am"*:
```
I'll take you to the **Scheduled Tasks** page where you can:

- Review and adjust the schedule (Monday at 9am)
- Customize what goes into the market summary
- See a preview of when the task will run
- Make any final edits before saving
```

User says *"schedule a daily reminder"* (no time provided — do NOT invent one):
```
I'll take you to the **Scheduled Tasks** page where you can:

- Set the schedule (time of day, timezone, recurrence)
- Customize the reminder message
- See a preview of when it will run
- Make any final edits before saving
```

User says *"delete my weekly report task"* (mode=delete or mode=view if no id):
```
I'll take you to the **Scheduled Tasks** page to confirm the deletion of your weekly report task. The change isn't applied until you confirm there.
```

For `view` mode keep it to one short line — *"Here are your scheduled tasks — I'll take you to the **Scheduled Tasks** page so you can review them."*

## Non-Web Entrypoints (Slack / Teams / MCP / REST)

The `scheduled_task_redirect` tool only renders a clickable button in the web UI. On other entrypoints it can't — and the system prompt tells you so via a **"Entrypoint Redirect Capability"** context note saying redirect buttons can't be rendered.

**When that note is present:**

- Do NOT write the "I'll take you to the **Scheduled Tasks** page where you can…" preview message or list what the user could do there — no button will appear, so that message is misleading.
- Reply with a single short message telling the user that managing scheduled tasks is only available in the Solace Agent Mesh web UI's Scheduled Tasks page. You may still call the tool once (its text response says the same thing), but do not narrate a destination page or retry, and never create, edit, or delete the task yourself.

**When that note is absent** (web UI): follow the Preview Message Pattern above as normal.

## Language Rules — Do NOT Use Internal Jargon

The user does not know or care about skills, tools, redirects, or loading anything. Speak as if you're simply taking them to a page.

**Never** say:
- "Let me load the scheduling skill..."
- "I'll use the scheduled_task_redirect tool..."
- "I'm going to redirect you..."
- "On the destination page..." (always name the page)
- "Click the button above/below..." (the button placement is the UI's job — don't reference it)

**Do** say:
- "I'll take you to the **Scheduled Tasks** page where you can..."
- "Here are your scheduled tasks — I'll open the **Scheduled Tasks** page..."

## CRITICAL RULES

1. **NEVER invent scheduling details the user did not provide.** If the user said *"schedule a daily reminder"* and did not say a time, do NOT pick "9am" or "morning" — leave timing details out and let the destination page resolve them with the user. Echo only what the user actually said.
2. **NEVER create, modify, or delete scheduled tasks yourself.** You have no direct API for this. Always redirect.
3. **NEVER write cron expressions, schedule definitions, or task YAML in chat.** The destination page's AI builder handles schedule wording; you would only get in its way.
4. **NEVER ask for in-chat confirmation before redirecting to delete or edit.** The destination page has its own confirm/preview UI.
5. **NEVER fabricate a `task_id`.** If you don't know the ID, use `mode=view` so the user can pick on the page.
6. **ALWAYS call `scheduled_task_redirect` immediately** after writing the brief preview message. No clarifying questions, no multi-turn back-and-forth.

**Do NOT:**
- Pre-design the cron expression in chat — the destination AI builder does that
- Ask which day, time, or timezone before redirecting — the user can supply or correct that on the page
- Add bullets to the preview message that reference details the user did not provide (no invented day, time, frequency, agent name, etc.)
- Have a multi-turn conversation gathering requirements — short preview message, then redirect
- Delegate scheduling tasks to peer agents
