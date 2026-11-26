# Agent Mesh Core Concepts

## What is Solace Agent Mesh

Solace Agent Mesh (Agent Mesh) is an event-driven AI agent platform. Agents communicate through a message broker rather than calling each other directly. This decoupled architecture means any component — an agent, an entrypoint, a workflow — can be added, removed, or replaced independently without affecting the rest of the system.

All inter-component communication uses the A2A (Agent-to-Agent) protocol over the event mesh. Components discover each other automatically: agents announce their capabilities, and other components find them dynamically through agent card publishing. There is no manual wiring required.

Agent Mesh supports real-time event-driven work (reacting to things as they happen) and scheduled work (recurring tasks on a timetable). Most real-world agents handle both. For design guidance on scheduled work, see *Scheduled work design* in the lookup table at the end of this guide.

---

## Component Types

### Agent

An agent is an LLM-powered processing unit. It is the core building block of Agent Mesh. An agent has:

- An instruction (system prompt) that defines its personality, expertise, and constraints
- One or more skills (knowledge bundles) and capabilities (public descriptions for discovery)
- Tools that let it take actions in the world
- An agent card that serves as its public identity for discovery
- A model configuration specifying which LLM to use and its parameters
- Session handling for conversation memory
- Artifact access for file and data storage

Agents are autonomous: they receive a task, reason about it using the LLM, call tools as needed, and produce a response. They can operate independently or collaborate with other agents through peer delegation.

### Skill

A skill is a loadable knowledge bundle for an agent. It contains reference documents (documentation, schemas, examples) and optionally associated tools that are only added to the agent's context when the skill is loaded. This keeps the agent's base context small — large bodies of reference material are deferred until actually needed.

Skills are distinct from **capabilities** (see below). A skill is internal to the agent — it's how the agent accesses knowledge. A capability is external — it's how other agents discover what this agent can do.

A well-designed skill has focused, non-overlapping content. Associated tools on a skill reduce context further — the agent doesn't carry those tools until the skill is loaded.

### Capability (Agent Card Entry)

A capability is a public description of something an agent can do, listed on the agent's card for discovery by other agents, the orchestrator, and entrypoints. When another agent needs help with a specific task, it finds the right peer by matching the task to capability descriptions.

**Important terminology note**: In the YAML configuration, capabilities are defined under the `skills` field on the agent card (this is the A2A protocol field name). Throughout this documentation we use "capability" to avoid confusion with knowledge-base skills, but the config field remains `skills`.

Capability descriptions should be:

- **Accurate**: Describe what the agent can actually do, not what it aspires to
- **Distinct**: Each capability should cover a clearly different area
- **Searchable**: Use keywords that other agents would look for when seeking help

### Workflow

A workflow is a directed acyclic graph (DAG) of nodes that executes a multi-step process with explicit, deterministic control flow. Use workflows when you need predictable ordering, branching, parallelism, or retry logic — situations where you want the structure of the process defined ahead of time rather than decided by an LLM at runtime.

Workflows are discovered as agents: they publish an agent card and receive tasks through the same A2A protocol. To the rest of the system, a workflow looks like any other agent.

Workflow node types are described in the Workflow Node Types section below.

### Entrypoint

An entrypoint bridges external protocols into Agent Mesh. Users and external systems interact with agents through entrypoints. An entrypoint handles:

- Protocol translation (HTTP/SSE, REST, webhooks, Slack, Teams, etc.)
- Authentication and authorization
- Message formatting and response streaming
- Session management for multi-turn conversations

The most common entrypoint is the HTTP SSE entrypoint, which provides a streaming chat interface. Slack, Teams, Event Mesh, MCP and webhook entrypoints are built-in types you configure; there is nothing extra to install.

#### Event Mesh Entrypoint

The event mesh entrypoint is particularly important for building autonomous, background-running agents. It connects agents to real-world events flowing through the Solace event broker — messages from IoT devices, application events, database change notifications, webhook payloads, or any other event source.

An event mesh entrypoint configuration defines:

- **Topic subscriptions**: Which broker topics to listen on for incoming events
- **Message mapping**: How to transform incoming event payloads into agent requests
- **Routing**: Which agent should handle events from which topics
- **Output handling**: How to publish agent responses back to the event mesh

