// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const effectSpec = `
openapi: 3.0.0
info: {title: Orders, version: "1"}
servers: [{url: "https://orders.example.com"}]
paths:
  /orders/{id}:
    parameters:
      - {name: id, in: path, required: true, schema: {type: string}}
    get:
      operationId: getOrder
      responses: {"200": {description: ok}}
    head:
      operationId: headOrder
      responses: {"200": {description: ok}}
    put:
      operationId: replaceOrder
      responses: {"200": {description: ok}}
    patch:
      operationId: patchOrder
      responses: {"200": {description: ok}}
    delete:
      operationId: deleteOrder
      responses: {"204": {description: gone}}
  /orders:
    post:
      operationId: createOrder
      responses: {"201": {description: created}}
`

func TestOpenAPIToolEffect_DerivedFromMethod(t *testing.T) {
	t.Parallel()
	tools, err := NewManager(slog.New(slog.DiscardHandler), nil).LoadAndDiscoverTools(&ToolsetConfig{SpecInline: effectSpec})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]tool.EffectClass{
		"get_order":     tool.DerivedEffect(tool.EffectReadOnly),
		"head_order":    tool.DerivedEffect(tool.EffectReadOnly),
		"replace_order": tool.DerivedEffect(tool.EffectSideEffecting),
		"patch_order":   tool.DerivedEffect(tool.EffectSideEffecting),
		"delete_order":  tool.DerivedEffect(tool.EffectSideEffecting),
		"create_order":  tool.DerivedEffect(tool.EffectSideEffecting),
	}
	if len(tools) != len(want) {
		t.Fatalf("discovered %d tools, want %d", len(tools), len(want))
	}
	for _, tl := range tools {
		w, ok := want[tl.Name()]
		if !ok {
			t.Fatalf("unexpected tool %q", tl.Name())
		}
		if got := tool.EffectClassOf(tl); got != w {
			t.Errorf("%s: EffectClassOf = %+v, want %+v", tl.Name(), got, w)
		}
	}
}
