---
name: sam-event-mesh-design
description: Event Mesh Entrypoint design guide — when to use event mesh entrypoints, event handler patterns, subscription topics, input expressions, acknowledgment strategies, target agent design, and common patterns.
tags:
  - builder
  - design
  - event-mesh
  - entrypoint
  - sam
---

# Solace Agent Mesh Event Mesh Entrypoint Design Guide

> **Note:** System fields like `namespace`, `session_service`, and `artifact_service` are injected by the platform at deployment time and must not be included in generated configs.

## When to Use an Event Mesh Entrypoint

An Event Mesh Entrypoint connects an external Solace PubSub+ broker to a Solace Agent Mesh agent or workflow. Use an event mesh entrypoint when:

- **Events arrive from an external system.** Messages on a Solace broker — IoT sensor readings, application events, database change notifications, webhook payloads, order submissions, alert notifications — anything that arrives asynchronously.
- **Processing should be autonomous.** No user is in the loop. The event arrives, the agent processes it, and optionally a response is published back to the broker.
- **You need to bridge the event mesh to AI.** The entrypoint subscribes to data plane topics, transforms incoming messages, submits A2A tasks to agents/workflows, and publishes responses.

**Signals that an event mesh entrypoint is needed:** The user says "when X happens", "listen for", "react to", "process incoming", "subscribe to", "monitor events", "whenever a message arrives". Any description of autonomous event-driven processing without a user in the chat loop.

---

## Event Mesh Entrypoint vs. Agent vs. Workflow

| Scenario | Components |
|----------|-----------|
| User sends a message, agent responds | Agent alone |
| External event arrives, agent processes it | **Event Mesh Entrypoint** + Agent |
| External event arrives, multi-step processing needed | **Event Mesh Entrypoint** + Workflow (with agent nodes) |
| Multiple event types to different agents | **Event Mesh Entrypoint** with multiple event handlers |
| User asks agent to do something that involves events | Agent with tools (not an Event Mesh entrypoint) |

**Key distinction:** Event mesh entrypoints are for events that arrive without a user initiating them. If a user is in the conversation and asks the agent to do something, that's just an agent with the right tools — no entrypoint needed.

---

## Event Handler Patterns

Each event mesh entrypoint has one or more **event handlers**. Each handler defines a subscription topic pattern, how to extract agent input, and where to route it.

### Subscription Topic Patterns

Solace topics use `/` as a level separator with two wildcards:

| Pattern | Meaning | Example |
|---------|---------|---------|
| `*` | Matches exactly one level | `orders/*/created` matches `orders/US/created` but not `orders/US/east/created` |
| `>` | Matches one or more levels | `orders/created/>` matches `orders/created/US` and `orders/created/US/east` |

### QoS Selection

- **QoS 0 (direct):** Best-effort delivery. Use for monitoring, alerting, and situations where occasional message loss is acceptable. Messages are not persisted.
- **QoS 1 (guaranteed):** Persisted delivery with acknowledgment. Use for transactional processing where every message must be handled. Default and recommended for most handlers.

### Design Guidelines

- Be as specific as possible. `orders/created/>` is better than `orders/>` if you only care about creation events.
- Multiple handlers can share an entrypoint if they use the same data plane broker.
- Consider topic hierarchy for future flexibility. `company/department/event_type/region` allows selective subscription at any level.

---

## Input Expression Patterns

Input expressions extract the agent's input from the incoming broker message. They use SAC (Solace AI Connector) expression syntax.

### Common Patterns

| Expression | Use Case |
|-----------|----------|
| `input.payload:` | Pass the entire JSON payload to the agent |
| `input.payload:data` | Extract a specific field |
| `input.payload:event.details` | Navigate nested fields with dots |
| `template:Process this order: {{json://input.payload:}}` | Wrap the payload in a prompt |
| `template:Analyze sensor reading: temp={{text://input.payload:temperature}}, humidity={{text://input.payload:humidity}}` | Construct a natural-language prompt from structured data |

### Guidance

- **For agents with good instructions:** Use `input.payload:` to pass the raw payload and let the agent's instruction guide how to interpret it. Simpler and more flexible.
- **For structured workflows:** Use specific field extraction (`input.payload:data`) to pass clean, focused input.
- **For maximum clarity:** Use `template:` to construct a human-readable prompt that gives the agent context about what it's looking at.

