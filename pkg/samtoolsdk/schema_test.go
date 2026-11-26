// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package samtoolsdk

import (
	"bytes"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestSchemaFromStruct_BasicTypes(t *testing.T) {
	type Params struct {
		Name   string  `json:"name" desc:"The person's name"`
		Age    int     `json:"age" desc:"Age in years"`
		Score  float64 `json:"score"`
		Active bool    `json:"active" desc:"Is active"`
	}

	schema, artifactInfo := schemaFromStruct(reflect.TypeOf(Params{}))

	if schema.Type != "object" {
		t.Fatalf("expected type 'object', got %q", schema.Type)
	}
	if len(schema.Properties) != 4 {
		t.Fatalf("expected 4 properties, got %d", len(schema.Properties))
	}
	if len(schema.Required) != 4 {
		t.Fatalf("expected 4 required, got %d: %v", len(schema.Required), schema.Required)
	}
	if len(artifactInfo) != 0 {
		t.Fatalf("expected no artifact params, got %d", len(artifactInfo))
	}

	nameProp := schema.Properties["name"].(map[string]any)
	if nameProp["type"] != "string" {
		t.Errorf("name type: got %v, want 'string'", nameProp["type"])
	}
	if nameProp["description"] != "The person's name" {
		t.Errorf("name desc: got %v", nameProp["description"])
	}

	ageProp := schema.Properties["age"].(map[string]any)
	if ageProp["type"] != "integer" {
		t.Errorf("age type: got %v, want 'integer'", ageProp["type"])
	}

	scoreProp := schema.Properties["score"].(map[string]any)
	if scoreProp["type"] != "number" {
		t.Errorf("score type: got %v, want 'number'", scoreProp["type"])
	}

	activeProp := schema.Properties["active"].(map[string]any)
	if activeProp["type"] != "boolean" {
		t.Errorf("active type: got %v, want 'boolean'", activeProp["type"])
	}
}

func TestSchemaFromStruct_OptionalFields(t *testing.T) {
	type Params struct {
		Name  string  `json:"name"`
		Count *int    `json:"count" desc:"Optional count"`
		Tag   *string `json:"tag"`
	}

	schema, _ := schemaFromStruct(reflect.TypeOf(Params{}))

	if len(schema.Required) != 1 {
		t.Fatalf("expected 1 required, got %d: %v", len(schema.Required), schema.Required)
	}
	if schema.Required[0] != "name" {
		t.Errorf("expected required[0] = 'name', got %q", schema.Required[0])
	}
	if len(schema.Properties) != 3 {
		t.Fatalf("expected 3 properties, got %d", len(schema.Properties))
	}
}

func TestSchemaFromStruct_ArtifactTypes(t *testing.T) {
	type Params struct {
		Input    Artifact   `json:"input" desc:"Input file"`
		OptFile  *Artifact  `json:"opt_file" desc:"Optional file"`
		Multiple []Artifact `json:"multiple" desc:"Multiple files"`
		Name     string     `json:"name"`
	}

	schema, artifactInfo := schemaFromStruct(reflect.TypeOf(Params{}))

	inputProp := schema.Properties["input"].(map[string]any)
	if inputProp["type"] != "string" {
		t.Errorf("input type: got %v, want 'string'", inputProp["type"])
	}
	if ai, ok := artifactInfo["input"]; !ok || ai.IsList || ai.IsOptional {
		t.Errorf("input artifact info: got %+v, want {IsList:false, IsOptional:false}", ai)
	}

	optProp := schema.Properties["opt_file"].(map[string]any)
	if optProp["type"] != "string" {
		t.Errorf("opt_file type: got %v, want 'string'", optProp["type"])
	}
	if ai, ok := artifactInfo["opt_file"]; !ok || ai.IsList || !ai.IsOptional {
		t.Errorf("opt_file artifact info: got %+v, want {IsList:false, IsOptional:true}", ai)
	}

	mulProp := schema.Properties["multiple"].(map[string]any)
	if mulProp["type"] != "array" {
		t.Errorf("multiple type: got %v, want 'array'", mulProp["type"])
	}
	items := mulProp["items"].(map[string]any)
	if items["type"] != "string" {
		t.Errorf("multiple items type: got %v, want 'string'", items["type"])
	}
	if ai, ok := artifactInfo["multiple"]; !ok || !ai.IsList {
		t.Errorf("multiple artifact info: got %+v, want {IsList:true, ...}", ai)
	}

	requiredSet := make(map[string]bool)
	for _, r := range schema.Required {
		requiredSet[r] = true
	}
	if !requiredSet["input"] {
		t.Error("expected 'input' in required")
	}
	if !requiredSet["name"] {
		t.Error("expected 'name' in required")
	}
	if !requiredSet["multiple"] {
		t.Error("expected 'multiple' in required")
	}
	if requiredSet["opt_file"] {
		t.Error("'opt_file' should NOT be in required")
	}
}

func TestSchemaFromStruct_NestedStruct(t *testing.T) {
	type Address struct {
		Street string `json:"street"`
		City   string `json:"city"`
	}
	type Params struct {
		Name    string  `json:"name"`
		Address Address `json:"address" desc:"Mailing address"`
	}

	schema, _ := schemaFromStruct(reflect.TypeOf(Params{}))

	addrProp := schema.Properties["address"].(map[string]any)
	if addrProp["type"] != "object" {
		t.Errorf("address type: got %v, want 'object'", addrProp["type"])
	}
	if addrProp["description"] != "Mailing address" {
		t.Errorf("address desc: got %v", addrProp["description"])
	}
	props := addrProp["properties"].(map[string]any)
	if len(props) != 2 {
		t.Errorf("expected 2 nested properties, got %d", len(props))
	}
}

func TestSchemaFromStruct_SliceAndMap(t *testing.T) {
	type Params struct {
		Tags    []string       `json:"tags"`
		Headers map[string]any `json:"headers"`
	}

	schema, _ := schemaFromStruct(reflect.TypeOf(Params{}))

	tagsProp := schema.Properties["tags"].(map[string]any)
	if tagsProp["type"] != "array" {
		t.Errorf("tags type: got %v, want 'array'", tagsProp["type"])
	}
	items := tagsProp["items"].(map[string]any)
	if items["type"] != "string" {
		t.Errorf("tags items type: got %v, want 'string'", items["type"])
	}

	headersProp := schema.Properties["headers"].(map[string]any)
	if headersProp["type"] != "object" {
		t.Errorf("headers type: got %v, want 'object'", headersProp["type"])
	}
}

func TestSchemaFromStruct_UnexportedFieldsSkipped(t *testing.T) {
	type Params struct {
		Name   string `json:"name"`
		secret string //nolint:unused
	}

	schema, _ := schemaFromStruct(reflect.TypeOf(Params{}))

	if len(schema.Properties) != 1 {
		t.Fatalf("expected 1 property, got %d", len(schema.Properties))
	}
	if _, ok := schema.Properties["name"]; !ok {
		t.Error("expected 'name' property")
	}
}

func TestSchemaFromStruct_JsonDashSkipped(t *testing.T) {
	type Params struct {
		Name   string `json:"name"`
		Hidden string `json:"-"`
	}

	schema, _ := schemaFromStruct(reflect.TypeOf(Params{}))

	if len(schema.Properties) != 1 {
		t.Fatalf("expected 1 property, got %d", len(schema.Properties))
	}
}

func TestSchemaFromStruct_EmptyStruct(t *testing.T) {
	type Params struct{}

	schema, artifactInfo := schemaFromStruct(reflect.TypeOf(Params{}))

	if schema.Type != "object" {
		t.Errorf("expected type 'object', got %q", schema.Type)
	}
	if len(schema.Properties) != 0 {
		t.Errorf("expected 0 properties, got %d", len(schema.Properties))
	}
	if len(artifactInfo) != 0 {
		t.Errorf("expected 0 artifact params, got %d", len(artifactInfo))
	}
}

type buildSchemaParams struct {
	Query    string  `json:"query" desc:"The query text"`
	Limit    *int    `json:"limit,omitempty" desc:"Max results"`
	Optional *string `json:"optional,omitempty"`
}

func TestBuildSchema(t *testing.T) {
	schema := BuildSchema[buildSchemaParams]()
	if schema == nil {
		t.Fatal("expected non-nil schema")
	}
	if schema.Type != "object" {
		t.Errorf("Type = %q, want 'object'", schema.Type)
	}
	if len(schema.Properties) != 3 {
		t.Fatalf("expected 3 properties, got %d", len(schema.Properties))
	}
	if len(schema.Required) != 1 || schema.Required[0] != "query" {
		t.Errorf("Required = %v, want ['query']", schema.Required)
	}
	queryProp, ok := schema.Properties["query"].(map[string]any)
	if !ok {
		t.Fatalf("query property has wrong type: %T", schema.Properties["query"])
	}
	if queryProp["type"] != "string" {
		t.Errorf("query type = %v, want 'string'", queryProp["type"])
	}
}

func TestBuildSchema_NonStruct(t *testing.T) {
	schema := BuildSchema[int]()
	if schema == nil {
		t.Fatal("expected non-nil schema")
	}
	if schema.Type != "object" {
		t.Errorf("Type = %q, want 'object'", schema.Type)
	}
	if len(schema.Properties) != 0 {
		t.Errorf("expected 0 properties, got %d", len(schema.Properties))
	}
}

func TestBuildSchema_PointerStruct(t *testing.T) {
	schema := BuildSchema[*buildSchemaParams]()
	if schema == nil {
		t.Fatal("expected non-nil schema")
	}
	if len(schema.Properties) != 3 {
		t.Fatalf("expected 3 properties, got %d", len(schema.Properties))
	}
}

func TestSchemaFromStruct_StructuredArtifactList(t *testing.T) {
	type clip struct {
		Filename     string `json:"filename" artifact:"filename"`
		PauseAfterMS *int   `json:"pause_after_ms,omitempty"`
	}
	type params struct {
		Clips  []clip  `json:"clips_to_join" desc:"Ordered list"`
		Output *string `json:"output,omitempty"`
	}

	schema, artifactInfo := schemaFromStruct(reflect.TypeOf(params{}))

	clipsProp, ok := schema.Properties["clips_to_join"].(map[string]any)
	if !ok {
		t.Fatalf("clips_to_join property has wrong type: %T", schema.Properties["clips_to_join"])
	}
	if clipsProp["type"] != "array" {
		t.Errorf("clips_to_join type = %v, want 'array'", clipsProp["type"])
	}
	items, ok := clipsProp["items"].(map[string]any)
	if !ok {
		t.Fatalf("clips_to_join.items has wrong type: %T", clipsProp["items"])
	}
	if items["type"] != "object" {
		t.Errorf("items type = %v, want 'object' (Python-compatible struct-array)", items["type"])
	}
	itemProps, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatalf("items.properties has wrong type: %T", items["properties"])
	}
	if _, found := itemProps["filename"]; !found {
		t.Error("items.properties missing 'filename'")
	}
	if _, found := itemProps["pause_after_ms"]; !found {
		t.Error("items.properties missing 'pause_after_ms'")
	}
	if clipsProp["description"] != "Ordered list" {
		t.Errorf("description = %v", clipsProp["description"])
	}

	ap, ok := artifactInfo["clips_to_join"]
	if !ok {
		t.Fatal("artifactInfo missing 'clips_to_join'")
	}
	if !ap.IsList {
		t.Error("IsList should be true")
	}
	if ap.IsOptional {
		t.Error("IsOptional should be false (non-pointer slice)")
	}
	if ap.FilenameKey != "filename" {
		t.Errorf("FilenameKey = %q, want 'filename'", ap.FilenameKey)
	}
}

