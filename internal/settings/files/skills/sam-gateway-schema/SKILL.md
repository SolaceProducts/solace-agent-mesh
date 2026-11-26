---
name: sam-gateway-schema
description: JSON Schema for Solace Agent Mesh entrypoints — HTTP SSE entrypoint, event mesh entrypoint, handlers, transforms, and examples.
---

> **Builder restriction:** HTTP SSE entrypoint configs are platform-managed and cannot be
> created through the Builder. The Builder only creates **Event Mesh Entrypoint** configs.
> For event-driven integration, use the `sam-event-mesh-schema` skill instead. This
> skill is reference documentation only — do not use it to generate HTTP SSE entrypoint
> configs in the Builder.

Solace Agent Mesh entrypoint configuration schema and practical guide. Covers both HTTP SSE (interactive chat) and Event Mesh (autonomous event-driven) entrypoints.

For **core concepts** see `sam-concepts`. For **design guidance** see `sam-best-practices`.

## Schema

```yaml
{{SCHEMA:gateway}}
```

---

## 1. Entrypoint Types

Solace Agent Mesh has two entrypoint types that serve different purposes:

| Type | Purpose | User Interaction | Config Indicator |
|------|---------|-----------------|------------------|
| **HTTP SSE** | Interactive chat via web UI or API | Users connect via browser/HTTP SSE | Has `fastapi_port`, no `event_handlers` |
| **Event Mesh** | Autonomous event processing | Events arrive from Solace broker | Has `event_handlers` |

Both types share the same base config structure. The presence of `event_handlers` distinguishes an event mesh entrypoint from an HTTP SSE entrypoint.

---

## 2. Common Fields (Both Entrypoint Types)

### Identity

> **Note:** System fields like `namespace`, `session_service`, `artifact_service`, and `model` are injected by the platform at deployment time and must not be included in generated configs.

```yaml
apps:
  - name: my_entrypoint
    app_config:
      gateway_id: "my-entrypoint"                       # Optional. Auto-generated as "gw-{UUID[:8]}".
      session_secret_key: "${SESSION_SECRET_KEY}"    # Secret for signing web session cookies.
```

### Session Service
Same options as agents. This is a system-injected field — do not include in generated configs.
```yaml
session_service:
  type: sql                                          # "memory" or "sql"
  database_url: "sqlite:///${SAM_DATA_DIR}/sam.db"   # SQLite or PostgreSQL
  default_behavior: PERSISTENT                       # PERSISTENT or RUN_BASED
```

- `memory` — development/testing, no persistence
- `sql` with SQLite — single-instance, desktop mode
- `sql` with PostgreSQL — production multi-instance

**Important:** Many entrypoint features require `type: sql` (persistent sessions). Features like task logging, prompt library, projects, and auto-title generation all require persistence.

### Artifact Service
Same options as agents. This is a system-injected field — do not include in generated configs.
```yaml
artifact_service:
  type: filesystem                                   # filesystem | memory | s3 | gcs
  base_path: "${SAM_DATA_DIR}/artifacts"
  artifact_scope: namespace                          # namespace | app | custom
```

### Agent Card Publishing
```yaml
card_publish_interval_seconds: 5    # How often to publish entrypoint card (seconds).
```
- `5` — development/testing (reliable discovery)
- `30` — production (default, less broker traffic)

### Agent Interaction
```yaml
system_purpose: "AI assistant for..."     # Injected into agent system prompts.
response_format: "..."                    # Response format guidance for agents.
enable_embed_resolution: true             # Enable «embed:filename» resolution (default true).
gateway_artifact_content_limit_bytes: 10000000  # Max artifact size for embed resolution (10MB).
```

### LLM Model (Optional)
Used for chat title generation and AI-assisted features. This is a system-injected field — do not include in generated configs.
```yaml
model:
  model: "${LLM_SERVICE_GENERAL_MODEL_NAME}"
  api_base: "${LLM_SERVICE_ENDPOINT}"
  api_key: "${LLM_SERVICE_API_KEY}"
```

### Task Logging
```yaml
task_logging:
  enabled: true      # Enable task event logging for STIM file export.
```

When enabled, all task events (messages, tool calls, status updates, artifacts) are recorded in the database. Required for the task logging UI feature and STIM file export.

---

## 3. HTTP SSE Entrypoint Configuration

The HTTP SSE entrypoint serves interactive chat via a web UI and REST/SSE API.

