// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package manifest

import "github.com/SolaceProducts/solace-agent-mesh/internal/configapply/codegen"

var ManifestSchema = codegen.KindSchema{
	Kind:        "manifest",
	PluralPath:  "(top-level document)",
	Description: "Top-level document that lists which resources to apply, where to fetch any imported resources from, and how to authenticate to the platform.",
	Fields: []codegen.FieldSchema{
		{
			JSONName:    "kind",
			GoType:      "string",
			Required:    true,
			MinLen:      -1,
			MaxLen:      -1,
			OneOf:       []string{"manifest"},
			Description: "Discriminator. Always \"manifest\".",
		},
		{
			JSONName:    "name",
			GoType:      "string",
			Required:    true,
			MinLen:      -1,
			MaxLen:      -1,
			Description: "Human-readable manifest name (used in plan/apply output).",
		},
		{
			JSONName:    "description",
			GoType:      "string",
			MinLen:      -1,
			MaxLen:      -1,
			Description: "Free-form description of what this manifest deploys.",
		},
		{
			JSONName:    "target",
			GoType:      "TargetBlock",
			Required:    true,
			MinLen:      -1,
			MaxLen:      -1,
			Description: "Platform endpoint and auth: { name: \"dev\", url: \"...\", auth: { type: \"bearer_token\" | \"oauth\", envVar: \"SAM_PLATFORM_TOKEN\" } }. `name` is the per-target cache key for `sam auth login` (defaults to URL host). For oauth, run `sam auth login` first to populate the token cache. SAM_PLATFORM_TOKEN env var overrides cached oauth credentials at apply time.",
		},
		{
			JSONName:    "defaults",
			GoType:      "Defaults",
			MinLen:      -1,
			MaxLen:      -1,
			Description: "Reserved for cross-cutting defaults. Parsed but not applied: no field in it, including namespace, currently affects any resource.",
		},
		{
			JSONName:    "variables",
			GoType:      "map[string]string",
			MinLen:      -1,
			MaxLen:      -1,
			Description: "Manifest-scoped variables substituted into per-resource YAML via ${VAR} expansion. A manifest variable wins over an env var of the same name; the environment is consulted only for names not declared here.",
		},
		{
			JSONName:    "sources",
			GoType:      "map[string]string",
			MinLen:      -1,
			MaxLen:      -1,
			Description: "Pip-style source URLs keyed by source name. Resource entries can reference imports from a source via name@source.",
		},
		{
			JSONName:    "resources",
			GoType:      "map[string][]ResourceEntry",
			Required:    true,
			MinLen:      -1,
			MaxLen:      -1,
			Description: "Per-kind list of resources to apply, keyed by plural kind name (models, agents, entrypoints, linkAgents, workflows, toolsets, connectors, skills, datasets, evaluators, experiments, rbacRoles, rbacClaimMappings, rbacGrants, serviceAccounts, budgets; systemUsers is accepted for serviceAccounts until 2027-12-01). Each entry is either a bare local name, a name@source import, or {from: name@source, as: aliased}.",
		},
		{
			JSONName:    "platform",
			GoType:      "PlatformBlock",
			MinLen:      -1,
			MaxLen:      -1,
			Description: "Deployment-wide platform settings, as opposed to per-resource ones: { profileProvider: { toolset, claim }, defaultRoles: { roles: [roleName] }, webuiSettings: { appName, welcomeMessage, collectFeedback, publishFeedback, feedbackTaskInfoLevel, disclaimerText, logoUrl, smallLogoUrl, systemPurpose, responseFormat } }. defaultRoles names the roles every new user receives; an absent defaultRoles block leaves the platform's default-role set unmanaged, while an empty roles list clears the override and restores the YAML fallback. Each declared role must be a config-defined or managed/in-DB role — built-in roles may be named, but cannot be authored. webuiSettings overrides the web UI's instance-wide branding, assistant defaults, and feedback handling on top of the gateway's own YAML/env values. publishFeedback publishes each submitted rating to the event mesh and is off unless set. Clearing an override is a removal and is gated: a field written as null clears on any apply, while a field left out — including every field of an empty {} block — keeps its stored value until apply --prune. Removing the whole webuiSettings key from a manifest that still declares other platform settings marks every override for removal under --prune; a manifest with no platform block at all manages none of these settings.",
		},
	},
}
