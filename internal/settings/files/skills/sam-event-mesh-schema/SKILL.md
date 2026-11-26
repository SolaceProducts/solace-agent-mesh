---
name: sam-event-mesh-schema
description: Event Mesh Entrypoint configuration schema — event handlers, output handlers, broker config, acknowledgment policy, and annotated examples for event-driven entrypoints.
---

Solace Agent Mesh Event Mesh Entrypoint configuration field reference and examples. An event mesh entrypoint uses the standard entrypoint schema with event-mesh-specific fields.

> **Note:** System fields like `namespace`, `session_service`, and `artifact_service` are injected by the platform at deployment time and must not be included in generated configs.

For **design guidance** (when to use Event Mesh entrypoints, common patterns) see `sam-event-mesh-design`. For the **full entrypoint schema** see `sam-gateway-schema`. For **core concepts** see `sam-concepts`.

## Config Structure

Event mesh entrypoint configs use the standard `apps[].app_config` structure:

```yaml
apps:
  - name: order_events_entrypoint
    app_config:
      event_handlers: [...]               # Required — how to process incoming events
      output_handlers: [...]              # Optional — where to publish responses
      acknowledgment_policy: {...}        # Optional — delivery guarantee settings
      event_mesh_broker_config: {...}     # Required — all four coordinates
```

**MIME type:** `application/vnd.sam-gateway-config+yaml`
**Component type detection:** The presence of `event_handlers` marks this as an entrypoint config.

---

## 1. Data Plane Broker

**The broker coordinates are always explicit — never inherited from the Solace Agent Mesh platform broker.** Always include `event_mesh_broker_config`:
Include **all four** fields — `broker_url`, `broker_vpn`, `broker_username`,
`broker_password` — with `<<__SAM_REQUIRED__>>` for any value you don't know.
Never emit a password-only block and never omit the block.

That shape saves whether or not the deployment has a system broker:

- **No system broker** (local, embedded or desktop): the platform ignores the
  placeholders and the entrypoint connects to the built-in broker. It deploys as-is.
- **System broker configured** (every cluster): every field must be filled with real
  values before the entrypoint can deploy. Deploy rejects any remaining placeholder, and
  that is not something you can fix afterwards, so hand off:
  - Ask the user for `broker_url`, `broker_vpn` and `broker_username` (not secret) and
    write them in.
  - If the user gives you `broker_password` in the chat, write it in; a value typed in
    the chat is always acceptable. Otherwise leave it `<<__SAM_REQUIRED__>>`.
  - In your final message, name each broker field still holding a placeholder and tell
    the user to fill it in on the **Entrypoints** page before deploying.

A password-only bag is NOT a safe shortcut. Where a system broker is configured,
validation rejects it with
`broker connection requires fields: broker_url, broker_vpn, broker_username`. The
platform does not fill those in from the system broker.

```yaml
event_mesh_broker_config:
  broker_url: "<<__SAM_REQUIRED__>>"       # Required. tcp://, tcps://, ws://, wss://
  broker_vpn: "<<__SAM_REQUIRED__>>"       # Solace VPN name
  broker_username: "<<__SAM_REQUIRED__>>"  # Broker username
  broker_password: "<<__SAM_REQUIRED__>>"  # Never inherited from the platform broker
```

This is the **external** Solace broker where business events flow (the data plane). It is separate from the Solace Agent Mesh control plane broker.

**Use `<<__SAM_REQUIRED__>>` for every credential you do not have — never hardcode them, and never write `${VAR}`.**

`${VAR}` substitution belongs to file-based SAM configs loaded by `sam run`. An
entrypoint you author here is persisted to the platform database instead, where
nothing expands it: the literal string `${BROKER_URL}` reaches validation and is
rejected with *"broker_url: Must be a valid broker URL"*. Without a system broker,
`<<__SAM_REQUIRED__>>` values are ignored and the built-in broker is used; only a
**real** `broker_url` switches the entrypoint to a remote broker, and then all four
fields need real values in every deployment mode.

---

## 2. Event Handlers

Each event handler defines how to process incoming messages from one or more topic subscriptions.

```yaml
event_handlers:
  - name: order_handler              # Required. Unique handler identifier.
    subscriptions:                   # Required. Topics to subscribe to.
      - topic: "orders/created/>"    # Solace topic pattern (wildcards: * = one level, > = one+)
        qos: 1                       # 0 = direct (best effort), 1 = guaranteed
    input_expression: "input.payload:"  # How to extract agent input from message
    payload_format: json             # json | text | yaml | csv (default: json)
    target_agent_name: OrderAgent    # Target agent name (or use target_workflow_name)
    on_success: result_handler       # Output handler name for success (optional)
    on_error: error_handler          # Output handler for errors (optional); see below
    default_user_identity: "system"  # User identity for the A2A task (default: "system")
```