### HTTP Server
```yaml
fastapi_host: "127.0.0.1"        # Bind address. Use "0.0.0.0" for external access.
fastapi_port: 8800                # Port number. 0 = auto-assign.
cors_allowed_origins:             # CORS allowed origins (list of strings).
  - "http://localhost:3000"
  - "https://myapp.com"
sse_max_queue_size: 200           # Max SSE event queue per connection.
```

### Limits
```yaml
gateway_max_upload_size_bytes: 50000000       # Max single file upload (50MB).
gateway_max_project_size_bytes: 50000000      # Max project import size (50MB).
gateway_max_batch_upload_size_bytes: 50000000 # Max batch upload size (50MB).
gateway_max_project_description_chars: 1000   # Max characters in a project's description.
gateway_max_project_instructions_chars: 32000 # Max characters in a project's instructions.
```

### User Identity
```yaml
default_user_identity: "sam_dev_user"    # Fallback user ID when no auth.
force_user_identity: "test_user"         # Override ALL user identities (dev only).
```

### Authentication

#### No Auth (development)
```yaml
frontend_use_authorization: false         # Default. No auth required.
default_user_identity: "sam_dev_user"
```

#### External Auth (OAuth/OIDC)
```yaml
frontend_use_authorization: true
external_auth_service_url: "https://auth.example.com/validate"
external_auth_provider: "generic"         # Auth provider type.
external_auth_callback_uri: "/auth/callback"
frontend_auth_login_url: "https://auth.example.com/login"
frontend_redirect_url: "https://myapp.com/callback"
frontend_server_url: "https://myapp.com"  # Entrypoint's public URL.
```

### Frontend Customization
```yaml
frontend_welcome_message: "Welcome to the AI assistant!"
frontend_app_name: "Solace Agent Mesh"                  # App name shown as the browser tab title.
frontend_collect_feedback: true           # Enable thumbs up/down feedback.
```

### Data Retention
```yaml
data_retention:
  enabled: true                 # Enable automatic cleanup (default true).
  task_retention_days: 90       # Keep tasks for 90 days.
  feedback_retention_days: 90   # Keep feedback for 90 days.
  cleanup_interval_hours: 24    # Run cleanup every 24 hours.
  batch_size: 1000              # Records per cleanup batch.
```

### Platform Service (Enterprise)
```yaml
platform_service:
  url: "${PLATFORM_SERVICE_URL}"  # Platform service URL for builder/deployment.
```

---

## 4. Event Mesh Entrypoint Configuration

The event mesh entrypoint bridges external Solace PubSub+ event meshes to Solace Agent Mesh agents. It subscribes to data plane topics, transforms incoming messages, submits A2A tasks, and publishes responses.

### Data Plane Broker

**Always include `event_mesh_broker_config`** with all four fields — `broker_url`, `broker_vpn`, `broker_username`, `broker_password` — using `<<__SAM_REQUIRED__>>` for any value you don't know. Never emit a password-only block and never omit the block.
- **No system broker** (local, embedded or desktop): the platform ignores these placeholders and the entrypoint connects to the built-in broker.
- **System broker configured** (a cluster): every field must be filled with real values before the entrypoint can deploy, so hand off. Ask the user for `broker_url`, `broker_vpn` and `broker_username` (not secret) and write them in. If the user gives you `broker_password` in the chat, write it in — a value typed in the chat is always acceptable — otherwise leave it `<<__SAM_REQUIRED__>>`. In your final message, name each broker field still holding a placeholder and tell the user to fill it in on the **Entrypoints** page before deploying.
- **Custom/remote broker**: use the user's real `broker_url`/`broker_vpn`/`broker_username` and a placeholder only for what they haven't given you.

```yaml
event_mesh_broker_config:
  broker_url: "<<__SAM_REQUIRED__>>"      # Required. tcp://, tcps://, ws://, wss://
  broker_vpn: "<<__SAM_REQUIRED__>>"       # Solace VPN name.
  broker_username: "<<__SAM_REQUIRED__>>"
  broker_password: "<<__SAM_REQUIRED__>>"
```

### Acknowledgment Policy

Controls when incoming messages are acknowledged:
```yaml
acknowledgment_policy:
  mode: "on_receive"              # "on_receive" (default) or "on_completion"
  timeout_seconds: 300            # Timeout for on_completion mode.
  on_failure:
    action: "nack"                # "nack" (default) or "ack"
    nack_outcome: "rejected"      # "rejected" (default) or "failed"
```

- `on_receive` — ACK immediately when message is received (fire-and-forget)
- `on_completion` — ACK after the agent completes processing (guaranteed delivery)

### Event Handlers

Event handlers define how incoming messages are routed to agents:

```yaml
event_handlers:
  - name: orders                              # Required. Unique handler name.
    subscriptions:                            # Required. Topics to subscribe to.
      - topic: "orders/created/>"             # Solace wildcard: * = one level, > = one+
        qos: 1                                # 0 = direct, 1 = guaranteed
    input_expression: "input.payload:data"    # How to extract agent input (see Section 5).
    payload_format: json                      # json | text | yaml | csv (default: json)
    payload_encoding: utf-8                   # Default: utf-8
    target_agent_name: OrderAgent             # Fixed agent target.
    on_success: order_response                # Output handler for success.
    on_error: error_handler                   # Output handler for errors (and, for a workflow target, failed runs).
    default_user_identity: "system"           # User identity for the task.
```

**Target routing** (use one — mutually exclusive):
- `target_agent_name` — fixed agent name
- `target_agent_name_expression` — dynamic agent name from expression
- `target_workflow_name` — fixed workflow name
- `target_workflow_name_expression` — dynamic workflow name from expression

**User identity:**
- `user_identity_expression` — extract user ID from message
- `default_user_identity` — fallback user ID

**Context forwarding** — pass data from the incoming message through to the output handler:
```yaml
    forward_context:
      order_id: "input.payload:id"
      customer: "input.payload:customer_name"
```

Forwarded context is available in output handler expressions via `user_data.forward_context:<key>`.

**Artifact extraction** — extract artifacts from incoming messages:
```yaml
    artifact_processing:
      extract_artifacts_expression: "input.payload:attachments"
      artifact_definition:
        filename: "input.payload:attachments.filename"
        content: "input.payload:attachments.data"
        mime_type: "input.payload:attachments.type"
        content_encoding: base64              # base64 | base64url | base64raw | base64rawurl | utf-8
```

**Structured invocation** — validate input/output with schemas:
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

**Per-handler acknowledgment** — override the entrypoint-level ACK policy:
```yaml
    acknowledgment_policy:
      mode: on_completion
      timeout_seconds: 600
```

### Output Handlers

Output handlers define how agent responses are published back to the event mesh:

```yaml
output_handlers:
  - name: order_response                       # Required. Referenced by event handlers.
    topic_expression: "static:orders/processed" # Required. Topic to publish to (see Section 5).
    payload_expression: "input.payload:text"    # What to include in the payload.
    payload_format: json                        # json | text | yaml | csv
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

**Dynamic topics** using forwarded context:
```yaml
    topic_expression: "template:orders/{{text://user_data.forward_context:order_id}}/result"
```

**Output transforms** — modify the output payload before publishing:
```yaml
    output_transforms:
      - type: copy                              # copy | accumulate | filter
        source_expression: "input.payload:text"
        dest_expression: "user_data.output:response"
      - type: copy
        source_expression: "user_data.forward_context:order_id"
        dest_expression: "user_data.output:order_id"
```

**Output schema validation:**
```yaml
    output_schema:
      type: object
      properties:
        status: { type: string }
        result: { type: object }
    on_validation_error: "log"                 # "log" or "drop"
