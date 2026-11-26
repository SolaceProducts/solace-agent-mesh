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
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type openAPIParseQuerier struct {
	response json.RawMessage
	err      error

	appName, method, path string
	body                  json.RawMessage
}

func (q *openAPIParseQuerier) Custom(_ context.Context, appName, method, path string, body json.RawMessage) (json.RawMessage, error) {
	q.appName, q.method, q.path, q.body = appName, method, path, body
	return q.response, q.err
}

const openAPIParseNote = "A workflow tool node on a connector created from a specification URL needs the operation's operationId listed in the connector's allow_list; toolName can be a shortened form that does not match."

const openAPIParseFixture = `{
 "schemes":[{"schemeName":"key","authType":"apikey","details":{"in":"header","name":"X-API-Key"}}],
 "suggestedAuth":{"authType":"apikey","config":{"in":"header","name":"X-API-Key"}},
 "operations":[
  {"toolName":"create_pet","method":"POST","path":"/pets","summary":"Create a pet","parameters":{"type":"object","properties":{"name":{"type":"string"},"tag":{"type":"string"}},"required":["name"]}},
  {"toolName":"get_pet","method":"GET","path":"/pets/{petId}","summary":"Get a pet","parameters":{"type":"object","properties":{"pet_id":{"type":"string"}},"required":["pet_id"]}},
  {"toolName":"list_pets","method":"GET","path":"/pets","summary":"List pets","parameters":{"type":"object","properties":{"limit":{"type":"integer"}}}}
 ],
 "operationsTotal":3,"truncated":false,
 "note":"` + openAPIParseNote + `"}`

func openAPIParseResponseWith(ops int, total int, truncated bool) string {
	list := make([]string, 0, ops)
	for i := range ops {
		list = append(list, fmt.Sprintf(`{"toolName":"operation_%03d","method":"GET","path":"/r/%03d","parameters":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}}`, i, i))
	}
	return fmt.Sprintf(`{"schemes":[],"operations":[%s],"operationsTotal":%d,"truncated":%t,"note":"%s"}`,
		strings.Join(list, ","), total, truncated, openAPIParseNote)
}

func runParseOpenAPI(t *testing.T, q tool.ControlQuerier) *tool.Result {
	t.Helper()
	return runParseOpenAPIWith(t, q, map[string]any{"spec_url": "https://api.example.test/openapi.json"})
}

func runParseOpenAPIWith(t *testing.T, q tool.ControlQuerier, args map[string]any) *tool.Result {
	t.Helper()
	res, err := (&ParseOpenAPISpecTool{}).Execute(t.Context(), args, &tool.Context{ControlQuerier: q})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return res
}

func TestParseOpenAPISpecTool_PostsToTheParseAction(t *testing.T) {
	q := &openAPIParseQuerier{response: json.RawMessage(openAPIParseFixture)}

	res := runParseOpenAPI(t, q)

	if res.Status != tool.StatusSuccess {
		t.Fatalf("status = %s: %s", res.Status, res.Message)
	}
	if q.appName != "platform" || q.method != "post" || q.path != "connectors/openapi/parse" {
		t.Errorf("call = %s %s %s, want platform post connectors/openapi/parse", q.appName, q.method, q.path)
	}
	if string(q.body) != `{"specUrl":"https://api.example.test/openapi.json"}` {
		t.Errorf("body = %s", q.body)
	}
}

func TestParseOpenAPISpecTool_ArtifactHoldsTheFullResult(t *testing.T) {
	res := runParseOpenAPI(t, &openAPIParseQuerier{response: json.RawMessage(openAPIParseFixture)})

	if len(res.DataObjects) != 1 {
		t.Fatalf("len(DataObjects) = %d, want 1", len(res.DataObjects))
	}
	do := res.DataObjects[0]
	if do.Name != "openapi_spec.json" {
		t.Errorf("artifact = %s, want openapi_spec.json", do.Name)
	}
	if do.Disposition != tool.DispositionArtifactWithPreview || do.Preview == "" {
		t.Errorf("disposition %s, preview %q: the preview must be set explicitly", do.Disposition, do.Preview)
	}
	for _, want := range []string{`"schemes"`, `"pet_id"`, `"operationsTotal"`} {
		if !strings.Contains(string(do.Content), want) {
			t.Errorf("artifact omits %s: the parameters live here, not in Data", want)
		}
	}
	if !slices.Contains(do.Tags, a2a.ArtifactTagWorking) {
		t.Errorf("tags = %v, want %q so the interim result stays out of the user's files", do.Tags, a2a.ArtifactTagWorking)
	}
}

