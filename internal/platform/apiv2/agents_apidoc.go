// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Agent) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "Agent is an agent the platform builds and deploys.",
		Fields: map[string]apidoc.Field{
			"ID":                   {Doc: "ID is the agent's UUID."},
			"Type":                 {Doc: "Type is always \"agent\"."},
			"AgentType":            {Doc: "AgentType is the kind of agent: standard, a2a_proxy for the platform's stand-in record of a remote Agent-to-Agent (A2A) agent, or link for its stand-in record of a link agent."},
			"Name":                 {Doc: "Name is the agent's unique name. Uniqueness ignores case and spaces."},
			"InstanceName":         {Doc: "InstanceName is the name the agent registers under on the event broker: \"agent_\" followed by its UUID with underscores for dashes."},
			"Description":          {Doc: "Description says what the agent does, and is published as its Agent-to-Agent (A2A) agent card description."},
			"SystemPrompt":         {Doc: "SystemPrompt is the agent's instructions to the model."},
			"Toolsets":             {Doc: "Toolsets lists the toolsets the agent uses, each a built-in toolset ID or the name of a custom tool package."},
			"ToolsetExcludedTools": {Doc: "ToolsetExcludedTools maps a toolset to the names of its tools the agent excludes. Null when no tool is excluded."},
			"ToolsetConfigs":       {Doc: "ToolsetConfigs holds the agent's own settings for the toolsets that take them."},
			"SkillConfigs": {Doc: `SkillConfigs holds the agent's own settings for its attached skills.
Filled only when the agent is fetched by ID. Empty in a list and after a create or update.`},
			"SkillConfigFields": {Doc: `SkillConfigFields maps a skill's UUID to the settings its bundled tools take, and says who already supplies each.
Null in a list, after a create or update, and when no attached skill declares settings.`},
			"Skills":       {Doc: "Skills lists the abilities the agent advertises on its Agent-to-Agent (A2A) agent card."},
			"SkillRefs":    {Doc: "SkillRefs names the built-in skills the agent uses."},
			"SkillIDs":     {Doc: "SkillIDs lists the UUIDs of the custom skills attached to the agent."},
			"InputModes":   {Doc: "InputModes lists the content types the agent accepts. When it is empty, the agent card advertises text."},
			"OutputModes":  {Doc: "OutputModes lists the content types the agent produces. When it is empty, the agent card advertises text."},
			"ConnectorIDs": {Doc: "ConnectorIDs lists the UUIDs of the connectors whose tools the agent uses."},
			"ModelProvider": {Doc: `ModelProvider holds the model configuration the agent uses, as its alias or ID. When it is empty, the agent
uses the general model.`},
			"AdditionalConfigurations": {Doc: `AdditionalConfigurations holds extra agent settings merged into the deployed configuration. Null when none
are set.`},
			"DeploymentStatus": {Doc: `DeploymentStatus is the agent's deployment state: deployed when the latest deploy or update
succeeded, deploy_failed when it failed, and not_deployed when the agent was never deployed or
was last undeployed.`},
			"SyncStatus": {Doc: `SyncStatus is whether the agent matches its latest successful deployment: in_sync when it does,
out_of_sync when it has changed since. Null when the agent is not deployed or never deployed
successfully.`},
			"Deployed": {Doc: `Deployed is the agent as its latest successful deployment left it. Null when the agent is not deployed or
never deployed successfully.`},
			"RuntimeStatus": {Doc: "RuntimeStatus is the agent's health as last reported by its running instance."},
			"ToolRegistrations": {Doc: `ToolRegistrations reports how each OpenAPI or Model Context Protocol (MCP) connector tool source registered when the agent last started.
Empty when the agent is not running or reports none.`},
			"Warnings": {Doc: `Warnings lists non-blocking problems found in additionalConfigurations, such as unrecognized keys.
Empty except after a create or update that found any.`},
			"IsEphemeral": {Doc: `IsEphemeral is true for a temporary test agent the agent builder creates when a user opens Test mode, and false otherwise.
A test agent is left out of the agent list and is deleted after expiresTime.`},
			"SourceSessionID":     {Doc: "SourceSessionID is the build session a test agent was created for. Null unless the agent is a test agent."},
			"SourceComponentName": {Doc: "SourceComponentName is the name of the component a test agent wraps. Null unless the agent is a test agent."},
			"SourceComponentKind": {Doc: `SourceComponentKind is what a test agent wraps: agent, workflow, or skill. Null unless the agent is a test
agent.`},
			"ExpiresTime": {Doc: "ExpiresTime is when a test agent expires and is deleted. Null for any other agent."},
			"CreatedBy":   {Doc: "CreatedBy identifies the user who created the agent: their email, or their user ID when no email is known."},
			"UpdatedBy":   {Doc: "UpdatedBy identifies the user who last changed the agent, in the same form as createdBy."},
			"CreatedTime": {Doc: "CreatedTime is when the agent was created."},
			"UpdatedTime": {Doc: "UpdatedTime is when the agent was last changed."},
		},
	}
}

