// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	petSpecURL      = "https://petstore3.swagger.io/api/v3/openapi.json"
	bindingErrorTag = "binds API connector"
)

const petWorkflowYAML = `
apps:
  - name: PetFlow
    app_config:
      agent_type: workflow
      output_mapping: {pets: "{{fetch_pets.output.result}}"}
      nodes:
        - id: fetch_pets
          type: tool
          connector: PetAPI
          tool: findPetsByStatus
          input: {status: "available"}
`

const nestedPetWorkflowYAML = `
apps:
  - name: PetFlow
    app_config:
      workflow:
        output_mapping: {pets: "{{fetch_pets.output.result}}"}
        nodes:
          - id: fetch_pets
            type: tool
            connector: PetAPI
            tool: findPetsByStatus
            input: {status: "available"}
`

func petConnector(t *testing.T, name, connectorType string, values map[string]any) string {
	t.Helper()
	body := map[string]any{"base_url": "https://petstore3.swagger.io/api/v3", "auth_type": "none", "specification_url": petSpecURL}
	for k, v := range values {
		body[k] = v
	}
	raw, err := json.Marshal(map[string]any{"name": name, "type": connectorType, "subtype": "openapi", "values": body})
	if err != nil {
		t.Fatalf("marshal connector: %v", err)
	}
	return string(raw)
}

type namedLookups struct {
	byName map[string]ConnectorLookup
	err    error
	calls  *[]string
}

func (q namedLookups) Custom(_ context.Context, appName, method, path string, body json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Name string `json:"name"`
	}
	if appName != "platform" || method != "post" || path != ConnectorLookupPath || json.Unmarshal(body, &req) != nil {
		return nil, fmt.Errorf("unknown resource: %s %s %s", appName, method, path)
	}
	if q.calls != nil {
		*q.calls = append(*q.calls, req.Name)
	}
	if q.err != nil {
		return nil, q.err
	}
	return json.Marshal(q.byName[req.Name])
}

type buildFile struct {
	filename, typ, name, origin, content string
}

func bindingRun(t *testing.T, files []buildFile, querier tool.ControlQuerier) (valid bool, errs []string) {
	t.Helper()
	var manifest strings.Builder
	manifest.WriteString("\nname: pet-project\ncomponents:\n")
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	toolCtx.ControlQuerier = querier
	ctx := t.Context()
	for _, f := range files {
		fmt.Fprintf(&manifest, "  - filename: %q\n    type: %s\n    name: %s\n    origin: %s\n", f.filename, f.typ, f.name, f.origin)
	}
	saveTestArtifact(ctx, svc, toolCtx, "build_manifest.yaml", manifest.String(), "application/vnd.sam-build-manifest+yaml")
	for _, f := range files {
		switch {
		case f.content == "":
		case f.typ == "connector":
			saveTestArtifact(ctx, svc, toolCtx, f.filename, f.content, SAMConnectorConfigMIMEType)
		default:
			saveTestArtifact(ctx, svc, toolCtx, f.filename, f.content, "application/x-yaml")
		}
	}
	result, err := (&ValidateBuildManifestTool{}).Execute(ctx, map[string]any{"manifest_yaml": manifest.String(), "full_validation": true}, toolCtx)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, e := range validationErrorsWithPath(result.Data) {
		if strings.Contains(e, bindingErrorTag) {
			errs = append(errs, e)
		}
	}
	valid, _ = result.Data["valid"].(bool)
	return valid, errs
}

func petFiles(workflow, connectorOrigin, connectorFile, connectorContent string) []buildFile {
	return []buildFile{
		{filename: "pet_flow.yaml", typ: "workflow", name: "PetFlow", origin: "session", content: workflow},
		{filename: connectorFile, typ: "connector", name: "PetAPI", origin: connectorOrigin, content: connectorContent},
	}
}

