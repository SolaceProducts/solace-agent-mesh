// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Skill) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Skill is a bundle of instructions, reference files, assets, and tools that an agent loads when a task calls
for it: one uploaded to this server, or one that ships with Agent Mesh.`,
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: "ID is the skill's UUID, or for a built-in skill its name, such as sam-docs."},
			"Type": {Doc: "Type is always \"skill\"."},
			"Origin": {Doc: `Origin is where the skill comes from: custom for one uploaded to this server, or builtin for one that ships
with Agent Mesh and cannot be changed.`},
			"Name":        {Doc: "Name is the skill's name, taken from the name in its SKILL.md. It cannot be changed."},
			"Label":       {Doc: "Label is the name a built-in skill is shown under. Null for a custom skill."},
			"Description": {Doc: "Description tells agents when to use the skill. Null when none was set."},
			"Tags":        {Doc: "Tags are the labels the skill's SKILL.md declares. Empty when it declares none."},
			"DiscoveryStatus": {Doc: `DiscoveryStatus is the bundle's discovery state: created until a bundle is uploaded, pending while the tool
runtime reads it, ready when it has, and failed when reading it failed. A built-in skill is always ready.`},
			"HasReferences":  {Doc: "HasReferences is true when the bundle holds files under references/."},
			"HasAssets":      {Doc: "HasAssets is true when the bundle holds files under assets/."},
			"HasTools":       {Doc: "HasTools is true when the bundle holds tools the skill runs."},
			"ResourceCount":  {Doc: "ResourceCount is the number of reference and asset files in the bundle."},
			"ReferenceCount": {Doc: "ReferenceCount is the number of files under references/."},
			"AssetCount":     {Doc: "AssetCount is the number of files under assets/."},
			"BundledTools":   {Doc: "BundledTools are the tools the bundle holds. Empty until discoveryStatus is ready."},
			"Toolsets":       {Doc: "Toolsets are the names of the toolsets the skill's SKILL.md declares it needs."},
			"Bundle":         {Doc: "Bundle is the uploaded ZIP. Null until one is uploaded, and for a built-in skill."},
			"Agents":         {Doc: "Agents are the agents that use the skill."},
			"ConfigSummary": {Doc: `ConfigSummary says which required settings are still unset. Null until discoveryStatus is ready, and for a
built-in skill.`},
			"CreatedBy":   {Doc: "CreatedBy identifies the user who created the skill. Null for a built-in skill."},
			"UpdatedBy":   {Doc: "UpdatedBy identifies the user who last changed the skill. Null for a built-in skill."},
			"CreatedTime": {Doc: "CreatedTime is when the skill was created. Null for a built-in skill."},
			"UpdatedTime": {Doc: "UpdatedTime is when the skill was last changed. Null for a built-in skill."},
		},
	}
}

func (SkillBundle) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SkillBundle is the ZIP a skill was uploaded in. GET /skills/{id}/bundle
downloads it.`,
		Fields: map[string]apidoc.Field{
			"ContentHash": {Doc: `ContentHash is the hex SHA-256 of the ZIP, for telling whether a local bundle has changed. Null for a skill
uploaded before hashes were recorded.`},
		},
	}
}

func (SkillBundledTool) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "SkillBundledTool is a tool a skill's bundle holds.",
		Fields: map[string]apidoc.Field{
			"ToolName": {Doc: `ToolName is the tool's name, which starts with the skill's name and two underscores. The skill's configuration
keys the tool's settings by the name that follows them.`},
			"Description":  {Doc: "Description says what the tool does. Null when the tool has none."},
			"Instructions": {Doc: "Instructions are the guidance that tells the agent how to use the tool. Null when there are none."},
			"Parameters":   {Doc: "Parameters is the JSON Schema of the tool's arguments. Null when the tool declares none."},
			"ArtifactParams": {Doc: `ArtifactParams maps the name of each argument that names an artifact to how it does so. Empty when no
argument names one.`},
			"ConfigSchema": {Doc: `ConfigSchema lists the settings the tool reads from the skill's configuration, as its manifest declares them.
Null when the manifest declares none.`},
		},
	}
}

func (SkillArtifactParam) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "SkillArtifactParam says how a tool argument names an artifact.",
		Fields: map[string]apidoc.Field{
			"IsList":     {Doc: "IsList is true when the argument is a list of artifact names."},
			"IsOptional": {Doc: "IsOptional is true when the argument can be left out."},
			"FilenameKey": {Doc: `FilenameKey names the field of each list item that holds the artifact's filename. Null when the argument is a
plain filename or a list of filenames.`},
		},
	}
}

func (SkillAgent) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "SkillAgent is an agent that uses a skill.",
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: "ID is the agent's UUID."},
			"Name":        {Doc: "Name is the agent's name."},
			"Description": {Doc: "Description says what the agent does. Null when the agent has none."},
		},
	}
}

