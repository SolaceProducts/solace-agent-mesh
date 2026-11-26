// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestGenerate_CompilesAndContainsExpectedSymbols(t *testing.T) {
	spec := Spec{
		Kind: "Sample",
		Request: StructInfo{
			Name: "CreateSampleRequest",
			Fields: []Field{
				{GoName: "Name", JSONName: "name", Kind: KindString},
				{GoName: "Count", JSONName: "count", Kind: KindInt, OmitEmpty: true},
				{GoName: "Tags", JSONName: "tags", Kind: KindStringSlice, OmitEmpty: true},
				{GoName: "MaxSize", JSONName: "maxSize", Kind: KindIntPtr, OmitEmpty: true},
			},
		},
		Response: StructInfo{
			Name: "SampleResponse",
			Fields: []Field{
				{GoName: "ID", JSONName: "id", Kind: KindString},
				{GoName: "Name", JSONName: "name", Kind: KindString},
				{GoName: "Count", JSONName: "count", Kind: KindInt},
				{GoName: "Tags", JSONName: "tags", Kind: KindStringSlice},
				{GoName: "MaxSize", JSONName: "maxSize", Kind: KindIntPtr},
			},
		},
		DTOImportPath:  "example.com/fixture",
		DTOImportAlias: "fixture",
	}

	src, err := Generate("translate", spec)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := parser.ParseFile(token.NewFileSet(), "out.go", src, 0); err != nil {
		t.Fatalf("emitted source did not parse: %v\n--- source ---\n%s", err, src)
	}

	source := string(src)
	wantContains := []string{
		"func ForwardSample(in map[string]any, hooks SampleHooks) (fixture.CreateSampleRequest, error)",
		"func ReverseSample(in fixture.SampleResponse, hooks SampleHooks) (map[string]any, error)",
		`in["name"]`,
		`out.Name = s`,
		`out["name"] = in.Name`,
	}
	for _, w := range wantContains {
		if !strings.Contains(source, w) {
			t.Errorf("output missing %q\n--- source ---\n%s", w, source)
		}
	}

	if strings.Contains(source, `out["id"]`) {
		t.Errorf("reverse incorrectly emits server-only field id\n--- source ---\n%s", source)
	}
}