func (DeployedAgent) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "DeployedAgent is the agent as its latest successful deploy left it.",
		Fields: map[string]apidoc.Field{
			"Name":         {Doc: "Name is the agent's name when it was deployed."},
			"Description":  {Doc: "Description is the agent's description when it was deployed."},
			"SystemPrompt": {Doc: "SystemPrompt is the agent's instructions when it was deployed."},
			"Toolsets":     {Doc: "Toolsets lists the toolsets the agent was deployed with."},
			"InputModes":   {Doc: "InputModes lists the content types the agent accepted when it was deployed."},
			"OutputModes":  {Doc: "OutputModes lists the content types the agent produced when it was deployed."},
			"ConnectorIDs": {Doc: "ConnectorIDs lists the UUIDs of the connectors the agent was deployed with."},
			"AdditionalConfigurations": {Doc: `AdditionalConfigurations holds the extra agent settings the agent was deployed with. Null when none were
set.`},
			"SkillIDs":     {Doc: "SkillIDs lists the UUIDs of the custom skills the agent was deployed with."},
			"SkillRefs":    {Doc: "SkillRefs names the built-in skills the agent was deployed with."},
			"Skills":       {Doc: "Skills lists the abilities the agent advertised on its Agent-to-Agent (A2A) agent card when it was deployed."},
			"DeployedTime": {Doc: "DeployedTime is when the deployment was requested."},
			"DeployedBy":   {Doc: "DeployedBy identifies the user who ran the deployment, in the same form as the agent's createdBy."},
		},
	}
}

func (AgentSkill) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `AgentSkill is one ability the agent advertises on its Agent-to-Agent (A2A) card. Its id is
the card's skill id, not a platform resource.`,
		Fields: map[string]apidoc.Field{
			"ID": {Doc: `ID is the skill's id on the agent card. When it is null or left out, the card uses the name converted to
snake_case.`},
			"Name":        {Doc: "Name is the skill's display name."},
			"Description": {Doc: "Description says what the skill does."},
			"Tags":        {Doc: "Tags are keywords describing the skill. Empty when none were given."},
			"Examples":    {Doc: "Examples are sample requests the skill handles. Empty when none were given."},
			"InputModes":  {Doc: "InputModes lists the content types this skill accepts. Empty when none were given."},
			"OutputModes": {Doc: "OutputModes lists the content types this skill produces. Empty when none were given."},
			"Required": {Doc: `Required is a list stored and returned with the skill. Neither the Agent-to-Agent (A2A) card nor the deployed configuration
includes it. Null when never sent.`},
			"Optional": {Doc: `Optional is a list stored and returned with the skill. Neither the Agent-to-Agent (A2A) card nor the deployed configuration
includes it. Null when never sent.`},
		},
	}
}

func (ToolsetConfig) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ToolsetConfig is the agent's own settings for one toolset. Secret values
are redacted.`,
		Fields: map[string]apidoc.Field{
			"ToolsetName": {Doc: "ToolsetName is the toolset the settings apply to. It must also be listed in the agent's toolsets."},
			"ConfigValues": {Doc: `ConfigValues holds the settings the toolset's manifest declares, keyed by tool name and then setting
