// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestIsSecretFieldName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"password", true},
		{"Password", true},
		{"apiKey", true},
		{"api_key", true},
		{"APIKey", true},
		{"authToken", true},
		{"secret", true},
		{"credentials", true},
		{"maxInputTokens", false},
		{"name", false},
		{"description", false},
		{"tokenize", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsSecretFieldName(tc.name); got != tc.want {
			t.Errorf("IsSecretFieldName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestBuildKindSchema_RequiredAndValidate(t *testing.T) {
	req := StructInfo{
		Name: "CreateThingRequest",
		Doc:  "CreateThingRequest creates a thing.",
		Fields: []Field{
			{GoName: "Name", JSONName: "name", Kind: KindString, GoTypeRaw: "string", Validate: "required,min=3,max=40"},
			{GoName: "SystemPrompt", JSONName: "systemPrompt", Kind: KindString, GoTypeRaw: "string", Validate: "omitempty,min=100,max=10000"},
			{GoName: "Description", JSONName: "description", OmitEmpty: true, Kind: KindString, GoTypeRaw: "string"},
			{GoName: "Type", JSONName: "type", OmitEmpty: true, Kind: KindString, GoTypeRaw: "string", Validate: "omitempty,oneof=standard custom"},
			{GoName: "MaxSize", JSONName: "maxSize", OmitEmpty: true, Kind: KindIntPtr, GoTypeRaw: "*int"},
			{GoName: "Note", JSONName: "note", OmitEmpty: true, Kind: KindString, GoTypeRaw: "string", Description: "A short note."},
			{GoName: "APIKey", JSONName: "apiKey", Kind: KindString, GoTypeRaw: "string"},
		},
	}
	got := BuildKindSchema("thing", "things", req, false)

	wantDesc := "CreateThingRequest creates a thing."
	if got.Description != wantDesc {
		t.Errorf("Description: got %q want %q", got.Description, wantDesc)
	}
	want := []FieldSchema{
		{JSONName: "name", GoType: "string", Required: true, MinLen: 3, MaxLen: 40},
		{JSONName: "systemPrompt", GoType: "string", Required: false, MinLen: 100, MaxLen: 10000},
		{JSONName: "description", GoType: "string", Required: false, MinLen: -1, MaxLen: -1},
		{JSONName: "type", GoType: "string", Required: false, MinLen: -1, MaxLen: -1, OneOf: []string{"standard", "custom"}},
		{JSONName: "maxSize", GoType: "*int", Required: false, MinLen: -1, MaxLen: -1, Pointer: true},
		{JSONName: "note", GoType: "string", Required: false, MinLen: -1, MaxLen: -1, Description: "A short note."},
		{JSONName: "apiKey", GoType: "string", Required: true, MinLen: -1, MaxLen: -1, Secret: true},
	}
	if diff := cmp.Diff(want, got.Fields); diff != "" {
		t.Errorf("Fields mismatch (-want +got):\n%s", diff)
	}
}

func TestBuildKindSchema_DocRequiredOverridesOmitEmpty(t *testing.T) {
	req := StructInfo{
		Name: "CreateThingRequest",
		Fields: []Field{
			{GoName: "SystemPrompt", JSONName: "systemPrompt", Kind: KindString, GoTypeRaw: "string", Validate: "omitempty,min=100,max=10000", DocRequired: true},
			{GoName: "Optional", JSONName: "optional", Kind: KindString, GoTypeRaw: "string", Validate: "omitempty"},
		},
	}
	ks := BuildKindSchema("thing", "things", req, false)
	if !ks.Fields[0].Required {
		t.Errorf("systemPrompt with doc:\"required\" should be Required, got %+v", ks.Fields[0])
	}
	if ks.Fields[1].Required {
		t.Errorf("optional with validate omitempty and no doc tag should stay optional, got %+v", ks.Fields[1])
	}
}

func TestBuildKindSchema_NoValidateTags(t *testing.T) {
	req := StructInfo{
		Name: "Bare",
		Fields: []Field{
			{GoName: "Alias", JSONName: "alias", Kind: KindString, GoTypeRaw: "string"},
			{GoName: "APIBase", JSONName: "apiBase", OmitEmpty: true, Kind: KindString, GoTypeRaw: "string"},
		},
	}
	ks := BuildKindSchema("bare", "bares", req, false)
	if !ks.Fields[0].Required {
		t.Errorf("alias should be required by JSON-tag inference, got %+v", ks.Fields[0])
	}
	if ks.Fields[1].Required {
		t.Errorf("apiBase has json:omitempty, should be optional, got %+v", ks.Fields[1])
	}
}

func TestEmitSchemaData_DeterministicAndParseable(t *testing.T) {
	specs := []SchemaSpec{
		{
			Kind:       "thing",
			PluralPath: "things",
			Request: StructInfo{
				Name: "CreateThingRequest",
				Doc:  "Creates a thing.",
				Fields: []Field{
					{GoName: "Name", JSONName: "name", Kind: KindString, GoTypeRaw: "string", Validate: "required"},
					{GoName: "Tags", JSONName: "tags", OmitEmpty: true, Kind: KindStringSlice, GoTypeRaw: "[]string"},
				},
			},
		},
	}
	out, err := EmitSchemaData(specs)
	if err != nil {
		t.Fatalf("EmitSchemaData: %v", err)
	}
	src := string(out)
	for _, want := range []string{
		"package codegen",
		"var SchemaData = map[string]KindSchema{",
		"\"thing\":",
		"Kind:        \"thing\"",
		"PluralPath:  \"things\"",
		"JSONName: \"name\"",
		"Required: true",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("emitted source missing %q\n--- source ---\n%s", want, src)
		}
	}

	out2, err := EmitSchemaData(specs)
	if err != nil {
		t.Fatalf("EmitSchemaData second pass: %v", err)
	}
	if string(out) != string(out2) {
		t.Error("EmitSchemaData not deterministic across calls")
	}
}

func TestEmitSchemaData_DescriptionWithBackslash(t *testing.T) {
	specs := []SchemaSpec{
		{
			Kind:       "thing",
			PluralPath: "things",
			Request: StructInfo{
				Name: "CreateThingRequest",
				Doc:  `Match validates against pattern \w+ and rejects empty input.`,
				Fields: []Field{
					{GoName: "Name", JSONName: "name", Kind: KindString, GoTypeRaw: "string", Validate: "required"},
				},
			},
		},
	}
	out, err := EmitSchemaData(specs)
	if err != nil {
		t.Fatalf("EmitSchemaData: %v", err)
	}
	src := string(out)
	if !strings.Contains(src, `\w+`) && !strings.Contains(src, `\\w+`) {
		t.Errorf("description content lost: \n%s", src)
	}
	if !strings.Contains(src, "`Match validates") {
		t.Logf("hint: emitter chose double-quote form for backslash-only description")
	}
}

func TestSanitizeSchemaDoc(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"gateway request body", "CreateGatewayRequest is the request body for POST /entrypoints.", ""},
		{"model request body", "CreateModelConfigRequest is the request body for POST /models.", ""},
		{"eval body (no request/response)", "CreateDatasetRequest is the body for POST /eval/datasets.", ""},
		{"response body with endpoint", "SecretsDiffResponse is the response body for POST /secretsDiff.", ""},
		{"response prose passes through", "GatewayResponse is the response body for gateway endpoints.", "GatewayResponse is the response body for gateway endpoints."},
		{"patch with path param", "UpdateModelConfigRequest is the request body for PATCH /models/{model_id}.", ""},
		{"rbac wire shape", "CreateRBACClaimMappingRequest is the wire shape for POST /rbac/claimMappings.", ""},
		{"boilerplate then prose", "CreateThingRequest is the request body for POST /things. It also does X.", "It also does X."},
		{"no boilerplate passes through", "SystemPrompt is the agent's base instructions.", "SystemPrompt is the agent's base instructions."},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeSchemaDoc(tc.in); got != tc.want {
				t.Errorf("sanitizeSchemaDoc(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFriendlyType(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"string", "string"},
		{"bool", "boolean"},
		{"*bool", "boolean"},
		{"int", "integer"},
		{"int64", "integer"},
		{"*int", "integer"},
		{"float64", "number"},
		{"time.Duration", "duration"},
		{"any", "object"},
		{"interface{}", "object"},
		{"map[string]any", "object"},
		{"map[string][]string", "object"},
		{"[]string", "list<string>"},
		{"[]*NodeConfig", "list<object>"},
		{"[]map[string]any", "list<object>"},
		{"[]SkillRequest", "list<object>"},
		{"TargetBlock", "object"},
		{"*RetryStrategy", "object"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := FriendlyType(tc.in); got != tc.want {
			t.Errorf("FriendlyType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFieldSchemaMarshalJSON(t *testing.T) {
	f := FieldSchema{JSONName: "values", GoType: "map[string]any", MinLen: -1, MaxLen: -1}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, `"type":"object"`) {
		t.Errorf("expected friendly type in JSON, got %s", got)
	}
	if strings.Contains(got, "map[string]any") || strings.Contains(got, `"GoType"`) {
		t.Errorf("raw Go type leaked into JSON: %s", got)
	}
}

var bannedLeakSubstrings = []string{
	"is the request body for", "is the body for", "is the response body for",
	"map[string]", "[]string", "[]any", "interface{}",
	"validate tag", "`omitempty`", `doc:"`, "cfgapply:",
	"WorkflowConfig", "HILDatabaseURL", "DefaultRegistry", "tool.Set", "nameToSnakeCase",
}

var httpMethodPathRe = regexp.MustCompile(`(?i)\b(POST|GET|PUT|PATCH|DELETE) +(/[a-zA-Z0-9/_{}.-]+)`)

func checkNoInternalLeak(t *testing.T, where, s string) {
	t.Helper()
	for _, b := range bannedLeakSubstrings {
		if strings.Contains(s, b) {
			t.Errorf("%s leaks internal detail %q: %q", where, b, s)
		}
	}
	for _, m := range httpMethodPathRe.FindAllStringSubmatch(s, -1) {
		if !strings.HasPrefix(m[2], "/api/") {
			t.Errorf("%s leaks internal endpoint path %q: %q", where, m[0], s)
		}
	}
}

func TestSchemaData_NoInternalLeaks(t *testing.T) {
	for kind, ks := range SchemaData {
		checkNoInternalLeak(t, "kind "+kind+" description", ks.Description)
		for _, f := range ks.Fields {
			checkNoInternalLeak(t, "kind "+kind+" field "+f.JSONName+" description", f.Description)
			checkNoInternalLeak(t, "kind "+kind+" field "+f.JSONName+" displayType", f.DisplayType())
		}
	}
}

func TestWorkflowSchema_NoInternalLeaks(t *testing.T) {
	checkNoInternalLeak(t, "workflow definition description", WorkflowSchema.Description)
	for _, f := range WorkflowSchema.Fields {
		checkNoInternalLeak(t, "workflow field "+f.JSONName+" description", f.Description)
		checkNoInternalLeak(t, "workflow field "+f.JSONName+" displayType", f.DisplayType())
	}
	for nt, node := range WorkflowSchema.NodeTypes {
		checkNoInternalLeak(t, "node "+nt+" description", node.Description)
		for _, f := range node.Fields {
			checkNoInternalLeak(t, "node "+nt+" field "+f.JSONName+" description", f.Description)
			checkNoInternalLeak(t, "node "+nt+" field "+f.JSONName+" displayType", f.DisplayType())
		}
	}
}

func TestSchemaData_AllKindsHaveFields(t *testing.T) {
	if len(SchemaData) == 0 {
		t.Fatal("SchemaData is empty — codegen did not run?")
	}
	for kind, ks := range SchemaData {
		if len(ks.Fields) == 0 {
			t.Errorf("kind %q has no fields", kind)
		}
		if ks.Kind != kind {
			t.Errorf("kind key %q does not match KindSchema.Kind %q", kind, ks.Kind)
		}
	}
}

func TestRBACHandDefinedSchemas_MatchAuthoringSurface(t *testing.T) {
	cases := []struct {
		schema      KindSchema
		wantFields  []string
		allRequired bool
	}{
		{RBACRoleSchema, []string{"scopes", "inherits", "users"}, false},
		{RBACClaimMappingSchema, []string{"name", "oidcProvider", "claimValue", "roleNames"}, false},
	}
	for _, tc := range cases {
		got := make([]string, 0, len(tc.schema.Fields))
		for _, f := range tc.schema.Fields {
			got = append(got, f.JSONName)
			if tc.allRequired && !f.Required {
				t.Errorf("%s field %q must be Required to match its spec reader", tc.schema.Kind, f.JSONName)
			}
		}
		if !slices.Equal(got, tc.wantFields) {
			t.Errorf("%s fields = %v, want %v", tc.schema.Kind, got, tc.wantFields)
		}
	}
}

func TestBuildKindSchema_LeavesOutAHiddenField(t *testing.T) {
	const src = `package fixture

type Thing struct {
	Shown  string ` + "`json:\"shown\"`" + `
	Hidden *string ` + "`json:\"hidden,omitempty\" doc:\"hidden\"`" + `
}`
	parsed, err := ParseStructs(writeTempGo(t, src), "Thing")
	if err != nil {
		t.Fatalf("ParseStructs: %v", err)
	}
	if len(parsed[0].Fields) != 2 || !parsed[0].Fields[1].DocHidden {
		t.Fatalf("the hidden field should still be parsed, flagged DocHidden: %+v", parsed[0].Fields)
	}
	ks := BuildKindSchema("thing", "things", parsed[0], false)
	if len(ks.Fields) != 1 || ks.Fields[0].JSONName != "shown" {
		t.Errorf("schema fields = %+v, want only shown", ks.Fields)
	}
}

func TestSchemaData_ConnectorKeepsYAMLTypeKeys(t *testing.T) {
	keys := map[string]bool{}
	for _, f := range SchemaData["connector"].Fields {
		keys[f.JSONName] = true
	}
	for _, want := range []string{"type", "subtype"} {
		if !keys[want] {
			t.Errorf("connector schema has no %q field: %v", want, keys)
		}
	}
	for _, wireName := range []string{"connectorType", "connectorSubtype"} {
		if keys[wireName] {
			t.Errorf("connector schema shows the wire name %q to authors", wireName)
		}
	}
}