func TestSchemaFromStruct_StructuredArtifactListIgnoredWithoutTag(t *testing.T) {
	type entry struct {
		Name string `json:"name"`
	}
	type params struct {
		Entries []entry `json:"entries"`
	}

	_, artifactInfo := schemaFromStruct(reflect.TypeOf(params{}))
	if _, exists := artifactInfo["entries"]; exists {
		t.Error("entries should not be treated as an artifact param without artifact:\"filename\" tag")
	}
}

func TestSchemaFromStruct_OmitemptyMakesFieldOptional(t *testing.T) {
	type Params struct {
		Required    string  `json:"required"`
		StrOmit     string  `json:"str_omit,omitempty"`
		IntOmit     int     `json:"int_omit,omitempty"`
		BoolOmit    bool    `json:"bool_omit,omitempty"`
		PtrPlain    *string `json:"ptr_plain"`
		PtrWithOmit *string `json:"ptr_with_omit,omitempty"`
	}

	var warnBuf bytes.Buffer
	prev := setSchemaWarnWriter(&warnBuf)
	t.Cleanup(func() { setSchemaWarnWriter(prev) })

	schema, _ := schemaFromStruct(reflect.TypeOf(Params{}))

	wantRequired := []string{"required"}
	if !slices.Equal(schema.Required, wantRequired) {
		t.Errorf("Required = %v, want %v", schema.Required, wantRequired)
	}

	wantProps := []string{"required", "str_omit", "int_omit", "bool_omit", "ptr_plain", "ptr_with_omit"}
	for _, name := range wantProps {
		if _, ok := schema.Properties[name]; !ok {
			t.Errorf("property %q missing from schema", name)
		}
	}

	out := warnBuf.String()
	gotLines := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1
	if out == "" {
		gotLines = 0
	}
	if gotLines != 3 {
		t.Errorf("warning line count = %d, want 3; out=%q", gotLines, out)
	}
}

