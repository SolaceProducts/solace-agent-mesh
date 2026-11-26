// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"log/slog"
	"slices"
	"strings"
	"testing"
)

const operationsSpec = `openapi: 3.0.0
info:
  title: Shipping
  version: 1.0.0
servers:
  - url: https://api.example.com
paths:
  /shipments/{tracking}:
    get:
      operationId: getShipment
      parameters:
        - name: tracking
          in: path
          required: true
          schema:
            type: string
        - name: carrierCode
          in: query
          schema:
            type: string
      responses:
        "200":
          description: ok
  /shipments:
    post:
      operationId: createShipment
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [destination]
              properties:
                destination:
                  type: string
                note:
                  type: string
      responses:
        "201":
          description: created
  /ping:
    get:
      operationId: ping
      responses:
        "200":
          description: ok
`

func paramsByName(t *testing.T, sigs []OperationSignature, tool string) map[string]bool {
	t.Helper()
	for _, sig := range sigs {
		if sig.ToolName != tool {
			continue
		}
		out := map[string]bool{}
		for _, p := range sig.Params {
			out[p.Name] = p.Required
		}
		return out
	}
	t.Fatalf("no signature for tool %q in %v", tool, sigs)
	return nil
}

func TestEnumerateOperations_DeclaresWhatTheRuntimeToolDeclares(t *testing.T) {
	t.Parallel()

	sigs, err := EnumerateOperations(t.Context(), []byte(operationsSpec), nil, nil)
	if err != nil {
		t.Fatalf("EnumerateOperations: %v", err)
	}

	cases := []struct {
		tool string
		want map[string]bool
	}{
		{"get_shipment", map[string]bool{"tracking": true, "carrier_code": false}},
		{"create_shipment", map[string]bool{"destination": true, "note": false}},
		{"ping", map[string]bool{}},
	}
	for _, tc := range cases {
		got := paramsByName(t, sigs, tc.tool)
		if len(got) != len(tc.want) {
			t.Errorf("%s params = %v, want %v", tc.tool, got, tc.want)
			continue
		}
		for name, required := range tc.want {
			if gotRequired, ok := got[name]; !ok || gotRequired != required {
				t.Errorf("%s param %q required = %v (present %v), want %v", tc.tool, name, gotRequired, ok, required)
			}
		}
	}
}

func TestEnumerateOperations_AgreesWithLoadAndDiscoverTools(t *testing.T) {
	t.Parallel()

	sigs, err := EnumerateOperations(t.Context(), []byte(operationsSpec), nil, nil)
	if err != nil {
		t.Fatalf("EnumerateOperations: %v", err)
	}
	tools, err := NewManager(slog.New(slog.DiscardHandler), nil).LoadAndDiscoverTools(&ToolsetConfig{SpecInline: operationsSpec})
	if err != nil {
		t.Fatalf("LoadAndDiscoverTools: %v", err)
	}
	if len(tools) != len(sigs) {
		t.Fatalf("runtime registered %d tools, enumerated %d signatures", len(tools), len(sigs))
	}
	for _, tl := range tools {
		schema := tl.Parameters()
		got := paramsByName(t, sigs, tl.Name())
		if len(got) != len(schema.Properties) {
			t.Errorf("%s: enumerated params %v, runtime properties %v", tl.Name(), got, schema.Properties)
		}
		for name := range schema.Properties {
			required := slices.Contains(schema.Required, name)
			if gotRequired, ok := got[name]; !ok || gotRequired != required {
				t.Errorf("%s: param %q required = %v (present %v), runtime says %v", tl.Name(), name, gotRequired, ok, required)
			}
		}
	}
}

func TestEnumerateOperations_AllowListFilters(t *testing.T) {
	t.Parallel()

	sigs, err := EnumerateOperations(t.Context(), []byte(operationsSpec), []string{"getShipment"}, nil)
	if err != nil {
		t.Fatalf("EnumerateOperations: %v", err)
	}
	if len(sigs) != 1 || sigs[0].ToolName != "get_shipment" {
		t.Fatalf("signatures = %v, want only get_shipment", sigs)
	}
}

func TestEnumerateOperations_OmitsAmbiguousToolNames(t *testing.T) {
	t.Parallel()

	spec := `openapi: 3.0.0
info:
  title: Clash
  version: 1.0.0
servers:
  - url: https://api.example.com
paths:
  /a:
    get:
      operationId: getThing
      parameters:
        - name: a
          in: query
          required: true
          schema:
            type: string
      responses:
        "200":
          description: ok
  /b:
    get:
      operationId: get_thing
      parameters:
        - name: b
          in: query
          schema:
            type: string
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
	sigs, err := EnumerateOperations(t.Context(), []byte(spec), nil, nil)
	if err != nil {
		t.Fatalf("EnumerateOperations: %v", err)
	}
	if len(sigs) != 1 || sigs[0].ToolName != "other" {
		t.Fatalf("signatures = %v, want only the unambiguous tool", sigs)
	}
}

func TestEnumerateOperations_ExternalRefRejected(t *testing.T) {
	t.Parallel()

	spec := `openapi: 3.0.0
info:
  title: Refs
  version: 1.0.0
paths:
  /pets:
    $ref: "http://192.0.2.1/path-item.yaml#/pets"
`
	if _, err := EnumerateOperations(t.Context(), []byte(spec), nil, nil); err == nil || !strings.Contains(err.Error(), "parse spec") {
		t.Fatalf("external $ref must fail without platform egress, got %v", err)
	}
}

func TestEnumerateOperations_RequestBodyOfSeveralMediaTypes(t *testing.T) {
	t.Parallel()

	spec := `openapi: 3.0.0
info:
  title: Bodies
  version: 1.0.0
servers:
  - url: https://api.example.com
paths:
  /agree:
    post:
      operationId: agree
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [name]
              properties:
                name: {type: string}
          application/x-www-form-urlencoded:
            schema:
              type: object
              required: [name]
              properties:
                name: {type: string}
      responses:
        "200":
          description: ok
  /disagree:
    post:
      operationId: disagree
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                name: {type: string}
          text/plain:
            schema:
              type: string
      responses:
        "200":
          description: ok
`
	for range 20 {
		sigs, err := EnumerateOperations(t.Context(), []byte(spec), nil, nil)
		if err != nil {
			t.Fatalf("EnumerateOperations: %v", err)
		}
		if len(sigs) != 1 || sigs[0].ToolName != "agree" {
			t.Fatalf("signatures = %v, want only the operation whose media types agree", sigs)
		}
		if got := paramsByName(t, sigs, "agree"); len(got) != 1 || !got["name"] {
			t.Fatalf("agree params = %v, want name (required)", got)
		}
	}
}