```

---

## 5. Expression Syntax (SAC Expressions)

Event mesh entrypoints use SAC (Solace AI Connector) expressions to extract, transform, and route data. These are used in `input_expression`, `topic_expression`, `payload_expression`, `forward_context`, and other expression fields.

### Data Access Expressions

| Expression | Description |
|-----------|-------------|
| `input.payload:<path>` | Navigate JSON payload. `input.payload:` = entire payload. |
| `input.payload:field.nested` | Dot notation for nested fields. |
| `input.payload:items[0].id` | Array index access (0-based, negative for from-end). |
| `input.topic:` | Full topic string. |
| `input.topic_levels:<index>` | Topic level at index (split by `/`). |
| `input.user_properties:<key>` | Solace user property by key. |
| `user_data.<name>:<path>` | Named data store (populated by transforms or forward_context). |
| `static:<value>` | Literal string value. |
| `list_item:<path>` | Current item during list iteration. |

### Template Expressions

Prefix: `template:` — supports `{{encoding://expression}}` blocks within a string.

```
template:Hello {{text://input.payload:name}}!
template:Order {{text://user_data.forward_context:order_id}} processed
template:Data: {{json://input.payload:data}}
```

**Supported encodings:**
- `text://` — string conversion (default if no encoding specified)
- `json://` — JSON marshaling
- `yaml://` — YAML marshaling
- `base64://` — base64 encoding
- `datauri:<mimetype>://` — data URI with base64

**Shorthand:** `{{input.payload:name}}` is equivalent to `{{text://input.payload:name}}`.

### Static Expressions

Prefix: `static:` — literal string value, no processing.

```
static:orders/processed
static:application/json
```

### Examples

```yaml
# Extract a field from JSON payload
input_expression: "input.payload:data"

# Extract entire payload
input_expression: "input.payload:"

# Use a template to construct agent input
input_expression: "template:Process this order: {{json://input.payload:}}"

# Fixed topic
topic_expression: "static:responses/orders"

# Dynamic topic from forwarded context
topic_expression: "template:responses/{{text://user_data.forward_context:order_id}}"

# Dynamic topic from input topic levels
topic_expression: "template:responses/{{text://input.topic_levels:1}}/done"

# Extract agent response text
payload_expression: "input.payload:text"
```

---

## 6. Topic Subscriptions (Solace Wildcards)

Solace topic wildcards for event handler subscriptions:

| Pattern | Meaning | Example |
|---------|---------|---------|
| `*` | Matches exactly one level | `orders/*/created` matches `orders/US/created` |
| `>` | Matches one or more levels | `orders/created/>` matches `orders/created/US/east` |

```yaml
subscriptions:
  - topic: "orders/created/>"       # All order creation events
    qos: 1                          # Guaranteed delivery
  - topic: "orders/*/updated"       # Updates for any region
    qos: 0                          # Direct (best effort)
```

**QoS levels:**
- `0` — Direct messaging (best effort, no persistence)
- `1` — Guaranteed messaging (persisted, acknowledged)

---

## 7. Output Transform Types

Output transforms modify the response payload before publishing:

### Copy
Copies a value from source to destination:
```yaml
- type: copy
  source_expression: "input.payload:text"
  dest_expression: "user_data.output:response"
```

### Accumulate
Collects values across multiple messages into a list:
```yaml
- type: accumulate
  source_expression: "input.payload:item"
  dest_expression: "user_data.output:all_items"
```

### Filter
Conditionally includes/excludes values:
```yaml
- type: filter
  source_expression: "input.payload:score"
  dest_expression: "user_data.output:high_scores"
```

---

## 8. Environment Variables

| Variable | Purpose | Default |
|----------|---------|---------|
| `${NAMESPACE, solace-agent-mesh}` | Broker namespace | `solace-agent-mesh` |
| `${SAM_DATA_DIR}` | Data directory | `~/.sam/data` |
| `${LLM_SERVICE_ENDPOINT}` | LLM API base URL | — |
| `${LLM_SERVICE_API_KEY}` | LLM API key | — |
| `${LLM_SERVICE_GENERAL_MODEL_NAME}` | Default model | — |
| `${SESSION_SECRET_KEY}` | Session signing secret | — |
| `${BROKER_URL}` | Event mesh broker URL — **file-based config only**; in an entrypoint you author, set `broker_url` with `<<__SAM_REQUIRED__>>` (see `sam-event-mesh-schema`) | — |
| `${PLATFORM_SERVICE_URL}` | Platform service URL (enterprise) | — |

---

## 9. Common Configuration Patterns

### Development HTTP SSE entrypoint (minimal)
```yaml
apps:
  - name: dev_entrypoint
    app_config:
      fastapi_host: "127.0.0.1"
      fastapi_port: 8800
      session_secret_key: "dev-secret-key"
      card_publish_interval_seconds: 5
      frontend_app_name: "Solace Agent Mesh"
```

### Production HTTP SSE entrypoint
```yaml
apps:
  - name: production_entrypoint
    app_config:
      gateway_id: "prod-entrypoint"
      fastapi_host: "0.0.0.0"
      fastapi_port: 8800
      session_secret_key: "${SESSION_SECRET_KEY}"
      cors_allowed_origins:
        - "https://myapp.com"
      task_logging:
        enabled: true
      card_publish_interval_seconds: 30
      system_purpose: "Enterprise AI assistant."
      frontend_app_name: "Assistant"
      frontend_collect_feedback: true
      data_retention:
        enabled: true
        task_retention_days: 90
```

### Desktop/embedded HTTP SSE entrypoint
```yaml
apps:
  - name: embedded_entrypoint
    app_config:
      gateway_id: "embedded-entrypoint"
      fastapi_host: "localhost"
      fastapi_port: 8800
      session_secret_key: "dev-secret-key"
      task_logging:
        enabled: true
      card_publish_interval_seconds: 5
      system_purpose: "AI assistant."
      frontend_app_name: "Solace Agent Mesh"
```

### Simple event mesh entrypoint (order processing)
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
          on_success: order_result
          on_error: order_error
          default_user_identity: "system"
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
```

### Event mesh entrypoint with guaranteed delivery
```yaml
apps:
  - name: reliable_events_entrypoint
    app_config:
      event_mesh_broker_config:
        broker_url: "<<__SAM_REQUIRED__>>"
        broker_vpn: "<<__SAM_REQUIRED__>>"
        broker_username: "<<__SAM_REQUIRED__>>"
        broker_password: "<<__SAM_REQUIRED__>>"
      acknowledgment_policy:
        mode: on_completion
        timeout_seconds: 600
        on_failure:
          action: nack
          nack_outcome: failed
      event_handlers:
        - name: critical_events
          subscriptions:
            - topic: "critical/>"
              qos: 1
          input_expression: "input.payload:data"
          target_agent_name: CriticalProcessor
          on_success: critical_response
          structured_invocation:
            input_schema:
              type: object
              properties:
                event_type: { type: string }
                payload: { type: object }
              required: ["event_type"]
      output_handlers:
        - name: critical_response
          topic_expression: "static:critical/processed"
          payload_expression: "input.payload:text"
          output_schema:
            type: object
            properties:
              status: { type: string, enum: ["success", "warning", "failure"] }
              details: { type: string }
          on_validation_error: "log"
```

### Event mesh entrypoint routing to workflows
```yaml
apps:
  - name: workflow_events_entrypoint
    app_config:
      event_mesh_broker_config:
        broker_url: "<<__SAM_REQUIRED__>>"
        broker_vpn: "<<__SAM_REQUIRED__>>"
        broker_username: "<<__SAM_REQUIRED__>>"
        broker_password: "<<__SAM_REQUIRED__>>"
      event_handlers:
        - name: data_pipeline
          subscriptions:
            - topic: "data/ingest/>"
              qos: 1
          input_expression: "input.payload:"
          target_workflow_name: DataPipeline
          on_success: pipeline_result
          on_error: pipeline_error
          forward_context:
            source_topic: "input.topic:"
      output_handlers:
        - name: pipeline_result
          topic_expression: "template:data/processed/{{text://user_data.forward_context:source_topic}}"
          payload_expression: "input.payload:data[0]"   # the workflow's output_mapping object
          payload_format: json
        - name: pipeline_error
          topic_expression: "static:data/errors"
          payload_expression: "input.payload:text"      # the failure message
```

A workflow's `text` is only a status line; its result is `data[0]`. A failed, rejected or canceled workflow run goes to `on_error` with an error-only payload, so give that handler its own `payload_expression` (`input.payload:text`, or `input.payload:a2a_task_response.error` with `payload_format: json`) — the `data[0]` handler publishes `null` for a failure. `data[0]` is Go runtime syntax; the Python runtime writes `data.0.data`.

---

## 10. Field Interaction Rules

1. **`event_handlers` determines entrypoint type**: Its presence makes this an event mesh entrypoint. Without it, the entrypoint is HTTP SSE. Always include `event_mesh_broker_config` with all four `broker_*` fields (`<<__SAM_REQUIRED__>>` for unknowns); without a system broker the placeholders are ignored and the built-in broker is used.

2. **`event_handlers` reference `output_handlers` by name**: `on_success` and `on_error` values must match an output handler's `name`. The entrypoint validates these references at startup.

3. **Target routing is mutually exclusive**: Use exactly one of `target_agent_name`, `target_agent_name_expression`, `target_workflow_name`, or `target_workflow_name_expression` per event handler.

4. **SQL session required for many features**: Task logging, prompt library, projects, auto-title generation, and data retention all require `session_service.type: sql`. With `type: memory`, these features are disabled.

5. **`model` enables AI features**: Auto-title generation and AI-assisted prompts require a `model` configured on the entrypoint. This is separate from the agents' models.

6. **`card_publish_interval_seconds: 5` for builder configs**: Same as agents — use 5 for reliable discovery in development.

7. **`namespace` must match agents**: The entrypoint's namespace must match the agents it communicates with. Mismatched namespaces mean agents are invisible to the entrypoint.

8. **`forward_context` connects input to output**: Data forwarded via `forward_context` in event handlers is available in output handlers via `user_data.forward_context:<key>`. This is how you pass request data (like IDs) through to the response topic.

9. **Event mesh entrypoint doesn't need HTTP fields**: `fastapi_port`, CORS, auth, and frontend settings are irrelevant for event mesh entrypoints. Only set them for HTTP SSE entrypoints.

10. **ACK policy `on_completion` requires guaranteed QoS**: Using `acknowledgment_policy.mode: on_completion` only makes sense with `qos: 1` subscriptions (guaranteed delivery). With `qos: 0` (direct), there's nothing to acknowledge.