func TestSchemaFromStruct_WarnsOnSignalMismatch(t *testing.T) {
	type Params struct {
		AlwaysReq    string  `json:"always_req"`
		Pointered    *string `json:"pointered"`
		BothOptional *string `json:"both_optional,omitempty"`
		StrMismatch  string  `json:"str_mismatch,omitempty"`
		IntMismatch  int     `json:"int_mismatch,omitempty"`
	}

	var warnBuf bytes.Buffer
	prev := setSchemaWarnWriter(&warnBuf)
	t.Cleanup(func() { setSchemaWarnWriter(prev) })

	_, _ = schemaFromStruct(reflect.TypeOf(Params{}))

	out := warnBuf.String()
	wantLines := 2
	gotLines := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1
	if out == "" {
		gotLines = 0
	}
	if gotLines != wantLines {
		t.Errorf("warning line count = %d, want %d; out=%q", gotLines, wantLines, out)
	}
	for _, wantField := range []string{"StrMismatch", "IntMismatch"} {
		if !strings.Contains(out, wantField) {
			t.Errorf("warning missing reference to %q; got: %q", wantField, out)
		}
	}
	for _, dontWantField := range []string{"AlwaysReq", "Pointered", "BothOptional"} {
		if strings.Contains(out, dontWantField) {
			t.Errorf("unexpected warning mentions %q; got: %q", dontWantField, out)
		}
	}
}