name. Secret values, of a custom tool package or of a built-in tool (such as web_search_google's
google_search_api_key), read back as "<REDACTED>", and sending that string keeps the stored secret.
The reserved key "hil" holds approval settings keyed by tool name, such as
{"hil":{"web_request":{"require_approval":true}}}. The agent asks the user before it runs a tool that
requires approval. Each entry takes require_approval, require_approval_when, approval_message,
show_args, and timeout. Sending configValues replaces the stored settings for the toolset, so a tool
left out of hil loses its approval.`},
			"VolumeBindings": {Doc: `VolumeBindings maps a tool's volume parameter to one of the agent's volume slots. Applies to built-in
toolsets only. Null when there are none.`},
			"NetworkBindings": {Doc: `NetworkBindings maps a tool's network parameter to one of the agent's network policies. Applies to
built-in toolsets only. Null when there are none.`},
		},
	}
}

func (SkillConfig) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SkillConfig is the agent's own settings for one skill, per tool. Secret
values are redacted.`,
		Fields: map[string]apidoc.Field{
			"SkillID": {Doc: `SkillID is the UUID of the skill the settings apply to. It must also be listed in the agent's skillIds.
A request can send skillName instead. When both are sent, skillId wins.`},
			"SkillName": {Doc: `SkillName is the skill's name. A request can send it in place of skillId. A response fills it in, and
sends null when the skill no longer exists.`},
			"ConfigValues": {Doc: `ConfigValues holds the settings the skill's manifest declares, keyed by bundled tool name and then
setting name. Secret values read back as "<REDACTED>", and sending that string keeps the stored secret.`},
		},
	}
}

func (SkillConfigField) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "SkillConfigField is one setting a skill's tool takes.",
		Fields: map[string]apidoc.Field{
			"ToolName":    {Doc: "ToolName is the skill's bundled tool the setting belongs to."},
			"Key":         {Doc: "Key is the setting's name."},
			"FieldType":   {Doc: "FieldType is the type of value the setting takes, such as string, integer, or boolean."},
			"Secret":      {Doc: "Secret is true when the setting holds a secret, which is redacted when read."},
			"Required":    {Doc: "Required is true when a value must be supplied: the tool declares it required, or it is secret with no default."},
			"Description": {Doc: "Description explains the setting. Null when the tool gives none."},
			"Default": {Doc: `Default is the value used when neither the skill nor the agent sets one. Null when there is no default.
The skill's manifest sets its type.`},
			"CoveredBySkill": {Doc: "CoveredBySkill is true when the skill's own settings already supply a non-null value."},
			"CoveredByAgent": {Doc: "CoveredByAgent is true when the agent's own settings for the skill supply a non-null value."},
		},
	}
}

func (ToolRegistration) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ToolRegistration is how one of the agent's tool sources registered when it
last started.`,
		Fields: map[string]apidoc.Field{
			"ToolType": {Doc: "ToolType is the kind of connector tool source: openapi or mcp."},
			"ConnectorID": {Doc: `ConnectorID is the UUID of the connector the tools came from. Null when the entry did not come from a
connector.`},
			"Name": {Doc: "Name is a short identifier for the tool entry, such as its tool name. Null when the agent reports none."},
			"Status": {Doc: `Status is the registration outcome: ready or failed. A ready entry can still carry an error when some
tools were lost, so status, not error, says whether it worked.`},
			"Error": {Doc: `Error says, in a sentence Agent Mesh wrote, that the entry failed or that a ready entry lost some of its
tools. Null when neither happened. The agent's log has the cause.`},
			"ToolCount": {Doc: "ToolCount is the number of tools the entry registered."},
			"ToolNames": {Doc: "ToolNames lists the names of the tools the entry registered."},
		},
	}
}

