// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (ToolsetConfigBinding) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ToolsetConfigBinding holds per-agent config values for one toolset.

configValues is NESTED by tool name for BOTH toolset kinds — the top-level
keys are tools the toolset registers (plus the reserved key "hil", below),
each mapping to that tool's {field: value} config (e.g. {g_cfg_show: {api_token: ..., greeting_prefix: ...}}
for a kind:toolset package, {deep_research: {max_iterations: 2}} for a
builtin). A flat {field: value} map is read as if its keys were tool names
and rejected with "unknown tool name for this toolset". The discovery
configSummary reinforces this by reporting required fields as
"<toolName>/<field>".
  - kind:toolset packages are schema-validated against each tool's
    config_schema by validateToolsetBindingValues. Exception: a package that
    declares NO config_schema on any tool skips validation entirely and
    persists whatever is sent as-is (so a flat map is accepted only in that
    schema-less case).
  - builtin toolsets (e.g. builtin_research_tools) are validated by
    validateBuiltinToolsetConfigKeys: every top-level key must be a member
    tool of the toolset or the reserved key "hil". The AWE runtime validates
    the inner per-tool config shape at parse time.
  - Secret fields of both kinds — a package's config_schema secrets and the
    built-in credentials builtinToolConfigSchemas declares — must be strings,
    read back as "<REDACTED>", and merge the same way: sending "<REDACTED>"
    back, null, or omitting the secret from a tool block that is present
    keeps the stored value, and "" keeps a required secret (any secret with
    no default, which includes every built-in credential).
  - The reserved key "hil" holds approval settings for either toolset kind,
    keyed by tool name: {hil: {web_request: {require_approval: true,
    approval_message: "Fetch {{.url}}?"}}}. Each entry takes require_approval,
    require_approval_when, approval_message, show_args, timeout and approvers.
    approvers names who answers in place of the user. It holds two lists:
    users, the user ids who may answer, and places, chat channels where the
    question is also shown, each with a channel_type and an address. Only a
    listed user can answer, and a place needs at least one user beside it.
    While approver sets are switched off, a call to a tool that names
    approvers is denied. The agent asks the user before it runs a gated tool.
    Sending configValues replaces
    the stored settings for the toolset, so a tool left out of the block
    loses its approval.

JSON tag uses camelCase to match the Python SAM Pydantic model alias.`,
		Fields: map[string]apidoc.Field{
			"VolumeBindings": {Doc: `VolumeBindings maps a tool volume_param name (e.g. "workspace") to one of
the agent's volume slot names. Applies to built-in toolsets whose tools
declare volume_params (today: builtin_volume_tools). At deploy time the
platform projects this onto the toolset's tools-list entry as
volume_bindings:, where the AWE runtime binds it to every tool the toolset
registers. Omitted when the toolset has no volume bindings.`},
			"NetworkBindings": {Doc: `NetworkBindings maps a tool network_param name (e.g. "anthropic_api",
"code_hosts", "outbound") to one of the agent's network policy names
(declared under networks: in additionalConfigurations). Applies to
built-in toolsets whose tools declare network_params (none ship today).
At deploy time the platform projects each binding onto the tools-list
entries of the tools that declare the param, as network_bindings:, where
the AWE runtime binds the policy's egress allowlist. Omitted when none.`},
		},
	}
}

func (SkillConfigBinding) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SkillConfigBinding holds per-agent config overrides for one attached skill.

Persisted as JSON in agent_skills.config_values. ConfigValues is nested by
bundled tool name (without the skill__ prefix); each inner map is field
name → value. At deploy time the merge layer combines this with the
skill-level skill_configs.config_values and the per-field schema defaults
(agent value > skill value > schema default).

Either SkillID or SkillName identifies the attached skill; on write callers
should send SkillID since that's the stable identifier and matches the
agent_skills FK. SkillName is filled in on response for UI convenience.`,
	}
}

func (CreateAgentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateAgentRequest is the request body for POST /agents.",
		Fields: map[string]apidoc.Field{
			"SystemPrompt": {Doc: `SystemPrompt is the agent's base instructions. Required for standard
