// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func specValuesFixture(t *testing.T, specContent string) (map[string]any, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "petstore.openapi.yaml"), []byte(specContent), 0o600); err != nil {
		t.Fatalf("write spec fixture: %v", err)
	}
	values := map[string]any{
		"specification_file": "petstore.openapi.yaml",
		"base_url":           "https://api.example.com",
	}
	return values, filepath.Join(dir, "petstore.yaml")
}

func TestResolveConnectorSpecFile_LoadsAndPopsKey(t *testing.T) {
	t.Parallel()
	values, sourceFile := specValuesFixture(t, "openapi: 3.0.0\n")

	sf, err := ResolveConnectorSpecFile(values, sourceFile)
	if err != nil {
		t.Fatalf("ResolveConnectorSpecFile: %v", err)
	}
	if sf == nil || sf.Filename != "petstore.openapi.yaml" || string(sf.Data) != "openapi: 3.0.0\n" {
		t.Errorf("spec file = %+v", sf)
	}
	if _, still := values["specification_file"]; still {
		t.Error("specification_file was not popped from values")
	}
	if values["base_url"] != "https://api.example.com" {
		t.Error("unrelated values key was disturbed")
	}
}

func TestResolveConnectorSpecFile_AbsentKeyIsNil(t *testing.T) {
	t.Parallel()
	sf, err := ResolveConnectorSpecFile(map[string]any{"base_url": "https://x"}, "connectors/c.yaml")
	if err != nil || sf != nil {
		t.Errorf("absent key: sf=%v err=%v, want nil/nil", sf, err)
	}
}

func TestResolveConnectorSpecFile_ErrorPaths(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		values  map[string]any
		wantSub string
	}{
		{
			name:    "missing file on disk",
			values:  map[string]any{"specification_file": "nope.yaml"},
			wantSub: "read specification_file",
		},
		{
			name:    "empty path",
			values:  map[string]any{"specification_file": "  "},
			wantSub: "must not be empty",
		},
		{
			name:    "non-string value",
			values:  map[string]any{"specification_file": 42},
			wantSub: "must be a string",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ResolveConnectorSpecFile(tc.values, filepath.Join(t.TempDir(), "c.yaml"))
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("err = %v, want substring %q", err, tc.wantSub)
			}
		})
	}
}

func TestResolveConnectorSpecFile_EmptyContentRejected(t *testing.T) {
	t.Parallel()
	values, sourceFile := specValuesFixture(t, "")

	_, err := ResolveConnectorSpecFile(values, sourceFile)
	if err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Errorf("err = %v, want empty-content rejection at plan time", err)
	}
}

func TestResolveConnectorSpecFile_SizeCap(t *testing.T) {
	t.Parallel()
	values, sourceFile := specValuesFixture(t, string(make([]byte, maxConnectorSpecFileBytes+1)))

	_, err := ResolveConnectorSpecFile(values, sourceFile)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("err = %v, want size-cap rejection", err)
	}
}