func TestGenerate_Float64Pointer(t *testing.T) {
	field := Field{GoName: "MinConfidence", JSONName: "minConfidence", Kind: KindFloat64Ptr, OmitEmpty: true, GoTypeRaw: "*float64"}
	src, err := Generate("translate", Spec{
		Kind:           "Sample",
		Request:        StructInfo{Name: "CreateSampleRequest", Fields: []Field{field}},
		Response:       StructInfo{Name: "SampleResponse", Fields: []Field{field}},
		DTOImportPath:  "example.com/fixture",
		DTOImportAlias: "fixture",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "out.go", src, 0); err != nil {
		t.Fatalf("emitted source did not parse: %v\n%s", err, src)
	}
	for _, w := range []string{`ok && v != nil`, `coerceFloat64("minConfidence", v)`, `out.MinConfidence = &n`, `out["minConfidence"] = *in.MinConfidence`} {
		if !strings.Contains(string(src), w) {
			t.Errorf("output missing %q\n%s", w, src)
		}
	}
}

func TestGenerate_BoolPointerLeavesAnOmittedOrNullKeyUnset(t *testing.T) {
	request := Field{GoName: "Enabled", JSONName: "enabled", Kind: KindBoolPtr, GoTypeRaw: "*bool"}
	response := Field{GoName: "Enabled", JSONName: "enabled", Kind: KindBoolPtr, GoTypeRaw: "*bool"}
	src, err := Generate("translate", Spec{
		Kind:           "Sample",
		Request:        StructInfo{Name: "CreateSampleRequest", Fields: []Field{request}},
		Response:       StructInfo{Name: "SampleResponse", Fields: []Field{response}},
		DTOImportPath:  "example.com/fixture",
		DTOImportAlias: "fixture",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "out.go", src, 0); err != nil {
		t.Fatalf("emitted source did not parse: %v\n%s", err, src)
	}
	for _, w := range []string{`in["enabled"]; ok && v != nil`, `coerceBool("enabled", v)`, `out.Enabled = &b`, `out["enabled"] = *in.Enabled`} {
		if !strings.Contains(string(src), w) {
			t.Errorf("output missing %q\n%s", w, src)
		}
	}
}

func TestGenerate_NoSpecsErrors(t *testing.T) {
	if _, err := Generate("translate"); err == nil {
		t.Fatal("expected error for empty specs")
	}
}

func TestGenerate_LocalFieldExcludedFromForward(t *testing.T) {
	spec := Spec{
		Kind: "Sample",
		Request: StructInfo{
			Name: "CreateSampleRequest",
			Fields: []Field{
				{GoName: "Name", JSONName: "name", Kind: KindString},
				{GoName: "ExamplesFile", JSONName: "examples_file", Kind: KindString, OmitEmpty: true, Local: true},
			},
		},
		Response: StructInfo{
			Name:   "SampleResponse",
			Fields: []Field{{GoName: "Name", JSONName: "name", Kind: KindString}},
		},
		DTOImportPath:  "example.com/fixture",
		DTOImportAlias: "fixture",
	}

	src, err := Generate("translate", spec)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	source := string(src)
	if _, err := parser.ParseFile(token.NewFileSet(), "out.go", src, 0); err != nil {
		t.Fatalf("emitted source did not parse: %v\n--- source ---\n%s", err, src)
	}
	if !strings.Contains(source, `in["name"]`) {
		t.Errorf("forward should still emit the wire field name\n--- source ---\n%s", source)
	}
	if strings.Contains(source, "examples_file") || strings.Contains(source, "ExamplesFile") {
		t.Errorf("local field must not appear in generated translator\n--- source ---\n%s", source)
	}
}

func TestGenerate_LocalFieldExcludedFromReverse(t *testing.T) {
	spec := Spec{
		Kind: "Sample",
		Request: StructInfo{
			Name: "CreateSampleRequest",
			Fields: []Field{
				{GoName: "Name", JSONName: "name", Kind: KindString},
				{GoName: "ExamplesFile", JSONName: "examples_file", Kind: KindString, OmitEmpty: true, Local: true},
			},
		},
		Response: StructInfo{
			Name: "SampleResponse",
			Fields: []Field{
				{GoName: "Name", JSONName: "name", Kind: KindString},
				{GoName: "ExamplesFile", JSONName: "examples_file", Kind: KindString},
			},
		},
		DTOImportPath:  "example.com/fixture",
		DTOImportAlias: "fixture",
	}

	src, err := Generate("translate", spec)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	source := string(src)
	if _, err := parser.ParseFile(token.NewFileSet(), "out.go", src, 0); err != nil {
		t.Fatalf("emitted source did not parse: %v\n--- source ---\n%s", err, src)
	}
	if !strings.Contains(source, `out["name"]`) {
		t.Errorf("reverse should still emit the wire field name\n--- source ---\n%s", source)
	}
	if strings.Contains(source, "examples_file") || strings.Contains(source, "ExamplesFile") {
		t.Errorf("local field must not appear in reverse translator even when the response shares its JSON name\n--- source ---\n%s", source)
	}
}

func TestGenerate_KindJSONObject_EmitsCoerceAndReverseHelpers(t *testing.T) {
	spec := Spec{
		Kind: "Sample",
		Request: StructInfo{
			Name: "CreateSampleRequest",
			Fields: []Field{
				{GoName: "Name", JSONName: "name", Kind: KindString},
				{
					GoName:    "AdditionalConfigurations",
					JSONName:  "additionalConfigurations",
					Kind:      KindJSONObject,
					OmitEmpty: true,
				},
			},
		},
		Response: StructInfo{
			Name: "SampleResponse",
			Fields: []Field{
				{GoName: "Name", JSONName: "name", Kind: KindString},
				{
					GoName:    "AdditionalConfigurations",
					JSONName:  "additionalConfigurations",
					Kind:      KindJSONObject,
					OmitEmpty: true,
				},
			},
		},
		DTOImportPath:  "example.com/fixture",
		DTOImportAlias: "fixture",
	}

	src, err := Generate("translate", spec)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "out.go", src, 0); err != nil {
		t.Fatalf("emitted source did not parse: %v\n--- source ---\n%s", err, src)
	}

	source := string(src)
	wantContains := []string{
		`coerceJSONObject("additionalConfigurations", v)`,
		`reverseJSONObject(in.AdditionalConfigurations`,
		`isZeroAny(anyAdditionalConfigurations)`,
		`out.AdditionalConfigurations = raw`,
	}
	for _, w := range wantContains {
		if !strings.Contains(source, w) {
			t.Errorf("output missing %q\n--- source ---\n%s", w, source)
		}
	}
}

func TestGenerate_StringDefaultOmittedFromReverse(t *testing.T) {
	spec := Spec{
		Kind: "Sample",
		Request: StructInfo{
			Name: "CreateSampleRequest",
			Fields: []Field{
				{GoName: "Name", JSONName: "name", Kind: KindString},
				{GoName: "Mode", JSONName: "mode", Kind: KindString, OmitEmpty: true, Default: "chat"},
			},
		},
		Response: StructInfo{
			Name: "SampleResponse",
			Fields: []Field{
				{GoName: "Name", JSONName: "name", Kind: KindString},
				{GoName: "Mode", JSONName: "mode", Kind: KindString, Default: "chat"},
			},
		},
		DTOImportPath:  "example.com/fixture",
		DTOImportAlias: "fixture",
	}

	src, err := Generate("translate", spec)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "out.go", src, 0); err != nil {
		t.Fatalf("emitted source did not parse: %v\n--- source ---\n%s", err, src)
	}
	source := string(src)
	for _, w := range []string{
		`if in.Mode != "" && in.Mode != "chat" {`,
		`out["name"] = in.Name`,
	} {
		if !strings.Contains(source, w) {
			t.Errorf("output missing %q\n--- source ---\n%s", w, source)
		}
	}
}
