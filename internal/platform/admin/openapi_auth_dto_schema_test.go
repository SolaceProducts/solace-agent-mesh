// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

const parseOpenAPISpecFallbackKey = "raw_response"

func wireFieldNames(t *testing.T, typ reflect.Type) []string {
	t.Helper()
	var names []string
	for i := range typ.NumField() {
		field := typ.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" && field.Anonymous && field.Type.Kind() == reflect.Struct {
			names = append(names, wireFieldNames(t, field.Type)...)
			continue
		}
		if name == "" || name == "-" {
			t.Fatalf("field %s carries no usable json tag (%q); parse_openapi_spec keys off these",
				field.Name, field.Tag.Get("json"))
		}
		names = append(names, name)
	}
	return names
}

func TestOpenAPIParseResponse_MatchesToolResultSchema(t *testing.T) {
	wire := wireFieldNames(t, reflect.TypeOf(OpenAPIParseResponse{}))
	slices.Sort(wire)

	schema := tool.ResultSchemaOf(&tools.ParseOpenAPISpecTool{})
	if schema == nil {
		t.Fatal("parse_openapi_spec declares no result schema")
	}
	declared := make([]string, 0, len(schema.Properties))
	for field := range schema.Properties {
		if field == parseOpenAPISpecFallbackKey {
			continue
		}
		declared = append(declared, field)
	}
	slices.Sort(declared)

	if !slices.Equal(wire, declared) {
		t.Errorf("OpenAPIParseResponse json keys = %v\nParseOpenAPISpecTool.ResultSchema() (minus %q) = %v\n"+
			"update ParseOpenAPISpecTool.ResultSchema in pkg/tools/builder_connector_discovery.go to match, in this change",
			wire, parseOpenAPISpecFallbackKey, declared)
	}

	if _, ok := schema.Properties[parseOpenAPISpecFallbackKey]; !ok {
		t.Errorf("parse_openapi_spec no longer declares %q; this test subtracts it and would now "+
			"be comparing the wrong set", parseOpenAPISpecFallbackKey)
	}
}
