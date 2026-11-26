---
name: sam-tool-schema
description: JSON Schema for Solace Agent Mesh tool configuration — built-in tools, Python Secure Tool Runtime tools, MCP tools, auth, HIL, and volumes.
tags:
  - builder
  - schema
  - tools
  - config
  - sam
---

Solace Agent Mesh tool configuration schema. Defines every tool type in an agent's `tools[]` array.

For **agent-level config** see `sam-agent-schema`. For **design guidance** see `sam-best-practices`.

## Schema

```yaml
{{SCHEMA:tool}}
```

## Secure Tool Runtime Manifest (separate from agent config)

```yaml
# tools/manifest.yaml — defines Python tools for the Secure Tool Runtime
version: 1
tools:
  my_tool:
    module: "my_package.my_module"
    function: "my_tool_function"
    package: "my-package"
    version: ">=1.0,<2.0"
    sandbox_profile: "standard"
    timeout_seconds: 30
```

## Examples

Mixed tool types:
```yaml
tools:
  - tool_type: builtin-group
    group_name: artifact_management
  - tool_type: builtin
    tool_name: web_request
    hil: { require_approval: true, timeout: "5m" }
  - tool_type: mcp
    connection_params:
      type: stdio
      command: "npx"
      args: ["-y", "@modelcontextprotocol/server-postgres"]
      env: { DATABASE_URL: "${DATABASE_URL}" }
    allow_list: [query, list_tables]
  - tool_type: builtin
    tool_name: mermaid_diagram_generator   # resolves via the Secure Tool Runtime at runtime
```