func (CreateAgentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateAgentRequest creates an agent without deploying it. Deploy it with
POST /agentDeployments.`,
		Fields: map[string]apidoc.Field{
			"Name": {Doc: `Name is the agent's unique name. 3 to 255 characters. If the name is already in use, ignoring case and
spaces, the request fails with 409.`},
			"Description": {Doc: "Description says what the agent does. 10 to 1,000 characters."},
			"SystemPrompt": {Doc: `SystemPrompt is the agent's instructions to the model. Required for a standard agent. 1 to 32,000
characters by default. The server's configuration can change these limits.`},
			"AgentType": {Doc: "AgentType is the kind of agent to create. Only standard is accepted. Defaults to standard."},
			"Skills":    {Doc: "Skills lists the abilities the agent advertises on its Agent-to-Agent (A2A) agent card. Up to 20."},
			"SkillRefs": {Doc: `SkillRefs names the built-in skills to attach. If a name starts with sam- and is not a known built-in
skill, the request fails with 422.`},
			"SkillIDs": {Doc: "SkillIDs lists the UUIDs of the custom skills to attach. If an ID is unknown, the request fails with 422."},
			"Toolsets": {Doc: `Toolsets lists the toolsets to use, each a built-in toolset ID or the name of a ready custom tool package.
If a name is unknown, the request fails with 422.`},
			"ToolsetExcludedTools": {Doc: "ToolsetExcludedTools maps a toolset to the names of its tools the agent excludes."},
			"ToolsetConfigs": {Doc: `ToolsetConfigs sets the agent's own settings per toolset. If a toolset is missing from toolsets or appears
more than once, the request fails with 422.`},
			"SkillConfigs": {Doc: `SkillConfigs sets the agent's own settings per skill. If a skill is missing from skillIds, the request
fails with 422.`},
			"ConnectorIDs": {Doc: `ConnectorIDs are the UUIDs of the connectors whose tools the agent uses. Up to 5. If two connectors
or toolsets have a tool with the same name, the request fails with 422.`},
			"InputModes":  {Doc: "InputModes lists the content types the agent accepts. When it is empty, the agent card advertises text."},
			"OutputModes": {Doc: "OutputModes lists the content types the agent produces. When it is empty, the agent card advertises text."},
			"ModelProvider": {Doc: `ModelProvider names the model configuration to use, as an alias or ID, in its first entry. When it is
empty, the agent uses the general model. If the model is not configured, the request fails with 422.`},
			"AdditionalConfigurations": {Doc: `AdditionalConfigurations holds extra agent settings merged into the deployed configuration. A JSON object
of up to 64 KiB, nested up to 8 levels. If a key is one the platform sets itself or repeats another agent
field, the request fails with 422. An unrecognized key is kept and listed in warnings.`},
			"Surface": {Doc: `Surface is the X-Sam-Client-Surface header: ui or cli names the first-party client making the request,
and any other value, or no header, counts as api. Used only for usage statistics.`},
		},
	}
}

