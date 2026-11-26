# Structured input/output (agent as typed function)

The agent validates its final answer against a JSON Schema and retries on mismatch — first-class runtime support, not prompt engineering. Use when a program (script, workflow node, downstream service) consumes the agent's answer.

## There is no form control for this in the manual editor (verified 2026-10)

Do not invent one, and do not invent a `structured_output:` YAML block. The **Quick Build chat** can set it: it saves `input_schema`, `output_schema` and `validation_max_retries` for each agent it writes, replaces a schema whole rather than merging it, and removes a stored one when the artifact states `null`. The real surface, capability level:

- **Platform API / declarative config**: the agent's `additionalConfigurations` keys **`inputSchema`**, **`outputSchema`**, **`validationMaxRetries`** (validate-and-retry enforcement; default 2 retries). Schemas are standard JSON Schema objects.
- **Runtime YAML**: `input_schema` / `output_schema` on the agent. Exact spelling and placement: `sam-declarative-config`.
- **Workflow nodes** can override schemas per node (`input_schema_override` / `output_schema_override` in the workflow builder's node detail) — relevant when the same agent is typed differently per step.

Enforcement is **validate-and-retry against the LLM** (validation errors are fed back, up to the retry limit), not constrained decoding — tell users to keep client-side validation as a backstop, and that a persistently failing schema surfaces as a task error rather than malformed prose.

## Consuming it

The structured result is delivered as the task's final output (as an artifact reference on the A2A result for workflow/programmatic invocations). For "my script calls the agent": submit via the entrypoint HTTP API, wait for the terminal task event — not intermediate streaming chunks — then parse. Entrypoint/API specifics for external callers (MCP clients, REST) are `sam-entrypoints` territory.
