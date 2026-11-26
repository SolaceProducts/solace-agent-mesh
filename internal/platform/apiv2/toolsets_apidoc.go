// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Toolset) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Toolset is a set of tools agents can use: a bundle uploaded to this server,
or one that ships with Agent Mesh.`,
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: "ID is the toolset's UUID, or for a built-in toolset its id, such as builtin_artifact_tools."},
			"Type": {Doc: "Type is always \"toolset\"."},
			"Origin": {Doc: `Origin is where the toolset comes from: custom for one uploaded to this server, or builtin for
one that ships with Agent Mesh and cannot be changed.`},
			"Name":        {Doc: "Name is the toolset's name: unique, and the prefix of its tools' names."},
			"Description": {Doc: "Description says what the toolset's tools do. Null when none was set."},
			"Runtime": {Doc: `Runtime is the language the bundle's tools run in: python, go, or unknown. Null until a bundle
is uploaded, and for a built-in toolset.`},
			"DiscoveryStatus": {Doc: `DiscoveryStatus is the state of tool discovery: created until a bundle is uploaded, pending
while the tool runtime discovers the tools, ready when it has, failed when discovery failed,
and removed when the tool runtime dropped them. Null for a built-in toolset.`},
			"DiscoveryErrors": {Doc: "DiscoveryErrors says why discovery failed. Empty unless discoveryStatus is failed."},
			"Bundle":          {Doc: "Bundle is the uploaded ZIP. Null until one is uploaded, and for a built-in toolset."},
			"Tools":           {Doc: "Tools are the tools the toolset provides. A built-in toolset's tools carry only their names."},
			"Agents":          {Doc: "Agents are the agents that use the toolset."},
			"ConfigSummary": {Doc: `ConfigSummary says which required settings are still unset. Null until discoveryStatus is
ready, and for a built-in toolset.`},
			"Capabilities": {Doc: `Capabilities are the features the toolset's manifest declares, such as profileProvider. The
manifest sets the keys.`},
			"Default": {Doc: "Default is true for a built-in toolset a new agent gets unless it is removed."},
			"Aliases": {Doc: `Aliases are other names an agent's configuration can use for a built-in toolset. Empty for a
custom toolset.`},
			"CreatedBy":   {Doc: "CreatedBy identifies the user who created the toolset. Null for a built-in toolset."},
			"UpdatedBy":   {Doc: "UpdatedBy identifies the user who last changed the toolset. Null for a built-in toolset."},
			"CreatedTime": {Doc: "CreatedTime is when the toolset was created. Null for a built-in toolset."},
			"UpdatedTime": {Doc: "UpdatedTime is when the toolset was last changed. Null for a built-in toolset."},
		},
	}
}

func (ToolsetBundle) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ToolsetBundle is the ZIP a toolset's tools were uploaded in. GET
/toolsets/{id}/bundle downloads it.`,
		Fields: map[string]apidoc.Field{
			"Filename":    {Doc: "Filename is the name the ZIP was uploaded with."},
			"SizeBytes":   {Doc: "SizeBytes is the ZIP's size in bytes. Null when it was not recorded."},
			"ContentHash": {Doc: "ContentHash is the hex SHA-256 of the ZIP, for telling whether a local bundle has changed."},
		},
	}
}

func (ToolsetTool) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ToolsetTool is one tool a toolset provides.",
		Fields: map[string]apidoc.Field{
			"ToolName":       {Doc: "ToolName is the tool's name."},
			"Description":    {Doc: "Description says what the tool does. Null for a built-in toolset's tool."},
			"TimeoutSeconds": {Doc: "TimeoutSeconds is how long one call can run, in seconds. Null for a built-in toolset's tool."},
			"SandboxProfile": {Doc: `SandboxProfile is the sandbox the tool runs in, as its manifest names it. Null for a built-in
toolset's tool.`},
			"Parameters": {Doc: "Parameters is the JSON Schema of the tool's arguments. Null when the tool declares none."},
			"ConfigSchema": {Doc: `ConfigSchema lists the settings the tool reads from its toolset's configuration, as its
manifest declares them. Null when the manifest declares none.`},
		},
	}
}

func (ToolsetAgent) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ToolsetAgent is an agent that uses a toolset.",
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: "ID is the agent's UUID."},
			"Name":        {Doc: "Name is the agent's name."},
			"Description": {Doc: "Description says what the agent does. Null when the agent has none."},
		},
	}
}

