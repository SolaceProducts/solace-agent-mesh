// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"cmp"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type copyOf struct {
	kind      string
	copy      reflect.Type
	apiv2     reflect.Type
	copyFile  string
	apiv2File string
}

var copies = []copyOf{
	{"agent", reflect.TypeFor[CreateAgentRequest](), reflect.TypeFor[apiv2.CreateAgentRequest](), "internal/configapply/wire/agent.go", "internal/platform/apiv2/agents.go"},
	{"agent", reflect.TypeFor[UpdateAgentRequest](), reflect.TypeFor[apiv2.UpdateAgentRequest](), "internal/configapply/wire/agent.go", "internal/platform/apiv2/agents.go"},
	{"agent", reflect.TypeFor[AgentResponse](), reflect.TypeFor[apiv2.Agent](), "internal/configapply/wire/agent.go", "internal/platform/apiv2/agents.go"},
	{"workflow", reflect.TypeFor[CreateWorkflowRequest](), reflect.TypeFor[apiv2.CreateWorkflowRequest](), "internal/configapply/wire/workflow.go", "internal/platform/apiv2/workflows.go"},
	{"workflow", reflect.TypeFor[UpdateWorkflowRequest](), reflect.TypeFor[apiv2.UpdateWorkflowRequest](), "internal/configapply/wire/workflow.go", "internal/platform/apiv2/workflows.go"},
	{"workflow", reflect.TypeFor[WorkflowResponse](), reflect.TypeFor[apiv2.Workflow](), "internal/configapply/wire/workflow.go", "internal/platform/apiv2/workflows.go"},
	{"entrypoint", reflect.TypeFor[CreateEntrypointRequest](), reflect.TypeFor[apiv2.CreateEntrypointRequest](), "internal/configapply/wire/entrypoint.go", "internal/platform/apiv2/entrypoints.go"},
	{"entrypoint", reflect.TypeFor[UpdateEntrypointRequest](), reflect.TypeFor[apiv2.UpdateEntrypointRequest](), "internal/configapply/wire/entrypoint.go", "internal/platform/apiv2/entrypoints.go"},
	{"entrypoint", reflect.TypeFor[EntrypointResponse](), reflect.TypeFor[apiv2.Entrypoint](), "internal/configapply/wire/entrypoint.go", "internal/platform/apiv2/entrypoints.go"},
	{"linkAgent", reflect.TypeFor[CreateLinkAgentRequest](), reflect.TypeFor[apiv2.CreateLinkAgentRequest](), "internal/configapply/wire/link_agent.go", "internal/platform/apiv2/link_agents.go"},
	{"linkAgent", reflect.TypeFor[UpdateLinkAgentRequest](), reflect.TypeFor[apiv2.UpdateLinkAgentRequest](), "internal/configapply/wire/link_agent.go", "internal/platform/apiv2/link_agents.go"},
	{"linkAgent", reflect.TypeFor[LinkAgentResponse](), reflect.TypeFor[apiv2.LinkAgent](), "internal/configapply/wire/link_agent.go", "internal/platform/apiv2/link_agents.go"},
	{"rbacRole", reflect.TypeFor[CreateRoleRequest](), reflect.TypeFor[apiv2.CreateRoleRequest](), "internal/configapply/wire/rbac.go", "internal/platform/apiv2/rbac_roles.go"},
	{"rbacRole", reflect.TypeFor[ReplaceRoleRequest](), reflect.TypeFor[apiv2.ReplaceRoleRequest](), "internal/configapply/wire/rbac.go", "internal/platform/apiv2/rbac_roles.go"},
	{"rbacRole", reflect.TypeFor[RoleResponse](), reflect.TypeFor[apiv2.Role](), "internal/configapply/wire/rbac.go", "internal/platform/apiv2/rbac_roles.go"},
	{"rbacGrant", reflect.TypeFor[CreateGrantRequest](), reflect.TypeFor[apiv2.CreateGrantRequest](), "internal/configapply/wire/rbac.go", "internal/platform/apiv2/rbac_grants.go"},
	{"rbacGrant", reflect.TypeFor[UpdateGrantRequest](), reflect.TypeFor[apiv2.UpdateGrantRequest](), "internal/configapply/wire/rbac.go", "internal/platform/apiv2/rbac_grants.go"},
	{"rbacGrant", reflect.TypeFor[GrantResponse](), reflect.TypeFor[apiv2.Grant](), "internal/configapply/wire/rbac.go", "internal/platform/apiv2/rbac_grants.go"},
	{"rbacClaimMapping", reflect.TypeFor[CreateClaimMappingRequest](), reflect.TypeFor[apiv2.CreateClaimMappingRequest](), "internal/configapply/wire/rbac.go", "internal/platform/apiv2/rbac_claim_mappings.go"},
	{"rbacClaimMapping", reflect.TypeFor[ReplaceClaimMappingRequest](), reflect.TypeFor[apiv2.ReplaceClaimMappingRequest](), "internal/configapply/wire/rbac.go", "internal/platform/apiv2/rbac_claim_mappings.go"},
	{"rbacClaimMapping", reflect.TypeFor[ClaimMappingResponse](), reflect.TypeFor[apiv2.ClaimMapping](), "internal/configapply/wire/rbac.go", "internal/platform/apiv2/rbac_claim_mappings.go"},
	{"systemUser", reflect.TypeFor[CreateSystemUserRequest](), reflect.TypeFor[apiv2.CreateServiceAccountRequest](), "internal/configapply/wire/rbac.go", "internal/platform/apiv2/rbac_system_users.go"},
	{"systemUser", reflect.TypeFor[ReplaceSystemUserRequest](), reflect.TypeFor[apiv2.ReplaceServiceAccountRequest](), "internal/configapply/wire/rbac.go", "internal/platform/apiv2/rbac_system_users.go"},
	{"systemUser", reflect.TypeFor[SystemUserResponse](), reflect.TypeFor[apiv2.ServiceAccount](), "internal/configapply/wire/rbac.go", "internal/platform/apiv2/rbac_system_users.go"},
	{"defaultRoles", reflect.TypeFor[ReplaceDefaultRolesRequest](), reflect.TypeFor[apiv2.ReplaceDefaultRolesRequest](), "internal/configapply/wire/rbac.go", "internal/platform/apiv2/rbac_default_roles.go"},
	{"defaultRoles", reflect.TypeFor[DefaultRolesResponse](), reflect.TypeFor[apiv2.DefaultRoles](), "internal/configapply/wire/rbac.go", "internal/platform/apiv2/rbac_default_roles.go"},
	{"webuiSettings", reflect.TypeFor[ReplaceWebuiSettingsRequest](), reflect.TypeFor[apiv2.ReplaceWebuiSettingsRequest](), "internal/configapply/wire/platform_settings.go", "internal/platform/apiv2/webui_settings.go"},
	{"webuiSettings", reflect.TypeFor[WebuiSettingsResponse](), reflect.TypeFor[apiv2.WebuiSettings](), "internal/configapply/wire/platform_settings.go", "internal/platform/apiv2/webui_settings.go"},
	{"profileProvider", reflect.TypeFor[ReplaceProfileProviderRequest](), reflect.TypeFor[apiv2.ReplaceProfileProviderRequest](), "internal/configapply/wire/platform_settings.go", "internal/platform/apiv2/profile_provider.go"},
	{"profileProvider", reflect.TypeFor[ProfileProviderResponse](), reflect.TypeFor[apiv2.ProfileProvider](), "internal/configapply/wire/platform_settings.go", "internal/platform/apiv2/profile_provider.go"},
	{"apiKey", reflect.TypeFor[CreateAPIKeyRequest](), reflect.TypeFor[apiv2.CreateAPIKeyRequest](), "internal/configapply/wire/platform_settings.go", "internal/platform/apiv2/api_keys.go"},
	{"apiKey", reflect.TypeFor[UpdateAPIKeyRequest](), reflect.TypeFor[apiv2.UpdateAPIKeyRequest](), "internal/configapply/wire/platform_settings.go", "internal/platform/apiv2/api_keys.go"},
	{"apiKey", reflect.TypeFor[APIKeyResponse](), reflect.TypeFor[apiv2.APIKey](), "internal/configapply/wire/platform_settings.go", "internal/platform/apiv2/api_keys.go"},
	{"apiKey", reflect.TypeFor[CreatedAPIKey](), reflect.TypeFor[apiv2.CreatedAPIKey](), "internal/configapply/wire/platform_settings.go", "internal/platform/apiv2/api_keys.go"},
	{"dataset", reflect.TypeFor[CreateDatasetRequest](), reflect.TypeFor[apiv2.CreateDatasetRequest](), "internal/configapply/wire/dataset.go", "internal/platform/apiv2/evaluation_datasets.go"},
	{"dataset", reflect.TypeFor[UpdateDatasetRequest](), reflect.TypeFor[apiv2.UpdateDatasetRequest](), "internal/configapply/wire/dataset.go", "internal/platform/apiv2/evaluation_datasets.go"},
	{"dataset", reflect.TypeFor[DatasetResponse](), reflect.TypeFor[apiv2.Dataset](), "internal/configapply/wire/dataset.go", "internal/platform/apiv2/evaluation_datasets.go"},
	{"example", reflect.TypeFor[CreateExampleRequest](), reflect.TypeFor[apiv2.CreateExampleRequest](), "internal/configapply/wire/dataset.go", "internal/platform/apiv2/evaluation_examples.go"},
	{"example", reflect.TypeFor[UpdateExampleRequest](), reflect.TypeFor[apiv2.UpdateExampleRequest](), "internal/configapply/wire/dataset.go", "internal/platform/apiv2/evaluation_examples.go"},
	{"example", reflect.TypeFor[ExampleResponse](), reflect.TypeFor[apiv2.Example](), "internal/configapply/wire/dataset.go", "internal/platform/apiv2/evaluation_examples.go"},
	{"evaluator", reflect.TypeFor[CreateEvaluatorRequest](), reflect.TypeFor[apiv2.CreateEvaluatorRequest](), "internal/configapply/wire/evaluator.go", "internal/platform/apiv2/evaluation_evaluators.go"},
	{"evaluator", reflect.TypeFor[UpdateEvaluatorRequest](), reflect.TypeFor[apiv2.UpdateEvaluatorRequest](), "internal/configapply/wire/evaluator.go", "internal/platform/apiv2/evaluation_evaluators.go"},
	{"evaluator", reflect.TypeFor[EvaluatorResponse](), reflect.TypeFor[apiv2.Evaluator](), "internal/configapply/wire/evaluator.go", "internal/platform/apiv2/evaluation_evaluators.go"},
	{"experiment", reflect.TypeFor[CreateExperimentRequest](), reflect.TypeFor[apiv2.CreateExperimentRequest](), "internal/configapply/wire/experiment.go", "internal/platform/apiv2/evaluation_experiments.go"},
	{"experiment", reflect.TypeFor[UpdateExperimentRequest](), reflect.TypeFor[apiv2.UpdateExperimentRequest](), "internal/configapply/wire/experiment.go", "internal/platform/apiv2/evaluation_experiments.go"},
	{"experiment", reflect.TypeFor[ExperimentResponse](), reflect.TypeFor[apiv2.Experiment](), "internal/configapply/wire/experiment.go", "internal/platform/apiv2/evaluation_experiments.go"},
	{"model", reflect.TypeFor[CreateModelRequest](), reflect.TypeFor[apiv2.CreateModelRequest](), "internal/configapply/wire/model.go", "internal/platform/apiv2/models.go"},
	{"model", reflect.TypeFor[UpdateModelRequest](), reflect.TypeFor[apiv2.UpdateModelRequest](), "internal/configapply/wire/model.go", "internal/platform/apiv2/models.go"},
	{"model", reflect.TypeFor[ModelResponse](), reflect.TypeFor[apiv2.Model](), "internal/configapply/wire/model.go", "internal/platform/apiv2/models.go"},
	{"connector", reflect.TypeFor[CreateConnectorRequest](), reflect.TypeFor[apiv2.CreateConnectorRequest](), "internal/configapply/wire/connector.go", "internal/platform/apiv2/connectors.go"},
	{"connector", reflect.TypeFor[UpdateConnectorRequest](), reflect.TypeFor[apiv2.UpdateConnectorRequest](), "internal/configapply/wire/connector.go", "internal/platform/apiv2/connectors.go"},
	{"connector", reflect.TypeFor[ConnectorResponse](), reflect.TypeFor[apiv2.Connector](), "internal/configapply/wire/connector.go", "internal/platform/apiv2/connectors.go"},
	{"toolset", reflect.TypeFor[CreateToolsetRequest](), reflect.TypeFor[apiv2.CreateToolsetRequest](), "internal/configapply/wire/toolset.go", "internal/platform/apiv2/toolsets.go"},
	{"toolset", reflect.TypeFor[UpdateToolsetRequest](), reflect.TypeFor[apiv2.UpdateToolsetRequest](), "internal/configapply/wire/toolset.go", "internal/platform/apiv2/toolsets.go"},
	{"toolset", reflect.TypeFor[ToolsetResponse](), reflect.TypeFor[apiv2.Toolset](), "internal/configapply/wire/toolset.go", "internal/platform/apiv2/toolsets.go"},
	{"toolset", reflect.TypeFor[ToolsetConfiguration](), reflect.TypeFor[apiv2.ToolsetConfiguration](), "internal/configapply/wire/toolset.go", "internal/platform/apiv2/toolsets.go"},
	{"toolset", reflect.TypeFor[UpdateToolsetConfigurationRequest](), reflect.TypeFor[apiv2.UpdateToolsetConfigurationRequest](), "internal/configapply/wire/toolset.go", "internal/platform/apiv2/toolsets.go"},
	{"skill", reflect.TypeFor[UpdateSkillRequest](), reflect.TypeFor[apiv2.UpdateSkillRequest](), "internal/configapply/wire/skill.go", "internal/platform/apiv2/skills.go"},
	{"skill", reflect.TypeFor[SkillResponse](), reflect.TypeFor[apiv2.Skill](), "internal/configapply/wire/skill.go", "internal/platform/apiv2/skills.go"},
	{"budget", reflect.TypeFor[CreateBudgetRequest](), reflect.TypeFor[apiv2.CreateBudgetRequest](), "internal/configapply/wire/budget.go", "internal/platform/apiv2/budgets.go"},
	{"budget", reflect.TypeFor[UpdateBudgetRequest](), reflect.TypeFor[apiv2.UpdateBudgetRequest](), "internal/configapply/wire/budget.go", "internal/platform/apiv2/budgets.go"},
	{"budget", reflect.TypeFor[BudgetResponse](), reflect.TypeFor[apiv2.Budget](), "internal/configapply/wire/budget.go", "internal/platform/apiv2/budgets.go"},
	{"budgetAssignment", reflect.TypeFor[CreateBudgetAssignmentRequest](), reflect.TypeFor[apiv2.CreateBudgetAssignmentRequest](), "internal/configapply/wire/budget.go", "internal/platform/apiv2/budget_assignments.go"},
	{"budgetAssignment", reflect.TypeFor[UpdateBudgetAssignmentRequest](), reflect.TypeFor[apiv2.UpdateBudgetAssignmentRequest](), "internal/configapply/wire/budget.go", "internal/platform/apiv2/budget_assignments.go"},
	{"budgetAssignment", reflect.TypeFor[BudgetAssignmentResponse](), reflect.TypeFor[apiv2.BudgetAssignment](), "internal/configapply/wire/budget.go", "internal/platform/apiv2/budget_assignments.go"},
}