This is the key component for building agents that operate autonomously without human interaction — reacting to events as they happen, processing data streams, and triggering actions based on real-time conditions. Without an event mesh entrypoint, agents can only respond to user-initiated chat requests.

For configuration details, see *Entrypoint schema* in the lookup table at the end of this guide.

You typically do not need to create custom entrypoints unless you are bridging a new external protocol. The built-in HTTP SSE entrypoint covers most interactive use cases, and the event mesh entrypoint covers autonomous event-driven use cases.

For design guidance on event mesh entrypoints, see *Event mesh entrypoint design* in the lookup table at the end of this guide; for configuration details, *Event mesh entrypoint schema* in the same table.

### Proxy

A proxy connects external A2A-over-HTTPS agents into Agent Mesh. It translates between the A2A/HTTPS protocol used by external agents and the A2A/Solace protocol used internally.

A single proxy can manage multiple external agents. It handles:

- Agent card fetching and publishing to mesh discovery
- Authentication (static bearer, API key, or OAuth 2.0)
- Artifact resolution between external and internal formats
- Task lifecycle management (initiation, cancellation, completion)

Use proxies when you need to integrate agents hosted outside your Agent Mesh deployment — third-party agents, agents in other organizations, or agents running on different infrastructure.

### Toolset

A toolset is a named, reusable bundle of tools that any number of agents can reference. Custom tools are packaged and uploaded as a toolset; built-in tool groups, MCP connections and OpenAPI specs are exposed the same way. Toolsets, together with skills, are the unit of reuse: define the capability once and reference it from every agent that needs it, rather than copying tool configuration between agents.

### Project

A project is a workspace that groups chat sessions and their associated artifacts. Projects provide organizational structure:

- Group related conversations together
- Maintain project-specific knowledge and instructions
- Set a default agent for the project
- Search across sessions within a project

Projects are organizational — they do not affect how agents run or communicate. They help users manage their interactions with agents.

### Prompt

A prompt is a reusable template with variable substitution. Prompts let users save commonly used messages and fill in variables at use time.

- Prompt groups contain one or more prompts
- Variables use `{{Variable Name}}` syntax (title case with spaces)
- Prompts can be accessed via shortcuts in the chat interface

Prompts are user-facing conveniences — they make it easier to interact with agents consistently.

---

## How Components Relate

Agents are the fundamental unit. Everything else either composes agents, exposes agents, or supports agents:

- **Workflows compose agents**: A workflow's agent nodes invoke agents as steps. The workflow controls the order and logic; the agents do the actual work.
- **Entrypoints expose agents**: Users reach agents through entrypoints. The entrypoint translates the user's protocol (HTTP, Slack, etc.) into A2A messages.
- **Proxies bridge agents**: External agents become available on the mesh through proxies, appearing as regular agents to everything else.
- **Toolsets package tools**: A toolset bundles tools once so many agents can reference them.
- **Skills provide agent knowledge**: Skills are loadable knowledge bundles with reference material and optional associated tools, loaded on demand.
- **Capabilities describe agent expertise**: Listed on the agent card, capabilities tell other components what this agent can do.
- **Tools extend agent reach**: Tools are how agents take actions — calling APIs, querying databases, managing artifacts, delegating to peers.

### Communication Model

All inter-component communication goes through the broker using the A2A protocol. Components never call each other directly. This means:

- Components can be deployed independently on different machines or processes
- Multiple instances of the same component can run for scaling
- Components can be added or removed without restarting others
- Communication is reliable — the broker handles delivery guarantees

### Discovery

Agent discovery is automatic. Every agent (and workflow) publishes an agent card to the broker at regular intervals. The agent card contains:

- Agent name and description
- List of capabilities with descriptions (defined as `skills` in the config YAML)
- Supported input and output modes (text, file, etc.)
- Provider information

Other components — including entrypoints, other agents, and the orchestrator — subscribe to agent card announcements and maintain a live directory of available capabilities. When an agent needs to delegate a task, it searches this directory to find the right peer.

---

## Agent Anatomy

### Instruction

