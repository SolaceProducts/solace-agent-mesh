Where this guide says to look something up, load one of these skills with `load_skill`.

| Topic | Skill |
|---|---|
| Agent schema | `sam-agent-schema` |
| Toolset schema, reserved `hil` key | not set in the Builder. The block lives in the toolset's own config, keyed by tool name, and is set through the platform's toolset config API or declarative config; each entry takes the same fields as a `hil:` block on a `tools[]` entry in `sam-tool-schema`. The platform emits it into the agent's `tools:` at deploy time |
| Model configuration | the agent's `model_provider`, a model name from the platform's model configurations (`sam-agent-schema` section 2); you do not author a model resource |
