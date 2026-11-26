// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

var intentionallyNotCarried = map[string]map[string]string{
	"UpdateAgentRequest": {
		"deploy": "saving is not deploying; the frontend drives deployment separately",
	},
	"UpdateGatewayRequest": {
		"deploy":    "saving is not deploying; the frontend drives deployment separately",
		"type":      "immutable after creation; the platform accepts and ignores it",
		"publicUrl": "assigned by the platform or set on the entrypoint page; the Builder cannot author it, and a nil pointer means preserve",
	},
	"UpdateConnectorRequest": {},
}

func TestUpdateBodies_CarryEveryFieldOrDeclareWhyNot(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		body any
	}{
		{"UpdateAgentRequest", updateAgentRequestFrom(fullyPopulatedAgentCreate(), true)},
		{"UpdateGatewayRequest", updateGatewayRequestFrom(fullyPopulatedGatewayCreate())},
		{"UpdateConnectorRequest", updateConnectorRequestFrom(fullyPopulatedConnectorCreate())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exempt := intentionallyNotCarried[tc.name]
			v := reflect.ValueOf(tc.body)
			typ := v.Type()

			for i := range typ.NumField() {
				f := typ.Field(i)
				if !f.IsExported() {
					continue
				}
				tag := jsonFieldName(f.Tag.Get("json"))
				if tag == "" || tag == "-" {
					continue
				}
				if _, declared := exempt[tag]; declared {
					if !isZeroish(v.Field(i)) {
						t.Errorf("%s.%s is listed as intentionally not carried but was populated", tc.name, tag)
					}
					continue
				}
				if isZeroish(v.Field(i)) {
					t.Errorf("%s.%s was not carried from the create DTO.\n"+
						"Either populate it in update%sFrom, or add it to intentionallyNotCarried with a reason.\n"+
						"An uncarried field is cleared by the update, and if hashed it also churns forever.",
						tc.name, tag, strings.TrimPrefix(tc.name, "Update"))
				}
			}
		})
	}
}

func isZeroish(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Pointer:
		return v.IsNil() || isZeroish(v.Elem())
	case reflect.Slice, reflect.Map, reflect.String:
		return v.Len() == 0
	case reflect.Interface:
		return v.IsNil() || isZeroish(v.Elem())
	}
	return v.IsZero()
}

func fullyPopulatedAgentCreate() entplatform.CreateAgentRequest {
	return entplatform.CreateAgentRequest{
		Name:                 "drift_agent",
		Description:          "Every field set, so an uncarried one shows up as empty.",
		SystemPrompt:         "You are a drift-detection fixture.",
		Type:                 "standard",
		Skills:               []entplatform.SkillRequest{{Name: "s", Description: "d"}},
		SkillRefs:            []string{"builtin_skill"},
		SkillIDs:             []string{"019ffb00-0000-7000-8000-000000000001"},
		Toolsets:             []string{"builtin_artifact_management"},
		ToolsetExcludedTools: map[string][]string{"builtin_artifact_management": {"delete_artifact"}},
		ToolsetConfigs: []entplatform.ToolsetConfigBinding{
			{ToolsetName: "builtin_artifact_management", ConfigValues: map[string]any{"t": map[string]any{"k": 1}}},
		},
		SkillConfigs: []entplatform.SkillConfigBinding{
			{SkillID: "019ffb00-0000-7000-8000-000000000001", ConfigValues: map[string]map[string]any{"t": {"k": 1}}},
		},
		Connectors:               []string{"019ffb00-0000-7000-8000-000000000002"},
		InputModes:               []string{"text"},
		OutputModes:              []string{"text"},
		ModelProvider:            []string{"planning"},
		AdditionalConfigurations: json.RawMessage(`{"supports_streaming":true}`),
		Deploy:                   true,
	}
}

func fullyPopulatedGatewayCreate() entplatform.CreateGatewayRequest {
	return entplatform.CreateGatewayRequest{
		Name:        "DriftEntrypoint",
		Slug:        "drift-entrypoint",
		Description: "Every field set, so an uncarried one shows up as empty.",
		Type:        "event_mesh",
		PublicURL:   "https://example.invalid",
		Values:      map[string]any{"event_rules": []any{}},
		Deploy:      true,
	}
}

func TestUpdateAgentRequest_LeavesSkillsAloneWhenUnauthored(t *testing.T) {
	t.Parallel()
	create := fullyPopulatedAgentCreate()

	if got := updateAgentRequestFrom(create, false).SkillIDs; got != nil {
		t.Errorf("SkillIDs = %v, want nil — an artifact that never mentioned skills must not touch them", *got)
	}

	create.SkillIDs = nil
	got := updateAgentRequestFrom(create, true).SkillIDs
	if got == nil {
		t.Fatal("SkillIDs = nil for an authored artifact; detaching every skill must stay expressible")
	}
	if len(*got) != 0 {
		t.Errorf("SkillIDs = %v, want an empty non-nil slice", *got)
	}
}

func fullyPopulatedConnectorCreate() entplatform.CreateConnectorRequest {
	return entplatform.CreateConnectorRequest{
		Name:        "drift-connector",
		Description: "Every field set, so an uncarried one shows up as empty.",
		Type:        "sql",
		Subtype:     "postgres",
		Values:      map[string]any{"hostname": "db"},
	}
}