type wireField struct {
	goName    string
	typ       reflect.Type
	omitEmpty bool
	omitZero  bool
	validate  string
	cfgapply  string
	clearable string
}

func wireFields(t *testing.T, typ reflect.Type) map[string]wireField {
	t.Helper()
	out := map[string]wireField{}
	for sf := range typ.Fields() {
		if !sf.IsExported() {
			continue
		}
		if sf.Anonymous {
			t.Fatalf("%s embeds %s; the copy check compares named fields only, so write the fields out", typ, sf.Name)
		}
		name, opts, _ := strings.Cut(sf.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		out[cmp.Or(name, sf.Name)] = wireField{
			goName:    sf.Name,
			typ:       sf.Type,
			omitEmpty: slices.Contains(strings.Split(opts, ","), "omitempty"),
			omitZero:  slices.Contains(strings.Split(opts, ","), "omitzero"),
			validate:  sf.Tag.Get("validate"),
			cfgapply:  sf.Tag.Get("cfgapply"),
			clearable: sf.Tag.Get("clearable"),
		}
	}
	return out
}

func TestCopiesMatchTheirAPIV2Types(t *testing.T) {
	for _, c := range copies {
		t.Run(c.copy.Name(), func(t *testing.T) {
			copyFields := wireFields(t, c.copy)
			apiFields := wireFields(t, c.apiv2)
			for _, name := range sortedKeys(apiFields) {
				api := apiFields[name]
				cp, ok := copyFields[name]
				if !ok {
					t.Errorf("%s: apiv2.%s has field %q (%s %s) that wire.%s lacks. Add it to %s and decide its cfgapply tags",
						c.kind, c.apiv2.Name(), name, api.goName, api.typ, c.copy.Name(), c.copyFile)
					continue
				}
				comparePatchOrType(t, c, name, cp, api)
				if cp.clearable != api.clearable {
					t.Errorf("%s: field %q has clearable:%q on wire.%s but clearable:%q on apiv2.%s, so config apply would send null where the server refuses it, or keep a value it could clear. Match it in %s",
						c.kind, name, cp.clearable, c.copy.Name(), api.clearable, c.apiv2.Name(), c.copyFile)
				}
				if cp.omitEmpty != api.omitEmpty {
					t.Errorf("%s: field %q has omitempty=%t on wire.%s but %t on apiv2.%s, so the copy sends different JSON. Match it in %s",
						c.kind, name, cp.omitEmpty, c.copy.Name(), api.omitEmpty, c.apiv2.Name(), c.copyFile)
				}
				if cp.validate != api.validate {
					t.Errorf("%s: field %q has validate:%q on wire.%s but validate:%q on apiv2.%s, so the authoring schema checks different rules than the server. Match it in %s",
						c.kind, name, cp.validate, c.copy.Name(), api.validate, c.apiv2.Name(), c.copyFile)
				}
			}
			for _, name := range sortedKeys(copyFields) {
				if _, ok := apiFields[name]; !ok {
					t.Errorf("%s: wire.%s has field %q that apiv2.%s (%s) does not. Remove it from %s, or add it to the API",
						c.kind, c.copy.Name(), name, c.apiv2.Name(), c.apiv2File, c.copyFile)
				}
			}
		})
	}
}

func comparePatchOrType(t *testing.T, c copyOf, name string, cp, api wireField) {
	t.Helper()
	apiValue, apiIsPatch := patchValue(api.typ, reflect.TypeFor[apiv2.Patch[string]]())
	if !apiIsPatch {
		if cp.typ != api.typ {
			t.Errorf("%s: field %q is %s on wire.%s but %s on apiv2.%s. Change the copy in %s to %s",
				c.kind, name, cp.typ, c.copy.Name(), api.typ, c.apiv2.Name(), c.copyFile, api.typ)
		}
		return
	}
	if cpValue, ok := patchValue(cp.typ, reflect.TypeFor[Patch[string]]()); !ok || cpValue != apiValue {
		t.Errorf("%s: field %q is %s on apiv2.%s, so wire.%s declares it wire.Patch[%s], not %s. Change it in %s",
			c.kind, name, api.typ, c.apiv2.Name(), c.copy.Name(), apiValue, cp.typ, c.copyFile)
	}
	if !cp.omitZero {
		t.Errorf("%s: PATCH field %q on wire.%s needs omitzero in its json tag, or a field left out is sent as its zero value. Add it in %s",
			c.kind, name, c.copy.Name(), c.copyFile)
	}
}

func patchValue(typ, sample reflect.Type) (reflect.Type, bool) {
	generic, _, _ := strings.Cut(sample.Name(), "[")
	if typ.Kind() != reflect.Struct || typ.PkgPath() != sample.PkgPath() || !strings.HasPrefix(typ.Name(), generic+"[") {
		return nil, false
	}
	value, ok := typ.FieldByName("Value")
	if !ok {
		return nil, false
	}
	return value.Type, true
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func cfgapplyTags(raw string) []string {
	var out []string
	for part := range strings.SplitSeq(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" || strings.HasPrefix(part, "key=") {
			continue
		}
		out = append(out, part)
	}
	slices.Sort(out)
	return out
}

func authoringKey(name string, f wireField) string {
	for part := range strings.SplitSeq(f.cfgapply, ",") {
		if key, ok := strings.CutPrefix(strings.TrimSpace(part), "key="); ok {
			return key
		}
	}
	return name
}

func describeTags(tags []string) string {
	if len(tags) == 0 {
		return "no cfgapply tags"
	}
	return fmt.Sprintf("cfgapply:%q", strings.Join(tags, ","))
}
