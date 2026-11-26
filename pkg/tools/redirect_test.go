// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func webCtxWithScopes(scopes ...string) *tool.Context {
	return &tool.Context{
		GatewayCapabilities: map[string]bool{"web_ui_redirect": true},
		Identity:            &auth.Identity{UserID: "u1", Scopes: scopes},
	}
}

func hasRedirectPart(res *tool.Result) bool {
	if res == nil || res.Data == nil {
		return false
	}
	_, ok := res.Data["type"]
	return ok
}

func TestRedirectUserTool_StaticMetadata(t *testing.T) {
	t.Parallel()
	tl := NewRedirectUserTool(nil)
	if tl.Name() != "redirect_user" {
		t.Errorf("Name() = %q, want redirect_user", tl.Name())
	}
	if !strings.Contains(strings.ToLower(tl.Description()), "builder") {
		t.Errorf("Description should mention builder; got %q", tl.Description())
	}
}

func TestRedirectUserTool_Parameters(t *testing.T) {
	t.Parallel()
	schema := NewRedirectUserTool(nil).Parameters()
	if schema == nil {
		t.Fatal("Parameters() returned nil")
	}
	if schema.Type != "object" {
		t.Errorf("Type = %q, want object", schema.Type)
	}
	if _, ok := schema.Properties["context"]; !ok {
		t.Error("schema missing 'context' property")
	}
	if _, ok := schema.Properties["artifacts"]; !ok {
		t.Error("schema missing 'artifacts' property")
	}
	if len(schema.Required) != 1 || schema.Required[0] != "context" {
		t.Errorf("Required = %v, want [context]", schema.Required)
	}
}

func TestRedirectUserTool_Execute_RejectsMissingContext(t *testing.T) {
	t.Parallel()
	tl := NewRedirectUserTool(nil)
	t.Run("missing", func(t *testing.T) {
		res, err := tl.Execute(t.Context(), map[string]any{}, webCtx())
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.Status != "error" {
			t.Errorf("Status = %q, want error", res.Status)
		}
	})
	t.Run("empty string", func(t *testing.T) {
		res, err := tl.Execute(t.Context(), map[string]any{"context": ""}, webCtx())
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.Status != "error" {
			t.Errorf("Status = %q, want error", res.Status)
		}
	})
}

func TestRedirectUserTool_Execute_AppliesDefaults(t *testing.T) {
	t.Parallel()
	tl := NewRedirectUserTool(nil)
	res, err := tl.Execute(t.Context(), map[string]any{"context": "Build me a connector"}, webCtx())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status == "error" {
		t.Fatalf("unexpected error result: %+v", res)
	}
	data := res.Data
	if data["type"] != "redirect" {
		t.Errorf("type = %v, want redirect", data["type"])
	}
	if data["route"] != "/builder" {
		t.Errorf("default route = %v, want /builder", data["route"])
	}
	if data["label"] != "Go to Builder" {
		t.Errorf("default label = %v, want 'Go to Builder'", data["label"])
	}
	if data["context"] != "Build me a connector" {
		t.Errorf("context not propagated: %v", data["context"])
	}
}

func TestRedirectUserTool_Execute_DefaultConfigOverrides(t *testing.T) {
	t.Parallel()
	tl := NewRedirectUserTool(map[string]any{
		"route":      "/agents/new",
		"label":      "Open builder",
		"url_params": map[string]any{"flow": "wizard"},
	})
	res, err := tl.Execute(t.Context(), map[string]any{"context": "ctx"}, webCtx())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Data["route"] != "/agents/new" {
		t.Errorf("route = %v, want /agents/new", res.Data["route"])
	}
	if res.Data["label"] != "Open builder" {
		t.Errorf("label = %v, want 'Open builder'", res.Data["label"])
	}
	urlParams, _ := res.Data["url_params"].(map[string]any)
	if urlParams["flow"] != "wizard" {
		t.Errorf("url_params.flow = %v, want wizard", urlParams["flow"])
	}
}

