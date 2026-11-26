---
name: sam-build-redirect
description: Send the user to the Agent Mesh Builder when they want to create or set up an agent, workflow, entrypoint or connector, or automate a process with one. Not for making files, documents, reports or decks.
tags:
  - builder
  - redirect
tools:
  - tool_type: builtin
    tool_name: redirect_user
---

# Redirect to Builder

`redirect_user` sends the user to the **Solace Agent Mesh Builder**, which creates agents, workflows, entrypoints and connectors.

Call it as soon as the user wants to create or set up one of those, turn something from this conversation into a reusable agent or automation, or automate a process they do by hand:

1. Say in one sentence what they want to build.
2. Call `redirect_user` with `context`: what they want and why, plus any requirements, tools, data sources or integrations they mentioned. Put the names of relevant uploaded files in `artifacts` so they carry over.

The user then sees a button that opens the Builder with that context and those files.

Never build, design or plan the component yourself. Don't write YAML, agent instructions, workflow definitions or tool definitions, don't ask clarifying questions first, don't ask for uploads, and don't delegate the build to a peer agent. The Builder does all of that.

## Outside the web UI

When the system prompt has an **"Entrypoint Redirect Capability"** note saying redirect buttons can't be rendered (Slack, Teams, MCP, REST), no button appears. Reply in one short message that building Agent Mesh components is only available in the Builder in the Agent Mesh web UI. You may call the tool once, but don't describe a destination page, don't retry, and never build the component yourself.