func TestParseOpenAPISpecTool_DataCarriesOperationsWithoutParameters(t *testing.T) {
	res := runParseOpenAPI(t, &openAPIParseQuerier{response: json.RawMessage(openAPIParseFixture)})

	raw, err := json.Marshal(res.Data["operations"])
	if err != nil {
		t.Fatalf("marshal Data[operations]: %v", err)
	}
	var ops []map[string]any
	if err := json.Unmarshal(raw, &ops); err != nil || len(ops) != 3 {
		t.Fatalf("Data[operations] = %s (err %v), want 3", raw, err)
	}
	for _, op := range ops {
		if _, present := op["parameters"]; present {
			t.Errorf("Data[operations] carries parameters for %v; they belong in the artifact", op["toolName"])
		}
		for _, key := range []string{"toolName", "method", "path"} {
			if op[key] == nil || op[key] == "" {
				t.Errorf("operation lost %s: %v", key, op)
			}
		}
	}
	if res.Data["operationsTotal"] != float64(3) && res.Data["operationsTotal"] != 3 {
		t.Errorf("operationsTotal = %v, want 3", res.Data["operationsTotal"])
	}
	if res.Data["note"] != openAPIParseNote || res.Data["truncated"] != false {
		t.Errorf("note/truncated = %v / %v", res.Data["note"], res.Data["truncated"])
	}
	if _, ok := res.Data["schemes"]; !ok {
		t.Error("Data lost the auth schemes the tool existed for")
	}
}

func TestParseOpenAPISpecTool_ResultSchemaCoversEveryDataKey(t *testing.T) {
	schema := tool.ResultSchemaOf(&ParseOpenAPISpecTool{})
	if schema == nil {
		t.Fatal("parse_openapi_spec declares no result schema")
	}
	declared := slices.Sorted(maps.Keys(schema.Properties))
	for _, want := range []string{"schemes", "suggestedAuth", "operations", "operationsTotal", "truncated", "note", "operationsError", "operationsWarning", "clashingOperationIds", "raw_response"} {
		if !slices.Contains(declared, want) {
			t.Errorf("result schema omits %q; declared %v", want, declared)
		}
	}

	res := runParseOpenAPI(t, &openAPIParseQuerier{response: json.RawMessage(openAPIParseFixture)})
	for key := range res.Data {
		if !slices.Contains(declared, key) {
			t.Errorf("Data carries %q, which the result schema does not declare", key)
		}
	}
}

func TestParseOpenAPISpecTool_UnparseableResponseKeepsTheRawFallback(t *testing.T) {
	res := runParseOpenAPI(t, &openAPIParseQuerier{response: json.RawMessage(`[1,2]`)})

	if res.Status != tool.StatusSuccess || res.Data["raw_response"] != "[1,2]" {
		t.Fatalf("got %s %v, want the raw response passed through", res.Status, res.Data)
	}
	if len(res.DataObjects) != 0 {
		t.Errorf("an unreadable response must not be written as an artifact: %v", res.DataObjects)
	}
}

func TestParseOpenAPISpecTool_PlatformErrorIsReported(t *testing.T) {
	res := runParseOpenAPI(t, &openAPIParseQuerier{err: errors.New("Could not fetch the OpenAPI specification from the URL (HTTP 404)")})
	if res.Status != tool.StatusError || !strings.Contains(res.Message, "HTTP 404") {
		t.Fatalf("got %s %q, want the platform's error", res.Status, res.Message)
	}
}

func TestOpenAPIParsePreview_ListsOperationsWithRequiredParameters(t *testing.T) {
	var view openAPIParseView
	if err := json.Unmarshal([]byte(openAPIParseFixture), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}

	got := openAPIParsePreview(view)

	want := "Note: " + openAPIParseNote + "\n" +
		"Operations: 3\n" +
		"create_pet: in(name*, tag)\n" +
		"get_pet: in(pet_id*)\n" +
		"list_pets: in(limit)\n"
	if got != want {
		t.Errorf("preview =\n%s\nwant\n%s", got, want)
	}
}

