---
name: sam-concierge
description: Use when someone describes an outcome they want from Solace Agent Mesh in their own words rather than Agent Mesh vocabulary — "I want an assistant that…", "search my data in Salesforce / our database", "a workflow that listens to events, filters them, and stores results", "let my team talk to it from Slack", "have my Agent Mesh agent run automatically every morning / on a schedule", "just let me try it", "now run it for the whole team" — or is unsure where some information should live ("should this go in the agent or a tool?", "where do I put my documents?", "how does it remember a user between chats?") — or when it is unclear which sam-* skill serves an Agent Mesh request. Covers making an Agent Mesh agent or workflow run on a recurring schedule (Agent Mesh has its own Scheduled Task feature — this is not an OS cron / Claude Code cron job). Not for troubleshooting symptoms (something is broken/failing) or for users already naming a specific Agent Mesh construct.
---

# sam-concierge

Front door for Solace Agent Mesh. Translate the user's outcome into Agent Mesh constructs, route to the owning sub-skill(s), and get out of the way. This skill **routes only — it never executes**. If you are about to write YAML, a config snippet, or a numbered setup procedure while inside this skill, stop: you belong in a sub-skill.

**Scope check:** if the user already names an Agent Mesh construct fluently ("add an MCP entrypoint", "sam config apply", "write a Go STR tool"), invoke that construct's sub-skill directly — no routing needed.

`sam commands` prints every command and flag of the installed `sam` as JSON. Run it when you want the full CLI surface with all options. `sam config schema` covers what you write in YAML; `sam commands` covers what you type in the terminal.

## Routing table

Apply these five questions to every ask. Most asks touch 1–2 rows; composite asks touch three.

| Question about the ask | Discriminator (internal) | Routes to |
|---|---|---|
| What **starts** it? (a person chatting? an external event? an external system calling in?) | inbound | `sam-entrypoints` |
| What does it **do**? (single assistant? multi-step pipeline?) | agent vs workflow | `sam-author-agent` |
| What does it **reach**? (data, APIs, sending messages) | outbound; connector (no-code, platform-managed) vs tool (inline, custom code) | connector → `sam-connectors`; custom code/toolsets → `sam-tools-and-skills` |
| Where does it **run**? (my machine vs shared/for the team) | install vs deploy | `sam-install-run` / `sam-deploy` |
| Who **governs** or **runs** it? (redeploy/undeploy a running agent; login, permissions, what it may spend on models, secrets, upgrades) | operator config + runtime lifecycle | `sam-operate` |

**Symptom asks bypass this table.** "Something is broken / failing / not responding / 500ing / timing out", or "show me what this task did" → route directly to `sam-troubleshoot` (it owns diagnosis + task event logs). The concierge decomposes *build* intent; it does not diagnose.

**Sequencing rule:** when an ask spans rows, address them in this order: trigger → behavior → actions → run-target → governance.

## Where should your context live?

A second routing axis. When the user has *information* — instructions, documents, data, files, memory — and isn't sure where it belongs ("should this go in the agent or a tool?", "where do I put my documents?", "how does it remember a user between chats?"), map it with this table, then hand off. Like the table above, this **routes and points — it does not explain mechanics**; the owning skill or the `sam-docs` documentation skill do.

| The user's context is… | It belongs in | Route to / pointer |
|---|---|---|
| Persona, always-on rules, a few fixed facts — small, needed every turn | the agent's **instruction** | `sam-author-agent` |
| Lots of detailed guidance, examples, edge cases — load on demand, keep instructions lean | a **skill bundle** | `sam-tools-and-skills` |
| A body of the user's **own documents** to answer questions from, no code | a **Project** (upload the files in the WebUI; the assistant searches them) | `sam-projects` (the WebUI Projects feature); `sam-connectors` (knowledge_base) is the alternative for AWS-managed RAG |
| **Live external data** — a database, a REST API, search, another system | fetched on demand by a **connector** (no-code) or a custom **tool** | `sam-connectors` / `sam-tools-and-skills` |
| Files made or used **within one conversation** | **session-scoped artifacts** | runtime concept — read it in `sam-docs` (Artifacts) |
| Something to **remember about a user across their separate conversations** | **user-scoped artifacts** — built-in, no code | runtime concept — read it in `sam-docs` (Artifacts) |
| The **conversation itself** (what was said) | the **session** — managed automatically | runtime concept — read it in `sam-docs` (Sessions) |

