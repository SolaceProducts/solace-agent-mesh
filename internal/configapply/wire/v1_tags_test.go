// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"reflect"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	evalplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
)

type v1Pair struct {
	kind     string
	copy     reflect.Type
	v1       reflect.Type
	copyFile string
	response bool
	renamed  map[string]string
	v1Only   map[string]string
	v2Only   map[string]string
	tagDiffs map[string]tagDiff
}

type tagDiff struct {
	tags   []string
	reason string
}

var agentRenamed = map[string]string{
	"type":       "agentType",
	"connectors": "connectorIds",
	"createdAt":  "createdTime",
	"updatedAt":  "updatedTime",
	"expiresAt":  "expiresTime",
}

var agentV1Only = map[string]string{
	"deploy": "v2 deploys through agentDeployments, so the request has no deploy flag",
}

var agentTagDiffs = map[string]tagDiff{
	"additionalConfigurations": {[]string{"json-object"}, "v1 sends it as raw JSON and needs json-object; v2 types it as an object"},
}

var v1Pairs = []v1Pair{
	{
		kind: "agent", copy: reflect.TypeFor[CreateAgentRequest](), v1: reflect.TypeFor[entplatform.CreateAgentRequest](),
		copyFile: "internal/configapply/wire/agent.go", v1Only: agentV1Only, tagDiffs: agentTagDiffs,
	},
	{
		kind: "agent", copy: reflect.TypeFor[UpdateAgentRequest](), v1: reflect.TypeFor[entplatform.UpdateAgentRequest](),
		copyFile: "internal/configapply/wire/agent.go", v1Only: agentV1Only, tagDiffs: agentTagDiffs,
	},
	{
		kind: "agent", copy: reflect.TypeFor[AgentResponse](), v1: reflect.TypeFor[entplatform.AgentResponse](),
		copyFile: "internal/configapply/wire/agent.go", response: true, renamed: agentRenamed, tagDiffs: agentTagDiffs,
	},
	{
		kind: "workflow", copy: reflect.TypeFor[CreateWorkflowRequest](), v1: reflect.TypeFor[entplatform.CreateWorkflowRequest](),
		copyFile: "internal/configapply/wire/workflow.go",
	},
	{
		kind: "workflow", copy: reflect.TypeFor[UpdateWorkflowRequest](), v1: reflect.TypeFor[entplatform.UpdateWorkflowRequest](),
		copyFile: "internal/configapply/wire/workflow.go",
	},
	{
		kind: "workflow", copy: reflect.TypeFor[WorkflowResponse](), v1: reflect.TypeFor[entplatform.WorkflowResponse](),
		copyFile: "internal/configapply/wire/workflow.go", response: true, renamed: timestampsRenamed,
	},
	{
		kind: "entrypoint", copy: reflect.TypeFor[CreateEntrypointRequest](), v1: reflect.TypeFor[entplatform.CreateGatewayRequest](),
		copyFile: "internal/configapply/wire/entrypoint.go", v1Only: entrypointV1Only,
	},
	{
		kind: "entrypoint", copy: reflect.TypeFor[UpdateEntrypointRequest](), v1: reflect.TypeFor[entplatform.UpdateGatewayRequest](),
		copyFile: "internal/configapply/wire/entrypoint.go", v1Only: entrypointV1Only,
		v2Only: map[string]string{
			"slug": "v2 accepts slug on PATCH only to reject it with 422, so it is read-only",
		},
		tagDiffs: map[string]tagDiff{
			"type":   {[]string{"read-only"}, "v1 ignores type on update; v2 rejects entrypointType on PATCH with 422, so it is never sent"},
			"values": {[]string{"merge"}, "v1 sends values whole on PATCH; the v2 PATCH merges them into the stored settings, so removed settings go as null"},
		},
	},
	{
		kind: "entrypoint", copy: reflect.TypeFor[EntrypointResponse](), v1: reflect.TypeFor[entplatform.GatewayResponse](),
		copyFile: "internal/configapply/wire/entrypoint.go", response: true,
		renamed: map[string]string{"type": "entrypointType", "createdAt": "createdTime", "updatedAt": "updatedTime"},
		v1Only: map[string]string{
			"dnsId":            "v2 serves networking details from /entrypoints/{id}/networkingDetails",
			"connectionStatus": "v2 reports health in runtimeStatus",
		},
	},
	{
		kind: "rbacRole", copy: reflect.TypeFor[CreateRoleRequest](), v1: reflect.TypeFor[entplatform.CreateRBACRoleRequest](),
		copyFile: "internal/configapply/wire/rbac.go",
	},
	{
		kind: "rbacRole", copy: reflect.TypeFor[ReplaceRoleRequest](), v1: reflect.TypeFor[entplatform.UpdateRBACRoleRequest](),
		copyFile: "internal/configapply/wire/rbac.go",
	},
	{
		kind: "rbacRole", copy: reflect.TypeFor[RoleResponse](), v1: reflect.TypeFor[entplatform.RBACRoleResponse](),
		copyFile: "internal/configapply/wire/rbac.go", response: true, renamed: timestampsRenamed,
	},
	{
		kind: "rbacGrant", copy: reflect.TypeFor[CreateGrantRequest](), v1: reflect.TypeFor[entplatform.CreateGrantRequest](),
		copyFile: "internal/configapply/wire/rbac.go",
	},
	{
		kind: "rbacGrant", copy: reflect.TypeFor[GrantResponse](), v1: reflect.TypeFor[entplatform.GrantResponse](),
		copyFile: "internal/configapply/wire/rbac.go", response: true,
	},
	{
		kind: "rbacClaimMapping", copy: reflect.TypeFor[CreateClaimMappingRequest](), v1: reflect.TypeFor[entplatform.CreateRBACClaimMappingRequest](),
		copyFile: "internal/configapply/wire/rbac.go",
	},
	{
		kind: "rbacClaimMapping", copy: reflect.TypeFor[ReplaceClaimMappingRequest](), v1: reflect.TypeFor[entplatform.UpdateRBACClaimMappingRequest](),
		copyFile: "internal/configapply/wire/rbac.go",
	},
	{
		kind: "rbacClaimMapping", copy: reflect.TypeFor[ClaimMappingResponse](), v1: reflect.TypeFor[entplatform.RBACClaimMappingResponse](),
		copyFile: "internal/configapply/wire/rbac.go", response: true, renamed: timestampsRenamed,
	},
	{
		kind: "systemUser", copy: reflect.TypeFor[CreateSystemUserRequest](), v1: reflect.TypeFor[entplatform.CreateSystemUserRequest](),
		copyFile: "internal/configapply/wire/rbac.go",
	},
	{
		kind: "systemUser", copy: reflect.TypeFor[ReplaceSystemUserRequest](), v1: reflect.TypeFor[entplatform.UpdateSystemUserRequest](),
		copyFile: "internal/configapply/wire/rbac.go",
	},
	{
		kind: "systemUser", copy: reflect.TypeFor[SystemUserResponse](), v1: reflect.TypeFor[entplatform.SystemUserResponse](),
		copyFile: "internal/configapply/wire/rbac.go", response: true,
	},
	{
		kind: "defaultRoles", copy: reflect.TypeFor[ReplaceDefaultRolesRequest](), v1: reflect.TypeFor[entplatform.SetDefaultRolesRequest](),
		copyFile: "internal/configapply/wire/rbac.go",
	},
	{
		kind: "defaultRoles", copy: reflect.TypeFor[DefaultRolesResponse](), v1: reflect.TypeFor[entplatform.DefaultRolesResponse](),
		copyFile: "internal/configapply/wire/rbac.go", response: true,
	},
	{
		kind: "webuiSettings", copy: reflect.TypeFor[ReplaceWebuiSettingsRequest](), v1: reflect.TypeFor[entplatform.UpdateWebuiSettingsRequest](),
		copyFile: "internal/configapply/wire/platform_settings.go",
	},
	{
		kind: "webuiSettings", copy: reflect.TypeFor[WebuiSettingsResponse](), v1: reflect.TypeFor[entplatform.WebuiSettingsResponse](),
		copyFile: "internal/configapply/wire/platform_settings.go", response: true, renamed: timestampsRenamed,
	},
	{
		kind: "profileProvider", copy: reflect.TypeFor[ReplaceProfileProviderRequest](), v1: reflect.TypeFor[entplatform.UpdateProfileProviderRequest](),
		copyFile: "internal/configapply/wire/platform_settings.go",
	},
	{
		kind: "profileProvider", copy: reflect.TypeFor[ProfileProviderResponse](), v1: reflect.TypeFor[entplatform.ProfileProviderResponse](),
		copyFile: "internal/configapply/wire/platform_settings.go", response: true, renamed: timestampsRenamed,
	},
	{
		kind: "apiKey", copy: reflect.TypeFor[CreateAPIKeyRequest](), v1: reflect.TypeFor[entplatform.CreateAPIKeyRequest](),
		copyFile: "internal/configapply/wire/platform_settings.go",
	},
	{
		kind: "apiKey", copy: reflect.TypeFor[UpdateAPIKeyRequest](), v1: reflect.TypeFor[entplatform.UpdateAPIKeyRequest](),
		copyFile: "internal/configapply/wire/platform_settings.go",
	},
	{
		kind: "apiKey", copy: reflect.TypeFor[APIKeyResponse](), v1: reflect.TypeFor[entplatform.APIKeyResponse](),
		copyFile: "internal/configapply/wire/platform_settings.go", response: true,
		v1Only: map[string]string{"secret": "v2 returns the plaintext only from the create, as CreatedAPIKey"},
	},
	{
		kind: "apiKey", copy: reflect.TypeFor[CreatedAPIKey](), v1: reflect.TypeFor[entplatform.APIKeyResponse](),
		copyFile: "internal/configapply/wire/platform_settings.go", response: true,
	},
	{
		kind: "dataset", copy: reflect.TypeFor[CreateDatasetRequest](), v1: reflect.TypeFor[evalplatform.CreateDatasetRequest](),
		copyFile: "internal/configapply/wire/dataset.go",
	},
	{
		kind: "dataset", copy: reflect.TypeFor[UpdateDatasetRequest](), v1: reflect.TypeFor[evalplatform.UpdateDatasetRequest](),
		copyFile: "internal/configapply/wire/dataset.go",
	},
	{
		kind: "dataset", copy: reflect.TypeFor[DatasetResponse](), v1: reflect.TypeFor[evalplatform.DatasetResponse](),
		copyFile: "internal/configapply/wire/dataset.go", response: true, renamed: timestampsRenamed,
	},
	{
		kind: "evaluator", copy: reflect.TypeFor[CreateEvaluatorRequest](), v1: reflect.TypeFor[evalplatform.CreateEvaluatorRequest](),
		copyFile: "internal/configapply/wire/evaluator.go",
	},
	{
		kind: "evaluator", copy: reflect.TypeFor[UpdateEvaluatorRequest](), v1: reflect.TypeFor[evalplatform.UpdateEvaluatorRequest](),
		copyFile: "internal/configapply/wire/evaluator.go",
		v2Only: map[string]string{
			"type": "v1 has no type on update; v2 accepts evaluatorType on PATCH only to reject it with 422, so it is read-only",
		},
	},
	{
		kind: "evaluator", copy: reflect.TypeFor[EvaluatorResponse](), v1: reflect.TypeFor[evalplatform.EvaluatorResponse](),
		copyFile: "internal/configapply/wire/evaluator.go", response: true,
		renamed: map[string]string{"type": "evaluatorType", "createdAt": "createdTime", "updatedAt": "updatedTime"},
	},
	{
		kind: "experiment", copy: reflect.TypeFor[CreateExperimentRequest](), v1: reflect.TypeFor[evalplatform.CreateExperimentRequest](),
		copyFile: "internal/configapply/wire/experiment.go",
	},
	{
		kind: "experiment", copy: reflect.TypeFor[UpdateExperimentRequest](), v1: reflect.TypeFor[evalplatform.UpdateExperimentRequest](),
		copyFile: "internal/configapply/wire/experiment.go",
	},
	{
		kind: "experiment", copy: reflect.TypeFor[ExperimentResponse](), v1: reflect.TypeFor[evalplatform.ExperimentResponse](),
		copyFile: "internal/configapply/wire/experiment.go", response: true, renamed: timestampsRenamed,
	},
	{
		kind: "model", copy: reflect.TypeFor[CreateModelRequest](), v1: reflect.TypeFor[platform.CreateModelConfigRequest](),
		copyFile: "internal/configapply/wire/model.go", v2Only: modelV2Only,
	},
	{
		kind: "model", copy: reflect.TypeFor[UpdateModelRequest](), v1: reflect.TypeFor[platform.UpdateModelConfigRequest](),
		copyFile: "internal/configapply/wire/model.go", v2Only: modelV2Only,
		tagDiffs: map[string]tagDiff{
			"apiBase":     {[]string{"clear-empty"}, "v1 keeps a left-out apiBase; the v2 PATCH resets it to the provider default with \"\""},
			"authConfig":  {[]string{"merge", "switch=type"}, "v1 sends authConfig whole; the v2 PATCH merges it, so removed settings go as null and a type change sends it whole"},
			"modelParams": {[]string{"clear-empty"}, "v1 keeps left-out modelParams; the v2 PATCH replaces them whole and clears them with {}"},
		},
	},
	{
		kind: "model", copy: reflect.TypeFor[ModelResponse](), v1: reflect.TypeFor[platform.ModelConfigResponse](),
		copyFile: "internal/configapply/wire/model.go", response: true, renamed: timestampsRenamed,
	},
	{
		kind: "connector", copy: reflect.TypeFor[CreateConnectorRequest](), v1: reflect.TypeFor[entplatform.CreateConnectorRequest](),
		copyFile: "internal/configapply/wire/connector.go",
	},
	{
		kind: "connector", copy: reflect.TypeFor[UpdateConnectorRequest](), v1: reflect.TypeFor[entplatform.UpdateConnectorRequest](),
		copyFile: "internal/configapply/wire/connector.go",
		v2Only: map[string]string{
			"type":    "v2 accepts connectorType on PATCH only to reject it with 422, so it is read-only",
			"subtype": "v2 accepts connectorSubtype on PATCH only to reject it with 422, so it is read-only",
		},
		tagDiffs: map[string]tagDiff{
			"values": {[]string{"clear-empty"}, "v1 keeps left-out values; the v2 PATCH replaces them whole and clears them with {}"},
		},
	},
	{
		kind: "connector", copy: reflect.TypeFor[ConnectorResponse](), v1: reflect.TypeFor[entplatform.ConnectorResponse](),
		copyFile: "internal/configapply/wire/connector.go", response: true,
		renamed: map[string]string{"type": "connectorType", "subtype": "connectorSubtype", "createdAt": "createdTime", "updatedAt": "updatedTime"},
	},
	{
		kind: "toolset", copy: reflect.TypeFor[CreateToolsetRequest](), v1: reflect.TypeFor[entplatform.CreateToolsetRequest](),
		copyFile: "internal/configapply/wire/toolset.go",
	},
	{
		kind: "toolset", copy: reflect.TypeFor[UpdateToolsetRequest](), v1: reflect.TypeFor[entplatform.UpdateToolsetRequest](),
		copyFile: "internal/configapply/wire/toolset.go",
		tagDiffs: map[string]tagDiff{
			"name": {[]string{"read-only"}, "v1 resends the name on every update; config apply finds a toolset by name, so the v2 PATCH never changes it"},
		},
	},
	{
		kind: "toolset", copy: reflect.TypeFor[ToolsetResponse](), v1: reflect.TypeFor[entplatform.ToolsetResponse](),
		copyFile: "internal/configapply/wire/toolset.go", response: true,
		renamed: map[string]string{"type": "origin", "createdAt": "createdTime", "updatedAt": "updatedTime"},
		v1Only:  toolsetBundleV1Only,
		tagDiffs: map[string]tagDiff{
			"runtime": {[]string{"read-only"}, "the runtime comes from the uploaded bundle, so it is never authored"},
		},
	},
	{
		kind: "toolset", copy: reflect.TypeFor[ToolsetConfiguration](), v1: reflect.TypeFor[entplatform.ToolsetConfigResponse](),
		copyFile: "internal/configapply/wire/toolset.go", response: true, renamed: map[string]string{"updatedAt": "updatedTime"},
		tagDiffs: map[string]tagDiff{
			"values":      {[]string{"secret"}, "v1 config apply reads the stored values untyped; the copy marks them secret so the PATCH diff skips <REDACTED> settings"},
			"updatedBy":   {[]string{"read-only"}, "the server records who changed the configuration"},
			"updatedTime": {[]string{"read-only"}, "the server records when the configuration changed"},
		},
	},
	{
		kind: "toolset", copy: reflect.TypeFor[UpdateToolsetConfigurationRequest](), v1: reflect.TypeFor[entplatform.ToolsetConfigPatchRequest](),
		copyFile: "internal/configapply/wire/toolset.go",
		tagDiffs: map[string]tagDiff{
			"values": {[]string{"secret", "merge=2"}, "v1 sends every setting spec.config sets; the v2 PATCH sends the changed ones and null for the removed ones, tool by tool"},
		},
	},
	{
		kind: "skill", copy: reflect.TypeFor[CreateSkillRequest](), v1: reflect.TypeFor[entplatform.CreateSkillRequest](),
		copyFile: "internal/configapply/wire/skill.go",
		v2Only: map[string]string{
			"name": "v1 keeps the name out of JSON with json:\"-\"; neither version sends it, since the server reads it from SKILL.md",
		},
	},
	{
		kind: "skill", copy: reflect.TypeFor[UpdateSkillRequest](), v1: reflect.TypeFor[entplatform.UpdateSkillRequest](),
		copyFile: "internal/configapply/wire/skill.go",
		v2Only: map[string]string{
			"name": "v2 accepts name on PATCH only to reject it with 422, so it is read-only",
		},
	},
	{
		kind: "skill", copy: reflect.TypeFor[SkillResponse](), v1: reflect.TypeFor[entplatform.SkillDetailResponse](),
		copyFile: "internal/configapply/wire/skill.go", response: true, renamed: timestampsRenamed,
		v1Only: map[string]string{
			"contentHash": "v2 nests it in bundle",
		},
	},
}