func (ConfigSummary) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ConfigSummary says which of a toolset's or skill's required settings are set.",
		Fields: map[string]apidoc.Field{
			"RequiredFields": {Doc: "RequiredFields names every required setting the tools declare."},
			"RequiredCoveredByDefaults": {Doc: `RequiredCoveredByDefaults names the required settings the toolset's or skill's own
configuration sets.`},
			"RequiresAgentConfig": {Doc: "RequiresAgentConfig is true when some required setting is unset, so each agent must set it."},
		},
	}
}

func (ToolsetConfiguration) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ToolsetConfiguration is the settings a toolset gives every agent that uses
it.`,
		Fields: map[string]apidoc.Field{
			"Values": {Doc: `Values maps each tool's name to its settings, and can also hold the reserved auth, hil, and
required_scopes blocks. A secret setting reads "<REDACTED>". Each tool's configSchema sets the
keys of its settings. Empty when nothing is set, and for a built-in toolset.`},
			"UpdatedBy":   {Doc: "UpdatedBy identifies the user who last changed the configuration. Null when it was never set."},
			"UpdatedTime": {Doc: "UpdatedTime is when the configuration was last changed. Null when it was never set."},
		},
	}
}

func (CreateToolsetRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateToolsetRequest creates a toolset with no bundle. Upload its tools with
PUT /toolsets/{id}/bundle or POST /toolsetUploads.`,
		Fields: map[string]apidoc.Field{
			"Name": {Doc: `Name is the toolset's name: up to 128 letters, digits, hyphens, and underscores, starting with
a letter. It must be unique and must not be the name of a built-in toolset.`},
			"Description": {Doc: "Description says what the toolset's tools do. Leave it out for none."},
		},
	}
}

func (UpdateToolsetRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UpdateToolsetRequest changes the fields it includes.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: `ID is the UUID of the toolset to update, or for a built-in toolset
its id, such as builtin_artifact_tools.`},
			"Name": {Doc: `Name is the toolset's name, with the same rules as on create. It cannot change after a bundle
is uploaded. Leave it out to keep it. Null or an empty string fails with 422.`},
			"Description": {Doc: `Description says what the toolset's tools do. Leave it out to keep it, or send null or "" to
clear it.`},
		},
	}
}

func (UploadToolsetBundleRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UploadToolsetBundleRequest replaces a toolset's bundle.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: `ID is the UUID of the toolset whose bundle to replace, or for a built-in toolset
its id, such as builtin_artifact_tools.`},
			"File": {Doc: `File is the toolset's ZIP, sent as the file part of a multipart/form-data body, with a filename
ending in .zip. Up to 100 MB, and up to 500 MB and 10,000 entries unpacked.`},
		},
	}
}

func (UpdateToolsetConfigurationRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UpdateToolsetConfigurationRequest changes the settings it includes.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: `ID is the UUID of the toolset whose configuration to change, or for a built-in toolset
its id, such as builtin_artifact_tools.`},
			"Values": {Doc: `Values maps each tool's name to the settings to change, and keeps the rest. A setting sent as
null is cleared, but a secret sent as null or "<REDACTED>" is kept. Leave values out to change
nothing. Null fails with 422. Each tool's configSchema sets the keys of its settings.`},
		},
	}
}

func (ToolsetSecretsDiffRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ToolsetSecretsDiffRequest carries the settings to compare with a toolset's stored secrets.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: `ID is the UUID of the toolset whose stored secrets are compared, or for a built-in toolset
its id, such as builtin_artifact_tools.`},
			"Values": {Doc: `Values holds the settings to compare, keyed by setting name or nested under each tool's name.
Only secret settings are compared. Each tool's configSchema sets the keys of its settings.`},
		},
	}
}

func (listToolsetsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listToolsetsRequest selects a page of toolsets.",
		Fields: map[string]apidoc.Field{
			"Page":   {Doc: "Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters."},
			"Origin": {Doc: "Origin limits the list to custom or builtin toolsets. Leave it out for both, built-in first."},
			"DiscoveryStatus": {Doc: `DiscoveryStatus limits the list to custom toolsets in one discovery status: pending, ready,
failed, or removed.`},
		},
	}
}

func (toolsetIDRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "toolsetIDRequest addresses one toolset.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the toolset's UUID, or for a built-in toolset its id, such as builtin_artifact_tools."},
		},
	}
}