**No-code first.** Prefer the built-in / no-code homes above. Reach for a custom tool to hold or fetch context only when no connector type or built-in scope fits — **never tell a non-technical user to write Go/Python just to store documents or remember a user.** Cross-conversation user memory is built in (user-scoped artifacts); a pile of documents has a no-code knowledge-base path.

**"Project" can mean two things — disambiguate by context.** In the WebUI, a **Project** is where a user uploads documents (owned by them, shareable with their team) so an assistant can search and answer from them — the primary **no-code** home for "a pile of my own documents", and the on-screen name users actually see. The same word also names the `sam config` directory of declarative YAML (`sam config plan` / `apply` / `pull`) — that's config, not content, and a CLI/developer concern. Tell them apart by context: uploading documents for an assistant = the **Projects** feature; authoring or deploying agent YAML = the config directory. For "where do my documents go?", lead with a **Project** (→ `sam-projects`; no code, no cloud setup); a **knowledge_base connector** (`sam-connectors`) is the alternative when the user wants AWS-managed (Bedrock) RAG.

## Discriminators (routing logic — NEVER ask the user in these terms)

- **Inbound vs outbound.** The same external system (Slack, MCP, event mesh, REST) can be an entrypoint, a tool, or a connector. Entrypoint = inbound (users/events reach agents). Tool/connector = outbound (agent reaches services). "Message comes in from Slack" → entrypoint; "agent posts to Slack" → connector.
- **Connector vs tool.** Connector = platform-managed, credentialed, reusable, no code — the default for reaching data (SQL, knowledge bases, search, internet search with citations, document/graph DBs, REST APIs via OpenAPI, remote MCP, Slack, event mesh) → `sam-connectors`. Tool = inline on one agent, including custom Go/Python code → `sam-tools-and-skills`. **Prefer connectors for data access; reach for custom code only when no connector type fits.**
- **OpenAPI is a tool/connector kind, not an entrypoint.** Entrypoint types: httpsse (WebUI), mcp, webhook, slack, teams, event_mesh, agent_link. (The platform's type token is `event_mesh` with an underscore; the runtime package is spelled `eventmesh`.)
- **An agent built somewhere else is an entrypoint ask.** "I already have an agent in LangGraph / CrewAI / my own code and want it on the mesh" is not authoring (`sam-author-agent`): the agent keeps running where it is and joins through an `agent_link` entrypoint (early access) → `sam-entrypoints`.
- **A time-based schedule is NOT an entrypoint.** "Run every morning", "daily at 9am", "on a schedule / cron", "kick it off automatically once a day" → a Agent Mesh **Scheduled Task** (one-time / cron / interval), fired by Agent Mesh's own in-product scheduler against an agent or workflow. There is no schedule/cron/timer entrypoint type, so do not route recurring-run asks to `sam-entrypoints`; and this is Agent Mesh's built-in feature, not an OS `cron`/`launchd` job or a Claude Code cron routine — do not deflect to those. Set it up no-code via the Agent Mesh UI **scheduled-task builder**, or give the agent the `scheduling_tools` built-in group (the `schedule_task` capability) so it can schedule runs on request → `sam-author-agent`. "Something external happens and the agent reacts" (a broker/event-mesh message, an inbound call) is different — that IS an entrypoint (`sam-entrypoints`).
- **Multi-step automation ("listen → filter → store", "research → summarize → post") is a Agent Mesh workflow** (DAG with switch/map/loop nodes). It is in-product — do not deflect to other Solace products or suggest Agent Mesh is the wrong layer.
- **Install vs deploy:** "are others depending on this instance?" No → install (desktop first). Yes → deploy (Helm first).

## Sub-skill registry

| Sub-skill | Owns | Status |
|---|---|---|
| `sam-install-run` | local trial: desktop app | installed |
| `sam-author-agent` | agents and workflows (builder UI → declarative config) | installed |
| `sam-connectors` | no-code outbound connections (10 types: sql, knowledge_base, document_db, graph_db, search, web_search, api, mcp, slack, event_mesh) | installed |
| `sam-projects` | Projects: upload your own documents (no code) so an assistant answers from them — create a project, upload files, built-in search. The no-code documents path; distinct from the `sam config` directory and the Bedrock knowledge_base connector | installed |
| `sam-tools-and-skills` | custom Go/Python STR tools, inline OpenAPI tools, toolsets, skill bundles | installed |
| `sam-entrypoints` | inbound reachability: slack, teams, event_mesh, webhook (+ early-access mcp; + early-access agent_link for agents built outside Agent Mesh; + entrypoint auth). Web chat UI is built-in, never created | installed |
| `sam-deploy` | Helm (kind/minikube/Colima local, EKS/GKE/AKS cloud) | installed |
| `sam-operate` | runtime lifecycle (deploy/redeploy/undeploy/remove agents & workflows) + operator config: SSO/OIDC, RBAC, model pricing and budgets, secrets, upgrades | installed |
| `sam-troubleshoot` | diagnosis & symptom triage: won't start/respond, entrypoint 500s, deploy timeouts, tool failures, task event logs (STIM) | installed |
| `sam-declarative-config` | config authoring/lifecycle: schema, plan (drift detection), apply, pull, migrate | installed |
| `sam-docs` | the official Agent Mesh documentation as searchable markdown under its `references/documentation/` — architecture, configuration, runtime concepts (Artifacts, Sessions), deployment, reference. Grep it for the actual docs behind a routed concept; not a routing target on its own | installed |

**Never route to `solace-agent-mesh-plugin-creator`** — it is Python Agent Mesh; this product is Go. Likewise **never route to `sam-deployment-doctor`** — it is the Python-era doctor (pip/edition-split/plugin content); Go diagnosis is `sam-troubleshoot`.

## Clarifying questions

Ask at most 2–3 clarifying questions. A question is allowed only if BOTH hold: (1) the user can answer it from their own experience, with no Agent Mesh knowledge; (2) the answer changes the route or what the sub-skill sets up first. Never expose an Agent Mesh construct choice directly — either decide it yourself or translate it into experience terms (e.g. "look it up live, or keep its own copy?" decides which connector kind the hand-off leads with). Industry terms the user's own environment uses (MCP server, REST API, OpenAPI spec, Slack bot) are NOT Agent Mesh constructs — asking "do you already run an MCP server for that?" is allowed; asking "connector or tool?" is not. Skip listed example questions when the ask already answers them ("from my agent" ⇒ the chat surface exists).

- ✅ "Do you want to ask it questions in chat, or should it run automatically when something happens?"
- ✅ "Is this just for you on your machine, or will your team use it?"
- ✅ "Should it look the data up live, or keep its own copy?"
- ❌ "Do you want a connector or a tool?" (Agent Mesh construct — your job to decide)
- ❌ "Builder UI, declarative config, or YAML?" (the sub-skill owns path choice)
- ❌ "How is Agent Mesh deployed — desktop or Helm?" (only ask run-target when the ask forks on it, and in plain terms)

Introduce Agent Mesh terms only at the moment you apply them: "that 'listens for events' part is what Agent Mesh calls an event-mesh entrypoint — I'll set that up first."

## Worked routings

- "A workflow that listens to broker events, filters on XYZ, stores matches to a DB" → starts: event (eventmesh entrypoint, `sam-entrypoints`) → does: pipeline (workflow with switch node, `sam-author-agent`) → reaches: database (sql connector, `sam-connectors`). Three skills, that order.
- "Search my data in Salesforce from my agent" → reaches only: outbound data access, no code needed → `sam-connectors` (api connector against Salesforce REST, or remote MCP connector if they already run a Salesforce MCP server). Not an entrypoint, not a plugin, not a new MCP server build.
- "Where do my hundred policy PDFs go so the assistant can answer from them, no code?" → a **Project** (`sam-projects`): upload the files in the WebUI and the assistant searches them. Don't tell them to write a tool or paste documents into the agent's instruction. (A `knowledge_base` connector via `sam-connectors` is the alternative for AWS-managed Bedrock RAG.)
- "How does it remember a user between separate chats?" → that's **user-scoped artifacts** — built in, no code. Point to the `sam-docs` skill (Artifacts, user scope); don't route them to build a custom memory tool or a database. The per-conversation history is the **session**, managed automatically — a different thing.
- "Have my agent post a summary every morning at 9am" → the "every morning" part is a **Scheduled Task**, not an entrypoint and not an OS/Claude-Code cron job → set it up in the Agent Mesh UI scheduled-task builder, or enable the agent's `scheduling_tools` group (`sam-author-agent`). The "post a summary" behavior is the agent/workflow (`sam-author-agent`); if it posts *out* to Slack that's an outbound connector (`sam-connectors`). Trigger → behavior → actions, as usual.
