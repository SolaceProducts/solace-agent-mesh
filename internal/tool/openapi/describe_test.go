// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"encoding/json"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const clashingOperationsSpec = `openapi: 3.0.0
info:
  title: Clash
  version: 1.0.0
servers:
  - url: https://api.example.com
paths:
  /a:
    get:
      operationId: getThing
      responses:
        "200":
          description: ok
  /b:
    get:
      operationId: get_thing
      responses:
        "200":
          description: ok
  /c:
    get:
      operationId: other
      responses:
        "200":
          description: ok
`

type parameterSchema struct {
	Type       string                    `json:"type"`
	Properties map[string]map[string]any `json:"properties"`
	Required   []string                  `json:"required"`
}

func describedSpec() string {
	return strings.Replace(operationsSpec,
		"      operationId: getShipment\n",
		"      operationId: getShipment\n      summary: Look up a shipment\n", 1)
}

func descriptionOf(t *testing.T, descriptions []OperationDescription, toolName string) OperationDescription {
	t.Helper()
	for _, d := range descriptions {
		if d.ToolName == toolName {
			return d
		}
	}
	t.Fatalf("no description for %q in %v", toolName, descriptions)
	return OperationDescription{}
}

func schemaOf(t *testing.T, d OperationDescription) parameterSchema {
	t.Helper()
	var schema parameterSchema
	if err := json.Unmarshal(d.Parameters, &schema); err != nil {
		t.Fatalf("%s parameters %s are not a JSON schema: %v", d.ToolName, d.Parameters, err)
	}
	return schema
}

func toolNames(descriptions []OperationDescription) []string {
	names := make([]string, len(descriptions))
	for i, d := range descriptions {
		names[i] = d.ToolName
	}
	return names
}

func TestDescribeOperations_NamesMethodsPathsSummariesAndParameters(t *testing.T) {
	t.Parallel()

	descriptions, err := DescribeOperations(t.Context(), []byte(describedSpec()), nil, nil)
	if err != nil {
		t.Fatalf("DescribeOperations: %v", err)
	}
	if got, want := toolNames(descriptions), []string{"create_shipment", "get_shipment", "ping"}; !slices.Equal(got, want) {
		t.Fatalf("tool names = %v, want %v, sorted", got, want)
	}

	get := descriptionOf(t, descriptions, "get_shipment")
	if get.Method != "GET" || get.Path != "/shipments/{tracking}" || get.Summary != "Look up a shipment" {
		t.Errorf("get_shipment = %s %s %q, want GET /shipments/{tracking} with its summary", get.Method, get.Path, get.Summary)
	}
	getSchema := schemaOf(t, get)
	if getSchema.Type != "object" || getSchema.Properties["tracking"]["type"] != "string" || getSchema.Properties["carrier_code"]["type"] != "string" {
		t.Errorf("get_shipment parameters = %s, want string tracking and carrier_code", get.Parameters)
	}
	if !slices.Equal(getSchema.Required, []string{"tracking"}) {
		t.Errorf("get_shipment required = %v, want [tracking]", getSchema.Required)
	}

	create := descriptionOf(t, descriptions, "create_shipment")
	if create.Method != "POST" || create.Path != "/shipments" {
		t.Errorf("create_shipment = %s %s, want POST /shipments", create.Method, create.Path)
	}
	createSchema := schemaOf(t, create)
	if len(createSchema.Properties) != 2 || !slices.Equal(createSchema.Required, []string{"destination"}) {
		t.Errorf("create_shipment parameters = %s, want body fields destination (required) and note", create.Parameters)
	}

	ping := descriptionOf(t, descriptions, "ping")
	if ping.Summary != "GET /ping" || len(schemaOf(t, ping).Properties) != 0 {
		t.Errorf("ping = %q %s, want the runtime's fallback description and no parameters", ping.Summary, ping.Parameters)
	}
}

func TestDescribeOperations_SummaryFallsBackLikeTheRuntimeToolDescription(t *testing.T) {
	t.Parallel()

	spec := strings.Replace(describedSpec(),
		"      operationId: createShipment\n",
		"      operationId: createShipment\n      description: Creates a shipment record\n", 1)
	descriptions, err := DescribeOperations(t.Context(), []byte(spec), nil, nil)
	if err != nil {
		t.Fatalf("DescribeOperations: %v", err)
	}
	for toolName, want := range map[string]string{
		"get_shipment":    "Look up a shipment",
		"create_shipment": "Creates a shipment record",
		"ping":            "GET /ping",
	} {
		if got := descriptionOf(t, descriptions, toolName).Summary; got != want {
			t.Errorf("%s summary = %q, want %q", toolName, got, want)
		}
	}
}

func TestDescribeOperations_AllowAndDenyListsFilter(t *testing.T) {
	t.Parallel()

	allowed, err := DescribeOperations(t.Context(), []byte(operationsSpec), []string{"getShipment"}, nil)
	if err != nil {
		t.Fatalf("DescribeOperations allow: %v", err)
	}
	if got := toolNames(allowed); !slices.Equal(got, []string{"get_shipment"}) {
		t.Errorf("allow_list names = %v, want only get_shipment", got)
	}

	denied, err := DescribeOperations(t.Context(), []byte(operationsSpec), nil, []string{"ping", "getShipment"})
	if err != nil {
		t.Fatalf("DescribeOperations deny: %v", err)
	}
	if got := toolNames(denied); !slices.Equal(got, []string{"create_shipment"}) {
		t.Errorf("deny_list names = %v, want only create_shipment", got)
	}
}

func TestDescribeOperations_AgreesWithEnumerateOperations(t *testing.T) {
	t.Parallel()

	for name, spec := range map[string]string{"shipping": operationsSpec, "clashing names": clashingOperationsSpec} {
		t.Run(name, func(t *testing.T) {
			descriptions, err := DescribeOperations(t.Context(), []byte(spec), nil, nil)
			if err != nil {
				t.Fatalf("DescribeOperations: %v", err)
			}
			sigs, err := EnumerateOperations(t.Context(), []byte(spec), nil, nil)
			if err != nil {
				t.Fatalf("EnumerateOperations: %v", err)
			}
			if len(descriptions) != len(sigs) {
				t.Fatalf("described %v, enumerated %d signatures", toolNames(descriptions), len(sigs))
			}
			for i, sig := range sigs {
				d := descriptions[i]
				if d.ToolName != sig.ToolName {
					t.Fatalf("operation %d: described %q, enumerated %q", i, d.ToolName, sig.ToolName)
				}
				schema := schemaOf(t, d)
				if len(schema.Properties) != len(sig.Params) {
					t.Errorf("%s: properties %v, enumerated params %v", d.ToolName, schema.Properties, sig.Params)
				}
				for _, p := range sig.Params {
					if _, ok := schema.Properties[p.Name]; !ok {
						t.Errorf("%s: enumerated param %q is not a property", d.ToolName, p.Name)
					}
					if slices.Contains(schema.Required, p.Name) != p.Required {
						t.Errorf("%s: param %q required = %t in the schema, %t enumerated", d.ToolName, p.Name, slices.Contains(schema.Required, p.Name), p.Required)
					}
				}
			}
		})
	}
}

func TestDescribeOperations_ParametersAreWhatTheRuntimeToolDeclares(t *testing.T) {
	t.Parallel()

	descriptions, err := DescribeOperations(t.Context(), []byte(operationsSpec), nil, nil)
	if err != nil {
		t.Fatalf("DescribeOperations: %v", err)
	}
	tools, err := NewManager(slog.New(slog.DiscardHandler), nil).LoadAndDiscoverTools(&ToolsetConfig{SpecInline: operationsSpec})
	if err != nil {
		t.Fatalf("LoadAndDiscoverTools: %v", err)
	}
	if len(tools) != len(descriptions) {
		t.Fatalf("runtime registered %d tools, described %d", len(tools), len(descriptions))
	}
	for _, tl := range tools {
		runtimeJSON, err := json.Marshal(tl.Parameters())
		if err != nil {
			t.Fatalf("marshal %s parameters: %v", tl.Name(), err)
		}
		var want, got any
		if err := json.Unmarshal(runtimeJSON, &want); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(descriptionOf(t, descriptions, tl.Name()).Parameters, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: described parameters %v, runtime schema %v", tl.Name(), got, want)
		}
	}
}

func TestDescribeOperations_CarriesTheSpecsOperationIDBesideTheToolName(t *testing.T) {
	t.Parallel()

	descriptions, err := DescribeOperations(t.Context(), []byte(operationsSpec), nil, nil)
	if err != nil {
		t.Fatalf("DescribeOperations: %v", err)
	}

	got := descriptionOf(t, descriptions, "get_shipment")
	if got.OperationID != "getShipment" {
		t.Errorf("operationId = %q, want getShipment: an allow_list entry must match it, not the tool name", got.OperationID)
	}
}