func TestOpenAPIParsePreview_ShedsParametersBeforeOperations(t *testing.T) {
	var view openAPIParseView
	if err := json.Unmarshal([]byte(openAPIParseResponseWith(60, 60, false)), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}

	got := openAPIParsePreview(view)

	if strings.Contains(got, "in(") {
		t.Errorf("60 operations should have shed parameters:\n%s", got)
	}
	for i := range 60 {
		if name := fmt.Sprintf("operation_%03d", i); !strings.Contains(got, name) {
			t.Fatalf("preview dropped %q: an operation the model cannot see is one it cannot allow-list", name)
		}
	}
	if !strings.Contains(got, "load_artifact openapi_spec.json for parameters") || !strings.Contains(got, "Note: ") {
		t.Errorf("preview must say where the parameters went and keep the allow_list note:\n%s", got)
	}
}

func TestOpenAPIParsePreview_SaysWhenOperationsCouldNotBeRead(t *testing.T) {
	var view openAPIParseView
	body := `{"schemes":[],"operations":[],"operationsTotal":0,"truncated":false,"note":"` + openAPIParseNote + `","operationsError":"the specification refers to other files, which are not followed"}`
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}

	got := openAPIParsePreview(view)

	want := "Note: " + openAPIParseNote + "\n" +
		"operations: could not be read (the specification refers to other files, which are not followed)\n"
	if got != want {
		t.Errorf("preview =\n%s\nwant\n%s", got, want)
	}
}

func TestParseOpenAPISpecTool_UnreadableOperationsStillReturnAuthAndTheReason(t *testing.T) {
	body := `{"schemes":[{"schemeName":"key","authType":"apikey","details":{}}],"operations":[],"operationsTotal":0,"truncated":false,"note":"` + openAPIParseNote + `","operationsError":"the specification is not valid OpenAPI for reading operations"}`

	res := runParseOpenAPI(t, &openAPIParseQuerier{response: json.RawMessage(body)})

	if res.Status != tool.StatusSuccess {
		t.Fatalf("status = %s: %s", res.Status, res.Message)
	}
	if res.Data["operationsError"] != "the specification is not valid OpenAPI for reading operations" {
		t.Errorf("Data lost operationsError: %v", res.Data)
	}
	if _, ok := res.Data["schemes"]; !ok {
		t.Error("Data lost the auth schemes")
	}
	if !strings.Contains(res.DataObjects[0].Preview, "operations: could not be read (") {
		t.Errorf("preview = %q", res.DataObjects[0].Preview)
	}
}

func TestOpenAPIParsePreview_NamesTheClashingOperationsBesideTheUsableOnes(t *testing.T) {
	const warning = "operationIds clash once snake-cased: the clashing operations can't back a tool; list only other operations in the allow_list"
	body := `{"schemes":[],"operations":[{"toolName":"other","operationId":"other","method":"GET","path":"/c","parameters":{"type":"object","properties":{}}}],"operationsTotal":1,"truncated":false,"note":"n","operationsWarning":"` + warning + `","clashingOperationIds":["getThing","get_thing"]}`

	res := runParseOpenAPI(t, &openAPIParseQuerier{response: json.RawMessage(body)})

	if res.Data["operationsWarning"] != warning {
		t.Errorf("Data lost operationsWarning: %v", res.Data)
	}
	if ids, _ := res.Data["clashingOperationIds"].([]any); len(ids) != 2 {
		t.Errorf("Data lost clashingOperationIds: %v", res.Data)
	}
	preview := res.DataObjects[0].Preview
	if want := "clash: getThing, get_thing \u2014 " + warning + "\n"; !strings.Contains(preview, want) {
		t.Errorf("preview lacks %q:\n%s", want, preview)
	}
	if !strings.Contains(preview, "other") {
		t.Errorf("preview dropped the usable operation:\n%s", preview)
	}
}

