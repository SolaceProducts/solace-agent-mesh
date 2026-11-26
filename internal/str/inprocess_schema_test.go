// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type schemaFixtureParams struct {
	Message string `json:"message" desc:"the static message"`
}

func newSchemaFixture(name string, dynamic sdk.DynamicSchemaFunc) *sdk.ToolDef {
	var opts []sdk.ToolOption
	if dynamic != nil {
		opts = append(opts, sdk.WithDynamicSchema(dynamic))
	}
	return sdk.NewTool(name, "in-process schema fixture tool",
		func(_ context.Context, _ schemaFixtureParams, _ *sdk.ToolContext) (*sdk.Result, error) {
			return sdk.OK("ok"), nil
		}, opts...)
}

func topicDrivenSchema(received *map[string]any) sdk.DynamicSchemaFunc {
	return func(config map[string]any) *sdk.SchemaOverride {
		*received = config
		topic, _ := config["topic"].(string)
		if topic == "" {
			return nil
		}
		return &sdk.SchemaOverride{
			Name: "renamed_" + topic,
			Parameters: &sdk.JSONSchema{
				Type:       "object",
				Properties: map[string]any{topic + "_id": map[string]any{"type": "string"}},
				Required:   []string{topic + "_id"},
			},
		}
	}
}

func parameterShape(t *testing.T, params json.RawMessage) (properties []string, required []string) {
	t.Helper()
	var schema struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(params, &schema); err != nil {
		t.Fatalf("parameters %s are not a JSON schema: %v", params, err)
	}
	for name := range schema.Properties {
		properties = append(properties, name)
	}
	slices.Sort(properties)
	return properties, schema.Required
}

func TestInProcessToolSchema_StaticToolIgnoresConfig(t *testing.T) {
	withGlobalInProcessTool(t, newSchemaFixture("zz_static_schema_tool", nil))

	params, applied, ok := InProcessToolSchema("zz_static_schema_tool", map[string]any{"topic": "orders"})
	if !ok || !applied {
		t.Fatalf("ok = %t, overrideApplied = %t, want both true", ok, applied)
	}
	properties, required := parameterShape(t, params)
	if !slices.Equal(properties, []string{"message"}) || !slices.Equal(required, []string{"message"}) {
		t.Errorf("properties = %v, required = %v, want the static message parameter", properties, required)
	}
}

func TestInProcessToolSchema_AllowlistedBinaryAppliesItsDynamicSchema(t *testing.T) {
	var received map[string]any
	withGlobalInProcessTool(t, newSchemaFixture("event_mesh_request", topicDrivenSchema(&received)))

	params, applied, ok := InProcessToolSchema("event_mesh_request", map[string]any{"topic": "orders"})
	if !ok || !applied {
		t.Fatalf("ok = %t, overrideApplied = %t, want both true", ok, applied)
	}
	properties, required := parameterShape(t, params)
	if !slices.Equal(properties, []string{"orders_id"}) || !slices.Equal(required, []string{"orders_id"}) {
		t.Errorf("properties = %v, required = %v, want the parameters the config derives", properties, required)
	}
	if received["topic"] != "orders" {
		t.Errorf("the dynamic schema received %v, want the caller's config", received)
	}
}

func TestInProcessToolSchema_DeclinedConfigIsNotAnAppliedOverride(t *testing.T) {
	var received map[string]any
	withGlobalInProcessTool(t, newSchemaFixture("event_mesh_request", topicDrivenSchema(&received)))

	for name, config := range map[string]map[string]any{"empty": {}, "nil": nil} {
		t.Run(name, func(t *testing.T) {
			params, applied, ok := InProcessToolSchema("event_mesh_request", config)
			if !ok {
				t.Fatal("ok = false, want true: the tool is registered")
			}
			if applied {
				t.Error("overrideApplied = true for a config the dynamic schema declined")
			}
			if properties, _ := parameterShape(t, params); !slices.Equal(properties, []string{"message"}) {
				t.Errorf("properties = %v, want the static fallback the caller must not present", properties)
			}
		})
	}
}

func TestInProcessToolSchema_NeverRunsTheDynamicSchemaOfOtherBinaries(t *testing.T) {
	withGlobalInProcessTool(t, newSchemaFixture("zz_connects_to_the_database", func(map[string]any) *sdk.SchemaOverride {
		panic("the dynamic schema of a binary off the allowlist must not run")
	}))

	params, applied, ok := InProcessToolSchema("zz_connects_to_the_database", map[string]any{"host": "db.internal"})
	if !ok || !applied {
		t.Fatalf("ok = %t, overrideApplied = %t, want both true", ok, applied)
	}
	if properties, _ := parameterShape(t, params); !slices.Equal(properties, []string{"message"}) {
		t.Errorf("properties = %v, want the static schema", properties)
	}
}

func TestInProcessToolSchema_UnknownBinary(t *testing.T) {
	params, applied, ok := InProcessToolSchema("zz_not_registered", nil)
	if ok || applied || params != nil {
		t.Errorf("got (%s, %t, %t), want (nil, false, false)", params, applied, ok)
	}
}

func TestInProcessToolSchema_DocumentDBForcesAutoDetectOff(t *testing.T) {
	var received map[string]any
	withGlobalInProcessTool(t, newSchemaFixture("document_db_query", func(config map[string]any) *sdk.SchemaOverride {
		received = config
		if config["auto_detect_schema"] != false {
			return nil
		}
		return &sdk.SchemaOverride{Parameters: &sdk.JSONSchema{
			Type:       "object",
			Properties: map[string]any{"collection": map[string]any{"type": "string"}},
		}}
	}))
	caller := map[string]any{"auto_detect_schema": true, "backend": "mongodb"}

	params, applied, ok := InProcessToolSchema("document_db_query", caller)
	if !ok || !applied {
		t.Fatalf("ok = %t, overrideApplied = %t, want both true", ok, applied)
	}
	if received["auto_detect_schema"] != false || received["backend"] != "mongodb" {
		t.Errorf("the dynamic schema received %v, want auto_detect_schema forced to false and the rest kept", received)
	}
	if caller["auto_detect_schema"] != true {
		t.Errorf("the caller's config was changed to %v", caller)
	}
	if properties, _ := parameterShape(t, params); !slices.Equal(properties, []string{"collection"}) {
		t.Errorf("properties = %v, want the backend's parameters", properties)
	}
}

func TestPureDynamicSchemaBinaries_AreExactlyTheTwoWithNoConnectionToTheCustomersSystem(t *testing.T) {
	t.Parallel()

	got := slices.Sorted(maps.Keys(pureDynamicSchemaBinaries))
	if want := []string{"document_db_query", "event_mesh_request"}; !slices.Equal(got, want) {
		t.Errorf("pureDynamicSchemaBinaries = %v, want %v: another binary's dynamic schema may connect to the customer's system, so widening this list is a deliberate change to this test", got, want)
	}
}
