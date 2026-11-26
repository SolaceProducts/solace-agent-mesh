# OpenTelemetry

Two separate things: reading a framework's OpenTelemetry GenAI spans to report its steps (for a framework with no
adapter), and exporting the agent's own metrics under SAM's names. Both need the `otel` extra:

```bash
pip install "sam-link-sdk[otel]"
```

## Any framework with OpenTelemetry GenAI spans

Many frameworks trace themselves with the OpenTelemetry GenAI semantic conventions: a `chat` span per model call and an
`execute_tool` span per tool call. `sam_link_sdk.otel_steps` reads those spans and reports the same model and tool steps
and token usage the dedicated adapters report, so a framework without an adapter joins with step-level visibility. Its
names are the adapters' own:

```python
from sam_link_sdk.otel_steps import run_agent, step_reporter

step_reporter()  # once, before the framework creates its tracer: adds the span processor to the SDK TracerProvider


async def handle(ctx):
    return await run_agent(ctx, my_agent.run)  # my_agent.run(ctx.text); my_agent: any framework with GenAI spans
```

`run_agent(ctx, run, *, input=None)` calls `run(<the task's text, or input>)`, awaited if it returns an awaitable, and
returns what `run` returns.

`examples/claims_triage_otel.py` is the claims-triage agent on Strands with no sam-link-sdk adapter, joined only through
Strands' own spans: `uv run --extra strands --extra otel python examples/claims_triage_otel.py`.

- **When to use it:** for a framework sam-link-sdk has no adapter for, or to try one before writing an adapter. Use the
  dedicated adapter where there is one: it can do what spans cannot (below), and it reports a tool when it starts.
- **What it cannot do:** spans record what already happened, so the generic adapter cannot ask the user before a tool
  runs, refuse a call to a tool the user lacks the scopes for, or tell a tool its call id. It is the one place the
  hooks do not enforce a declared tool scope: `run_agent` logs a warning when the agent declares tool scopes. A tool
  guarded with a framework adapter's `with_scopes` (a Strands `@tool`, say) still refuses such a user itself, since
  `run_agent` runs the agent with its task current ([security.md](security.md#required-scopes-rbac)); otherwise the
  tool runs, and its run is audited as any other. Use the framework's own tool hook with `ctx.request_approval` and
  `ctx.check_tool`, and give the model peer agents with a tool of your own that calls `ctx.call_agent(...)`.
  Approvals, scopes and peer tools still need these thin helpers.
- **Routing:** `run_agent` binds the run to its task. A span started under it belongs to that task, and so does
  every span below it, on any thread and even after its parent ended; spans from runs that are not bound are ignored,
  so concurrent tasks stay separate. Run blocking work through `asyncio.to_thread` inside the run: it copies the
  context, so its spans are routed too.
- **Timing:** a model call is announced when its span starts if the span already names the model (so the task's
  `max_llm_calls` stops the call over the limit), and answered when it ends. A tool is reported when its span ends,
  because frameworks add the arguments after the span starts. Spans that end after `run_agent` returns are not reported.
- **Content:** prompts, model output, tool arguments and results are reported only when the span carries them (Strands
  records them by default; Agent Framework only with `enable_sensitive_data`), with the adapters' credential redaction
  and 16 KB caps, and never logged. A value the instrumentation wrote as JSON is decoded, then read as the adapters read
  a tool's output: text holding a JSON object or array is parsed, other text stays text. Without them the steps carry
  names, call ids, outcomes and tokens.
- **Failures:** a tool span with status ERROR is a failed step (its error text, or the status description). A model
  call span with status ERROR is announced and not answered, as with the adapters, and its category goes in the task's
  error reply: from the span's `error.type` (an exception type such as `openai.RateLimitError`, or an HTTP status
  code), else the type of the exception recorded on the span, with the provider from `gen_ai.provider.name` (older
  `gen_ai.system`). An unrecognised type adds nothing, so the agent's classification of the exception the run raises
  stands.
- **Two instrumentations:** a `chat` span inside a `chat` span (a framework span around a client library's) is one
  model call and is counted once.
- **Checked against:** opentelemetry-sdk 1.45.2, Strands Agents 1.61.1 (its default conventions and
  `gen_ai_latest_experimental`) and Microsoft Agent Framework 1.22.0: the tests run each with its own telemetry and no
  adapter and get the same steps as its dedicated adapter. ADK 2.14's tool span attributes are read too. The attribute
  names (and the older ones still emitted: `gen_ai.usage.prompt_tokens`, Strands' `gen_ai.choice` events, flattened
  `gen_ai.completion.<i>.*`) are listed in the `sam_link_sdk.otel_steps` module docstring.
- **Tracer provider:** `step_reporter()` adds the processor to the global SDK `TracerProvider` (once per provider) and
  returns it, creating and setting one if none is set yet; pass `step_reporter(provider)` for the provider your
  framework traces with. Exporters you add to the
  provider are unaffected.

## The agent's metrics

SAM agents export metrics from their own process, and SAM derives none from an agent's step events, so an SDK
agent's model calls are missing from those dashboards unless it exports them too. `AgentConfig(metrics=OtelMetrics())`
(`sam_link_sdk.metrics`) records the agent-side metrics under SAM's names, units, attributes and histogram buckets:

- `sam.gen_ai.tokens.used` (by model, input/output, `component.name` = `agent-<display name>`);
- `sam.gen_ai.client.operation.duration` (by model and `error.type`);
- `sam.operation.duration` for the task (`type=agent`) and each tool (`type=tool`);
- `sam.agent.active_executions`.

`otlp_meter_provider()` sets up OTLP/HTTP export with the standard `OTEL_EXPORTER_OTLP_*` and
`OTEL_RESOURCE_ATTRIBUTES` settings; any other OpenTelemetry meter provider works too. Labels are names and categories
only. SAM's `owner.id` label is off by default there and is not sent. `OTEL_SDK_DISABLED=true`, which
`claims_triage_crewai.py` sets to keep CrewAI's own telemetry off, turns these metrics off too;
`CREWAI_DISABLE_TELEMETRY=true` alone is enough for CrewAI. (SAM exports metrics and logs over OTLP, no traces.)
