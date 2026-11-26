Where this guide says to look something up, read one of these files. Paths are relative to the `sam-declarative-config` skill root.

| Topic | Where |
|---|---|
| Agent schema | `references/agent.md` |
| Toolset schema, reserved `hil` key | `references/toolset.md`, section *Reserved key: `hil`* |
| Model configuration | a `kind: model` resource, see `references/model.md`. The agent spec carries no model field; the platform binds the model by alias when the agent is deployed |

### Translating the YAML in this guide

The YAML snippets above show the agent's runtime configuration. In a declarative-config agent file the same settings live elsewhere, and copying a snippet verbatim is a mistake: the platform rejects a `tools:` block under `additionalConfigurations` with HTTP 422 when `sam config apply` writes the agent (`sam config plan` does not catch it), and snake_case keys are deep-merged verbatim with only an unknown-key warning, so they take effect untyped.

| Runtime YAML in this guide | Declarative config |
|---|---|
| `tools:` with `tool_type: builtin-group` / `group_name: …` | `spec.toolsets:` with the built-in toolset ID (for example `builtin_artifact_tools`, or `hil_tools` for `ask_user_question`); see `references/toolset.md` |
| `tools:` with `tool_type: builtin` and a `hil:` block | the toolset's reserved `hil` key, as in the lookup table above |
| `agent_card.defaultInputModes` / `defaultOutputModes` | `spec.inputModes` / `spec.outputModes` |
| `skills` under `agent_card` (capabilities) | `spec.skills`; knowledge skills go in `spec.skillRefs` instead |
| `output_schema`, `input_schema`, `validation_max_retries` | `spec.additionalConfigurations.outputSchema`, `inputSchema`, `validationMaxRetries` |
| `inter_agent_communication.allow_list` / `request_timeout_seconds` | `spec.additionalConfigurations.interAgentCommunication.allowList` / `requestTimeoutSeconds` |
| `max_call_depth`, `supports_streaming` | `spec.additionalConfigurations.maxCallDepth`, `supportsStreaming` |
| `tool_result_auto_artifact_threshold_bytes` / `tool_result_inline_truncation_bytes` | `spec.additionalConfigurations.toolResultAutoArtifactThresholdBytes` / `toolResultInlineTruncationBytes` |
