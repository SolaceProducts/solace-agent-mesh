// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"log/slog"
	"strings"
	"testing"
)

func TestLoadSpec_FieldNamesAreCaseSensitive(t *testing.T) {
	tests := []struct {
		name   string
		format string
		spec   string
	}{
		{"json", "json", `{"openapi":"3.0.0","info":{"title":"T","version":"1"},` +
			`"paths":{"/items":{"get":{"OperationId":"listItems","responses":{"200":{"description":"ok"}}}}}}`},
		{"yaml", "yaml", "openapi: 3.0.0\ninfo:\n  title: T\n  version: '1'\npaths:\n  /items:\n    get:\n" +
			"      OperationId: listItems\n      responses:\n        '200':\n          description: ok\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &ToolsetConfig{SpecInline: tt.spec, SpecFormat: tt.format}
			doc, err := NewManager(slog.Default(), nil).loadSpec(t.Context(), cfg)
			if err != nil {
				t.Fatalf("loadSpec: %v", err)
			}
			ops := extractOperations(doc)
			if len(ops) != 1 {
				t.Fatalf("operations = %d, want 1", len(ops))
			}
			if want := generateOperationID("get", "/items"); ops[0].OperationID != want {
				t.Errorf("operation id = %q, want the generated %q because OperationId is not operationId", ops[0].OperationID, want)
			}
		})
	}
}

func TestLoadSpec_DuplicateKeyIsRefused(t *testing.T) {
	tests := []struct {
		name   string
		format string
		spec   string
	}{
		{"json", "json", `{"openapi":"3.0.0","info":{"title":"A","title":"B","version":"1"},"paths":{}}`},
		{"yaml", "yaml", "openapi: 3.0.0\ninfo:\n  title: A\n  title: B\n  version: '1'\npaths: {}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &ToolsetConfig{SpecInline: tt.spec, SpecFormat: tt.format}
			_, err := NewManager(slog.Default(), nil).loadSpec(t.Context(), cfg)
			if err == nil {
				t.Fatal("loadSpec accepted a spec with a duplicate key")
			}
			if !strings.Contains(err.Error(), `"title"`) {
				t.Errorf("error does not name the duplicate key: %v", err)
			}
		})
	}
}