func (UpdateAgentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateAgentRequest replaces an agent. A field left out is cleared, except
toolsetConfigs and skillConfigs, which are kept unless sent as an empty array.
If agentType is sent, the request fails with 400.`,
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: "ID is the UUID of the agent to replace. If the ID is unknown, the request fails with 404."},
			"Name":        {Doc: "Name is the agent's new name. 3 to 255 characters."},
			"Description": {Doc: "Description says what the agent does. 10 to 1,000 characters."},
			"SystemPrompt": {Doc: `SystemPrompt is the agent's instructions to the model. Required for a standard agent. 1 to 32,000
characters by default. The server's configuration can change these limits.`},
			"Skills": {Doc: "Skills lists the abilities the agent advertises on its Agent-to-Agent (A2A) agent card. Up to 20."},
			"SkillRefs": {Doc: `SkillRefs names the built-in skills to attach. If a name starts with sam- and is not a known built-in
skill, the request fails with 422.`},
			"SkillIDs": {Doc: "SkillIDs lists the UUIDs of the custom skills to attach. If an ID is unknown, the request fails with 422."},
			"Toolsets": {Doc: `Toolsets lists the toolsets to use, each a built-in toolset ID or the name of a ready custom tool package.
If a name is unknown, the request fails with 422.`},
			"ToolsetExcludedTools": {Doc: "ToolsetExcludedTools maps a toolset to the names of its tools the agent excludes."},
			"ToolsetConfigs": {Doc: `ToolsetConfigs sets the agent's own settings per toolset. If a toolset is missing from toolsets or appears
more than once, the request fails with 422.
Leave it out or send null to keep the stored settings, and send [] to clear them.`},
			"SkillConfigs": {Doc: `SkillConfigs sets the agent's own settings per skill. If a skill is missing from skillIds, the request
fails with 422.
Leave it out or send null to keep the stored settings of the skills that stay attached, and send [] to clear them.`},
			"ConnectorIDs": {Doc: `ConnectorIDs are the UUIDs of the connectors whose tools the agent uses. Up to 5. If two connectors
or toolsets have a tool with the same name, the request fails with 422.`},
			"InputModes":  {Doc: "InputModes lists the content types the agent accepts. When it is empty, the agent card advertises text."},
			"OutputModes": {Doc: "OutputModes lists the content types the agent produces. When it is empty, the agent card advertises text."},
			"ModelProvider": {Doc: `ModelProvider names the model configuration to use, as an alias or ID, in its first entry. When it is
empty, the agent uses the general model. If the model is not configured, the request fails with 422.`},
			"AdditionalConfigurations": {Doc: `AdditionalConfigurations holds extra agent settings merged into the deployed configuration. A JSON object
of up to 64 KiB, nested up to 8 levels. If a key is one the platform sets itself or repeats another agent
field, the request fails with 422. An unrecognized key is kept and listed in warnings.`},
		},
	}
}

func (AgentSecretsDiffRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "AgentSecretsDiffRequest carries the toolset and skill settings a form holds.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: `ID is the UUID of the agent whose stored secrets are compared. If the ID is unknown, the request fails
with 404.`},
			"Values": {Doc: "Values holds the toolset and skill settings to compare with the stored ones. Required."},
		},
	}
}

func (AgentSecretsDiffForm) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "AgentSecretsDiffForm is the agent's own settings as a form holds them.",
		Fields: map[string]apidoc.Field{
			"ToolsetConfigs": {Doc: "ToolsetConfigs holds the settings per toolset to compare with the stored ones."},
			"SkillConfigs":   {Doc: "SkillConfigs holds the settings per skill to compare with the stored ones."},
		},
	}
}

func (AgentPlatformDefaults) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `AgentPlatformDefaults are the settings a new agent starts from. Secrets are
left out.`,
		Fields: map[string]apidoc.Field{
			"Broker":           {Doc: "Broker is the broker connection a new agent starts with."},
			"Model":            {Doc: "Model is the model service a new agent starts with."},
			"SessionService":   {Doc: "SessionService is where a new agent keeps its sessions."},
			"ArtifactService":  {Doc: "ArtifactService is where a new agent keeps its artifacts."},
			"Log":              {Doc: "Log is how a new agent logs."},
			"ValidationLimits": {Doc: "ValidationLimits is the length the platform accepts for an agent's systemPrompt."},
		},
	}
}

func (BrokerDefaults) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "BrokerDefaults is the broker a new agent connects to.",
		Fields: map[string]apidoc.Field{
			"DevMode": {Doc: `DevMode is the broker's development mode as the platform is configured, normally "true" or "false".
Defaults to "false".`},
			"BrokerURL":      {Doc: "BrokerURL is the broker's URL. Defaults to ws://localhost:8080."},
			"BrokerUsername": {Doc: "BrokerUsername is the username agents connect with. Defaults to \"default\"."},
			"BrokerVPN":      {Doc: "BrokerVPN is the message VPN agents connect to. Defaults to \"default\"."},
			"TemporaryQueue": {Doc: `TemporaryQueue is whether agents use temporary queues as the platform is configured, normally "true" or
"false". Defaults to "true".`},
		},
	}
}

func (ModelDefaults) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ModelDefaults is the model a new agent calls.",
		Fields: map[string]apidoc.Field{
			"Model":   {Doc: "Model is the general model's name. Null when not configured."},
			"APIBase": {Doc: "APIBase is the model service's endpoint. Null when not configured."},
		},
	}
}

func (SessionServiceDefaults) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "SessionServiceDefaults is where a new agent keeps sessions.",
		Fields: map[string]apidoc.Field{
			"ServiceType":     {Doc: "ServiceType is the kind of session store. Defaults to \"sql\"."},
			"DefaultBehavior": {Doc: "DefaultBehavior is always \"persistent\"."},
		},
	}
}

func (ArtifactServiceDefaults) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ArtifactServiceDefaults is where a new agent keeps artifacts.",
		Fields: map[string]apidoc.Field{
			"ServiceType": {Doc: "ServiceType is the kind of artifact store. Defaults to \"filesystem\"."},
			"BasePath":    {Doc: "BasePath is the base path for artifact storage. Defaults to \"/tmp/samv2\"."},
			"BucketName": {Doc: `BucketName is the object-storage bucket for artifacts: the artifact bucket if one is configured,
otherwise the platform's general bucket. Null when neither is configured.`},
			"EndpointURL":   {Doc: "EndpointURL is the object-storage endpoint. Null when not configured."},
			"ArtifactScope": {Doc: "ArtifactScope is always \"namespace\"."},
		},
	}
}

