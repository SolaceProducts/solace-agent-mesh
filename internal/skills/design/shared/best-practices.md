# Agent Mesh Best Practices

## The Core Principle

**LLM context is the scarcest resource.** Every design decision in Agent Mesh flows from managing it well. An LLM produces its best work when its context is focused, relevant, and appropriately sized for the task at hand. Overloading an agent with unrelated knowledge, too many tools, or an unfocused instruction degrades reasoning quality — the LLM becomes vague instead of precise, makes poor tool choices, and loses track of what it's supposed to be doing.

This principle drives everything: how you decompose requirements into components, how you structure agents, how you decide between agents and workflows, and how you organize skills and tools.

---

## Decision Framework for Component Selection

When mapping requirements to Agent Mesh components, evaluate these considerations. They are not sequential steps — a workflow may be the right choice from the start if the process is clearly structured, even before you consider whether a single agent could handle it.

### Start simple, add complexity with reason

Do not split prematurely. A single agent with a clear role, good instruction, and appropriate tools handles most tasks well. Multi-agent systems add complexity — every agent boundary is a potential point of miscommunication and makes debugging harder.

Start with one agent. Only add complexity when you have a concrete reason.

### Use workflows when the process structure is known

Use a workflow instead of (or wrapping) agents when any of these apply:

- **The process has a known structure.** You know the steps ahead of time — first extract, then analyze, then summarize, then route. The sequence doesn't depend on LLM judgment.
- **You need data validation between steps.** Each node's output can be checked before feeding into the next node. This prevents error cascades — a malformed extraction doesn't corrupt the analysis.
- **You need structured I/O contracts.** When processing data through a pipeline where each stage expects a specific format, a workflow enforces that contract. An agent passing data between tool calls has no such validation boundary.
- **You need parallelism.** Independent steps should execute in parallel. Workflows express this naturally through dependencies — nodes with no dependencies run simultaneously.
- **You need auditability.** Workflows produce a clear record of which node produced what output, making post-hoc analysis straightforward.
- **You need per-step retry.** If step 3 of 5 fails, the workflow retries just that step. With a single agent, a failure often means starting over.

Workflows often contain agents as nodes — the workflow controls the "what order" and the agents handle the "how" within each step. Recognize when a workflow is the right choice early — don't default to agents and retrofit a workflow later.

### Use event mesh entrypoints for event-driven integration

Use an event mesh entrypoint when events arrive from an external Solace broker and need autonomous processing:

- **Events from external systems.** IoT sensor readings, application events, database change notifications, webhook payloads — anything that arrives asynchronously without a user initiating it.
- **Autonomous processing.** No user is in the loop. The event arrives, the agent processes it, and optionally a response is published back to the broker.
- **Flexible routing.** One or more topic subscription patterns, routed to agents or workflows, with optional response routing, context forwarding, and acknowledgment policies.

An event mesh entrypoint always requires an autonomous target agent — one designed without interactive tools (`ask_user_question`) and with explicit error handling in the instruction. If the event processing is multi-step, point the entrypoint at a workflow instead of a single agent.

An event mesh entrypoint supports multiple event handlers (subscription patterns routed to different agents), output transforms, artifact extraction from events, and dynamic agent routing.

### Add skills for on-demand knowledge

If the agent needs access to large reference material — documentation, schemas, domain knowledge — but only uses it for specific tasks, package that material as skills. Skills are loaded on demand, keeping the agent's base context small.

Associate tools with skills when those tools are only relevant while the skill is loaded. This further reduces the agent's default tool list, keeping each turn's context focused. Note that loading a skill with associated tools expands the agent's context — both the reference material and the tool definitions are added. This is why skills should be focused: loading a skill should bring in only what's needed for the current task.

### Split into multiple agents when focus is lost

Split when the agent's responsibilities become distinctly different — when you find yourself writing an instruction that says "when the user asks about X, do this completely different thing than when they ask about Y." The signal is that the subject matter diverges enough that the instruction becomes a collection of unrelated directives rather than a coherent role description.

Signs you should split:
- The agent's instruction reads like a manual for multiple different jobs
- Tools needed for one capability are completely irrelevant to another
- The agent frequently loads multiple unrelated skills in the same conversation
- Quality degrades because the LLM is trying to be an expert in too many unrelated domains

