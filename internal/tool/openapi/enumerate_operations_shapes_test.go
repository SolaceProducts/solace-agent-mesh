// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"log/slog"
	"slices"
	"testing"
)

func TestEnumerateOperations_EmptyRequestBodyMediaTypeDoesNotPanic(t *testing.T) {
	t.Parallel()

	spec := `openapi: 3.0.0
info:
  title: Empty media
  version: 1.0.0
servers:
  - url: https://api.example.com
paths:
  /upload:
    post:
      operationId: upload
      requestBody:
        content:
          application/octet-stream:
      responses:
        "200":
          description: ok
  /mixed:
    post:
      operationId: mixed
      requestBody:
        required: true
        content:
          application/octet-stream:
          application/json:
            schema:
              type: object
              required: [name]
              properties:
                name: {type: string}
      responses:
        "200":
          description: ok
`
	for range 20 {
		sigs, err := EnumerateOperations(t.Context(), []byte(spec), nil, nil)
		if err != nil {
			t.Fatalf("EnumerateOperations: %v", err)
		}
		if got := paramsByName(t, sigs, "upload"); len(got) != 0 {
			t.Fatalf("upload params = %v, want none: an empty media type declares nothing", got)
		}
		if got := paramsByName(t, sigs, "mixed"); len(got) != 1 || !got["name"] {
			t.Fatalf("mixed params = %v, want name (required): the empty media type is skipped, not a rival shape", got)
		}
	}
	if _, err := NewManager(slog.New(slog.DiscardHandler), nil).LoadAndDiscoverTools(&ToolsetConfig{SpecInline: spec}); err != nil {
		t.Fatalf("the runtime loader must survive the same spec: %v", err)
	}
}

func TestEnumerateOperations_MediaTypesThatDifferOnlyInRequiredAreAmbiguous(t *testing.T) {
	t.Parallel()

	spec := `openapi: 3.0.0
info:
  title: Required differs
  version: 1.0.0
servers:
  - url: https://api.example.com
paths:
  /thing:
    post:
      operationId: thing
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
              properties:
                name: {type: string}
      responses:
        "200":
          description: ok
`
	for range 20 {
		sigs, err := EnumerateOperations(t.Context(), []byte(spec), nil, nil)
		if err != nil {
			t.Fatalf("EnumerateOperations: %v", err)
		}
		if len(sigs) != 0 {
			t.Fatalf("signatures = %v, want none: the required flag depends on the media type picked", sigs)
		}
	}
}

const shapesSpec = `openapi: 3.0.0
info:
  title: Shapes
  version: 1.0.0
servers:
  - url: https://api.example.com
components:
  parameters:
    Limit:
      name: limit
      in: query
      required: true
      schema: {type: integer}
  schemas:
    Pet:
      type: object
      required: [name]
      properties:
        name: {type: string}
        tag: {type: string}
paths:
  /headers:
    get:
      operationId: headers
      parameters:
        - $ref: "#/components/parameters/Limit"
        - {name: id, in: query, schema: {type: string}}
        - {name: id, in: header, schema: {type: string}}
        - {name: X-Request-Id, in: header, required: true, schema: {type: string}}
        - {name: session, in: cookie, schema: {type: string}}
      responses:
        "200":
          description: ok
  /array:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: array
              items: {type: string}
      responses:
        "200":
          description: ok
  /primitive:
    put:
      operationId: primitive
      requestBody:
        content:
          text/plain:
            schema: {type: string}
      responses:
        "200":
          description: ok
  /ref-body:
    post:
      operationId: refBody
      requestBody:
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/Pet"
      responses:
        "200":
          description: ok
  /optional-body:
    post:
      operationId: optionalBody
      requestBody:
        required: false
        content:
          application/json:
            schema:
              type: object
              required: [name]
              properties:
                name: {type: string}
      responses:
        "200":
          description: ok
  /no-body-schema:
    post:
      operationId: noBodySchema
      requestBody:
        content:
          application/json: {}
      responses:
        "200":
          description: ok
`

func TestEnumerateOperations_AgreesWithTheRuntimeOnEveryShape(t *testing.T) {
	t.Parallel()

	sigs, err := EnumerateOperations(t.Context(), []byte(shapesSpec), nil, nil)
	if err != nil {
		t.Fatalf("EnumerateOperations: %v", err)
	}
	tools, err := NewManager(slog.New(slog.DiscardHandler), nil).LoadAndDiscoverTools(&ToolsetConfig{SpecInline: shapesSpec})
	if err != nil {
		t.Fatalf("LoadAndDiscoverTools: %v", err)
	}
	if len(tools) != 6 || len(sigs) != len(tools) {
		t.Fatalf("runtime registered %d tools, enumerated %d signatures, want 6 of each", len(tools), len(sigs))
	}
	for _, tl := range tools {
		schema := tl.Parameters()
		got := paramsByName(t, sigs, tl.Name())
		if len(got) != len(schema.Properties) {
			t.Errorf("%s: enumerated %v, runtime properties %v", tl.Name(), got, schema.Properties)
		}
		for name := range schema.Properties {
			required := slices.Contains(schema.Required, name)
			if gotRequired, ok := got[name]; !ok || gotRequired != required {
				t.Errorf("%s: param %q required = %v (present %v), runtime says %v", tl.Name(), name, gotRequired, ok, required)
			}
		}
	}
	if got := paramsByName(t, sigs, "headers"); len(got) != 5 || !got["limit"] || !got["x_request_id"] {
		t.Errorf("headers params = %v, want limit and x_request_id required, id and its suffixed twin, session", got)
	}
}

func TestEnumerateOperations_EmptyMediaTypesBesideRealOnesAreSkippedNotCounted(t *testing.T) {
	t.Parallel()

	spec := `openapi: 3.0.0
info:
  title: Empty media
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
          application/octet-stream:
          application/json:
            schema:
              type: object
              required: [name]
              properties:
                name: {type: string}
          application/xml:
            schema:
              type: object
              required: [name]
              properties:
                name: {type: string}
      responses:
        "200":
          description: ok
  /nothing-but-empty:
    post:
      operationId: nothingButEmpty
      parameters:
        - {name: q, in: query, required: true, schema: {type: string}}
      requestBody:
        content:
          application/octet-stream:
          text/plain:
      responses:
        "200":
          description: ok
`
	for range 20 {
		sigs, err := EnumerateOperations(t.Context(), []byte(spec), nil, nil)
		if err != nil {
			t.Fatalf("EnumerateOperations: %v", err)
		}
		if got := paramsByName(t, sigs, "agree"); len(got) != 1 || !got["name"] {
			t.Fatalf("agree params = %v, want name (required): two agreeing media types and an empty one are graded", got)
		}
		if got := paramsByName(t, sigs, "nothing_but_empty"); len(got) != 1 || !got["q"] {
			t.Fatalf("nothing_but_empty params = %v, want q (required): empty media types must not erase the operation's own parameters", got)
		}
	}
}
