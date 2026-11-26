Where this guide says to look something up: *Skill schema* is the `sam-skill-schema` skill; load it with `load_skill`.

### Supplying reference files

Each reference can be provided as:
- **Inline content** — The content is embedded directly in the skill config. Use for content you generate during the build.
- **Artifact reference** — Points to a user-uploaded artifact by name. Use for large documents the user has already uploaded.

**Inline content:**
- You are generating the reference content as part of the build
- Content is relatively short (under ~5000 tokens per file)
- Content is specific to this skill and doesn't exist elsewhere

**Artifact reference:**
- User uploaded a document (PDF extract, API spec, manual)
- Content is large and already exists as an artifact
- Multiple skills might reference the same document

**Assets:** a file under `assets/`, such as a `.samt` report template, is always an artifact reference — add an `assets` entry naming the session artifact (a template the user uploaded or packaged in chat). Assets have no inline form, so you cannot write a template yourself; ask the user for the packaged `.samt`. The template contract is documented under Asset Templates in the published documentation: https://docs.solace.com/Agent-Mesh/agent-mesh.htm

### Attaching a skill to an agent

```yaml
skills:
  - name: my-api-reference
```

Agent card capabilities are `agent_card.skills[]`; knowledge skills are `skills[]`.

### Builder Workflow for Skills

#### 1. Identify skill needs during Discovery phase

Listen for signals that suggest skills:
- "I have reference documentation for..."
- "The agent needs to know about our API..."
- "Can you include these docs for the agent to reference?"

#### 2. Create skill artifacts BEFORE agent artifacts

Skills should be created before agents that reference them. The build manifest should list skills before agents that depend on them.

#### 3. Generate instruction content and references

For each skill:
1. Write focused instruction content (purpose, key concepts, reference guide)
2. For references you generate, use inline `content` fields
3. For user-uploaded documents, use `artifact` fields referencing existing artifacts

#### 4. Validate before saving

Always validate skill artifacts with `validate_component_config`:
```
validate_component_config(
  config_yaml: '<the YAML string>',
  mime_type: 'application/vnd.sam-skill-config+yaml'
)
```

#### 5. Wire skills to agents

After creating the skill, ensure the agent config includes the skill reference and the agent instruction mentions when to load it.