`on_error` receives, for every rule, JSON-RPC errors — including `-32600` when the caller lacks the target agent's `required_scopes` — and an event whose `user_identity_expression` resolves to no identity while the rule sets no `default_user_identity`. For a rule that targets a **workflow** it also receives failed, rejected and canceled runs; without an `on_error` handler those runs go to `on_success`. For a rule that targets an **agent**, every final result goes to `on_success` — including a run marked failed after a tool failure the agent recovered from.

### Target Routing (mutually exclusive — use exactly one)

- `target_agent_name` — fixed agent name
- `target_workflow_name` — fixed workflow name

> **Builder entrypoints must use a FIXED target.** Set exactly one of
> `target_agent_name` or `target_workflow_name` per handler. The dynamic
> `target_agent_name_expression` / `target_workflow_name_expression` variants
> (where the destination is computed from a SAC expression) are **not supported
> for Builder-created entrypoints** and are rejected at creation time — do not
> generate them.

### Context Forwarding

Pass data from the incoming event through to output handlers:

```yaml
    forward_context:
      order_id: "input.payload:order_id"
      customer: "input.payload:customer_name"
      source_topic: "input.topic:"
```

Forwarded values are available in output handlers via `user_data.forward_context:<key>`.

### Per-Handler Acknowledgment Override

Override the entrypoint-level policy for specific handlers:

```yaml
    acknowledgment_policy:
      mode: on_completion
      timeout_seconds: 120
```

### Structured Invocation (optional)

Validate an agent target's input and output with JSON Schemas. Publish the validated object with `payload_expression: "task_response:structured_output"`; a result that still fails validation after retries goes to `on_error`. A workflow target ignores these schemas and applies its own:

```yaml
    structured_invocation:
      input_schema:
        type: object
        properties:
          order_id: { type: string }
        required: ["order_id"]
      output_schema:
        type: object
        properties:
          status: { type: string }
```

### Artifact Extraction (optional)

Extract artifacts (files, binary data) from incoming messages:

```yaml
    artifact_processing:
      extract_artifacts_expression: "input.payload:attachments"
      artifact_definition:
        filename: "list_item:name"
        content: "list_item:data"
        mime_type: "list_item:type"
        content_encoding: "base64"
```

---

## 3. Output Handlers

Define how agent responses are published back to the event mesh:

```yaml
output_handlers:
  - name: result_handler              # Required. Referenced by event handlers.
    topic_expression: "static:orders/processed"    # Required. Where to publish.
    payload_expression: "input.payload:text"        # What to include in payload.
    payload_format: json               # json | text | yaml | csv (default: json)
```

**Delivery mode, Solace headers and user properties** (Go runtime only):
```yaml
    delivery_mode: guaranteed                  # direct (default) | guaranteed: wait for the broker ack
    headers:                                   # Solace Micro-Integration header names
      solace_dmqEligible: true                 # dmqEligible, timeToLive, expiration need guaranteed
      solace_timeToLive: 30000                 # ms
      solace_priority: 7                       # 0-255
      solace_correlationId: "template:{{text://user_data.forward_context:corr}}"
    user_properties:
      source: "static:sam"
```
A number or boolean is a literal; a string is an expression, so a literal string needs `static:`. Other headers: `solace_expiration` (epoch ms), `solace_applicationMessageId`, `solace_applicationMessageType`, `solace_senderId`, `solace_sequenceNumber`, `solace_httpContentEncoding`. `solace_isReply` and `solace_senderTimestamp` are rejected.

**Pick the field that holds the answer.** `input.payload:text` is an agent's final reply, which is right for a rule that targets an agent. For a rule that targets a workflow, `text` is only a status line (`Workflow "X" completed successfully.`); the workflow's result — its `output_mapping` object — is the first data part. Publish it with `payload_expression: "input.payload:data[0]"` (and `payload_format: json`), or one field of it with `input.payload:data[0].action`. `data[0]` is the Go runtime's bracket index; the Python runtime indexes lists with dotted segments (`data.0`) and nests each entry's value one level down (`data.0.data`).

**Give a workflow rule an `on_error` handler, with its own payload.** Without one a failed run is published on `on_success` with no data part, and `data[0]` publishes `null`; reusing the `data[0]` handler as `on_error` publishes `null` too. The `on_error` payload carries only the error:

- `input.payload:text` — the failure message.
- `input.payload:a2a_task_response.error` (with `payload_format: json`) — `{code, message}`. The code is `-32603` for a failed, rejected or canceled workflow run, `-32003` for an unresolved `user_identity_expression`, and the JSON-RPC error code as received otherwise (`-32600` when the caller lacks the agent's `required_scopes`).

### Dynamic Topics

Use forwarded context data in topic expressions:

```yaml
  - name: order_result
    topic_expression: "template:orders/{{text://user_data.forward_context:order_id}}/result"
    payload_expression: "input.payload:text"
```

### Output Transforms

Modify the response payload before publishing:

```yaml
  - name: transformed_output
    topic_expression: "static:results/processed"
    payload_expression: "user_data.output:"
    output_transforms:
      - type: copy
        source_expression: "input.payload:text"
        dest_expression: "user_data.output:response"
      - type: copy
        source_expression: "user_data.forward_context:order_id"
        dest_expression: "user_data.output:order_id"
```

### Output Schema Validation

Validate output before publishing:

```yaml
  - name: validated_output
    topic_expression: "static:results"
    payload_expression: "input.payload:text"
    output_schema:
      type: object
      properties:
        status: { type: string, enum: ["success", "failure"] }
    on_validation_error: "log"    # "log" or "drop"
```

---

## 4. Acknowledgment Policy

Entrypoint-level delivery guarantee settings:

```yaml
acknowledgment_policy:
  mode: on_receive              # "on_receive" (default) or "on_completion"
  timeout_seconds: 300          # Timeout for on_completion (default: 300)
  on_failure:
    action: nack                # "nack" (default) or "ack"
    nack_outcome: rejected      # "rejected" (redeliver) or "failed" (DLQ)
```

- `on_receive` — ACK immediately (fire-and-forget from broker's perspective)
- `on_completion` — ACK after agent finishes (guaranteed delivery, requires `qos: 1`)

Under `on_completion`, a failed workflow run whose failure is published is ACKed, never NACKed. `on_failure` applies to a JSON-RPC error, a failed publish, the acknowledgment timeout, or an event that never becomes a task (a rejected or unresolved identity, or a target, artifact, input expression or submission that fails).

---

## 5. SAC Expression Quick Reference

| Expression | Description |
|-----------|-------------|
| `input.payload:` | Entire message payload |
| `input.payload:field` | Extract a field from JSON payload |
| `input.payload:field.nested` | Dot notation for nested fields |
| `input.payload:field[0]` | List element by index (Go runtime syntax; the Python runtime uses `field.0`) |
| `input.topic:` | Full topic string |
| `input.topic_levels:<N>` | Topic level at index N (split by `/`) |
| `input.user_properties:<key>` | Solace user property by key |
| `user_data.forward_context:<key>` | Forwarded context value |
| `static:<value>` | Literal string value |
| `template:...{{encoding://expr}}...` | Template with substitution blocks |
| `text://expr` | String conversion (default encoding) |
| `json://expr` | JSON marshaling |

---

## 6. Complete Examples

### Fire-and-Forget Monitoring

```yaml
apps:
  - name: alert_events_entrypoint
    app_config:
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

### With Response Routing and Forward Context

```yaml
apps:
  - name: order_events_entrypoint
    app_config:
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

### Targeting a Workflow

```yaml
apps:
  - name: sensor_events_entrypoint
    app_config:
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
      output_handlers:
        - name: sensor_result
          topic_expression: "template:sensors/{{text://user_data.forward_context:sensor_id}}/analysis"
          payload_expression: "input.payload:data[0]"   # the workflow's output_mapping object
          payload_format: json
        - name: sensor_error
          topic_expression: "template:sensors/{{text://user_data.forward_context:sensor_id}}/errors"
          payload_expression: "input.payload:text"      # the failure message
```

---

## 7. Field Interaction Rules

1. **`event_mesh_broker_config` + `event_handlers` make it an Event Mesh entrypoint.** Their presence distinguishes an event mesh entrypoint from an HTTP SSE entrypoint.

2. **`event_handlers` reference `output_handlers` by name.** `on_success` and `on_error` values must match an output handler's `name`.

3. **Target routing is a single FIXED target.** Use exactly one of `target_agent_name` or `target_workflow_name` per handler. Dynamic-target expressions (`target_agent_name_expression` / `target_workflow_name_expression`) are not supported for Builder-created entrypoints and are rejected at creation — never emit them.

   Handler-level config keys are snake_case only. The camelCase entrypoint-form names (`targetWorkflowName`, `targetAgent`, `inputExpression`, `promptTemplate`) and a nested `target:` object are unknown inside `event_handlers` and are rejected. A workflow target has no prompt template; always give it an `input_expression` — raw (`"input.payload:"`) or a `template:` expression — because without one the workflow receives empty input.

4. **`on_completion` requires `qos: 1`.** With `qos: 0` (direct messaging), there's nothing to acknowledge.

5. **`forward_context` connects input to output.** Data forwarded from event handlers is available in output handlers via `user_data.forward_context:<key>`.

6. **Target agent must be autonomous.** Do not use interactive tools (`ask_user_question`). Set `supports_streaming: false`. Include explicit error handling in the instruction.