func TestFullValidation_WorkflowToolNodeNeedsAPIConnectorAllowList(t *testing.T) {
	withList := map[string]any{"allow_list": "findPetsByStatus"}
	storedList := namedLookups{byName: map[string]ConnectorLookup{"PetAPI": {Exists: true, Type: "api", HasAllowList: true}}}
	storedUpload := namedLookups{byName: map[string]ConnectorLookup{"PetAPI": {Exists: true, Type: "api", HasResource: true}}}
	storedBare := namedLookups{byName: map[string]ConnectorLookup{"PetAPI": {Exists: true, Type: "api"}}}
	nothingStored := namedLookups{}
	oldPlatform := namedLookups{byName: map[string]ConnectorLookup{"PetAPI": {Exists: true}}}
	oldPlatformUpload := namedLookups{byName: map[string]ConnectorLookup{"PetAPI": {Exists: true, HasResource: true}}}
	storedMCP := namedLookups{byName: map[string]ConnectorLookup{"PetAPI": {Exists: true, Type: "mcp"}}}
	const replacedListMessage = "is empty or not a comma-separated string"
	const lacksListMessage = "which has no allow_list. The platform cannot list its tools"
	failingQuerier := namedLookups{err: errors.New("control request failed")}
	const oldPlatformMessage = "does not report whether it stores an allow_list"
	const existsBareMessage = "which already exists on the platform with no allow_list and no uploaded spec"
	sessionPet := func(connectorName string, values map[string]any) []buildFile {
		return []buildFile{
			{filename: "pet_flow.yaml", typ: "workflow", name: "PetFlow", origin: "session", content: petWorkflowYAML},
			{filename: "pet.json", typ: "connector", name: "PetAPI", origin: "session", content: petConnector(t, connectorName, "api", values)},
		}
	}
	beforeBadPet := func(other buildFile) []buildFile {
		return []buildFile{
			{filename: "pet_flow.yaml", typ: "workflow", name: "PetFlow", origin: "session", content: petWorkflowYAML},
			other,
			{filename: "pet.json", typ: "connector", name: "PetAPI", origin: "session", content: petConnector(t, "PetAPI", "api", nil)},
		}
	}

	for _, tc := range []struct {
		name             string
		files            []buildFile
		querier          tool.ControlQuerier
		wantError        string
		otherwiseInvalid bool
	}{
		{name: "no allow_list", querier: nothingStored, wantError: lacksListMessage,
			files: petFiles(petWorkflowYAML, "session", "pet.json", petConnector(t, "PetAPI", "api", nil))},
		{name: "no allow_list, workflow nested under workflow:", querier: nothingStored, wantError: lacksListMessage,
			files: petFiles(nestedPetWorkflowYAML, "session", "pet.json", petConnector(t, "PetAPI", "api", nil))},
		{name: "allow_list of only separators", querier: nothingStored, wantError: lacksListMessage,
			files: petFiles(petWorkflowYAML, "session", "pet.json", petConnector(t, "PetAPI", "api", map[string]any{"allow_list": " , "}))},
		{name: "allow_list set to a non-string", querier: nothingStored, wantError: lacksListMessage,
			files: petFiles(petWorkflowYAML, "session", "pet.json", petConnector(t, "PetAPI", "api", map[string]any{"allow_list": 7}))},
		{name: "stored connector has neither allow_list nor upload", querier: storedBare, wantError: existsBareMessage,
			files: petFiles(petWorkflowYAML, "session", "pet.json", petConnector(t, "PetAPI", "api", nil))},
		{name: "artifact named differently from its component", querier: nothingStored, wantError: lacksListMessage,
			files: []buildFile{
				{filename: "pet_flow.yaml", typ: "workflow", name: "PetFlow", origin: "session", content: petWorkflowYAML},
				{filename: "pet.json", typ: "connector", name: "pet_api", origin: "session", content: petConnector(t, "PetAPI", "api", nil)},
			}},
		{name: "allow_list set", querier: nothingStored,
			files: petFiles(petWorkflowYAML, "session", "pet.json", petConnector(t, "PetAPI", "api", withList))},
		{name: "padded allow_list", querier: nothingStored,
			files: petFiles(petWorkflowYAML, "session", "pet.json", petConnector(t, "PetAPI", "api", map[string]any{"allow_list": " findPetsByStatus , "}))},
		{name: "edit keeps an uploaded spec", querier: storedUpload,
			files: petFiles(petWorkflowYAML, "session", "pet.json", petConnector(t, "PetAPI", "api", nil))},
		{name: "edit omits allow_list that is stored", querier: storedList,
			files: petFiles(petWorkflowYAML, "session", "pet.json", petConnector(t, "PetAPI", "api", nil))},
		{name: "edit blanks allow_list that is stored", querier: storedList, wantError: replacedListMessage,
			files: petFiles(petWorkflowYAML, "session", "pet.json", petConnector(t, "PetAPI", "api", map[string]any{"allow_list": ""}))},
		{name: "artifact named differently from its component, stored allow_list", querier: storedList,
			files: []buildFile{
				{filename: "pet_flow.yaml", typ: "workflow", name: "PetFlow", origin: "session", content: petWorkflowYAML},
				{filename: "pet.json", typ: "connector", name: "pet_api", origin: "session", content: petConnector(t, "PetAPI", "api", nil)},
			}},
		{name: "platform connector with neither allow_list nor upload", querier: storedBare, wantError: "an existing platform connector with no allow_list and no uploaded spec",
			files: petFiles(petWorkflowYAML, "platform", "", "")},
		{name: "connector absent from the manifest, stored bare", querier: storedBare, wantError: "an existing platform connector with no allow_list and no uploaded spec",
			files: petFiles(petWorkflowYAML, "session", "pet.json", "")[:1]},
		{name: "platform connector with an allow_list", querier: storedList,
			files: petFiles(petWorkflowYAML, "platform", "", "")},
		{name: "platform connector with an upload", querier: storedUpload,
			files: petFiles(petWorkflowYAML, "platform", "", "")},
		{name: "platform connector of another type", querier: namedLookups{byName: map[string]ConnectorLookup{"PetAPI": {Exists: true, Type: "mcp"}}},
			files: petFiles(petWorkflowYAML, "platform", "", "")},
		{name: "connector absent from the manifest and the platform", querier: nothingStored,
			files: petFiles(petWorkflowYAML, "session", "pet.json", "")[:1]},
		{name: "platform connector lookup fails", querier: namedLookups{err: errors.New("control request failed")},
			files: petFiles(petWorkflowYAML, "platform", "", "")},
		{name: "platform connector, no lookup available",
			files: petFiles(petWorkflowYAML, "platform", "", "")},
		{name: "session connector, lookup fails", querier: namedLookups{err: errors.New("control request failed")}, wantError: "could not be checked (control request failed)",
			files: petFiles(petWorkflowYAML, "session", "pet.json", petConnector(t, "PetAPI", "api", nil))},
		{name: "session connector, no lookup available", wantError: "could not be checked (" + errConnectorLookupUnavailable.Error() + ")",
			files: petFiles(petWorkflowYAML, "session", "pet.json", petConnector(t, "PetAPI", "api", nil))},
		{name: "non-API connector has no allow_list", querier: storedMCP,
			files: petFiles(petWorkflowYAML, "session", "pet.json", petConnector(t, "PetAPI", "mcp", nil))},
		{name: "artifact says mcp but the stored connector is api", querier: storedBare, wantError: existsBareMessage,
			files: petFiles(petWorkflowYAML, "session", "pet.json", petConnector(t, "PetAPI", "mcp", nil))},
		{name: "blank allow_list over an uploaded spec stays exempt", querier: storedUpload,
			files: sessionPet("PetAPI", map[string]any{"allow_list": ""})},
		{name: "blank allow_list over an uploaded spec and a stored list stays exempt", querier: namedLookups{byName: map[string]ConnectorLookup{"PetAPI": {Exists: true, Type: "api", HasResource: true, HasAllowList: true}}},
			files: sessionPet("PetAPI", map[string]any{"allow_list": ""})},
		{name: "connector config is not JSON", querier: nothingStored, otherwiseInvalid: true,
			files: petFiles(petWorkflowYAML, "session", "pet.json", "{not json")},
		{name: "connector artifact was never written", querier: nothingStored, otherwiseInvalid: true,
			files: petFiles(petWorkflowYAML, "session", "pet.json", "")},
		{name: "authored allow_list, existing connector stores none (deliberate false positive with server-side apply)", querier: storedBare, wantError: "validation cannot confirm that the allow_list in this build will be written to it",
			files: sessionPet("PetAPI", withList)},
		{name: "authored allow_list, existing connector stores one", querier: storedList, files: sessionPet("PetAPI", withList)},
		{name: "authored allow_list, existing connector has an upload", querier: storedUpload, files: sessionPet("PetAPI", withList)},
		{name: "authored allow_list, lookup fails", querier: namedLookups{err: errors.New("control request failed")}, files: sessionPet("PetAPI", withList)},
		{name: "authored null allow_list over a stored one", querier: storedList, wantError: replacedListMessage,
			files: sessionPet("PetAPI", map[string]any{"allow_list": nil})},
		{name: "authored array allow_list over a stored one", querier: storedList, wantError: replacedListMessage,
			files: sessionPet("PetAPI", map[string]any{"allow_list": []string{"findPetsByStatus"}})},
		{name: "nameless artifact is keyed by its component name", querier: nothingStored, wantError: lacksListMessage,
			files: sessionPet("", nil)},
		{name: "nameless artifact with allow_list", querier: nothingStored, files: sessionPet("", withList)},
		{name: "lookup predates allow_list reporting", querier: oldPlatform, wantError: oldPlatformMessage,
			files: sessionPet("PetAPI", nil)},
		{name: "lookup predates allow_list reporting, list authored", querier: oldPlatform, wantError: oldPlatformMessage,
			files: sessionPet("PetAPI", withList)},
		{name: "lookup predates allow_list reporting, upload reported", querier: oldPlatformUpload,
			files: sessionPet("PetAPI", nil)},
		{name: "persistent lookup failure, list authored, connector presumed new", querier: namedLookups{err: errors.New("insufficient permissions")},
			files: sessionPet("PetAPI", withList)},
		{name: "platform connector, lookup predates allow_list reporting", querier: oldPlatform,
			files: petFiles(petWorkflowYAML, "platform", "", "")},
		{name: "unreadable connector config before a bad API connector", querier: nothingStored, wantError: lacksListMessage,
			files: beforeBadPet(buildFile{filename: "other.json", typ: "connector", name: "Other", origin: "session", content: "{not json"})},
		{name: "unwritten connector before a bad API connector", querier: nothingStored, wantError: lacksListMessage,
			files: beforeBadPet(buildFile{filename: "other.json", typ: "connector", name: "Other", origin: "session"})},
		{name: "mcp session connector, lookup fails", querier: failingQuerier,
			files: petFiles(petWorkflowYAML, "session", "pet.json", petConnector(t, "PetAPI", "mcp", nil))},
		{name: "mcp session connector, no lookup available",
			files: petFiles(petWorkflowYAML, "session", "pet.json", petConnector(t, "PetAPI", "mcp", nil))},
		{name: "mcp session connector, older platform", querier: oldPlatform,
			files: petFiles(petWorkflowYAML, "session", "pet.json", petConnector(t, "PetAPI", "mcp", nil))},
		{name: "artifact says api but the stored connector is mcp", querier: storedMCP,
			files: petFiles(petWorkflowYAML, "session", "pet.json", petConnector(t, "PetAPI", "api", nil))},
		{name: "blank allow_list over a connector that stores none", querier: storedBare, wantError: existsBareMessage,
			files: sessionPet("PetAPI", map[string]any{"allow_list": ""})},
		{name: "node binds no connector", querier: nothingStored,
			files: petFiles(strings.Replace(petWorkflowYAML, "          connector: PetAPI\n", "          tool_name: find_pets_by_status\n", 1), "session", "pet.json", petConnector(t, "PetAPI", "api", nil))},
		{name: "connector on a non-tool node", querier: nothingStored, otherwiseInvalid: true,
			files: petFiles(strings.Replace(petWorkflowYAML, "type: tool", "type: agent", 1), "session", "pet.json", petConnector(t, "PetAPI", "api", nil))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			valid, errs := bindingRun(t, tc.files, tc.querier)
			if tc.wantError == "" {
				if len(errs) > 0 {
					t.Fatalf("unexpected errors: %v", errs)
				}
				if !valid && !tc.otherwiseInvalid {
					t.Error("valid = false for a build with no binding error")
				}
				return
			}
			if len(errs) != 1 {
				t.Fatalf("binding errors = %v, want exactly one", errs)
			}
			for _, want := range []string{"workflow 'PetFlow'.node 'fetch_pets'.connector: ", "connector 'PetAPI'", tc.wantError} {
				if !strings.Contains(errs[0], want) {
					t.Errorf("error %q does not contain %q", errs[0], want)
				}
			}
			if valid {
				t.Error("valid = true, want false")
			}
		})
	}
}

