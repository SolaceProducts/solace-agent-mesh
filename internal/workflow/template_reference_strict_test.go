// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"fmt"
	"strings"
	"testing"
)

func upstreamOutputs() map[string]map[string]any {
	return map[string]map[string]any{
		"fetch_image": {"output": map[string]any{
			"filename":   "web_content_3d353b8c.bin",
			"size_bytes": 42456,
		}},
		"classify": {"output": map[string]any{"category": "urgent"}},
		"workflow_input": {"output": map[string]any{
			"url": "https://cameras.example.com/camera?id=32",
		}},
	}
}

func TestResolveValue_MixedString_MissingFieldOnExistingNode_Errors(t *testing.T) {
	tests := []struct {
		name       string
		input      any
		wantErr    bool
		wantInErr  []string
		wantResult string
	}{
		{
			name:      "late embed naming a field the upstream tool does not return",
			input:     "«artifact_content:{{fetch_image.output.output_filename}} >>> format:datauri»",
			wantErr:   true,
			wantInErr: []string{"fetch_image", "output_filename", "not found"},
		},
		{
			name:      "prose input naming a field the upstream tool does not return",
			input:     "The image artifact filename is {{fetch_image.output.output_filename}}.",
			wantErr:   true,
			wantInErr: []string{"fetch_image", "output_filename"},
		},
		{
			name:       "unknown node stays literal",
			input:      "value is {{missing.output.field}}",
			wantResult: "value is {{missing.output.field}}",
		},
		{
			name:       "workflow.input is safe-navigated and stays literal",
			input:      "url is {{workflow.input.nonexistent}}",
			wantResult: "url is {{workflow.input.nonexistent}}",
		},
		{
			name:       "resolvable mixed string still interpolates",
			input:      "Category is {{classify.output.category}}",
			wantResult: "Category is urgent",
		},
		{
			name:       "correct field name resolves inside a late embed",
			input:      "«artifact_content:{{fetch_image.output.filename}} >>> format:datauri»",
			wantResult: "«artifact_content:web_content_3d353b8c.bin >>> format:datauri»",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveValue(tt.input, upstreamOutputs())
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ResolveValue(%q) = %v, want an error", tt.input, got)
				}
				for _, want := range tt.wantInErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not mention %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveValue(%q): unexpected error: %v", tt.input, err)
			}
			if got != tt.wantResult {
				t.Errorf("ResolveValue(%q) = %q, want %q", tt.input, got, tt.wantResult)
			}
		})
	}
}

func TestResolveValue_MissingFieldInsideNestedInput_Errors(t *testing.T) {
	input := map[string]any{
		"classes": []any{"car", "person"},
		"image":   "«artifact_content:{{fetch_image.output.output_filename}} >>> format:datauri»",
	}
	if _, err := ResolveValue(input, upstreamOutputs()); err == nil {
		t.Fatal("nested bad reference resolved without error")
	}

	inList := []any{"ok", map[string]any{"f": "x {{fetch_image.output.output_filename}}"}}
	if _, err := ResolveValue(inList, upstreamOutputs()); err == nil {
		t.Fatal("bad reference inside a list resolved without error")
	}
}

func TestMissingFieldError_NamesTheAvailableFields(t *testing.T) {
	const ref = "«artifact_content:{{fetch_image.output.output_artifact_filename}} >>> format:datauri»"

	_, err := resolveValue(ref, upstreamOutputs(), schemaDerived{"fetch_image": true})
	if err == nil {
		t.Fatal("reference to a parameter name rather than a result field resolved without error")
	}
	msg := err.Error()
	for _, want := range []string{"output_artifact_filename", "available fields", "filename", "size_bytes"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}

	_, err = ResolveValue(ref, upstreamOutputs())
	if err == nil {
		t.Fatal("the reference should fail whether or not its fields can be named")
	}
	if strings.Contains(err.Error(), "size_bytes") {
		t.Errorf("keys were named for a node with no declared schema: %v", err)
	}
}

