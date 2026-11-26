// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"maps"
	"path/filepath"
	"strings"
	"testing"
)

func validateEntry(extra map[string]any) map[string]any {
	entry := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "sse",
			"url":  "https://mcp.example.com/v1/sse",
		},
	}
	maps.Copy(entry, extra)
	return entry
}

func TestValidateToolsetConfig(t *testing.T) {
	inlineManifest := []any{map[string]any{"name": "x", "inputSchema": map[string]any{"type": "object"}}}
	absent := filepath.Join(t.TempDir(), "only-in-the-runtime-pod.yaml")

	tests := []struct {
		name    string
		entry   map[string]any
		wantErr string
	}{
		{"manifest_file absent on this host is accepted", validateEntry(map[string]any{"manifest_file": absent}), ""},
		{"manifest_file naming a directory is accepted", validateEntry(map[string]any{"manifest_file": t.TempDir()}), ""},
		{"inline manifest is accepted", validateEntry(map[string]any{"manifest": inlineManifest}), ""},
		{"inline manifest entry without a name is rejected", validateEntry(map[string]any{"manifest": []any{map[string]any{"description": "x"}}}), "missing required field 'name'"},
		{"manifest with manifest_file stays mutually exclusive", validateEntry(map[string]any{"manifest": inlineManifest, "manifest_file": absent}), "mutually exclusive"},
		{"missing connection_params is rejected", map[string]any{"tool_type": "mcp", "manifest_file": absent}, "connection_params"},
		{"conflicting tool filters are rejected", validateEntry(map[string]any{"manifest_file": absent, "tool_name": "a", "allow_list": []any{"b"}}), "mutually exclusive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateToolsetConfig(tt.entry)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseToolsetConfig_StillRejectsAbsentManifestFile(t *testing.T) {
	entry := validateEntry(map[string]any{"manifest_file": filepath.Join(t.TempDir(), "absent.yaml")})
	if _, err := ParseToolsetConfig(entry); err == nil || !strings.Contains(err.Error(), "read manifest file") {
		t.Fatalf("ParseToolsetConfig error = %v, want a read manifest file error", err)
	}
}