---

## Response Routing

### Fire-and-Forget (No Response)

Omit `on_success` and `on_error` entirely. The handler processes the event and discards the result. Good for:
- Logging and monitoring
- Updating internal state
- Side effects where the caller doesn't need a response

### Echo-Back (Fixed Response Topic)

Publish the agent's response to a known topic via an output handler:
```yaml
output_handlers:
  - name: order_response
    topic_expression: "static:orders/processed"
    payload_expression: "input.payload:text"
```

When the rule targets a **workflow**, publish its result instead: `text` is only a status line (`Workflow "X" completed successfully.`), and the workflow's `output_mapping` object is the first data part:
```yaml
  - name: decision_response
    topic_expression: "static:bank/decisions"
    payload_expression: "input.payload:data[0]"
    payload_format: json
  - name: decision_error
    topic_expression: "static:bank/decisions/errors"
    payload_expression: "input.payload:text"
```
Wire both on the workflow rule (`on_success: decision_response`, `on_error: decision_error`). A failed, rejected or canceled workflow run goes to `on_error`; without an `on_error` handler it goes to `on_success` with no data part, and `data[0]` publishes `null`. Never reuse the `data[0]` handler as `on_error` — it publishes `null` too. `data[0]` is the Go runtime's bracket index; the Python runtime indexes lists with dotted segments (`data.0`) and nests each entry's value one level down (`data.0.data`).

### What `on_error` Receives

- **Workflow target:** failed, rejected and canceled runs, JSON-RPC errors, and an unresolved identity.
- **Agent target:** JSON-RPC errors and an unresolved identity only. Every final result of an agent run goes to `on_success` — including a run marked failed after a tool failure the agent recovered from.

A JSON-RPC error includes `-32600` when the caller lacks the target agent's `required_scopes`. An unresolved identity is a `user_identity_expression` that resolves to nothing while the rule sets no `default_user_identity`; an identity the entrypoint rejects for another reason publishes nothing.

The `on_error` payload carries only the error: `text` is the failure message, and `a2a_task_response.error` is `{code, message}` — `-32603` for a failed, rejected or canceled workflow run, `-32003` for an unresolved identity, and the JSON-RPC error code as received otherwise. Publish the message with `payload_expression: "input.payload:text"`, or the error object with `payload_expression: "input.payload:a2a_task_response.error"` and `payload_format: json`.

### Dynamic Response Topics

Use forward_context to carry data from the incoming event through to the response topic:
```yaml
event_handlers:
  - name: orders
    # ...
    forward_context:
      order_id: "input.payload:order_id"
      region: "input.payload:region"
    on_success: order_response

output_handlers:
  - name: order_response
    topic_expression: "template:orders/{{text://user_data.forward_context:region}}/{{text://user_data.forward_context:order_id}}/result"
    payload_expression: "input.payload:text"
```

### Error Segregation

Route errors to a separate topic for dead-letter handling or alerting:
```yaml
event_handlers:
  - name: orders
    # ...
    on_success: order_response
    on_error: order_error

output_handlers:
  - name: order_response
    topic_expression: "static:events/processed"
    payload_expression: "input.payload:text"
  - name: order_error
    topic_expression: "static:events/errors"
    payload_expression: "input.payload:text"
```

---

## Acknowledgment Strategies

Acknowledgment controls when the broker considers a message successfully delivered.

### on_receive (Default)

The message is ACKed immediately when the entrypoint receives it. Processing happens asynchronously. If the agent fails, the message is already gone.

**Use for:** Monitoring, alerting, non-critical processing, high-throughput scenarios where occasional loss is acceptable.

### on_completion

The message is ACKed only after the agent or workflow finishes and its result is published. A failed workflow run whose failure is published is ACKed too, never NACKed, so a deterministic failure is not redelivered into an endless re-run. A JSON-RPC error, a failed publish, the acknowledgment timeout, or an event that never becomes a task (a rejected or unresolved identity, or a target, artifact, input expression or submission that fails) applies `on_failure` instead: by default the message is NACKed and the broker can redeliver it.

