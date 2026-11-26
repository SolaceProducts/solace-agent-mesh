// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"strings"
	"testing"
)

const petPathItemSpec = `openapi: 3.0.0
info: {title: Pets, version: 1.0.0}
servers: [{url: "https://api.example.com"}]
components:
  parameters:
    Include: {name: include, in: query, schema: {type: string}}
paths:
  /pets/{petId}:
    parameters:
      - {name: petId, in: path, required: true, schema: {type: string}}
      - $ref: "#/components/parameters/Include"
    get:
      operationId: getPet
      responses:
        "200": {description: ok}
  /pets/{petId}/events:
    parameters:
      - {name: petId, in: path, required: true, schema: {type: string}}
      - {name: limit, in: query, schema: {type: integer}}
    get:
      operationId: listPetEvents
      parameters:
        - {name: limit, in: query, required: true, schema: {type: integer}}
      responses:
        "200": {description: ok}
`

func (f *inputCheckFixture) petConnector(t *testing.T) {
	t.Helper()
	conn := createHydrationConnector(t, f.repo, "pet-api", "api", "openapi", apiConnectorValues(""))
	storeStoredSpec(t, f.repo, f.specs, conn, petPathItemSpec)
}

func (f *inputCheckFixture) createPetFlow(t *testing.T, tool string, input map[string]any) error {
	t.Helper()
	_, err := f.svc.Create(t.Context(), CreateWorkflowRequest{
		Name:        "pet-flow",
		Description: "binds a pet connector",
		AppConfig: map[string]any{
			"output_mapping": map[string]any{"result": "{{pet.output}}"},
			"nodes":          []any{connectorNode("pet", "pet-api", tool, input)},
		},
	}, "u")
	return err
}

func TestWorkflowSave_GradesAPathItemParameterLikeAnyOther(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		tool  string
		input map[string]any
		wants []string
	}{
		{
			name:  "the path-item path parameter named",
			tool:  "get_pet",
			input: map[string]any{"pet_id": "p1"},
		},
		{
			name:  "the path-item path parameter and a $ref'd optional one named",
			tool:  "get_pet",
			input: map[string]any{"pet_id": "p1", "include": "owner"},
		},
		{
			name:  "the path-item parameter as a template",
			tool:  "get_pet",
			input: map[string]any{"pet_id": "{{workflow.input.id}}"},
		},
		{
			name:  "the override's required parameter and the path-item one named",
			tool:  "list_pet_events",
			input: map[string]any{"pet_id": "p1", "limit": 5},
		},
		{
			name:  "the path-item path parameter left out",
			tool:  "get_pet",
			input: map[string]any{"include": "owner"},
			wants: []string{`node "pet"`, `operation "get_pet" of connector "pet-api"`, "pet_id (required)", "include (optional)",
				"mandatory input parameters are not present"},
		},
		{
			name:  "an empty input for an operation whose only required parameter is on the path item",
			tool:  "get_pet",
			input: map[string]any{},
			wants: []string{"required input parameter(s) pet_id"},
		},
		{
			name:  "the key spelled as the spec's camelCase name",
			tool:  "get_pet",
			input: map[string]any{"petId": "p1"},
			wants: []string{"petId", "pet_id (required)"},
		},
		{
			name:  "the limit left out where the operation makes an optional path-item parameter required",
			tool:  "list_pet_events",
			input: map[string]any{"pet_id": "p1"},
			wants: []string{"required input parameter(s) limit", "limit (required)"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newInputCheckFixture(t)
			f.petConnector(t)

			err := f.createPetFlow(t, tc.tool, tc.input)
			if len(tc.wants) == 0 {
				if err != nil {
					t.Fatalf("save refused a node that names every required parameter: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("save accepted a node that leaves a required path-item parameter out")
			}
			if _, ok := errors.AsType[*ValidationError](err); !ok {
				t.Fatalf("err is %T, want *ValidationError", err)
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q is missing %q", err, want)
				}
			}
		})
	}
}

func TestControlAction_ReportsAnUndeclaredKeyOnAPathItemOperation(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	f.petConnector(t)

	report := f.report(t, connectorNode("pet", "pet-api", "get_pet", map[string]any{"pet_id": "p1", "stray": 1}))

	if len(report.Unchecked) != 0 {
		t.Fatalf("unchecked = %+v, want the operation graded", report.Unchecked)
	}
	if len(report.Problems) != 1 || report.Problems[0].Fatal || !strings.Contains(report.Problems[0].Message, "stray") {
		t.Fatalf("problems = %+v, want one non-fatal finding naming stray", report.Problems)
	}
}