func (SkillFile) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "SkillFile is one reference or asset file in a skill's bundle.",
		Fields: map[string]apidoc.Field{
			"Path": {Doc: `Path is the file's path from the bundle's root, such as references/guide.md. GET /skills/{id}/fileContent
takes it as its path.`},
			"Category":  {Doc: "Category is the file's kind: reference for a file under references/, or asset for one under assets/."},
			"SizeBytes": {Doc: "SizeBytes is the file's size in bytes, unpacked."},
		},
	}
}

func (SkillConfiguration) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "SkillConfiguration is the settings a skill gives every agent that uses it.",
		Fields: map[string]apidoc.Field{
			"Values": {Doc: `Values maps each bundled tool's name, without the skill's prefix, to its settings. A secret setting reads
"<REDACTED>". Each tool's configSchema sets the keys of its settings. Empty when nothing is set, and for a
built-in skill.`},
			"UpdatedBy":   {Doc: "UpdatedBy identifies the user who last changed the configuration. Null when it was never set."},
			"UpdatedTime": {Doc: "UpdatedTime is when the configuration was last changed. Null when it was never set."},
		},
	}
}

func (CreateSkillRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateSkillRequest creates a skill from a bundle. The skill's name is the name in the bundle's SKILL.md.",
		Fields: map[string]apidoc.Field{
			"File": {Doc: `File is the skill's ZIP, sent as the file part of a multipart/form-data body, with a filename ending in .zip.
Up to 100 MB, and up to 500 MB and 10,000 entries unpacked. Its SKILL.md, at the root or in one top-level
directory, must start with YAML frontmatter that names the skill. The name has up to 128 letters, digits,
hyphens, and underscores, and starts with a letter. It must be unique and must not start with the reserved
prefix of a built-in skill.`},
			"Description": {Doc: `Description tells agents when to use the skill, sent as a part of the multipart/form-data body. Leave it out
for none.`},
		},
	}
}

func (UpdateSkillRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UpdateSkillRequest changes the fields it includes.",
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: "ID is the UUID of the skill to update, or for a built-in skill its name, such as sam-docs."},
			"Description": {Doc: "Description tells agents when to use the skill. Leave it out to keep it. Null or an empty string fails with 422."},
			"Name":        {Doc: "Name cannot be changed, because it comes from the bundle's SKILL.md: sending it, even as null, fails with 422."},
		},
	}
}

func (ReplaceSkillBundleRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ReplaceSkillBundleRequest replaces a skill's bundle.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the UUID of the skill whose bundle to replace, or for a built-in skill its name, such as sam-docs."},
			"File": {Doc: `File is the skill's ZIP, sent as the file part of a multipart/form-data body, with a filename ending in .zip.
Its SKILL.md must name the skill. Up to 100 MB, and up to 500 MB and 10,000 entries unpacked.`},
		},
	}
}

func (UpdateSkillConfigurationRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UpdateSkillConfigurationRequest changes the settings it includes.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the UUID of the skill whose configuration to change, or for a built-in skill its name, such as sam-docs."},
			"Values": {Doc: `Values maps each bundled tool's name, without the skill's prefix, to the settings to change, and keeps the
rest. A setting sent as null is cleared, but a secret sent as null or "<REDACTED>" is kept. Leave values out to
change nothing. Null fails with 422. Each tool's configSchema sets the keys of its settings.`},
		},
	}
}

func (SkillSecretsDiffRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "SkillSecretsDiffRequest carries the settings to compare with a skill's stored secrets.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the UUID of the skill whose stored secrets are compared, or for a built-in skill its name, such as sam-docs."},
			"Values": {Doc: `Values holds the settings to compare, nested under each bundled tool's name without the skill's prefix. Only
secret settings are compared. Each tool's configSchema sets the keys of its settings.`},
		},
	}
}

func (listSkillsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listSkillsRequest selects a page of skills.",
		Fields: map[string]apidoc.Field{
			"Page":   {Doc: "Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters."},
			"Origin": {Doc: "Origin limits the list to custom or builtin skills. Leave it out for both, built-in first."},
		},
	}
}

func (skillIDRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "skillIDRequest addresses one skill.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the skill's UUID, or for a built-in skill its name, such as sam-docs."},
		},
	}
}

func (listSkillFilesRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listSkillFilesRequest selects a page of a skill's files.",
		Fields: map[string]apidoc.Field{
			"ID":       {Doc: "ID is the skill's UUID, or for a built-in skill its name, such as sam-docs."},
			"Page":     {Doc: "Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters."},
			"Category": {Doc: "Category limits the list to reference or asset files. Leave it out for both, references first."},
		},
	}
}

func (getSkillFileContentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "getSkillFileContentRequest names one file in a skill's bundle.",
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: "ID is the skill's UUID, or for a built-in skill its name, such as sam-docs."},
			"Path": {Doc: "Path is the file's path from the bundle's root: SKILL.md, or a file under references/ or assets/."},
		},
	}
}