var updateBodyByKind = map[string]struct {
	updateDTO     string
	componentKind string
	create        any
	update        any
}{
	kindAgent:     {"UpdateAgentRequest", kindAgent, entplatform.CreateAgentRequest{}, entplatform.UpdateAgentRequest{}},
	kindGateway:   {"UpdateGatewayRequest", kindGateway, entplatform.CreateGatewayRequest{}, entplatform.UpdateGatewayRequest{}},
	kindConnector: {"UpdateConnectorRequest", kindConnector, entplatform.CreateConnectorRequest{}, entplatform.UpdateConnectorRequest{}},
	kindA2AAgent:  {"UpdateRemoteAgentRequest", kindA2AAgent, entplatform.CreateRemoteAgentRequest{}, entplatform.UpdateRemoteAgentRequest{}},
	kindWorkflow:  {"UpdateWorkflowRequest", kindWorkflow, workflowHashView{}, entplatform.UpdateWorkflowRequest{}},
}

func TestImmutableFields_CoverEveryHashedUncarriedField(t *testing.T) {
	t.Parallel()
	byKind := updateBodyByKind

	var cases []struct {
		updateDTO     string
		componentKind string
		create        any
		update        any
	}
	for _, kind := range componentKindsInOrder() {
		tc, covered := byKind[kind]
		if !covered {
			t.Fatalf("kind %q is applied by this endpoint but has no entry here, so it is not guarded at all", kind)
		}
		cases = append(cases, tc)
	}

	for _, tc := range cases {
		t.Run(tc.updateDTO, func(t *testing.T) {
			hashed := hashedTags(tc.create)
			carried := wireTags(tc.update)
			guarded := map[string]struct{}{}
			for _, f := range immutableFields[tc.componentKind] {
				guarded[f.label] = struct{}{}
			}

			for _, tag := range authorableFields[tc.componentKind] {
				if _, isHashed := hashed[tag]; !isHashed {
					continue
				}
				_, isCarried := carried[tag]
				_, isExempt := intentionallyNotCarried[tc.updateDTO][tag]
				if isCarried && !isExempt {
					continue
				}
				if _, isGuarded := guarded[tag]; isGuarded {
					continue
				}
				t.Errorf("%s.%s is authorable and hashed, but the update body does not carry it.\n"+
					"An edit to it plans an Update the write cannot deliver, so it churns forever while "+
					"the rest of the write lands against a resource it no longer describes.\n"+
					"Either carry it, stop hashing it, or add it to immutableFields[%q] so the plan refuses the edit.",
					tc.updateDTO, tag, tc.componentKind)
			}
		})
	}
}

func TestAuthorableFields_MatchWhatTheConvertersActuallyAuthor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		componentKind string
		component     BuildComponentInput
	}{
		{kindConnector, sqlConnector("drift", "postgres", fullSQLValues())},
		{kindGateway, gatewayComponent("drift_events", "Target")},
		{kindAgent, richestAgentComponent("drift_agent")},
	}

	for _, tc := range cases {
		t.Run(tc.componentKind, func(t *testing.T) {
			d, err := desiredForKind(tc.componentKind, tc.component.Name, tc.component.Content, newToolsetCatalog())
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			reg := newXRefRegistry()
			reg.put(xrefKindFor(kindConnector), driftConnectorName, "0f6f2e9c-52a4-4a5b-9a2a-2b7a1f0d4c31")
			if err := bindAgentReferences(&d, tc.component, reg); err != nil {
				t.Fatalf("bind references: %v", err)
			}
			declared := authoredKeys(authorableFields[tc.componentKind]...)
			hashed, known := updateBodyByKind[tc.componentKind]
			if !known {
				t.Fatalf("kind %q is not in updateBodyByKind, so there is no way to know which fields the consumer guard checks", tc.componentKind)
			}
			onTheDTO := authoredKeys(wireFieldNames(hashed.create)...)
			for tag := range d.authored {
				if _, isField := onTheDTO[tag]; !isField {
					continue
				}
				if _, ok := declared[tag]; !ok {
					t.Errorf("the %s converter authors %q, which authorableFields[%q] does not list.\n"+
						"That field is then skipped by TestImmutableFields_CoverEveryHashedUncarriedField.",
						tc.componentKind, tag, tc.componentKind)
				}
			}
		})
	}
}

const driftConnectorName = "drift"

func richestAgentComponent(name string) BuildComponentInput {
	return BuildComponentInput{
		Connectors: []string{driftConnectorName},
		Kind:       kindAgent,
		Name:       name,
		Content: `
apps:
  - name: ` + name + `
    app_config:
      agent_name: ` + name + `
      description: An agent naming every source key the converter reads.
      instruction: You are a drift fixture.
      model_provider: planning
      input_modes: [text]
      output_modes: [text]
      skills: []
      tools:
        - tool_type: builtin-group
          group_name: artifact_management
      agent_card:
        description: An agent naming every source key the converter reads.
        skills: []
        defaultInputModes: [text]
        defaultOutputModes: [text]
`,
	}
}

func wireTags(dto any) map[string]struct{} {
	out := map[string]struct{}{}
	t := reflect.TypeOf(dto)
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if tag := jsonFieldName(f.Tag.Get("json")); tag != "" && tag != "-" {
			out[tag] = struct{}{}
		}
	}
	return out
}

func hashedTags(dto any) map[string]struct{} {
	projected := generic.ProjectForHash(dto, nil)
	out := make(map[string]struct{}, len(projected))
	for tag := range projected {
		out[tag] = struct{}{}
	}
	return out
}

func TestUpdateBodies_CoverEveryHandBuiltKind(t *testing.T) {
	t.Parallel()
	covered := []string{"UpdateAgentRequest", "UpdateGatewayRequest", "UpdateConnectorRequest"}
	for name := range intentionallyNotCarried {
		if !slices.Contains(covered, name) {
			t.Errorf("intentionallyNotCarried names %q, which no test exercises", name)
		}
	}
	var (
		_ = entplatform.UpdateWorkflowRequest(entplatform.CreateWorkflowRequest{})
		_ = entplatform.UpdateRemoteAgentRequest(entplatform.CreateRemoteAgentRequest{})
	)
}
