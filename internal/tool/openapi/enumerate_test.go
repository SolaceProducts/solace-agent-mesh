// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"slices"
	"strings"
	"testing"
)

const enumerateSpec = `openapi: 3.0.0
info:
  title: Petstore
  version: 1.0.0
servers:
  - url: https://api.example.com
paths:
  /pets:
    get:
      operationId: listPets
      responses:
        "200":
          description: ok
    post:
      responses:
        "201":
          description: created
  /pets/{petId}:
    get:
      operationId: getPetById
      parameters:
        - name: petId
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: ok
`

func TestEnumerateToolNames(t *testing.T) {
	t.Parallel()

	names, err := EnumerateToolNames(t.Context(), []byte(enumerateSpec), nil, nil)
	if err != nil {
		t.Fatalf("EnumerateToolNames: %v", err)
	}
	want := []string{"get_pet_by_id", "list_pets", "post_pets"}
	if !slices.Equal(names, want) {
		t.Errorf("names = %v, want %v (sorted runtime snake_case names)", names, want)
	}

	filtered, err := EnumerateToolNames(t.Context(), []byte(enumerateSpec), []string{"listPets"}, nil)
	if err != nil {
		t.Fatalf("EnumerateToolNames with allow list: %v", err)
	}
	if !slices.Equal(filtered, []string{"list_pets"}) {
		t.Errorf("allow-filtered names = %v, want [list_pets] (camelCase entry matches snake_cased name)", filtered)
	}
}

func TestEnumerateToolNames_ExternalRefRejected(t *testing.T) {
	t.Parallel()

	spec := `openapi: 3.0.0
info:
  title: Refs
  version: 1.0.0
paths:
  /pets:
    $ref: "http://192.0.2.1/path-item.yaml#/pets"
`
	_, err := EnumerateToolNames(t.Context(), []byte(spec), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "parse spec") {
		t.Fatalf("external $ref must fail without platform egress, got %v", err)
	}
}

func TestClashingOperationIDs_NamesOperationsThatShareAToolName(t *testing.T) {
	t.Parallel()

	spec := `openapi: 3.0.0
info: {title: Clash, version: "1"}
paths:
  /a: {get: {operationId: getThing, responses: {"200": {description: ok}}}}
  /b: {get: {operationId: get_thing, responses: {"200": {description: ok}}}}
  /c: {get: {operationId: other, responses: {"200": {description: ok}}}}
`
	cases := []struct {
		name  string
		allow []string
		want  []string
	}{
		{"no filter", nil, []string{"getThing", "get_thing"}},
		{"naming one of the pair selects both", []string{"getThing"}, []string{"getThing", "get_thing"}},
		{"a filter that avoids the pair", []string{"other"}, nil},
	}
	for _, tc := range cases {
		got, err := ClashingOperationIDs(t.Context(), []byte(spec), tc.allow, nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