func (LogDefaults) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "LogDefaults is how a new agent logs.",
		Fields: map[string]apidoc.Field{
			"StdoutLogLevel": {Doc: "StdoutLogLevel is the level an agent logs to standard output at. Always \"info\"."},
			"LogFileLevel":   {Doc: "LogFileLevel is the level an agent logs to its log file at. Always \"info\"."},
			"LogFile":        {Doc: "LogFile is the pattern for an agent's log filename. Always \"{{agentName}}.log\"."},
		},
	}
}

func (AgentValidationLimits) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "AgentValidationLimits are the operator's bounds on an agent's instructions.",
		Fields: map[string]apidoc.Field{
			"AgentInstructionMin": {Doc: `AgentInstructionMin is the fewest characters a systemPrompt can have. Defaults to 1. A standard agent
still requires a systemPrompt.`},
			"AgentInstructionMax": {Doc: "AgentInstructionMax is the most characters a systemPrompt can have. Defaults to 32,000."},
		},
	}
}

func (UserAccess) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UserAccess is a user who can invoke a component.",
		Fields: map[string]apidoc.Field{
			"UserID":      {Doc: "UserID is the user's platform ID."},
			"DisplayName": {Doc: "DisplayName is the user's display name. Null when none is on record."},
			"UserName":    {Doc: "UserName is the user's username. Null when none is on record."},
			"Email":       {Doc: "Email is the user's email address. Null when none is on record."},
			"IsSystemUser": {Doc: `IsSystemUser is true for an Agent Mesh service account, such as an entrypoint's run-as principal, rather than
a person.

Deprecated: Reason: system users were renamed to service accounts; read isServiceAccount instead. Removal Date: 2027-12-01.`},
			"IsServiceAccount": {Doc: `IsServiceAccount is true for an Agent Mesh service account, such as an entrypoint's run-as principal, rather than
a person.`},
		},
	}
}

func (listAgentsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listAgentsRequest selects a page of agents.",
		Fields: map[string]apidoc.Field{
			"Page": {Doc: "Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters."},
			"ConnectorID": {Doc: `ConnectorID limits the list to agents that use this connector, by UUID. Leave it out to list every agent.
If it is not a UUID, the request fails with 400.`},
		},
	}
}

func (componentAccessRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "componentAccessRequest selects a page of the users who can invoke an agent or a workflow.",
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: "ID is the UUID of the agent or workflow. If the ID is unknown, the request fails with 404."},
			"Page": {Doc: "Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters."},
			"Search": {Doc: `Search limits the users to those whose display name, username, or email contains this text, ignoring
case and surrounding spaces. Leave it out to list every user who can invoke the component.`},
		},
	}
}
