// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	openapitool "github.com/SolaceProducts/solace-agent-mesh/internal/tool/openapi"
)

const parseTestSpec = `{
  "openapi": "3.0.0",
  "info": {"title": "Pets", "version": "1"},
  "paths": {
    "/pets": {
      "get": {"operationId": "list_pets", "summary": "List pets",
        "parameters": [{"name": "limit", "in": "query", "schema": {"type": "integer"}}],
        "responses": {"200": {"description": "ok"}}},
      "post": {"operationId": "create_pet", "summary": "Create a pet",
        "requestBody": {"required": true, "content": {"application/json": {"schema": {"type": "object", "required": ["name"], "properties": {"name": {"type": "string"}, "tag": {"type": "string"}}}}}},
        "responses": {"201": {"description": "created"}}}
    },
    "/pets/{petId}": {
      "get": {"operationId": "get_pet", "summary": "Get a pet",
        "parameters": [{"name": "petId", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {"200": {"description": "ok"}}}
    }
  },
  "components": {"securitySchemes": {"key": {"type": "apiKey", "in": "header", "name": "X-API-Key"}}}
}`

func parseSpecWithOperations(count int) string {
	var paths []string
	for i := range count {
		paths = append(paths, fmt.Sprintf(`"/r/%03d": {"get": {"operationId": "op_%03d", "responses": {"200": {"description": "ok"}}}}`, i, i))
	}
	return `{"openapi": "3.0.0", "info": {"title": "Big", "version": "1"}, "paths": {` + strings.Join(paths, ",") + `}}`
}

func serveSpec(t *testing.T, spec string) *internalTarget {
	t.Helper()
	t.Setenv(allowPrivateMCPEnvVar, "true")
	return newInternalTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, spec) })
}

func callOpenAPIParse(t *testing.T, env *controlTestEnv, specURL string) (OpenAPIParseResponse, map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"specUrl": specURL})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	raw, err := env.SlowClient.Custom(t.Context(), "platform", "post", "connectors/openapi/parse", body)
	if err != nil {
		t.Fatalf("connectors/openapi/parse: %v", err)
	}
	var resp OpenAPIParseResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, raw)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("decode generic: %v", err)
	}
	return resp, generic
}

func newParseEnv(t *testing.T) *controlTestEnv {
	t.Helper()
	return setupControlTestWithDeps(t, func(d *RouteDeps) { d.AuthDetector = NewOpenAPIAuthDetector(nil) })
}

func TestControlProtocol_OpenAPIParse_ReturnsAuthAndOperationsFromOneFetch(t *testing.T) {
	target := serveSpec(t, parseTestSpec)
	env := newParseEnv(t)

	resp, generic := callOpenAPIParse(t, env, target.srv.URL+"/spec.json")

	if len(resp.Schemes) != 1 || resp.Schemes[0].AuthType != "apikey" {
		t.Errorf("schemes = %+v, want the one apikey scheme", resp.Schemes)
	}
	if resp.SuggestedAuth == nil || resp.SuggestedAuth.AuthType != "apikey" {
		t.Errorf("suggestedAuth = %+v, want apikey", resp.SuggestedAuth)
	}
	var names []string
	for _, op := range resp.Operations {
		names = append(names, op.ToolName)
	}
	want := []string{openapitool.OperationToolName("create_pet"), openapitool.OperationToolName("get_pet"), openapitool.OperationToolName("list_pets")}
	if !slices.Equal(names, want) {
		t.Errorf("operation tool names = %v, want %v (sorted)", names, want)
	}
	if resp.OperationsTotal != 3 || resp.Truncated {
		t.Errorf("operationsTotal = %d, truncated = %v, want 3 and false", resp.OperationsTotal, resp.Truncated)
	}
	if resp.Note != openAPIParseAllowListNote {
		t.Errorf("note = %q, want the fixed allow_list note", resp.Note)
	}
	for _, key := range []string{"schemes", "operations", "operationsTotal", "truncated", "note"} {
		if _, ok := generic[key]; !ok {
			t.Errorf("response has no %q key: %v", key, generic)
		}
	}
	if hits := target.hits.Load(); hits != 1 {
		t.Errorf("the spec server was fetched %d times, want exactly 1 for auth and operations together", hits)
	}
}

