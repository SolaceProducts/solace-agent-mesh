---
name: sam-event-mesh-design
description: Event Mesh Entrypoint design patterns — when to use Event Mesh entrypoints, event handler design, subscription topics, acknowledgment strategies, target agent design.
---

# Event Mesh Entrypoint Design Reference

> **Note:** System fields like `namespace`, `session_service`, and `artifact_service` are injected by the platform at deployment time and must not be included in generated configs.

This is the reference companion to the `sam-event-mesh-design` skill. The main design guidance is in the SKILL.md file.

## Quick Decision Guide

| Question | Answer | Component |
|----------|--------|-----------|
| Does a user initiate this? | Yes | Agent (no Event Mesh entrypoint needed) |
| Do events arrive from a Solace broker? | Yes | Event Mesh Entrypoint + Agent |
| Is multi-step processing needed? | Yes | Event Mesh Entrypoint + Workflow |
| Do multiple event types go to different agents? | Yes | Event Mesh Entrypoint with multiple handlers |

## Config Structure Reminder

Event Mesh Entrypoints use the standard entrypoint YAML format. Always include `event_mesh_broker_config` with all four fields — `broker_url`, `broker_vpn`, `broker_username`, `broker_password` — using `<<__SAM_REQUIRED__>>` for any value you don't know. Never emit a password-only block and never omit the block. When the deployment has no system broker (local, embedded or desktop), the platform ignores these placeholders and the entrypoint connects to the built-in broker. When a system broker is configured (a cluster), every field must be filled with real values before the entrypoint can deploy, so hand off: ask the user for `broker_url`, `broker_vpn` and `broker_username` (not secret) and write them in; accept `broker_password` if the user gives it to you in the chat, otherwise leave it `<<__SAM_REQUIRED__>>`; then, in your final message, name each broker field still holding a placeholder and tell the user to fill it in on the **Entrypoints** page before deploying.

```yaml
apps:
  - name: order_events_entrypoint
    app_config:
      event_mesh_broker_config:            # Always — all four fields
        broker_url: "<<__SAM_REQUIRED__>>"
        broker_vpn: "<<__SAM_REQUIRED__>>"
        broker_username: "<<__SAM_REQUIRED__>>"
        broker_password: "<<__SAM_REQUIRED__>>"
      event_handlers:
        - name: handler_name
          subscriptions:
            - topic: "my/topic/>"
              qos: 1
          input_expression: "input.payload:"
          target_agent_name: MyAgent
      # Optional:
      output_handlers: [...]
      acknowledgment_policy: {...}
```

The MIME type for Event Mesh entrypoint configs is `application/vnd.sam-gateway-config+yaml`.

For the full schema, load the `sam-gateway-schema` skill. For the `sam-event-mesh-schema` skill, see detailed field reference and examples.