var modelV2Only = map[string]string{
	"authType":  "v2 takes authType beside authConfig.type, which wins; authors set authConfig.type, so it is read-only",
	"modelType": "v1 has no modelType on update; v2 rejects it on PATCH with 422, so it is read-only",
}

var toolsetBundleV1Only = map[string]string{
	"zipFilename": "v2 nests it in bundle as filename",
	"zipFileSize": "v2 nests it in bundle as sizeBytes",
	"contentHash": "v2 nests it in bundle",
	"files":       "v2 lists a toolset's tools, not its stored files",
}

var timestampsRenamed = map[string]string{"createdAt": "createdTime", "updatedAt": "updatedTime"}

var entrypointV1Only = map[string]string{
	"deploy": "v2 deploys through entrypointDeployments, so the request has no deploy flag",
}

func TestCopiesKeepEveryV1Tag(t *testing.T) {
	for _, p := range v1Pairs {
		t.Run(p.copy.Name(), func(t *testing.T) {
			v1Fields := wireFields(t, p.v1)
			byKey := map[string]wireField{}
			byKeyName := map[string]string{}
			for name, f := range wireFields(t, p.copy) {
				key := authoringKey(name, f)
				if p.response {
					key = name
				}
				byKey[key] = f
				byKeyName[key] = name
			}
			matched := map[string]bool{}
			for _, v1Name := range sortedKeys(v1Fields) {
				v1f := v1Fields[v1Name]
				key := v1Name
				if p.response {
					key = cmpOrRename(p.renamed, v1Name)
				}
				cp, ok := byKey[key]
				if !ok {
					if _, gone := p.v1Only[v1Name]; !gone {
						t.Errorf("%s: v1 %s has %q (%s), but wire.%s has no field with that %s. Add the field to %s (tagged key=%s if v2 renamed it), or record why it is gone in v1Only",
							p.kind, p.v1.Name(), v1Name, describeTags(cfgapplyTags(v1f.cfgapply)), p.copy.Name(), keyKind(p.response), p.copyFile, v1Name)
					}
					continue
				}
				matched[key] = true
				allowed := p.tagDiffs[key].tags
				want := withoutTags(cfgapplyTags(v1f.cfgapply), allowed)
				got := withoutTags(cfgapplyTags(cp.cfgapply), allowed)
				if !slices.Equal(want, got) {
					t.Errorf("%s: wire.%s.%s (json %q) has %s, but v1 %s has %s. Copy the v1 tags in %s, or record the tags allowed to differ in tagDiffs",
						p.kind, p.copy.Name(), cp.goName, byKeyName[key], describeTags(got), p.v1.Name(), describeTags(want), p.copyFile)
				}
			}
			for _, key := range sortedKeys(byKey) {
				if matched[key] {
					continue
				}
				f := byKey[key]
				if p.response && slices.Contains(cfgapplyTags(f.cfgapply), "read-only") {
					continue
				}
				if _, isNew := p.v2Only[key]; isNew {
					continue
				}
				t.Errorf("%s: wire.%s.%s (%s %q) has no v1 counterpart. %s (%s)",
					p.kind, p.copy.Name(), f.goName, keyKind(p.response), key, newFieldAdvice(p.response, key), p.copyFile)
			}
		})
	}
}

func withoutTags(tags, drop []string) []string {
	return slices.DeleteFunc(slices.Clone(tags), func(tag string) bool { return slices.Contains(drop, tag) })
}

func cmpOrRename(renamed map[string]string, name string) string {
	if to, ok := renamed[name]; ok {
		return to
	}
	return name
}

func newFieldAdvice(response bool, key string) string {
	if response {
		return "Tag it read-only if the server owns it"
	}
	return "If v2 renamed a v1 field, tag the field key=<the v1 name> so authors keep writing it; " + key + " would otherwise become a new YAML key"
}

func keyKind(response bool) string {
	if response {
		return "JSON name"
	}
	return "authoring key"
}