**Use for:** Transactional processing, order handling, financial operations, any scenario where every message must be processed exactly once (or retried until successful).

**Important:** `on_completion` only makes sense with `qos: 1` (guaranteed delivery). With `qos: 0`, there's nothing to acknowledge.

### Timeout Considerations

The `timeout_seconds` field (default 300) sets how long to wait for the agent to complete before NACKing. Set this based on the expected processing time:
- Simple agent processing: 60-120 seconds
- Workflow with multiple steps: 300-600 seconds
- Complex multi-agent workflows: up to 900 seconds

---

## Designing the Target Agent

The agent behind an event mesh entrypoint operates autonomously — there is no user to ask questions or provide clarification. This has specific design implications:

### Must-Have Properties

1. **No interactive tools.** Do not include `general_agent_tools` (which contains `ask_user_question`). The agent must never try to ask a human for input.
2. **`supports_streaming: false`.** There is no user watching a stream. Set streaming to false to avoid unnecessary overhead.
3. **Explicit error handling in the instruction.** Tell the agent what to do when something goes wrong: retry, log, produce a structured error response, etc. Without a user to report errors to, the instruction must cover failure modes.
4. **Focused instruction.** The agent processes one type of event. Its instruction should be specific to that event type, not generic.

### Instruction Pattern for Event-Mesh-Backed Agents

```
You are an [event type] processor. You receive [description of incoming data]
and [what you do with it].

## Input Format
[Describe the expected payload structure]

## Processing Steps
[What the agent should do, step by step]

## Error Handling
- If [condition], respond with [structured error]
- If data is malformed, respond with a clear error message describing what's wrong
- Never ask for clarification — make your best judgment based on available data

## Output
[What the response should look like]
```

### Model Selection

- **Simple classification/routing:** Use a fast, cheap model (e.g., `openai/gpt-4o-mini`)
- **Complex analysis/reasoning:** Use a capable model (e.g., `openai/gpt-4o`, `anthropic/claude-sonnet-4-20250514`)
- **High-throughput events:** Favor speed over capability — the agent processes many events and latency matters

---

## Common Patterns

Every pattern below includes `event_mesh_broker_config` with all four fields — `broker_url`, `broker_vpn`, `broker_username`, `broker_password` — using `<<__SAM_REQUIRED__>>` for any value you don't know. Never emit a password-only block and never omit the block. When the deployment has no system broker (local, embedded or desktop), the platform ignores these placeholders and the entrypoint connects to the built-in broker. When a system broker is configured (a cluster), every field must be filled with real values before the entrypoint can deploy, so hand off: ask the user for `broker_url`, `broker_vpn` and `broker_username` (not secret) and write them in; accept `broker_password` if the user gives it to you in the chat, otherwise leave it `<<__SAM_REQUIRED__>>`; then, in your final message, name each broker field still holding a placeholder and tell the user to fill it in on the **Entrypoints** page before deploying. For a custom/remote broker, use the user's real `broker_url`/`broker_vpn`/`broker_username` and a placeholder only for what they haven't given you.

### 1. Simple Monitoring (Fire-and-Forget)

Listen for events and process them without publishing a response:

```yaml
apps:
  - name: alert_events_entrypoint
    app_config:
      # Always include all four broker_* fields; <<__SAM_REQUIRED__>> for any value
      # you don't know. Ignored without a system broker (built-in broker is used);
      # with one, the user must fill every field before the entrypoint can deploy.
      event_mesh_broker_config:
        broker_url: "<<__SAM_REQUIRED__>>"
        broker_vpn: "<<__SAM_REQUIRED__>>"
        broker_username: "<<__SAM_REQUIRED__>>"
        broker_password: "<<__SAM_REQUIRED__>>"
      event_handlers:
        - name: critical_alerts
          subscriptions:
            - topic: "alerts/critical/>"
              qos: 0
          input_expression: "template:Analyze this alert: {{json://input.payload:}}"
          target_agent_name: AlertAnalyzer
```

### 2. Order Processing with Response

Process orders and publish results back:

```yaml
apps:
  - name: order_events_entrypoint
    app_config:
      # Always include all four broker_* fields; <<__SAM_REQUIRED__>> for any value
      # you don't know. Ignored without a system broker (built-in broker is used);
      # with one, the user must fill every field before the entrypoint can deploy.
      event_mesh_broker_config:
        broker_url: "<<__SAM_REQUIRED__>>"
        broker_vpn: "<<__SAM_REQUIRED__>>"
        broker_username: "<<__SAM_REQUIRED__>>"
        broker_password: "<<__SAM_REQUIRED__>>"
      event_handlers:
        - name: new_orders
          subscriptions:
            - topic: "orders/created/>"
              qos: 1
          input_expression: "input.payload:"
          target_agent_name: OrderProcessor
          default_user_identity: "system"
          on_success: order_result
          on_error: order_error
          forward_context:
            order_id: "input.payload:order_id"
      output_handlers:
        - name: order_result
          topic_expression: "template:orders/{{text://user_data.forward_context:order_id}}/processed"
          payload_expression: "input.payload:text"
          payload_format: json
        - name: order_error
          topic_expression: "static:orders/errors"
          payload_expression: "input.payload:text"
      acknowledgment_policy:
        mode: on_completion
        timeout_seconds: 120
        on_failure:
          action: nack
          nack_outcome: rejected
```

### 3. IoT Data to Workflow

Route sensor data through a multi-step processing workflow:

```yaml
apps:
  - name: sensor_events_entrypoint
    app_config:
      # Always include all four broker_* fields; <<__SAM_REQUIRED__>> for any value
      # you don't know. Ignored without a system broker (built-in broker is used);
      # with one, the user must fill every field before the entrypoint can deploy.
      event_mesh_broker_config:
        broker_url: "<<__SAM_REQUIRED__>>"
        broker_vpn: "<<__SAM_REQUIRED__>>"
        broker_username: "<<__SAM_REQUIRED__>>"
        broker_password: "<<__SAM_REQUIRED__>>"
      event_handlers:
        - name: temperature_readings
          subscriptions:
            - topic: "sensors/temperature/>"
              qos: 1
          input_expression: "template:Temperature reading: {{json://input.payload:}}"
          target_workflow_name: SensorAnalysisPipeline
          on_success: sensor_result
          on_error: sensor_error
          forward_context:
            sensor_id: "input.payload:sensor_id"
            location: "input.payload:location"
      output_handlers:
        - name: sensor_result
          topic_expression: "template:sensors/{{text://user_data.forward_context:sensor_id}}/analysis"
          payload_expression: "input.payload:data[0]"   # the workflow's output_mapping object
          payload_format: json
        - name: sensor_error
          topic_expression: "template:sensors/{{text://user_data.forward_context:sensor_id}}/errors"
          payload_expression: "input.payload:a2a_task_response.error"   # {code, message}
          payload_format: json
```

### 4. Guaranteed Delivery with Structured I/O

Schema-validated processing with guaranteed delivery:

```yaml
apps:
  - name: payment_events_entrypoint
    app_config:
      # Always include all four broker_* fields; <<__SAM_REQUIRED__>> for any value
      # you don't know. Ignored without a system broker (built-in broker is used);
      # with one, the user must fill every field before the entrypoint can deploy.
      event_mesh_broker_config:
        broker_url: "<<__SAM_REQUIRED__>>"
        broker_vpn: "<<__SAM_REQUIRED__>>"
        broker_username: "<<__SAM_REQUIRED__>>"
        broker_password: "<<__SAM_REQUIRED__>>"
      acknowledgment_policy:
        mode: on_completion
        timeout_seconds: 300
        on_failure:
          action: nack
          nack_outcome: rejected
      event_handlers:
        - name: payments
          subscriptions:
            - topic: "payments/received/>"
              qos: 1
          input_expression: "input.payload:"
          payload_format: json
          target_agent_name: PaymentProcessor
          on_success: payment_result
          structured_invocation:
            input_schema:
              type: object
              properties:
                payment_id: { type: string }
                amount: { type: number }
                currency: { type: string }
              required: [payment_id, amount, currency]
      output_handlers:
        - name: payment_result
          topic_expression: "static:payments/processed"
          payload_expression: "input.payload:text"
          payload_format: json
```
