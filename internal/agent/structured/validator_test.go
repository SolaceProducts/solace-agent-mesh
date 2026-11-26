// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package structured

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidate_ValidData(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
			"age":  map[string]any{"type": "integer"},
		},
		"required": []any{"name"},
	}

	data := map[string]any{"name": "Alice", "age": 30}
	errs := Validate(data, schema)
	if errs != nil {
		t.Errorf("expected valid, got errors: %v", errs)
	}
}

func TestValidate_MissingRequired(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
		"required": []any{"name"},
	}

	data := map[string]any{"age": 30}
	errs := Validate(data, schema)
	if errs == nil {
		t.Fatal("expected validation errors, got nil")
	}
	if len(errs) == 0 {
		t.Fatal("expected at least one error")
	}
}

func TestValidate_WrongType(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{"type": "integer"},
		},
	}

	data := map[string]any{"count": "not-a-number"}
	errs := Validate(data, schema)
	if errs == nil {
		t.Fatal("expected validation errors for wrong type")
	}
}

func TestValidate_NestedObject(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"address": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
				},
				"required": []any{"city"},
			},
		},
		"required": []any{"address"},
	}

	data := map[string]any{"address": map[string]any{"city": "NYC"}}
	if errs := Validate(data, schema); errs != nil {
		t.Errorf("expected valid, got: %v", errs)
	}

	data = map[string]any{"address": map[string]any{}}
	if errs := Validate(data, schema); errs == nil {
		t.Error("expected error for missing nested required field")
	}
}

func TestValidate_ArrayItems(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tags": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
	}

	data := map[string]any{"tags": []any{"a", "b"}}
	if errs := Validate(data, schema); errs != nil {
		t.Errorf("expected valid, got: %v", errs)
	}

	data = map[string]any{"tags": []any{1, 2}}
	if errs := Validate(data, schema); errs == nil {
		t.Error("expected error for integer items in string array")
	}
}

func TestValidate_EmptySchema(t *testing.T) {
	schema := map[string]any{}
	data := map[string]any{"anything": "goes"}
	if errs := Validate(data, schema); errs != nil {
		t.Errorf("empty schema should allow anything, got: %v", errs)
	}
}

func TestValidate_AdditionalFields(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
		"required": []any{"name"},
	}

	data := map[string]any{"name": "Alice", "extra_field": "allowed"}
	if errs := Validate(data, schema); errs != nil {
		t.Errorf("additional fields should be allowed, got: %v", errs)
	}
}

func TestCompileSchema_Valid(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	}
	if err := CompileSchema(schema); err != nil {
		t.Errorf("expected valid schema compilation, got: %v", err)
	}
}

func TestCompileSchema_Invalid(t *testing.T) {
	schema := map[string]any{
		"type": "invalid_type_value",
	}
	if err := CompileSchema(schema); err == nil {
		t.Error("expected error for invalid schema type")
	}
}

func writeValidSchemaFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(path, []byte(`{"type":"string"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func externalRefSchemas(target string) map[string]map[string]any {
	return map[string]map[string]any{
		"$ref":           {"type": "object", "properties": map[string]any{"a": map[string]any{"$ref": target}}},
		"root $schema":   {"$schema": target, "type": "object"},
		"nested $schema": {"type": "object", "properties": map[string]any{"a": map[string]any{"$schema": target}}},
		"$recursiveRef":  {"type": "object", "$recursiveRef": target},
		"$dynamicRef":    {"type": "object", "$dynamicRef": target},
	}
}

func TestCompileSchema_RefusesExternalReferences(t *testing.T) {
	t.Parallel()
	target := (&url.URL{Scheme: "file", Path: writeValidSchemaFile(t)}).String()
	for name, schema := range externalRefSchemas(target) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := CompileSchema(schema)
			if err == nil {
				t.Fatal("expected a reference to an existing local file to fail to compile")
			}
			if !strings.Contains(err.Error(), "schemas must be self-contained") {
				t.Errorf("error should say external references are unsupported: %v", err)
			}
		})
	}
}

func TestCompileSchema_OnlyExternalReferenceErrorsGetTheSelfContainedHint(t *testing.T) {
	t.Parallel()
	err := CompileSchema(map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "strin"}}})
	if err == nil {
		t.Fatal("expected a compile error")
	}
	if strings.Contains(err.Error(), "self-contained") {
		t.Errorf("a plain schema defect must not be blamed on external references: %v", err)
	}
}

func TestCompileSchema_RefusesRelativeReferences(t *testing.T) {
	path := writeValidSchemaFile(t)
	t.Chdir(filepath.Dir(path))
	for name, schema := range externalRefSchemas(filepath.Base(path)) {
		if strings.HasSuffix(name, "$schema") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			if err := CompileSchema(schema); err == nil {
				t.Fatal("expected a relative reference to an existing local file to fail to compile")
			}
		})
	}
}

func TestValidate_ErrorsDoNotLeakTheWorkingDirectory(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cyclic := map[string]any{"type": "object", "$ref": "#"}
	broken := map[string]any{"type": "object", "properties": "nope"}
	for name, schema := range map[string]map[string]any{"cycle": cyclic, "compile error": broken} {
		got := strings.Join(Validate(map[string]any{}, schema), "\n")
		if got == "" {
			t.Fatalf("%s: expected an error", name)
		}
		if strings.Contains(got, cwd) || strings.Contains(got, "file://") {
			t.Errorf("%s: error leaks the working directory: %s", name, got)
		}
	}
}

func TestCompileSchema_StillAcceptsLocalRefsAndStandardMetaschemas(t *testing.T) {
	t.Parallel()
	schema := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"$defs":   map[string]any{"s": map[string]any{"type": "string"}},
		"properties": map[string]any{
			"a": map[string]any{"$ref": "#/$defs/s"},
		},
	}
	if err := CompileSchema(schema); err != nil {
		t.Fatalf("local ref with a standard $schema must still compile: %v", err)
	}
	if errs := Validate(map[string]any{"a": 1}, schema); len(errs) == 0 {
		t.Error("expected a type violation through the local $ref")
	}
}