func TestRedirectUserTool_Execute_ToolConfigOverridesDefaults(t *testing.T) {
	t.Parallel()
	tl := NewRedirectUserTool(map[string]any{
		"route": "/builder",
		"label": "default",
	})
	toolCtx := &tool.Context{
		GatewayCapabilities: map[string]bool{"web_ui_redirect": true},
		ToolConfig: map[string]any{
			"route": "/different/route",
			"label": "Override label",
		},
	}
	res, err := tl.Execute(t.Context(), map[string]any{"context": "ctx"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Data["route"] != "/different/route" {
		t.Errorf("route = %v, want /different/route", res.Data["route"])
	}
	if res.Data["label"] != "Override label" {
		t.Errorf("label = %v, want 'Override label'", res.Data["label"])
	}
}

func TestRedirectUserTool_Execute_PropagatesArtifacts(t *testing.T) {
	t.Parallel()
	tl := NewRedirectUserTool(nil)
	toolCtx := &tool.Context{
		SessionID:           "sess-1",
		GatewayCapabilities: map[string]bool{"web_ui_redirect": true},
	}
	res, err := tl.Execute(t.Context(), map[string]any{
		"context":   "carry these over",
		"artifacts": []any{"plan.md", "diagram.png", 42},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	arts, ok := res.Data["artifacts"].([]string)
	if !ok {
		t.Fatalf("artifacts not []string: %T", res.Data["artifacts"])
	}
	if len(arts) != 2 || arts[0] != "plan.md" || arts[1] != "diagram.png" {
		t.Errorf("artifacts = %v, want [plan.md diagram.png]", arts)
	}
	if res.Data["source_session_id"] != "sess-1" {
		t.Errorf("source_session_id = %v, want sess-1", res.Data["source_session_id"])
	}
}

func TestRedirectUserTool_Execute_OmitsArtifactsWhenNoSession(t *testing.T) {
	t.Parallel()
	tl := NewRedirectUserTool(nil)
	res, err := tl.Execute(t.Context(), map[string]any{
		"context":   "ctx",
		"artifacts": []any{"foo.txt"},
	}, webCtx())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, ok := res.Data["artifacts"]; ok {
		t.Error("artifacts should not be set when SessionID is empty")
	}
	if _, ok := res.Data["source_session_id"]; ok {
		t.Error("source_session_id should not be set when SessionID is empty")
	}
}

func TestRedirectUserTool_Execute_NonWebGatewayFallback(t *testing.T) {
	t.Parallel()
	tl := NewRedirectUserTool(map[string]any{"route": "/preset"})
	noWebCtx := &tool.Context{GatewayCapabilities: map[string]bool{}}
	res, err := tl.Execute(t.Context(), map[string]any{"context": "build me a connector"}, noWebCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status == "error" {
		t.Fatalf("unexpected error result: %+v", res)
	}
	if res.Data != nil {
		if _, ok := res.Data["type"]; ok {
			t.Errorf("non-web gateway should not produce a redirect data part; got %v", res.Data)
		}
	}
	body := strings.ToLower(res.Message)
	if !strings.Contains(body, "web ui") || !strings.Contains(body, "builder") {
		t.Errorf("fallback message should point to the Builder in the web UI; got %q", res.Message)
	}
}

func TestRedirectUserTool_Execute_BuilderScopeGate(t *testing.T) {
	t.Parallel()
	tl := NewRedirectUserTool(nil)

	t.Run("with builder scope -> button offered", func(t *testing.T) {
		res, err := tl.Execute(t.Context(), map[string]any{"context": "ctx"}, webCtxWithScopes(rbac.ScopeBuilderUse))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !hasRedirectPart(res) {
			t.Errorf("user with builder:_:use should get a redirect part; got %+v", res)
		}
	})

	t.Run("wildcard scope -> button offered", func(t *testing.T) {
		res, err := tl.Execute(t.Context(), map[string]any{"context": "ctx"}, webCtxWithScopes("*"))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !hasRedirectPart(res) {
			t.Errorf("hierarchical wildcard should satisfy the gate; got %+v", res)
		}
	})

	t.Run("without builder scope -> denied, no button", func(t *testing.T) {
		res, err := tl.Execute(t.Context(), map[string]any{"context": "ctx"}, webCtxWithScopes("tool:artifact:read"))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if hasRedirectPart(res) {
			t.Errorf("user lacking builder:_:use should NOT get a redirect part; got %+v", res)
		}
		if !strings.Contains(strings.ToLower(res.Message), "access to the builder") {
			t.Errorf("expected an access-denied message; got %q", res.Message)
		}
	})

	t.Run("de-roled user (non-nil, empty scopes) -> denied, no button", func(t *testing.T) {
		res, err := tl.Execute(t.Context(), map[string]any{"context": "ctx"}, webCtxWithScopes())
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if hasRedirectPart(res) {
			t.Errorf("de-roled (empty-scope) identity must NOT get a redirect part; got %+v", res)
		}
	})

	t.Run("dev mode (nil identity) -> button offered", func(t *testing.T) {
		res, err := tl.Execute(t.Context(), map[string]any{"context": "ctx"}, webCtx())
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !hasRedirectPart(res) {
			t.Errorf("nil identity (auth disabled) should pass the gate; got %+v", res)
		}
	})
}

func TestRedirectUserTool_Execute_RequiredScopeOverride(t *testing.T) {
	t.Parallel()
	tl := NewRedirectUserTool(map[string]any{
		"route":          "/scheduled-tasks",
		"required_scope": "scheduled_tasks:_:use",
	})

	t.Run("missing override scope -> denied", func(t *testing.T) {
		res, err := tl.Execute(t.Context(), map[string]any{"context": "ctx"}, webCtxWithScopes(rbac.ScopeBuilderUse))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if hasRedirectPart(res) {
			t.Errorf("override scope not held -> no redirect part; got %+v", res)
		}
	})

	t.Run("has override scope -> button offered", func(t *testing.T) {
		res, err := tl.Execute(t.Context(), map[string]any{"context": "ctx"}, webCtxWithScopes("scheduled_tasks:_:use"))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !hasRedirectPart(res) {
			t.Errorf("override scope held -> redirect part expected; got %+v", res)
		}
	})
}

func TestRedirectUserTool_Execute_OverrideReplacesBuilderDefaultOnBuilderRoute(t *testing.T) {
	t.Parallel()
	tl := NewRedirectUserTool(map[string]any{
		"route":          "/builder",
		"required_scope": "custom:_:use",
	})

	t.Run("builder scope alone -> denied with generic message", func(t *testing.T) {
		res, err := tl.Execute(t.Context(), map[string]any{"context": "ctx"}, webCtxWithScopes(rbac.ScopeBuilderUse))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if hasRedirectPart(res) {
			t.Errorf("override scope not held -> no redirect part; got %+v", res)
		}
		msg := strings.ToLower(res.Message)
		if strings.Contains(msg, "builder") {
			t.Errorf("override (non-builder scope) should use the generic denial message; got %q", res.Message)
		}
		if !strings.Contains(msg, "access to that area") {
			t.Errorf("expected the generic access-denied message; got %q", res.Message)
		}
	})

	t.Run("override scope held -> button offered", func(t *testing.T) {
		res, err := tl.Execute(t.Context(), map[string]any{"context": "ctx"}, webCtxWithScopes("custom:_:use"))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !hasRedirectPart(res) {
			t.Errorf("override scope held -> redirect part expected; got %+v", res)
		}
	})
}

func TestRedirectUserTool_Execute_NonBuilderRouteUngated(t *testing.T) {
	t.Parallel()
	tl := NewRedirectUserTool(map[string]any{"route": "/chat"})
	res, err := tl.Execute(t.Context(), map[string]any{"context": "ctx"}, webCtxWithScopes("tool:artifact:read"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !hasRedirectPart(res) {
		t.Errorf("non-builder route without required_scope should not be gated; got %+v", res)
	}
}

func TestRedirectUserTool_Execute_NilToolContextFallsBack(t *testing.T) {
	t.Parallel()
	tl := NewRedirectUserTool(map[string]any{"route": "/preset"})
	res, err := tl.Execute(t.Context(), map[string]any{"context": "hi"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status == "error" {
		t.Fatalf("unexpected error: %+v", res)
	}
	if res.Data != nil {
		if _, ok := res.Data["type"]; ok {
			t.Errorf("nil toolCtx should not produce a redirect data part; got %v", res.Data)
		}
	}
}
