// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

const exclusiveBound30Spec = `openapi: 3.0.0
info:
  title: Bounds
  version: 1.0.0
paths: {}
components:
  schemas:
    Order:
      type: object
      properties:
        shares:
          type: number
          minimum: 0
          exclusiveMinimum: true
          maximum: 100
          exclusiveMaximum: true
        dividend:
          type: number
          minimum: 0
          exclusiveMinimum: false
          maximum: 100
          exclusiveMaximum: false
        stockId:
          type: integer
`

const exclusiveBound31Spec = `openapi: 3.1.0
info:
  title: Bounds
  version: 1.0.0
paths: {}
components:
  schemas:
    Order:
      type: object
      properties:
        shares:
          type: number
          exclusiveMinimum: 0
          exclusiveMaximum: 100
        stockId:
          type: integer
`

func renderOrderProperties(t *testing.T, spec string) map[string]any {
	t.Helper()

	doc, err := openapi3.NewLoader().LoadFromData([]byte(spec))
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	order := doc.Components.Schemas["Order"]
	if order == nil || order.Value == nil {
		t.Fatal("spec has no Order schema")
	}

	rendered := renderSchema(order.Value, "")
	props, ok := rendered["properties"].(map[string]any)
	if !ok {
		t.Fatalf("rendered schema has no properties: %#v", rendered)
	}
	return props
}

func propertyOf(t *testing.T, props map[string]any, name string) map[string]any {
	t.Helper()

	prop, ok := props[name].(map[string]any)
	if !ok {
		t.Fatalf("property %q missing or not an object: %#v", name, props[name])
	}
	return prop
}

func TestRenderSchemaExclusiveBoundsOpenAPI30(t *testing.T) {
	props := renderOrderProperties(t, exclusiveBound30Spec)
	shares := propertyOf(t, props, "shares")

	if got := shares["exclusiveMinimum"]; got != true {
		t.Errorf("exclusiveMinimum = %#v, want true", got)
	}
	if got := shares["exclusiveMaximum"]; got != true {
		t.Errorf("exclusiveMaximum = %#v, want true", got)
	}
	if got := shares["minimum"]; got != float64(0) {
		t.Errorf("minimum = %#v, want 0", got)
	}
	if got := shares["maximum"]; got != float64(100) {
		t.Errorf("maximum = %#v, want 100", got)
	}
}

func TestRenderSchemaOmitsFalseExclusiveBoundsOpenAPI30(t *testing.T) {
	dividend := propertyOf(t, renderOrderProperties(t, exclusiveBound30Spec), "dividend")

	for _, key := range []string{"exclusiveMinimum", "exclusiveMaximum"} {
		if _, present := dividend[key]; present {
			t.Errorf("%s should be absent when declared false, got %#v", key, dividend[key])
		}
	}
	if got := dividend["minimum"]; got != float64(0) {
		t.Errorf("minimum = %#v, want 0", got)
	}
	if got := dividend["maximum"]; got != float64(100) {
		t.Errorf("maximum = %#v, want 100", got)
	}
}

func TestRenderSchemaExclusiveBoundsOpenAPI31(t *testing.T) {
	props := renderOrderProperties(t, exclusiveBound31Spec)
	shares := propertyOf(t, props, "shares")

	if got := shares["exclusiveMinimum"]; got != float64(0) {
		t.Errorf("exclusiveMinimum = %#v, want 0", got)
	}
	if got := shares["exclusiveMaximum"]; got != float64(100) {
		t.Errorf("exclusiveMaximum = %#v, want 100", got)
	}
	if _, present := shares["minimum"]; present {
		t.Errorf("minimum should be absent when the spec only sets exclusive bounds, got %#v", shares["minimum"])
	}
	if _, present := shares["maximum"]; present {
		t.Errorf("maximum should be absent when the spec only sets exclusive bounds, got %#v", shares["maximum"])
	}
}

func TestRenderSchemaOmitsUnsetExclusiveBounds(t *testing.T) {
	for name, spec := range map[string]string{
		"3.0": exclusiveBound30Spec,
		"3.1": exclusiveBound31Spec,
	} {
		t.Run(name, func(t *testing.T) {
			stockID := propertyOf(t, renderOrderProperties(t, spec), "stockId")

			for _, key := range []string{"exclusiveMinimum", "exclusiveMaximum"} {
				if _, present := stockID[key]; present {
					t.Errorf("%s should be absent when unset, got %#v", key, stockID[key])
				}
			}
		})
	}
}
