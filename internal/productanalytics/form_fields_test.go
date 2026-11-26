// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

func TestFormFieldsCoverThePlatformFieldSchemas(t *testing.T) {
	for _, field := range append(connectorSchemaFields(t), entrypointSchemaFields(t)...) {
		path := "values." + field
		if !slices.Contains(productanalytics.FormFields, path) {
			t.Errorf("platform field schema declares %q; add %q to FormFields and to PRODUCT_ANALYTICS_FORM_FIELDS in the frontend formFields.ts", field, path)
		}
	}
}

func TestNestedFormFieldsAreTwoSegmentsUnderAContainer(t *testing.T) {
	for _, field := range productanalytics.FormFields {
		segments := strings.Split(field, ".")
		if len(segments) == 1 {
			continue
		}
		if len(segments) != 2 {
			t.Errorf("form field %q is %d segments deep; trackFormErrors truncates to 2, so this can never be emitted", field, len(segments))
			continue
		}
		if !slices.Contains(productanalytics.FormFields, segments[0]) {
			t.Errorf("form field %q nests under %q, which is not itself a form field; the fallback path would be dropped", field, segments[0])
		}
	}
}

func connectorSchemaFields(t *testing.T) []string {
	t.Helper()
	pattern := filepath.Join(repoRootFromTest(t), "internal", "platform", "admin", "connector_types", "*.yaml")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob connector types: %v", err)
	}
	if len(paths) == 0 {
		t.Fatalf("no connector type definitions found at %s", pattern)
	}
	var fields []string
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var def struct {
			Subtypes []struct {
				FieldSchema []struct {
					Name string `yaml:"name"`
				} `yaml:"field_schema"`
			} `yaml:"subtypes"`
		}
		if err := yaml.Unmarshal(data, &def); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, subtype := range def.Subtypes {
			for _, field := range subtype.FieldSchema {
				fields = append(fields, field.Name)
			}
		}
	}
	if len(fields) == 0 {
		t.Fatal("parsed no connector fields; the YAML shape this test reads has changed")
	}
	return fields
}

func entrypointSchemaFields(t *testing.T) []string {
	t.Helper()
	registry := features.NewRegistry()
	registry.Register(
		features.Definition{Key: "email_gateway", Default: true},
		features.Definition{Key: "whatsapp_gateway", Default: true},
		features.Definition{Key: features.KeyGoogleChatGateway, Default: true},
	)
	var fields []string
	for _, schema := range platform.VisibleGatewaySchemas(registry) {
		shape, ok := schema.Schema.(map[string]any)
		if !ok {
			t.Fatalf("entrypoint schema %q is not a field map; this test reads the wrong shape", schema.Type)
		}
		for name := range shape {
			fields = append(fields, name)
		}
	}
	if len(fields) == 0 {
		t.Fatal("the platform served no entrypoint fields; the gate or the accessor has changed")
	}
	return fields
}

func repoRootFromTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from the test working dir")
		}
		dir = parent
	}
}