agents (the only supported type today): a create or update that omits it
is rejected with HTTP 422 "system prompt is required for standard agents".
Length is bounded by agent_instruction_min_chars and
agent_instruction_max_chars in the platform config (1 and 32000 by
default), counted in characters rather than bytes.`},
			"AdditionalConfigurations": {Doc: "AdditionalConfigurations is a JSON-object catch-all for agent config keys\nthat have no dedicated field, written in camelCase (e.g. supportsStreaming,\nagentCardPublishing.intervalSeconds).\nSize, depth and key collisions are checked at create/update time, and\nknown keys are type-checked. Authored values are deep-merged into the\ndeploy-time YAML.\nForbidden keys, rejected with HTTP 422: `tools` (computed by the platform\nfrom spec.toolsets at deploy time. Declare built-in toolset IDs under\nspec.toolsets instead), plus any key that duplicates a structured field\nabove. The runtime `tools:` / `group_name` shape from the AWE agent YAML\ndoes NOT belong here."},
		},
	}
}

func (UpdateAgentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UpdateAgentRequest is the request body for PUT /agents/{id}.",
		Fields: map[string]apidoc.Field{
			"AdditionalConfigurations": {Doc: "AdditionalConfigurations: see CreateAgentRequest for semantics."},
		},
	}
}

func (DeployedAgentInfo) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "DeployedAgentInfo is the snapshot of an agent's config at deploy time.",
		Fields: map[string]apidoc.Field{
			"SkillIDs":  {Comment: "custom skills (uuids)"},
			"SkillRefs": {Comment: "built-in skill refs (names)"},
			"Skills":    {Comment: "A2A agent-card abilities"},
		},
	}
}

func (ConnectorSnapshot) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ConnectorSnapshot captures a connector's identity at deploy time. It is stored
on the deployment data snapshot so the deployed-agent view can show the
connectors the agent was deployed with even after the underlying connector is
renamed (shows the deploy-time name) or deleted (the UI flags the id as NOT
FOUND since it no longer resolves to a live connector).`,
	}
}

func (DeployedGatewayInfo) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "DeployedGatewayInfo is the snapshot of a gateway's config at deploy time.",
	}
}

func (SkillRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SkillRequest represents a skill definition in create/update requests.

Fields ID, Name, Description, Tags, Examples, InputModes, OutputModes mirror
the A2A AgentCard skill spec (see internal/a2a/protocol.go AgentSkill — the
wire-format source of truth). When ID is omitted on the request side, the
platform derives it via nameToSnakeCase(name) at YAML-emit time; provided
IDs are persisted and emitted verbatim.

Required and Optional are SAM-specific extensions and are not part of the
A2A spec. They are stored on the DB but are not currently round-tripped
through runtime YAML emission — kept for backwards compatibility with
existing API clients.`,
		Fields: map[string]apidoc.Field{
			"Required": {Comment: "SAM-specific; not part of A2A spec; not in runtime YAML"},
			"Optional": {Comment: "SAM-specific; not part of A2A spec; not in runtime YAML"},
		},
	}
}

func (AgentResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "AgentResponse is the response body for agent endpoints.",
		Fields: map[string]apidoc.Field{
			"SkillConfigFields": {Doc: `SkillConfigFields is computed server-side for GetByID only (omitted on
list). Keyed by attached skill ID; each entry lists declared schema
fields across the skill's bundled tools plus per-field coverage bits
(skill default vs. agent override). Drives the AgentSkillConfigDialog
"(from skill)" tags and required-field validation in the UI.`},
			"AdditionalConfigurations": {Doc: "AdditionalConfigurations: see CreateAgentRequest for semantics. Elided\nfrom the response when the stored value is the default `{}` object."},
			"IsEphemeral": {Doc: `Ephemeral test-agent fields. Always emitted on ephemerals; omitted on
the rest. Lets the AI Builder Test mode panel decide whether the
existing test agent already wraps the right component (so it doesn't
have to re-create on every entry) and re-attach after a refresh.`},
			"SourceComponentKind": {Doc: `SourceComponentKind tells the FE which kind ("agent" | "workflow" |
"skill") the ephemeral wraps so it can detect same-name cross-kind
switches and force a recreate. Empty for ephemerals created before
the column existed.`},
			"Warnings": {Doc: `Warnings carries non-blocking validation messages produced when the
caller submits an authored request (POST /agents, PUT /agents/{id}).
Tier-2 schema validation populates this with messages about unknown
keys in additionalConfigurations when ?strict=true is not set; in
strict mode unknown keys become a 422 error instead. Read-only and
elided when empty so list / detail responses do not surface stale
warnings from prior writes.`},
			"ToolRegistrations": {Doc: `ToolRegistrations reports the running agent's per-tool-entry
registration outcomes (openapi / mcp connector entries) as published
on its agent card. Present only while the agent is running AND the
runtime reports statuses (Go agents); absent for Python agents.`},
		},
	}
}

func (ToolRegistrationResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ToolRegistrationResponse is one connector-sourced tool entry's registration
outcome as reported by the running agent. Error is set on failed entries
AND on ready entries with partial losses ("N tools discovered, M
registered") — status, not error presence, is the success signal.`,
		Fields: map[string]apidoc.Field{
			"Status": {Comment: "ready / failed"},
		},
	}
}

func (SkillResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SkillResponse represents a skill in agent responses.

Fields ID, Name, Description, Tags, Examples, InputModes, OutputModes mirror
the A2A AgentCard skill spec. Required and Optional are SAM-specific
extensions retained for backwards compatibility (see SkillRequest).`,
		Fields: map[string]apidoc.Field{
			"Required": {Comment: "SAM-specific; not part of A2A spec; not in runtime YAML"},
			"Optional": {Comment: "SAM-specific; not part of A2A spec; not in runtime YAML"},
		},
	}
}