func TestFullValidation_CleanAPIConnectorBindingIsValid(t *testing.T) {
	files := petFiles(petWorkflowYAML, "session", "pet.json", petConnector(t, "PetAPI", "api", map[string]any{"allow_list": "findPetsByStatus"}))
	valid, errs := bindingRun(t, files, namedLookups{})
	if !valid || len(errs) > 0 {
		t.Fatalf("valid = %v, binding errors = %v, want a valid build", valid, errs)
	}
}

func secondPetWorkflow(name string) buildFile {
	return buildFile{filename: strings.ToLower(name) + ".yaml", typ: "workflow", name: name, origin: "session",
		content: strings.Replace(petWorkflowYAML, "PetFlow", name, 1)}
}

func TestFullValidation_APIConnectorAllowListAcrossNodesAndWorkflows(t *testing.T) {
	second := strings.Replace(petWorkflowYAML, "          input: {status: \"available\"}\n",
		"          input: {status: \"available\"}\n        - id: fetch_again\n          type: tool\n          connector: PetAPI\n          tool: findPetsByStatus\n          depends_on: [fetch_pets]\n", 1)
	connector := petConnector(t, "PetAPI", "api", nil)

	t.Run("one error per bound node", func(t *testing.T) {
		_, errs := bindingRun(t, petFiles(second, "session", "pet.json", connector), namedLookups{})
		if len(errs) != 2 || !strings.Contains(errs[0], "'fetch_pets'") || !strings.Contains(errs[1], "'fetch_again'") {
			t.Fatalf("errors = %v, want one per bound node", errs)
		}
	})

	t.Run("one error per workflow and one lookup per connector", func(t *testing.T) {
		var calls []string
		files := append(petFiles(petWorkflowYAML, "session", "pet.json", connector), secondPetWorkflow("PetFlowTwo"))
		_, errs := bindingRun(t, files, namedLookups{calls: &calls})
		if len(errs) != 2 || !strings.Contains(errs[0], "workflow 'PetFlow'") || !strings.Contains(errs[1], "workflow 'PetFlowTwo'") {
			t.Fatalf("errors = %v, want one per workflow", errs)
		}
		if len(calls) != 1 {
			t.Errorf("connector lookups = %v, want exactly one", calls)
		}
	})

	t.Run("an unreadable workflow does not hide the next one", func(t *testing.T) {
		unreadable := buildFile{filename: "broken.yaml", typ: "workflow", name: "Broken", origin: "session"}
		files := append([]buildFile{unreadable}, petFiles(petWorkflowYAML, "session", "pet.json", connector)...)
		_, errs := bindingRun(t, files, namedLookups{})
		if len(errs) != 1 || !strings.Contains(errs[0], "workflow 'PetFlow'") {
			t.Fatalf("errors = %v, want one for PetFlow", errs)
		}
	})
}

