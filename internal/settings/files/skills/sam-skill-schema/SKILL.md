---
name: sam-skill-schema
description: YAML Schema for Solace Agent Mesh skill configuration — all fields, types, validation rules, reference model, and complete examples.
tags:
  - builder
  - schema
  - skill
  - sam
---

Solace Agent Mesh skill configuration schema. Skills are knowledge bundles with instruction content and optional reference documents that agents load on demand.

For **design guidance** see `sam-skill-design`.

## Schema

```yaml
{{SCHEMA:skill}}
```

---

## Skill Config Format

Skill artifacts use **YAML** with a flat top-level structure (NOT `apps[].app_config`):

```yaml
name: my-api-reference
description: "REST API reference documentation for the Acme platform."
tags:
  - api
  - documentation
examples:
  - "How do I authenticate with the API?"
  - "What endpoints are available?"
instruction_content: |
  # Acme API Reference
  This skill provides documentation for the Acme REST API.
  Use grep_skill_resources to search for endpoints.
references:
  - name: endpoints.md
    content: |
      # Endpoints
      ## GET /users
      Returns a list of users.
  - name: full_spec.md
    artifact: uploaded_openapi_spec
```

**MIME type**: `application/vnd.sam-skill-config+yaml`

---

## Field Reference

### Required Fields

| Field | Type | Constraints | Description |
|-------|------|-------------|-------------|
| `name` | string | 2-60 chars, pattern `^[a-z0-9][a-z0-9-]*$` | Skill identifier. Lowercase alphanumeric + hyphens. |
| `description` | string | 10-500 chars | What this skill provides. Shown for discovery. |
| `instruction_content` | string | 50-50000 chars | Markdown instruction body. Injected into agent context on `load_skill`. |

### Optional Fields

| Field | Type | Constraints | Description |
|-------|------|-------------|-------------|
| `tags` | array of strings | max 20 items | Categorization tags for discovery and filtering. |
| `examples` | array of strings | max 10 items | Example user queries. Used by entrypoint for routing. |
| `required_scopes` | array of strings | — | **Deprecated, ignored** — skill loading is not scope-gated. |
| `tools` | array of objects | — | Tools attached when this skill is loaded. Entries mirror an agent's `tools:` block, e.g. `- tool_type: builtin` / `tool_name: <name>`. |
| `required_builtins` | array of strings | — | **Deprecated** — use `tools:` with `tool_type: builtin` instead. Built-in tool names needed when this skill is loaded. |
| `references` | array of objects | — | Reference documents (see Reference Model below). |
| `assets` | array of objects | — | Binary or opaque files packaged under `assets/` (see Asset Model below). |

---

## Reference Model

Each reference entry has a `name` and exactly one of `content` or `artifact`:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Filename in the references/ directory (e.g., `api_reference.md`). |
| `content` | string | one of content/artifact | Inline reference content. |
| `artifact` | string | one of content/artifact | Artifact name containing the reference content. |

**Rules:**
- Each reference **must** have exactly one of `content` or `artifact` — not both, not neither
- `content` is for builder-generated references (inline in the config)
- `artifact` is for documents in the current session's artifact store — user uploads or tool outputs — referenced by artifact name
- No additional properties allowed on reference entries

---

## Asset Model

Assets are files packaged into the skill bundle's `assets/` directory. Unlike references they may be **binary** (e.g. a packaged `.samt` report template), so there is no inline `content` variant — each entry pulls a session artifact into the bundle:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Filename in the assets/ directory (e.g., `quarterly_report.samt`). |
| `artifact` | string | yes | Session artifact whose bytes are packaged as `assets/<name>`. |

**Rules:**
- `artifact` is required — assets are artifact-only, never inline
- The named artifact must exist in the current session (uploaded by the user or produced by a tool such as `package_template`); the save fails otherwise
- No additional properties allowed on asset entries

---

## Validation Rules

- `name` must match pattern `^[a-z0-9][a-z0-9-]*$` — no uppercase, no underscores, no spaces
- `description` must be 10-500 characters
- `instruction_content` must be 50-50000 characters
- Each reference must have exactly one of `content` or `artifact`
- Each asset must have both `name` and `artifact` (no inline content variant)
- No additional top-level properties allowed (`additionalProperties: false`)
- No additional properties on reference or asset entries (`additionalProperties: false`)

---

## Complete Examples

### Minimal Skill