func TestSchemaFromStruct_SliceMapInterfaceOmitemptyDoesNotWarn(t *testing.T) {
	type Params struct {
		Items  []string          `json:"items,omitempty"`
		Lookup map[string]string `json:"lookup,omitempty"`
		Value  any               `json:"value,omitempty"`
		Scalar string            `json:"scalar,omitempty"`
	}

	var warnBuf bytes.Buffer
	prev := setSchemaWarnWriter(&warnBuf)
	t.Cleanup(func() { setSchemaWarnWriter(prev) })

	schema, _ := schemaFromStruct(reflect.TypeOf(Params{}))

	if len(schema.Required) != 0 {
		t.Errorf("Required = %v, want none (all fields optional)", schema.Required)
	}

	out := warnBuf.String()
	for _, dontWant := range []string{"Items", "Lookup", "Value"} {
		if strings.Contains(out, dontWant) {
			t.Errorf("slice/map/interface field %q should not warn; got: %q", dontWant, out)
		}
	}
	if !strings.Contains(out, "Scalar") {
		t.Errorf("scalar field should still warn; got: %q", out)
	}
}

func TestJSONTagHasOmitempty(t *testing.T) {
	cases := []struct {
		name string
		tag  string
		want bool
	}{
		{"Bare", ``, false},
		{"NameOnly", `json:"name_only"`, false},
		{"NameAndOpt", `json:"name_and_opt,omitempty"`, true},
		{"NoName", `json:",omitempty"`, true},
		{"MultiOpt", `json:"multi_opt,string,omitempty"`, true},
		{"LookAlike", `json:"look_alike,omitemptyish"`, false},
		{"Unrelated", `json:"unrelated,string"`, false},
	}
	for _, c := range cases {
		f := reflect.StructField{
			Name: c.name,
			Type: reflect.TypeOf(""),
			Tag:  reflect.StructTag(c.tag),
		}
		got := jsonTagHasOmitempty(f)
		if got != c.want {
			t.Errorf("%s: jsonTagHasOmitempty = %v, want %v (tag=%q)", c.name, got, c.want, c.tag)
		}
	}
}