The instruction is the agent's system prompt. It defines who the agent is, what it's good at, what constraints it operates under, and how it should behave. A good instruction is:

- **Specific**: Clearly states the agent's role and domain
- **Bounded**: Defines what the agent should and should not do
- **Contextual**: Provides enough background for the LLM to make good decisions
- **Skill-aware**: References the agent's skills and when to use them

### Capabilities on the Agent Card

The agent card's capability list is the agent's public interface. Other agents and the orchestrator use these descriptions to decide whether to route a task to this agent. See the Capability section under Component Types for guidance on writing good capability descriptions.

Note: In the YAML config, capabilities are defined under the `skills` field on the agent card (A2A protocol convention).

### Tools

Tools are the agent's hands — they let it interact with the world beyond generating text. See the Tool Ecosystem section below for the full taxonomy.

An agent's tool set should match its capabilities. If a capability promises the agent can "manage artifacts," the agent needs artifact management tools. If a capability promises "web research," it needs web request tools.

### Model Configuration

Each agent specifies which LLM to use and with what parameters (temperature, max tokens, etc.). Different agents can use different models — a simple routing agent might use a fast, cheap model while a complex analysis agent uses a more capable one.

### Session Handling

Agents maintain conversation history through sessions. Each user-agent interaction gets a session that tracks the back-and-forth messages, allowing multi-turn conversations. Session storage backends include in-memory (ephemeral), SQLite, and PostgreSQL.

### Artifacts

Artifacts are versioned files that agents can create, read, update, and share. They serve as the persistent data layer — reports, generated code, analysis results, uploaded files. Artifacts are stored through pluggable backends (filesystem, S3, GCS, in-memory).

---

## Tool Ecosystem

Agents use tools to take actions. Agent Mesh supports five categories of tools:

### Built-in Tools

Tools that ship with Agent Mesh. Most are Go-native and run in-process with the agent; a few (file and PDF conversion, Mermaid rendering) run in the Secure Tool Runtime, and the Python-backed ones are not available in the desktop app. Built-in tool groups include:

- **Artifact management**: List, load, delete, append to, grep, and search-and-replace in artifacts
- **Web requests**: Make HTTP requests to external APIs and services
- **Image processing**: Analyze, transform, and generate images
- **Data analysis**: Process and visualize data
- **General utilities**: Time, file-to-Markdown conversion, PDF text extraction
- **Diagrams**: Render Mermaid syntax to an image

Built-in tools are configured by referencing their group name in the agent config.

### Custom Python Tools

User-written tools that run in the Secure Tool Runtime (STR) sandbox, written in Python or in Go (a standalone binary built with the Agent Mesh tool SDK). Use custom tools when you need functionality not covered by built-in tools — integrating with a specific API, running domain-specific logic, processing specialized data formats.

Custom tools are defined with:

- A tool name and description
- Input parameters with types and descriptions
- A Python function or Go handler that implements the tool logic

The STR provides a secure execution environment with resource isolation.

### OpenAPI and Connector Tools

Tools generated from a service definition rather than written by hand. An OpenAPI spec becomes one tool per operation, and a connector (a REST API, database, event broker, or similar) produces tools for the system it connects to. Use them when the integration is a standard API or data source — no code to write or maintain.

### MCP Tools

Tools exposed by external Model Context Protocol (MCP) servers. MCP is a standard protocol for connecting LLMs to external tool providers. Agent Mesh agents can connect to any MCP server via stdio, SSE, or HTTP transport.

Use MCP tools when you want to leverage existing MCP-compatible tool servers — database access, code execution environments, third-party integrations that already have MCP support.

### Peer Agents

Other agents can serve as tools through peer delegation. When an agent encounters a task outside its expertise, it can delegate to a peer agent that has the right skills. The delegation happens transparently through the A2A protocol.

Peer delegation is automatic when the agent has peer routing enabled — the agent discovers available peers through their agent cards and routes tasks based on skill matching. No explicit configuration of peer-to-peer connections is needed.

---

## Workflow Node Types

Workflows are built from seven node types:

### Agent Node

Invokes an agent with a prompt. The prompt can use template expressions to inject data from the workflow input or from previous nodes' outputs.