func TestControlProtocol_OpenAPIParse_OperationsAgreeWithEnumerateOperations(t *testing.T) {
	target := serveSpec(t, parseTestSpec)
	env := newParseEnv(t)

	resp, _ := callOpenAPIParse(t, env, target.srv.URL+"/spec.json")
	signatures, err := openapitool.EnumerateOperations(t.Context(), []byte(parseTestSpec), nil, nil)
	if err != nil {
		t.Fatalf("EnumerateOperations: %v", err)
	}
	if len(resp.Operations) != len(signatures) {
		t.Fatalf("%d operations described, %d enumerated", len(resp.Operations), len(signatures))
	}
	for i, op := range resp.Operations {
		if op.ToolName != signatures[i].ToolName {
			t.Fatalf("operation %d is %s, want %s", i, op.ToolName, signatures[i].ToolName)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(op.Parameters, &schema); err != nil {
			t.Fatalf("%s: parameters is not a JSON schema: %v\n%s", op.ToolName, err, op.Parameters)
		}
		for _, param := range signatures[i].Params {
			if _, ok := schema.Properties[param.Name]; !ok {
				t.Errorf("%s: parameter %q is enumerated but not described", op.ToolName, param.Name)
			}
			if param.Required != slices.Contains(schema.Required, param.Name) {
				t.Errorf("%s: parameter %q required = %v in EnumerateOperations, %v in the description", op.ToolName, param.Name, param.Required, slices.Contains(schema.Required, param.Name))
			}
		}
		if len(schema.Properties) != len(signatures[i].Params) {
			t.Errorf("%s: %d properties described, %d parameters enumerated", op.ToolName, len(schema.Properties), len(signatures[i].Params))
		}
	}
}

func TestControlProtocol_OpenAPIParse_NoOperationsIsAnEmptyListNotNull(t *testing.T) {
	target := serveSpec(t, validSpecJSON)
	env := newParseEnv(t)

	resp, generic := callOpenAPIParse(t, env, target.srv.URL+"/spec.json")

	ops, ok := generic["operations"].([]any)
	if !ok || len(ops) != 0 {
		t.Fatalf("operations = %#v, want [] (a null crashes the Builder tool's reader)", generic["operations"])
	}
	if resp.OperationsTotal != 0 || resp.Truncated {
		t.Errorf("operationsTotal = %d, truncated = %v", resp.OperationsTotal, resp.Truncated)
	}
}

func TestControlProtocol_OpenAPIParse_CapsOperations(t *testing.T) {
	target := serveSpec(t, parseSpecWithOperations(openAPIParseMaxOperations+5))
	env := newParseEnv(t)

	resp, _ := callOpenAPIParse(t, env, target.srv.URL+"/spec.json")

	if len(resp.Operations) != openAPIParseMaxOperations {
		t.Errorf("len(operations) = %d, want %d", len(resp.Operations), openAPIParseMaxOperations)
	}
	if resp.OperationsTotal != openAPIParseMaxOperations+5 || !resp.Truncated {
		t.Errorf("operationsTotal = %d, truncated = %v, want %d and true", resp.OperationsTotal, resp.Truncated, openAPIParseMaxOperations+5)
	}
	if first := resp.Operations[0].ToolName; first != openapitool.OperationToolName("op_000") {
		t.Errorf("first operation = %s, want the cap to keep the head of the sorted list", first)
	}
}

func TestControlProtocol_OpenAPIParse_UnreadableOperationsKeepTheAuthDetection(t *testing.T) {
	spec := `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{"/x":{"$ref":"secret-other.json#/x"}},` +
		`"components":{"securitySchemes":{"key":{"type":"apiKey","in":"header","name":"X-API-Key"}}}}`
	target := serveSpec(t, spec)
	env := newParseEnv(t)

	specURL := target.srv.URL + "/spec.json"
	resp, generic := callOpenAPIParse(t, env, specURL)

	if len(resp.Schemes) != 1 || resp.Schemes[0].AuthType != "apikey" {
		t.Errorf("schemes = %+v, want the auth detection kept, as parse_openapi_spec returned it before", resp.Schemes)
	}
	ops, ok := generic["operations"].([]any)
	if !ok || len(ops) != 0 || resp.OperationsTotal != 0 || resp.Truncated {
		t.Errorf("operations = %#v, total %d, truncated %v, want [] / 0 / false", generic["operations"], resp.OperationsTotal, resp.Truncated)
	}
	if resp.OperationsError != operationsErrorExternalRefs {
		t.Errorf("operationsError = %q, want %q", resp.OperationsError, operationsErrorExternalRefs)
	}
	raw, _ := json.Marshal(resp)
	for _, leak := range []string{"secret-other.json", specURL, target.srv.Listener.Addr().String()} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("response echoes %q: the reason must be a fixed string, not the loader's text", leak)
		}
	}
}