func TestOpenAPIParsePreview_ClashWithNoUsableOperationIsNotATypo(t *testing.T) {
	body := `{"schemes":[],"operations":[],"operationsTotal":0,"truncated":false,"note":"n","operationsWarning":"w","clashingOperationIds":["getThing","get_thing"]}`
	args := map[string]any{"spec_url": "https://example.com/openapi.json", "operations": []any{"getThing"}}

	res := runParseOpenAPIWith(t, &openAPIParseQuerier{response: json.RawMessage(body)}, args)

	preview := res.DataObjects[0].Preview
	if !strings.Contains(preview, "clash: getThing, get_thing") {
		t.Errorf("preview lacks the clash line:\n%s", preview)
	}
	if strings.Contains(res.Message, noOperationsMatchedText) || strings.Contains(preview, noOperationsMatchedText) {
		t.Errorf("a clash read as no operation matching:\nmessage %q\npreview\n%s", res.Message, preview)
	}
}

func TestOpenAPIParsePreview_SaysWhenTheListIsCapped(t *testing.T) {
	var view openAPIParseView
	if err := json.Unmarshal([]byte(openAPIParseResponseWith(3, 350, true)), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := openAPIParsePreview(view)
	if !strings.Contains(got, "Operations: first 3 of 350") {
		t.Errorf("preview does not say the list is capped:\n%s", got)
	}
	if !strings.Contains(got, "call parse_openapi_spec again with `operations: [...]` to see specific ones") {
		t.Errorf("preview does not say how to see the rest:\n%s", got)
	}
}

func TestOpenAPIParsePreview_HardCapped(t *testing.T) {
	var view openAPIParseView
	if err := json.Unmarshal([]byte(openAPIParseResponseWith(200, 200, false)), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i := range view.Operations {
		view.Operations[i].ToolName = strings.Repeat("x", 80) + view.Operations[i].ToolName
	}

	got := openAPIParsePreview(view)

	if len(got) > 4000+250 {
		t.Errorf("preview is %d chars, over the 4000-char hard cap plus its notice", len(got))
	}
	if !strings.Contains(got, "preview cut at 4000 characters") {
		t.Errorf("a capped preview must say it is incomplete: %q", got[max(0, len(got)-200):])
	}
}

func TestParseOpenAPISpecTool_ForwardsTheOperationsFilter(t *testing.T) {
	q := &openAPIParseQuerier{response: json.RawMessage(openAPIParseFixture)}

	res, err := (&ParseOpenAPISpecTool{}).Execute(t.Context(),
		map[string]any{"spec_url": "https://api.example.test/openapi.json", "operations": []any{"getPet", "list_pets"}}, &tool.Context{ControlQuerier: q})
	if err != nil || res.Status != tool.StatusSuccess {
		t.Fatalf("got %v %v", res, err)
	}

	var sent struct {
		SpecURL    string   `json:"specUrl"`
		Operations []string `json:"operations"`
	}
	if err := json.Unmarshal(q.body, &sent); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !slices.Equal(sent.Operations, []string{"getPet", "list_pets"}) {
		t.Errorf("operations sent = %v, want the filter forwarded", sent.Operations)
	}

	runParseOpenAPI(t, q)
	if strings.Contains(string(q.body), "operations") {
		t.Errorf("body = %s, want no operations key when none was asked for", q.body)
	}
}

func TestParseOpenAPISpecTool_DeclaresTheOperationsParameter(t *testing.T) {
	prop, _ := (&ParseOpenAPISpecTool{}).Parameters().Properties["operations"].(map[string]any)
	if prop["type"] != "array" {
		t.Errorf("operations parameter = %v, want an array", prop)
	}
}

func TestParseOpenAPISpecTool_OperationsFilterMatchesOperationIDsOnly(t *testing.T) {
	tl := &ParseOpenAPISpecTool{}
	prop, _ := tl.Parameters().Properties["operations"].(map[string]any)
	for name, text := range map[string]string{"description": tl.Description(), "parameter": fmt.Sprint(prop["description"])} {
		if strings.Contains(text, "operationIds or toolNames") {
			t.Errorf("%s = %q, must not say the filter matches toolNames: the connector's allow_list takes operationIds", name, text)
		}
	}
}

func TestParseOpenAPISpecTool_OperationIDFlowsToTheInlineResult(t *testing.T) {
	body := `{"schemes":[],"operations":[{"toolName":"get_pet","operationId":"GetPet","method":"GET","path":"/p","parameters":{"type":"object"}}],"operationsTotal":1,"truncated":false,"note":"n"}`

	res := runParseOpenAPI(t, &openAPIParseQuerier{response: json.RawMessage(body)})

	ops, _ := res.Data["operations"].([]openAPIOperationView)
	if len(ops) != 1 || ops[0].OperationID != "GetPet" || ops[0].Parameters != nil {
		t.Errorf("inline operations = %+v, want operationId kept and parameters shed", ops)
	}
}

func TestOpenAPIParsePreview_NamesTheOperationIDWhenItDiffersFromTheToolName(t *testing.T) {
	var view openAPIParseView
	body := `{"schemes":[],"operations":[` +
		`{"toolName":"get_pet","operationId":"GetPet","method":"GET","path":"/p","parameters":{"type":"object"}},` +
		`{"toolName":"list_pets","operationId":"list_pets","method":"GET","path":"/ps","parameters":{"type":"object"}}],` +
		`"operationsTotal":2,"truncated":false,"note":"n"}`
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, withParameters := range []bool{true, false} {
		got := renderOperations(view.Operations, withParameters)
		if !strings.Contains(got, "get_pet [operationId: GetPet]") {
			t.Errorf("withParameters=%v: the allow_list names the operationId, so the preview must show it:\n%s", withParameters, got)
		}
		if strings.Contains(got, "list_pets [operationId") {
			t.Errorf("withParameters=%v: operationId equal to toolName must not be repeated:\n%s", withParameters, got)
		}
	}
}

func TestOpenAPIParsePreview_SaysWhenTheFilterMatchedNothing(t *testing.T) {
	body := `{"schemes":[],"operations":[],"operationsTotal":0,"truncated":false,"note":"n"}`
	res, err := (&ParseOpenAPISpecTool{}).Execute(t.Context(),
		map[string]any{"spec_url": "https://api.example.test/openapi.json", "operations": []any{"nothing_here"}},
		&tool.Context{ControlQuerier: &openAPIParseQuerier{response: json.RawMessage(body)}})
	if err != nil || res.Status != tool.StatusSuccess {
		t.Fatalf("got %v %v", res, err)
	}

	const phrase = "0 operations matching the requested names"
	if !strings.Contains(res.Message, phrase) || strings.Contains(res.Message, "0 operation(s)") {
		t.Errorf("message = %q, want %q", res.Message, phrase)
	}
	if preview := res.DataObjects[0].Preview; !strings.Contains(preview, "Operations: "+phrase) {
		t.Errorf("preview = %q, want it to say %q", preview, phrase)
	}
}

func TestParseOpenAPISpecTool_FilterOnAnUnreadableSpecSaysCouldNotBeRead(t *testing.T) {
	body := `{"schemes":[],"operations":[],"operationsTotal":0,"truncated":false,"note":"` + openAPIParseNote + `","operationsError":"the specification refers to other files, which are not followed"}`
	args := map[string]any{"spec_url": "https://example.com/openapi.json", "operations": []any{"get_pet"}}

	res := runParseOpenAPIWith(t, &openAPIParseQuerier{response: json.RawMessage(body)}, args)

	preview := res.DataObjects[0].Preview
	for label, text := range map[string]string{"message": res.Message, "preview": preview} {
		if !strings.Contains(text, "could not be read") || strings.Contains(text, "matching the requested names") {
			t.Errorf("%s = %q, want it to say the operations could not be read, not that none matched", label, text)
		}
	}
}

func TestOpenAPIParsePreview_NoFilterNoOperationsStaysPlain(t *testing.T) {
	body := `{"schemes":[],"operations":[],"operationsTotal":0,"truncated":false,"note":"n"}`
	res := runParseOpenAPI(t, &openAPIParseQuerier{response: json.RawMessage(body)})

	if strings.Contains(res.Message, "matching the requested names") || strings.Contains(res.DataObjects[0].Preview, "matching the requested names") {
		t.Errorf("no filter was given, so nothing was filtered out: %q / %q", res.Message, res.DataObjects[0].Preview)
	}
}