When you split, the original agent can delegate to specialists via peer delegation. The original agent becomes a coordinator that routes tasks based on skill matching, while each specialist agent maintains a focused context.

### Use peer delegation for cross-domain expertise

When an agent encounters a task outside its domain, it should delegate to a peer rather than trying to handle it with a loaded skill. Peer delegation is the right choice when:

- The task requires a different set of tools than the current agent has
- The subject matter requires sustained expertise (not just a quick reference lookup)
- The response quality would benefit from a specialist agent's focused instruction

Peer delegation happens automatically through agent card discovery — the delegating agent finds the right peer by matching the task to capability descriptions.

---

## Guiding Policies

When making design decisions, consider these factors in order of priority:

### 1. Context Management

Keep each agent's context small and focused. This means:
- Write specific instructions, not generic ones
- Use skills for large reference material rather than stuffing everything into the instruction
- Associate tools with skills so they only appear when relevant
- Prefer peer delegation over giving one agent every tool in the system

### 2. Data Correctness

Validate data between processing stages to prevent error cascades. A single bad output early in a pipeline can corrupt everything downstream. Use workflows when you need validation boundaries, and use structured output mode when an agent must produce data in a specific format.

### 3. Parallelism

Design for parallel execution where possible. In workflows, declare accurate dependencies so independent nodes run simultaneously. In multi-agent designs, structure tasks so agents can work concurrently rather than sequentially.

### 4. Auditability

Make it easy to understand what happened after the fact. Workflows are inherently auditable — each node's input and output is recorded. For agents, use artifacts to persist intermediate results and reasoning. Name things clearly so log inspection is straightforward.

### 5. Reliability

Use workflows for processes that need per-step retry and graceful degradation. Configure appropriate timeouts for external calls. Design exit handlers for cleanup on failure. For agents, include explicit error recovery guidance in the instruction.

### 6. Cost

Smaller, focused contexts use fewer tokens per LLM call. This matters at scale. A general-purpose agent that loads its entire knowledge base on every turn costs significantly more than a specialist that loads only what it needs. Use skills and peer delegation to keep per-turn costs manageable.

### 7. Testability

Focused agents with clear responsibilities are easier to test independently. You can validate a "data extraction agent" separately from a "report generation agent." Monolithic agents that do everything are hard to test because the test surface is enormous.

### 8. Reusability

A focused agent with a clear capability description can be reused across multiple workflows, invoked by multiple peer agents, and composed in ways you didn't originally plan for. A monolithic agent that does everything is a one-off.

---

## Terminology

To avoid confusion between two different concepts that share the word "skills" in the A2A protocol:

- **Skill**: A loadable knowledge bundle for an agent — reference documents, schemas, examples, and optionally associated tools. Internal to the agent. Loaded on demand.
- **Capability**: A public description of what an agent can do, listed on its agent card for discovery by other agents and entrypoints. External-facing.

**Important**: In the YAML configuration, capabilities are defined under the `skills` field on the agent card. This is the A2A protocol field name and must be used in configs. Use "capability" when discussing design to maintain clarity.

---

## LLM-Aware Design Practices

These practices are derived from how LLMs actually behave and what makes them effective as agents.

### Progressive Disclosure of Context

Give agents a focused instruction that defines their role, then let them load additional context via skills when they need it. An agent with a 2,000-token instruction and on-demand skills produces better results than one with a 20,000-token instruction that tries to cover everything upfront.

The instruction should tell the agent what skills are available and when to use them. The agent loads the skill, searches its references, and gets exactly the information it needs for the current task.

### Tool Descriptions Matter

The quality of an agent's tool use depends heavily on how well each tool is described. Invest in:
- **Clear tool names**: The name should indicate what the tool does without reading the description
- **Specific parameter descriptions**: Not just the type, but what values are expected and what happens with different inputs
- **Usage guidance**: When to use this tool versus alternatives
- **Return value documentation**: What the tool returns and how to interpret it

A poorly described tool gets misused, called with wrong parameters, or ignored entirely. This is one of the highest-leverage areas for improving agent quality.

### Structured Output as a Forcing Function

When an agent must produce precise, machine-parseable data — configuration files, structured reports, API payloads — provide the schema or a template. LLMs produce more accurate structured output when they have a reference to conform to than when generating free-form.

In Agent Mesh, this means:
- Use structured invocation mode when the output must conform to a specific schema
- Provide schema skills so agents can reference exact field definitions
- When building configs, the agent should load the relevant schema skill and follow it