func TestAvailableFieldsHint(t *testing.T) {
	if got := availableFieldsHint(map[string]any{}); !strings.Contains(got, "no fields") {
		t.Errorf("empty map hint = %q", got)
	}

	got := availableFieldsHint(map[string]any{"zeta": 1, "alpha": 2, "mid": 3})
	if got != "available fields: alpha, mid, zeta" {
		t.Errorf("hint = %q, want sorted", got)
	}

	big := make(map[string]any, 40)
	for i := range 40 {
		big[fmt.Sprintf("k%02d", i)] = i
	}
	capped := availableFieldsHint(big)
	if !strings.Contains(capped, "+20 more") {
		t.Errorf("hint for a 40-key map should be capped, got %q", capped)
	}
	if strings.Count(capped, ",") != 19 {
		t.Errorf("expected 20 listed keys, got %q", capped)
	}
}

func TestResolveStringWithTemplates_StaysLenient(t *testing.T) {
	const s = "Describe {{fetch_image.output.output_filename}} please"
	if got := ResolveStringWithTemplates(s, upstreamOutputs()); got != s {
		t.Errorf("lenient resolve = %q, want the input left verbatim", got)
	}
	if got := ResolveStringWithTemplates("Category is {{classify.output.category}}", upstreamOutputs()); got != "Category is urgent" {
		t.Errorf("lenient resolve dropped a valid interpolation: %q", got)
	}
}

func TestFieldsHint_EnumeratesSchemaFieldsButNotReturnedData(t *testing.T) {
	payload := map[string]any{
		"alice@example.com": 1,
		"bob@example.com":   2,
		"carol@secret.org":  3,
	}

	for _, depth := range []int{0, 1} {
		if got := fieldsHint(payload, depth, 1); !strings.Contains(got, "alice@example.com") {
			t.Errorf("depth %d is schema-derived and should name its fields, got %q", depth, got)
		}
	}

	returned := fieldsHint(payload, 1, 0)
	for _, leaked := range []string{"alice@example.com", "bob@example.com", "carol@secret.org"} {
		if strings.Contains(returned, leaked) {
			t.Errorf("a node with no declared schema leaked %q into %q", leaked, returned)
		}
	}
	if !strings.Contains(returned, "3 fields") {
		t.Errorf("an unnamed level should still report the count, got %q", returned)
	}

	deep := fieldsHint(payload, 2, 1)
	for _, leaked := range []string{"alice@example.com", "bob@example.com", "carol@secret.org"} {
		if strings.Contains(deep, leaked) {
			t.Errorf("depth 2 leaked a returned-data key %q into %q", leaked, deep)
		}
	}
	if !strings.Contains(deep, "3 fields") {
		t.Errorf("depth 2 should still report the count, got %q", deep)
	}
}

func TestNavigatePath_DeepMissForbidsKeyEnumeration(t *testing.T) {
	nodeWrapper := map[string]any{
		"output": map[string]any{
			"records": map[string]any{
				"dave@example.com": "x",
				"erin@secret.org":  "y",
			},
		},
	}

	_, err := navigatePath(nodeWrapper, []string{"output", "no_such_field"}, false, 1)
	if err == nil {
		t.Fatal("expected an error for a missing declared field")
	}
	if !strings.Contains(err.Error(), "records") {
		t.Errorf("a missing declared field should name the real ones, got %v", err)
	}

	_, err = navigatePath(nodeWrapper, []string{"output", "no_such_field"}, false, 0)
	if err == nil {
		t.Fatal("expected an error for a missing field")
	}
	if strings.Contains(err.Error(), "records") {
		t.Errorf("an undeclared node's output keys were enumerated: %v", err)
	}

	_, err = navigatePath(nodeWrapper, []string{"output", "records", "missing"}, false, 1)
	if err == nil {
		t.Fatal("expected an error for a missing nested field")
	}
	for _, leaked := range []string{"dave@example.com", "erin@secret.org"} {
		if strings.Contains(err.Error(), leaked) {
			t.Errorf("error leaked a returned-data key %q: %v", leaked, err)
		}
	}
}
