// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestRequestBuildActivateTool_StaticMetadata(t *testing.T) {
	t.Parallel()
	tl := &RequestBuildActivateTool{}
	if tl.Name() != "request_build_activate" {
		t.Errorf("Name() = %q, want request_build_activate", tl.Name())
	}
	desc := strings.ToLower(tl.Description())
	if !strings.Contains(desc, "skill") {
		t.Errorf("Description should warn about skills; got %q", tl.Description())
	}
	if !strings.Contains(desc, "connector") {
		t.Errorf("Description should warn about standalone connectors; got %q", tl.Description())
	}
}

func TestRequestBuildActivateTool_WebEmitsSignal(t *testing.T) {
	t.Parallel()
	tl := &RequestBuildActivateTool{}
	ctx := &tool.Context{GatewayCapabilities: map[string]bool{"web_ui_redirect": true}}

	res, err := tl.Execute(context.Background(), nil, ctx)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if res == nil || res.Data == nil {
		t.Fatalf("expected a data signal, got %+v", res)
	}
	if res.Data["type"] != "request_build_activate" {
		t.Errorf("data type = %v, want request_build_activate", res.Data["type"])
	}
}

func TestRequestBuildActivateTool_NonWebIsTextOnly(t *testing.T) {
	t.Parallel()
	tl := &RequestBuildActivateTool{}

	for name, ctx := range map[string]*tool.Context{
		"nil-context":     nil,
		"no-capabilities": {GatewayCapabilities: map[string]bool{}},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := tl.Execute(context.Background(), nil, ctx)
			if err != nil {
				t.Fatalf("Execute err = %v", err)
			}
			if res == nil {
				t.Fatal("expected a text result, got nil")
			}
			if res.Data != nil {
				t.Errorf("expected no data signal on a non-web gateway; got %+v", res.Data)
			}
		})
	}
}