func TestControlProtocol_OpenAPIParse_AReadableSpecHasNoOperationsError(t *testing.T) {
	target := serveSpec(t, parseTestSpec)
	env := newParseEnv(t)

	resp, generic := callOpenAPIParse(t, env, target.srv.URL+"/spec.json")

	if resp.OperationsError != "" {
		t.Errorf("operationsError = %q on a readable spec", resp.OperationsError)
	}
	if _, present := generic["operationsError"]; present {
		t.Error("operationsError must be omitted when operations were read")
	}
}

func TestOperationsErrorReason_IsAFixedStringWhateverTheLoaderSaid(t *testing.T) {
	for _, tc := range []struct{ err, want string }{
		{`encountered disallowed external reference: "https://u:p@host/x.json#/a?token=s3cret"`, operationsErrorExternalRefs},
		{`failed to unmarshal data: json error: invalid character 'o' in literal null`, operationsErrorUnreadable},
		{"anything else, including secret-body-text", operationsErrorUnreadable},
	} {
		got := operationsErrorReason(errors.New(tc.err))
		if got != tc.want {
			t.Errorf("operationsErrorReason(%q) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestOpenAPIParse_RequiresASpecURLAndTheConnectorReadScope(t *testing.T) {
	t.Parallel()
	mp := testManageablePlatform()
	mp.deps.AuthDetector = NewOpenAPIAuthDetector(nil)
	path := []string{"openapi", "parse"}

	for _, body := range []string{`{}`, `{"specUrl":"  "}`, `not json`} {
		_, err := mp.handleConnectorManagement(t.Context(), "post", path, []byte(body), nil)
		if ve, ok := asValidationError(err); !ok || !strings.Contains(ve.Message, "non-empty specUrl") {
			t.Errorf("body %q: err = %v, want a ValidationError naming specUrl", body, err)
		}
	}

	denied := &auth.Identity{Scopes: []string{rbac.ScopeAgentBuilderRead}}
	_, err := mp.handleConnectorManagement(t.Context(), "post", path, []byte(`{"specUrl":"https://example.com/spec.json"}`), denied)
	if err == nil || !strings.Contains(err.Error(), rbac.ScopeConnectorRead) {
		t.Errorf("err = %v, want a refusal naming %s", err, rbac.ScopeConnectorRead)
	}

	mp.deps.AuthDetector = nil
	_, err = mp.handleConnectorManagement(t.Context(), "post", path, []byte(`{"specUrl":"https://example.com/spec.json"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Errorf("err = %v, want the detector-unavailable error", err)
	}
}

func asValidationError(err error) (*ValidationError, bool) {
	ve, ok := err.(*ValidationError)
	return ve, ok
}

func callOpenAPIParseOnly(t *testing.T, env *controlTestEnv, specURL string, only []string) OpenAPIParseResponse {
	t.Helper()
	resp, _ := callOpenAPIParseWith(t, env, specURL, only)
	return resp
}

func callOpenAPIParseWith(t *testing.T, env *controlTestEnv, specURL string, only []string) (OpenAPIParseResponse, map[string]any) {
	t.Helper()
	body, err := json.Marshal(OpenAPIParseRequest{SpecURL: specURL, Operations: only})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	raw, err := env.SlowClient.Custom(t.Context(), "platform", "post", "connectors/openapi/parse", body)
	if err != nil {
		t.Fatalf("connectors/openapi/parse: %v", err)
	}
	var resp OpenAPIParseResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, raw)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("decode generic: %v", err)
	}
	return resp, generic
}

func TestControlProtocol_OpenAPIParse_OperationsFilterMatchesOperationID(t *testing.T) {
	target := serveSpec(t, strings.ReplaceAll(parseTestSpec, `"list_pets"`, `"listPets"`))
	env := newParseEnv(t)

	resp := callOpenAPIParseOnly(t, env, target.srv.URL+"/spec.json", []string{"listPets", openapitool.OperationToolName("get_pet"), "no_such_operation"})

	var ids []string
	for _, op := range resp.Operations {
		ids = append(ids, op.OperationID)
	}
	if !slices.Equal(ids, []string{"get_pet", "listPets"}) {
		t.Errorf("operationIds = %v, want get_pet and listPets, each matched by its operationId", ids)
	}
	if resp.OperationsTotal != 2 || resp.Truncated {
		t.Errorf("operationsTotal = %d, truncated = %v, want the filtered count 2 and false", resp.OperationsTotal, resp.Truncated)
	}
	if len(resp.Schemes) != 1 {
		t.Errorf("schemes = %+v, want the auth detection unaffected by the filter", resp.Schemes)
	}
}

func TestControlProtocol_OpenAPIParse_OperationsFilterIgnoresATruncatedToolName(t *testing.T) {
	longOperationID := "listPetsOwnedByEveryRegisteredHouseholdInTheNeighbourhoodDirectory"
	spec := strings.ReplaceAll(parseTestSpec, `"list_pets"`, `"`+longOperationID+`"`)
	truncatedToolName := openapitool.OperationToolName(longOperationID)
	if len(truncatedToolName) != 60 {
		t.Fatalf("fixture operationId must produce a toolName cut to 60 characters, got %q", truncatedToolName)
	}
	target := serveSpec(t, spec)
	env := newParseEnv(t)
	specURL := target.srv.URL + "/spec.json"

	byToolName := callOpenAPIParseOnly(t, env, specURL, []string{truncatedToolName})
	if byToolName.OperationsTotal != 0 {
		t.Errorf("the truncated toolName %q matched %d operations; the runtime allow_list would drop it", truncatedToolName, byToolName.OperationsTotal)
	}
	byOperationID := callOpenAPIParseOnly(t, env, specURL, []string{longOperationID})
	if byOperationID.OperationsTotal != 1 {
		t.Errorf("the full operationId matched %d operations, want 1", byOperationID.OperationsTotal)
	}
}

func TestControlProtocol_OpenAPIParse_OperationsFilterWithNoMatchIsAnEmptyList(t *testing.T) {
	target := serveSpec(t, parseTestSpec)
	env := newParseEnv(t)

	resp := callOpenAPIParseOnly(t, env, target.srv.URL+"/spec.json", []string{"nothing_here"})

	if resp.Operations == nil || len(resp.Operations) != 0 || resp.OperationsTotal != 0 {
		t.Errorf("operations = %#v, total %d, want an empty non-nil list", resp.Operations, resp.OperationsTotal)
	}
}

func TestControlProtocol_OpenAPIParse_OperationsFilterMatchesLikeTheAllowList(t *testing.T) {
	target := serveSpec(t, parseTestSpec)
	env := newParseEnv(t)

	resp := callOpenAPIParseOnly(t, env, target.srv.URL+"/spec.json", []string{"createPet", "GET-PET"})

	var got []string
	for _, op := range resp.Operations {
		got = append(got, op.OperationID)
	}
	if !slices.Equal(got, []string{"create_pet", "get_pet"}) || resp.OperationsTotal != 2 {
		t.Errorf("matched %v, total %d; want create_pet and get_pet: the filter snake-cases both sides as the allow_list does", got, resp.OperationsTotal)
	}
}

func TestControlProtocol_OpenAPIParse_CapAppliesAfterTheFilter(t *testing.T) {
	const operations = openAPIParseMaxOperations + 5
	target := serveSpec(t, parseSpecWithOperations(operations+20))
	env := newParseEnv(t)

	var only []string
	for i := range operations {
		only = append(only, fmt.Sprintf("op_%03d", i))
	}
	resp := callOpenAPIParseOnly(t, env, target.srv.URL+"/spec.json", only)

	if len(resp.Operations) != openAPIParseMaxOperations || resp.OperationsTotal != operations || !resp.Truncated {
		t.Errorf("got %d operations, total %d, truncated %v; want %d, %d, true", len(resp.Operations), resp.OperationsTotal, resp.Truncated, openAPIParseMaxOperations, operations)
	}
}

func TestControlProtocol_OpenAPIParse_CarriesOperationIDAndNamesItInTheNote(t *testing.T) {
	target := serveSpec(t, parseTestSpec)
	env := newParseEnv(t)

	resp, _ := callOpenAPIParse(t, env, target.srv.URL+"/spec.json")

	if len(resp.Operations) == 0 || resp.Operations[0].OperationID != "create_pet" {
		t.Errorf("operations = %+v, want each to carry its operationId", resp.Operations)
	}
	if !strings.Contains(resp.Note, "operationId") {
		t.Errorf("note = %q, want it to say the allow_list entry is the operationId", resp.Note)
	}
}

func TestControlProtocol_OpenAPIParse_TruncatesLongSummaries(t *testing.T) {
	long := strings.Repeat("é", describeMaxDescription*3)
	spec := `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{"/x":{"get":{"operationId":"op_x","summary":"` + long + `","responses":{"200":{"description":"ok"}}}}}}`
	target := serveSpec(t, spec)
	env := newParseEnv(t)

	resp, _ := callOpenAPIParse(t, env, target.srv.URL+"/spec.json")

	if len(resp.Operations) != 1 {
		t.Fatalf("operations = %+v", resp.Operations)
	}
	if got := len([]rune(resp.Operations[0].Summary)); got != describeMaxDescription {
		t.Errorf("summary is %d runes, want it cut to %d", got, describeMaxDescription)
	}
}

func TestOperationsErrorReason_RecognisesTheRealLoaderErrorForAnExternalReference(t *testing.T) {
	spec := `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{"/x":{"$ref":"other.json#/x"}}}`

	_, err := openapitool.DescribeOperations(t.Context(), []byte(spec), nil, nil)
	if err == nil {
		t.Fatal("DescribeOperations accepted a specification with an external $ref")
	}

	if got := operationsErrorReason(err); got != operationsErrorExternalRefs {
		t.Errorf("operationsErrorReason(%q) = %q, want %q", err, got, operationsErrorExternalRefs)
	}
}

const clashingParseSpec = `{"openapi":"3.0.0","info":{"title":"Clash","version":"1"},"paths":{
  "/a":{"get":{"operationId":"getThing","responses":{"200":{"description":"ok"}}}},
  "/b":{"get":{"operationId":"get_thing","responses":{"200":{"description":"ok"}}}},
  "/c":{"get":{"operationId":"other","responses":{"200":{"description":"ok"}}}}}}`

func operationIDsOf(operations []openapitool.OperationDescription) []string {
	ids := make([]string, 0, len(operations))
	for _, op := range operations {
		ids = append(ids, op.OperationID)
	}
	return ids
}

func TestControlProtocol_OpenAPIParse_AClashingSpecKeepsItsUsableOperationsAndNamesTheClash(t *testing.T) {
	target := serveSpec(t, clashingParseSpec)
	env := newParseEnv(t)

	resp, generic := callOpenAPIParse(t, env, target.srv.URL+"/spec.json")

	if resp.OperationsError != "" {
		t.Errorf("operationsError = %q, want none: the spec is readable", resp.OperationsError)
	}
	if resp.OperationsWarning != operationsWarningIDClash {
		t.Errorf("operationsWarning = %q, want %q", resp.OperationsWarning, operationsWarningIDClash)
	}
	if !slices.Equal(resp.ClashingOperationIDs, []string{"getThing", "get_thing"}) {
		t.Errorf("clashingOperationIds = %v, want [getThing get_thing] sorted", resp.ClashingOperationIDs)
	}
	if ids := operationIDsOf(resp.Operations); !slices.Equal(ids, []string{"other"}) || resp.OperationsTotal != 1 {
		t.Errorf("operations = %v, total %d, want only other: the clashing pair registers no tool", ids, resp.OperationsTotal)
	}
	if _, present := generic["operationsError"]; present {
		t.Error("operationsError must be omitted for a clash")
	}
}

func TestControlProtocol_OpenAPIParse_AFilterThatAvoidsTheClashIsDescribedWithoutAWarning(t *testing.T) {
	target := serveSpec(t, clashingParseSpec)
	env := newParseEnv(t)

	resp, generic := callOpenAPIParseWith(t, env, target.srv.URL+"/spec.json", []string{"other"})

	if resp.OperationsError != "" || resp.OperationsWarning != "" || resp.OperationsTotal != 1 || !slices.Equal(operationIDsOf(resp.Operations), []string{"other"}) {
		t.Errorf("got error %q, warning %q, total %d, operations %+v, want only other with no warning", resp.OperationsError, resp.OperationsWarning, resp.OperationsTotal, resp.Operations)
	}
	for _, key := range []string{"operationsWarning", "clashingOperationIds"} {
		if _, present := generic[key]; present {
			t.Errorf("%s must be omitted when the filter avoids the clash: %v", key, generic)
		}
	}
}

func TestControlProtocol_OpenAPIParse_AFilterNamingOneClashingIDIsWarnedNotLeftEmpty(t *testing.T) {
	target := serveSpec(t, clashingParseSpec)
	env := newParseEnv(t)

	resp, _ := callOpenAPIParseWith(t, env, target.srv.URL+"/spec.json", []string{"getThing"})

	if len(resp.Operations) != 0 || resp.OperationsTotal != 0 {
		t.Errorf("operations = %+v, total %d, want none: the runtime selects both clashing operations and registers neither", resp.Operations, resp.OperationsTotal)
	}
	if resp.OperationsWarning != operationsWarningIDClash {
		t.Errorf("operationsWarning = %q, want %q, so an empty result is not read as a typo", resp.OperationsWarning, operationsWarningIDClash)
	}
	if !slices.Equal(resp.ClashingOperationIDs, []string{"getThing", "get_thing"}) {
		t.Errorf("clashingOperationIds = %v, want both of the pair", resp.ClashingOperationIDs)
	}
}

func TestControlProtocol_OpenAPIParse_AClashOutsideTheFilterIsNotReported(t *testing.T) {
	spec := strings.Replace(clashingParseSpec, `"/c":`, `"/c2":{"get":{"operationId":"listAll","responses":{"200":{"description":"ok"}}}},"/c3":{"get":{"operationId":"list_all","responses":{"200":{"description":"ok"}}}},"/c":`, 1)
	target := serveSpec(t, spec)
	env := newParseEnv(t)

	resp, _ := callOpenAPIParseWith(t, env, target.srv.URL+"/spec.json", []string{"getThing", "other"})

	if !slices.Equal(resp.ClashingOperationIDs, []string{"getThing", "get_thing"}) {
		t.Errorf("clashingOperationIds = %v, want only the pair the filter selects", resp.ClashingOperationIDs)
	}
}

func TestControlProtocol_OpenAPIParse_AnUnparseableSpecIsUnreadableNotAClash(t *testing.T) {
	target := serveSpec(t, `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{"/x":{"get":{"operationId":42}}}}`)
	env := newParseEnv(t)

	resp, _ := callOpenAPIParse(t, env, target.srv.URL+"/spec.json")

	if resp.OperationsError != operationsErrorUnreadable {
		t.Errorf("operationsError = %q, want %q", resp.OperationsError, operationsErrorUnreadable)
	}
	if resp.OperationsWarning != "" || len(resp.ClashingOperationIDs) != 0 {
		t.Errorf("warning %q, clashes %v, want neither for an unreadable spec", resp.OperationsWarning, resp.ClashingOperationIDs)
	}
}

func TestOperationsErrorReason_ATimeoutIsNotAnInvalidSpec(t *testing.T) {
	for _, err := range []error{context.DeadlineExceeded, fmt.Errorf("loading: %w", context.DeadlineExceeded)} {
		if got := operationsErrorReason(err); got != operationsErrorTimeout {
			t.Errorf("operationsErrorReason(%v) = %q, want %q", err, got, operationsErrorTimeout)
		}
	}
}

func TestOperationsErrorReason_AContextPastItsDeadlineGivesTheTimeoutReason(t *testing.T) {
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := openapitool.DescribeOperations(ctx, []byte(parseTestSpec), nil, nil)
	if err == nil {
		t.Fatal("DescribeOperations ignored a context past its deadline")
	}
	if got := operationsErrorReason(err); got != operationsErrorTimeout {
		t.Errorf("operationsErrorReason(%q) = %q, want %q", err, got, operationsErrorTimeout)
	}
}

func sharedSchemaDAGSpecJSON(levels int) string {
	var schemas []string
	for i := range levels {
		next := fmt.Sprintf(`{"$ref": "#/components/schemas/S%d"}`, i+1)
		schemas = append(schemas, fmt.Sprintf(`"S%d": {"type": "object", "properties": {"a": %s, "b": %s}}`, i, next, next))
	}
	schemas = append(schemas, fmt.Sprintf(`"S%d": {"type": "string"}`, levels))
	return `{"openapi": "3.0.0", "info": {"title": "Dag", "version": "1"}, "paths": {"/dag": {"post": {"operationId": "post_dag",
		"requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/S0"}}}},
		"responses": {"200": {"description": "ok"}}}}}, "components": {"schemas": {` + strings.Join(schemas, ",") + `}}}`
}

func TestControlProtocol_OpenAPIParse_ASharedSchemaGraphIsBounded(t *testing.T) {
	target := serveSpec(t, sharedSchemaDAGSpecJSON(30))
	env := newParseEnv(t)

	resp, _ := callOpenAPIParse(t, env, target.srv.URL+"/spec.json")

	if resp.OperationsTotal != 1 || resp.OperationsError != "" {
		t.Fatalf("operationsTotal = %d, operationsError = %q, want the one operation described", resp.OperationsTotal, resp.OperationsError)
	}
	if !strings.Contains(string(resp.Operations[0].Parameters), "schema too large") {
		t.Errorf("parameters of the over-budget schema carry no stub: the render was not bounded")
	}
}