### Reason Before Acting

Design agent instructions that encourage analysis before tool calls. A common failure mode is an agent that immediately starts calling tools without thinking through the approach. Add guidance like:
- "Before using tools, analyze the request and plan your approach"
- "Consider which tools are relevant before making any tool calls"
- "If the task is ambiguous, break it down into sub-tasks first"

This is especially important for agents with many tools — without this guidance, the LLM may call the first plausible tool rather than the best one.

### Explicit Error Recovery

Without explicit guidance on what to do when things fail, agents tend to either retry blindly (calling the same failing tool repeatedly) or give up (reporting failure without attempting alternatives). Include error recovery guidance in the instruction:
- What to do when a tool call fails
- When to retry versus try an alternative approach
- How to report errors to the user or calling agent
- What constitutes a recoverable vs. unrecoverable error

### Interactive vs. Autonomous Mode

Agents operate in two fundamentally different modes, and the instruction must be designed accordingly:

**Interactive agents** have a user on the other end. They should:
- Include the `ask_user_question` tool in their tool set
- Be instructed to ask for clarification on high-stakes or ambiguous decisions
- Include tone and personality guidance appropriate to the user interaction (conversational, patient, professional, etc.)
- Explain their reasoning and provide options when the path forward isn't clear

**Autonomous agents** (event-driven agents behind an event mesh entrypoint, or agents running as workflow nodes) have no user to ask. They must:
- NOT include `ask_user_question` in their tool set
- Be explicitly instructed: "You operate autonomously. Do not ask for clarification — use your best judgment based on available data and log uncertainty."
- Be purely functional in instruction style — no tone or personality guidance
- Handle all ambiguity through fallback logic, defaults, and logging rather than blocking
- Include robust error handling since there's no human to intervene

This distinction must be made at design time. An agent built for interactive use will fail in an autonomous context (it blocks waiting for user input that never comes), and an agent built for autonomous use may frustrate interactive users by never asking for clarification.

---

## Instruction Writing

A well-structured agent instruction follows this pattern:

### 1. Role and Identity
State who the agent is and what it does. One to three sentences that establish the agent's purpose and domain. This is the foundation that every subsequent decision references.

### 2. Core Expertise and Domain
Describe the agent's knowledge area. What does it understand deeply? What context does it bring to every task? This helps the LLM calibrate its responses — an agent that knows it's a "security auditor" reasons differently than a "customer support agent."

### 3. Behavioral Guidelines
How should the agent approach its work? This includes:
- How to analyze requests before acting
- When to use which tools
- How to handle multi-step tasks
- Quality standards for output

For interactive agents, include tone and personality guidance here — "Be concise and direct" or "Be patient and explain step by step." For autonomous agents, keep this section purely functional.

### 4. Constraints and Boundaries
What the agent should NOT do:
- Tasks outside its domain (delegate to peers instead)
- Actions that require human approval
- Assumptions it should not make
- Data it should not access or expose

### 5. Skill References
List available skills and when to use each one:
- "Load the `api-reference` skill when you need endpoint details or data formats"
- "Use the `data-analysis` skill when the user provides data files for analysis"

This tells the agent that it has on-demand knowledge available and prevents it from guessing when it should be looking things up.

---

## Skill Design

### Keep Skills Focused

Each skill should cover one coherent topic. A skill that combines "database administration" with "email templates" forces the agent to load irrelevant content. Split into separate skills that the agent loads independently as needed.

### Associate Tools with Skills

When a set of tools is only useful in the context of a specific skill, associate them with that skill. The tools appear in the agent's context only when the skill is loaded. This keeps the default tool list small and reduces the chance of the agent calling irrelevant tools.

### Write Descriptive SKILL.md Files

The SKILL.md file tells the agent what the skill contains and how to use it efficiently. Include:
- What the skill covers
- How to search it (suggested grep patterns)
- When to use it versus other skills
- How the reference files are organized

### Size Appropriately

A skill that's too large (hundreds of reference files) is slow to search and the results may be noisy. A skill that's too small (a single paragraph) isn't worth the overhead of loading. Aim for skills that contain 1-10 reference files covering a coherent topic in sufficient depth.

---

## Tool Selection

### Prefer Built-in Tools