func TestFullValidation_APIConnectorChecksEachBoundConnectorSeparately(t *testing.T) {
	workflow := strings.Replace(petWorkflowYAML, "          input: {status: \"available\"}\n",
		"          input: {status: \"available\"}\n        - id: fetch_owner\n          type: tool\n          connector: OwnerAPI\n          tool: getOwner\n          depends_on: [fetch_pets]\n", 1)
	var calls []string
	querier := namedLookups{calls: &calls, byName: map[string]ConnectorLookup{
		"PetAPI":   {Exists: true, Type: "api", HasAllowList: true},
		"OwnerAPI": {Exists: true, Type: "api"},
	}}
	files := []buildFile{
		{filename: "pet_flow.yaml", typ: "workflow", name: "PetFlow", origin: "session", content: workflow},
		{filename: "pet.json", typ: "connector", name: "PetAPI", origin: "session", content: petConnector(t, "PetAPI", "api", nil)},
		{filename: "owner.json", typ: "connector", name: "OwnerAPI", origin: "session", content: petConnector(t, "OwnerAPI", "api", nil)},
	}
	_, errs := bindingRun(t, files, querier)
	if len(errs) != 1 || !strings.Contains(errs[0], "node 'fetch_owner'") || !strings.Contains(errs[0], "connector 'OwnerAPI'") {
		t.Fatalf("errors = %v, want exactly one, for OwnerAPI", errs)
	}
	if len(calls) != 2 {
		t.Errorf("connector lookups = %v, want one per connector", calls)
	}
}