Template variables:
- `{{workflow.input}}` — the original workflow input
- `{{node_id.output}}` — the output of a previously completed node
- `{{_map_item}}` — the current item when inside a map node

### Switch Node

Conditional branching. Evaluates a condition expression and routes execution to one of several branches. Condition expressions support safe operators: `==`, `!=`, `<`, `>`, `<=`, `>=`, `and`, `or`, `not`, `in`, `contains`.

### Map Node

Parallel iteration over a collection. Takes a list and executes a subgraph for each item in parallel. Each iteration receives the current item as `{{_map_item}}`.

### Loop Node

Repeated execution with a termination condition. Runs a subgraph repeatedly until a condition is met or a maximum iteration count is reached.

### Workflow Node (Nested)

Invokes another workflow as a node. Enables composition of workflows — complex processes can be broken into smaller, reusable sub-workflows.

### Tool Node

Calls a single tool directly, with no LLM in the loop. Use it for deterministic steps — a lookup, a conversion, a fixed API call — where an agent would only add cost and the risk of transcription errors. Tool nodes never retry.

### Decision Node (Early Access)

Asks a decision model typed questions about its input in one call. A question can be yes or no, a choice among options, or a score on a scale. The node can route on the answer. Each option of a choice question maps to a branch, an answer below a confidence threshold escalates to another node (usually an agent), and a default covers options with no route. Use it for a narrow classification step, such as which team owns a ticket or how urgent it is, in place of a classify agent followed by a switch. Keep open-ended reasoning, tool use and anything that needs the conversation in agent nodes. It needs a decision model (TypeSafe, OpenAI or Google's Gemini) registered in the deployment, and it sends the node's input to that provider.

### Workflow Execution Features

- **Dependencies**: Nodes declare `depends_on` to define execution order. Nodes with no dependencies run in parallel.
- **Retry**: Configurable retry strategy with exponential backoff per node.
- **Exit handlers**: `on_success`, `on_failure`, `on_cancel`, `always` — cleanup or notification logic that runs after the workflow completes.
- **Fail-fast**: By default, the workflow stops on the first node failure. This can be disabled for workflows where partial completion is acceptable.
- **Timeouts**: Per-node and per-workflow timeout configuration.

---

## When to Use What

For detailed guidance on mapping requirements to Agent Mesh components, read the best-practices guide (*Best-practices guide* in the lookup table at the end of this guide). The brief guidance is:

| Need | Component |
|------|-----------|
| Flexible reasoning about a task, tool use, conversation | Agent |
| Deterministic multi-step process with explicit control flow | Workflow |
| Expose agents to users or external systems | Entrypoint |
| Integrate an agent hosted outside your deployment | Proxy |
| Package tools for reuse across agents | Toolset |
| Organize user sessions and artifacts | Project |
| Save reusable message templates | Prompt |
| Provide loadable knowledge and associated tools to an agent | Skill |
| Connect agents to real-world events for autonomous operation | Event Mesh Entrypoint |

**Agent vs. Workflow**: Use an agent when the LLM should decide what to do next. Use a workflow when you know the sequence of steps ahead of time and want deterministic execution. Workflows often contain agents as nodes — the workflow controls the process, the agents handle the reasoning within each step.

**Single agent vs. Multi-agent**: Start with a single agent. Add more agents only when responsibilities are clearly distinct and a single agent's instruction would become unfocused. Multi-agent systems add complexity — each agent boundary is a potential point of miscommunication.

**Built-in tool vs. Custom tool vs. MCP tool**: Prefer built-in tools when available (fastest, no external dependencies). Use custom Python tools for domain-specific logic. Use MCP tools to leverage existing MCP servers. Use peer delegation when the "tool" is really another agent's expertise.

---

## Configuration

All Agent Mesh components are defined as YAML configuration. Each component type has a specific schema that defines its required and optional fields.

For exact field definitions, required fields, and annotated examples, use the schema reference for the component type — *Agent, workflow, tool schema*, *Entrypoint schema* and *Event mesh entrypoint schema* in the lookup table at the end of this guide. Where the configuration itself lives, and how it reaches the platform, depends on where you are working; the same table says.