Built-in tools ship with Agent Mesh and need no packaging; most run in-process and are fast. Use them when available:
- Artifact management tools for file/data handling
- Web request tools for external API calls
- Image processing tools for visual content
- General utilities for time, format conversion, etc.

### Use Custom Python Tools for Domain Logic

When you need functionality not covered by built-in tools — integrating with a specific API, running domain-specific calculations, processing specialized data formats — create a custom Python tool. Keep tools focused: one tool per operation, not a Swiss army knife that does everything.

### Use MCP Tools for Existing Servers

If there's an existing MCP server that provides the capability you need (database access, code execution, third-party integrations), connect to it rather than reimplementing the functionality. MCP tools leverage existing, tested implementations.

### Use Peer Delegation for Agent-Level Expertise

If the "tool" you need is really another agent's domain expertise — not just a function call but a task requiring reasoning, context, and judgment — delegate to a peer agent rather than trying to capture that expertise in a tool.

---

## Common Pitfalls

### The Kitchen Sink Agent

Giving one agent every tool, every skill, and a generic "you can do anything" instruction. The agent becomes mediocre at everything and excellent at nothing. Its context is bloated, tool selection is unreliable, and the instruction gives it no guidance on prioritization.

**Fix**: Define a clear role. Only include tools and skills relevant to that role. Delegate everything else to specialists.

### Skill Overload

Loading many skills into a single agent, assuming the agent will figure out which one to use. In practice, the agent often loads the wrong skill or loads multiple skills unnecessarily, wasting context and confusing its reasoning.

**Fix**: Reduce the number of skills per agent. If an agent needs many different knowledge domains, that's a signal it should be split into specialists with peer delegation.

### Tool Description Neglect

Adding tools with minimal descriptions ("Sends an email") and expecting the agent to use them correctly. The agent doesn't know what parameters to provide, when to use the tool versus alternatives, or how to interpret the results.

**Fix**: Write thorough tool descriptions including parameter semantics, usage context, expected outputs, and error cases.

### Missing Validation Boundaries

Chaining multiple agent calls or tool calls without validating intermediate results. A single malformed output early in the chain corrupts everything downstream, and debugging requires tracing back through multiple steps.

**Fix**: Use workflows with explicit validation between nodes. Or add validation tool calls between processing stages in an agent's instruction.

### Autonomous Agent with Interactive Assumptions

Building an agent that asks for user input, then deploying it behind an event mesh entrypoint or as a workflow node where no user exists. The agent blocks indefinitely waiting for input.

**Fix**: Design for the deployment mode from the start. Autonomous agents must handle all ambiguity through defaults, fallback logic, and logging — never by asking.

### Interactive Agent Behind an Event Mesh Entrypoint

Building an agent with `ask_user_question` and interactive tools, then wiring it to an event mesh entrypoint. The agent blocks indefinitely because there is no user to respond. This is the event-mesh-specific version of "Autonomous Agent with Interactive Assumptions."

**Fix**: When building an event mesh entrypoint, always design the target agent for autonomous operation. Set `supports_streaming: false`, exclude `general_agent_tools` (which includes `ask_user_question`), and include explicit autonomous operation guidance in the instruction. For the recommended agent instruction pattern, see *Event mesh agent instruction pattern* in the lookup table at the end of this guide.

### Premature Multi-Agent Split

Splitting into multiple agents before a single agent has proven inadequate. Each agent boundary adds potential for miscommunication (lost context between agents) and debugging complexity.

**Fix**: Start with one agent. Measure. Split only when you observe concrete problems — degraded quality, context overflow, clearly incompatible responsibilities.

### Ignoring Context Accumulation

In multi-turn conversations, each turn adds to the session history. An agent that works well for 3 turns may degrade at 30 turns because its context is filled with conversation history, leaving less room for reasoning and tool use.

**Fix**: Design with session length in mind. Use artifacts for persistent data rather than expecting the agent to remember everything from conversation history. Consider session summarization for long-running interactions.

---

## Performance and Cost

### Token Usage

Every token in the context window costs money and time. The largest contributors to context size are:
1. **System instruction**: Present in every turn. Keep it focused and concise.
2. **Conversation history**: Grows with every turn. Use artifacts for data persistence instead of relying on history.
3. **Tool definitions**: Every tool adds to the context. Use skill-associated tools to reduce the default set.
4. **Loaded skill content**: Skill references added to context when loaded. Search efficiently rather than loading everything.