```yaml
name: coding-standards
description: "Coding standards and style guide for the engineering team."
instruction_content: |
  # Coding Standards

  This skill provides the team's coding standards. Reference it when
  reviewing code or generating new code.

  ## Key Principles
  - Use descriptive variable names
  - Keep functions under 50 lines
  - Write tests for all public functions
  - Use meaningful commit messages
```

### Skill with Inline References

```yaml
name: api-reference
description: "REST API documentation with endpoint details and examples."
tags:
  - api
  - documentation
examples:
  - "How do I call the users endpoint?"
  - "What authentication does the API use?"
instruction_content: |
  # API Reference Skill

  This skill contains documentation for the Acme REST API v2.

  ## Available References
  - `endpoints.md` — Complete endpoint listing
  - `examples.md` — Request/response examples

  When you need endpoint details:
  1. Search with grep_skill_resources for the endpoint or method
  2. Read the relevant section with read_skill_resource
references:
  - name: endpoints.md
    content: |
      # API Endpoints

      ## Authentication
      All endpoints require Bearer token in the Authorization header.

      ## GET /api/v2/users
      Returns a paginated list of users.

      Parameters:
      - page (integer, optional): Page number, default 1
      - limit (integer, optional): Items per page, default 20

      ## POST /api/v2/users
      Creates a new user.

      Body:
      - name (string, required): User display name
      - email (string, required): User email address
  - name: examples.md
    content: |
      # API Examples

      ## List users
      ```
      GET /api/v2/users?page=1&limit=10
      Authorization: Bearer <token>
      ```

      ## Create user
      ```
      POST /api/v2/users
      Authorization: Bearer <token>
      Content-Type: application/json

      {"name": "Jane Doe", "email": "jane@example.com"}
      ```
```

### Skill with Artifact References

```yaml
name: product-docs
description: "Product documentation uploaded by the team."
tags:
  - product
  - documentation
instruction_content: |
  # Product Documentation

  This skill contains the team's product documentation.
  Use grep_skill_resources to search across all documents.

  ## Available Documents
  - `user_guide.md` — End-user documentation
  - `admin_guide.md` — Administrator documentation
references:
  - name: user_guide.md
    artifact: uploaded_user_guide
  - name: admin_guide.md
    artifact: uploaded_admin_guide
```

### Skill with a Binary Asset

```yaml
name: quarterly-report
description: "Renders the quarterly business report from a packaged template."
tags:
  - reporting
instruction_content: |
  # Quarterly Report Skill

  This skill ships a packaged report template (`quarterly_report.samt`)
  produced by the package_template tool.

  To render the report, call instantiate_template with:
  - skill_name: quarterly-report
  - asset: quarterly_report.samt
  - the data fields the template declares
assets:
  - name: quarterly_report.samt
    artifact: packaged_quarterly_report
```

### Skill with Mixed References

```yaml
name: compliance-rules
description: "Compliance rules and regulatory requirements for data handling."
tags:
  - compliance
  - security
  - regulations
examples:
  - "What are the data retention requirements?"
  - "How should PII be handled?"
instruction_content: |
  # Compliance Rules

  This skill provides compliance and regulatory guidance.

  ## Quick Reference
  - PII must be encrypted at rest and in transit
  - Data retention: 7 years for financial, 3 years for operational
  - Access logs must be maintained for all PII access

  ## Detailed References
  - `overview.md` — Summary of compliance requirements (generated)
  - `full_policy.md` — Complete compliance policy document (uploaded)

  Search references with grep_skill_resources for specific regulations.
references:
  - name: overview.md
    content: |
      # Compliance Overview

      ## Data Classification
      - Public: No restrictions
      - Internal: Access controls required
      - Confidential: Encryption + access logs
      - Restricted: All of the above + approval workflow

      ## Key Regulations
      - GDPR: EU data protection
      - SOC2: Security controls
      - PCI-DSS: Payment card data
  - name: full_policy.md
    artifact: uploaded_compliance_policy
```

---

## Build Manifest Entry

Skills appear in the build manifest as `type: skill`:

```yaml
components:
  - filename: api_reference.yaml
    type: skill
    name: api-reference
    description: "REST API reference documentation."
    status: validated
    origin: session
  - filename: my_agent.yaml
    type: agent
    name: MyAgent
    description: "Agent that uses the API reference skill."
    status: validated
    origin: session
    depends_on: []
    skills: [api-reference]
```

Agents reference skills by name in the manifest's `skills` array. This is validated during full manifest validation (`full_validation=true`).