type recordedLogs struct {
	slog.Handler
	messages *[]string
}

func (r recordedLogs) Enabled(context.Context, slog.Level) bool { return true }

func (r recordedLogs) Handle(_ context.Context, rec slog.Record) error {
	*r.messages = append(*r.messages, rec.Message)
	return nil
}

func (r recordedLogs) WithAttrs([]slog.Attr) slog.Handler { return r }

func (r recordedLogs) WithGroup(string) slog.Handler { return r }

func TestFullValidation_FailedConnectorLookupIsLoggedOncePerConnector(t *testing.T) {
	twoNodes := strings.Replace(petWorkflowYAML, "          input: {status: \"available\"}\n",
		"          input: {status: \"available\"}\n        - id: fetch_again\n          type: tool\n          connector: PetAPI\n          tool: findPetsByStatus\n          depends_on: [fetch_pets]\n", 1)
	const lookupFailed = "workflow connector allow_list check: connector lookup failed"
	for _, tc := range []struct {
		name      string
		connector string
		values    map[string]any
		want      int
	}{
		{name: "presumed new, list authored", connector: "api", values: map[string]any{"allow_list": "findPetsByStatus"}, want: 1},
		{name: "reported, no list", connector: "api", want: 1},
		{name: "non-API connector, verdict unaffected", connector: "mcp", want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var messages []string
			previous := slog.Default()
			slog.SetDefault(slog.New(recordedLogs{Handler: previous.Handler(), messages: &messages}))
			t.Cleanup(func() { slog.SetDefault(previous) })

			bindingRun(t, petFiles(twoNodes, "session", "pet.json", petConnector(t, "PetAPI", tc.connector, tc.values)), namedLookups{err: errors.New("control request failed")})

			got := 0
			for _, m := range messages {
				if m == lookupFailed {
					got++
				}
			}
			if got != tc.want {
				t.Errorf("%q logged %d times, want %d", lookupFailed, got, tc.want)
			}
		})
	}
}
