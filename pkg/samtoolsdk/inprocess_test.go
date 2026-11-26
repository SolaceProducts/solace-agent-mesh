// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package samtoolsdk

import (
	"context"
	"encoding/json"
	"testing"
)

type echoParams struct {
	Text string `json:"text" desc:"text to echo"`
}

func newEchoTool() *ToolDef {
	return NewTool("echo", "Echo the input back.",
		func(_ context.Context, p echoParams, tc *ToolContext) (*Result, error) {
			prefix := tc.GetConfigString("prefix", "")
			return OK("ok", WithData(map[string]any{"echoed": prefix + p.Text})), nil
		},
		WithConfigSchema(ConfigSchemaField{Key: "prefix", Type: "string"}),
		WithDynamicSchema(func(cfg map[string]any) *SchemaOverride {
			if name, _ := cfg["tool_name"].(string); name != "" {
				return &SchemaOverride{Name: name}
			}
			return nil
		}),
	)
}

func TestInvokeInProcess_ReturnsWireResult(t *testing.T) {
	tools := []*ToolDef{newEchoTool()}
	wire, err := InvokeInProcess(t.Context(), tools, "echo",
		map[string]any{"text": "hi"},
		map[string]any{"prefix": ">> "},
		InProcessBindings{})
	if err != nil {
		t.Fatalf("InvokeInProcess: %v", err)
	}
	m, ok := wire.(map[string]any)
	if !ok {
		t.Fatalf("wire result is %T, want map", wire)
	}
	if m["status"] != "success" {
		t.Fatalf("status = %v, want success", m["status"])
	}
	data, _ := m["data"].(map[string]any)
	if got := data["echoed"]; got != ">> hi" {
		t.Fatalf("echoed = %v, want %q", got, ">> hi")
	}
}

func TestInvokeInProcess_StatusBinding(t *testing.T) {
	var statuses []string
	statusTool := NewTool("ping", "Send a status.",
		func(_ context.Context, _ echoParams, tc *ToolContext) (*Result, error) {
			_ = tc.SendStatus("working")
			return OK("done"), nil
		})
	_, err := InvokeInProcess(t.Context(), []*ToolDef{statusTool}, "ping",
		map[string]any{"text": ""}, nil,
		InProcessBindings{Status: func(s string) error { statuses = append(statuses, s); return nil }})
	if err != nil {
		t.Fatalf("InvokeInProcess: %v", err)
	}
	if len(statuses) != 1 || statuses[0] != "working" {
		t.Fatalf("statuses = %v, want [working]", statuses)
	}
}

func TestInvokeInProcess_UnknownTool(t *testing.T) {
	_, err := InvokeInProcess(t.Context(), []*ToolDef{newEchoTool()}, "nope", nil, nil, InProcessBindings{})
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestSchemaJSON_MatchesParserShape(t *testing.T) {
	data, err := SchemaJSON([]*ToolDef{newEchoTool()}, nil)
	if err != nil {
		t.Fatalf("SchemaJSON: %v", err)
	}
	var doc struct {
		Tools map[string]json.RawMessage `json:"tools"`
	}
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if _, ok := doc.Tools["echo"]; !ok {
		t.Fatalf("static schema missing 'echo': %s", data)
	}

	data, err = SchemaJSON([]*ToolDef{newEchoTool()}, map[string]any{"tool_name": "renamed_echo"})
	if err != nil {
		t.Fatalf("SchemaJSON config: %v", err)
	}
	doc.Tools = nil
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if _, ok := doc.Tools["renamed_echo"]; !ok {
		t.Fatalf("dynamic schema missing 'renamed_echo': %s", data)
	}
}

func TestInvokeInProcess_UserProfileBinding(t *testing.T) {
	type noParams struct{}
	tool := NewTool("whoami", "Reports the caller's country",
		func(_ context.Context, _ noParams, tc *ToolContext) (*Result, error) {
			country, _ := tc.UserProfile()["country"].(string)
			return OK("ok", WithData(map[string]any{"country": country})), nil
		})
	wire, err := InvokeInProcess(t.Context(), []*ToolDef{tool}, "whoami", nil, nil,
		InProcessBindings{A2AUserConfig: `{"user_profile":{"id":"u-1","country":"Germany"}}`})
	if err != nil {
		t.Fatalf("InvokeInProcess: %v", err)
	}
	m, _ := wire.(map[string]any)
	data, _ := m["data"].(map[string]any)
	if got := data["country"]; got != "Germany" {
		t.Fatalf("country = %v, want Germany", got)
	}
}

func TestDynamicSchemaApplies(t *testing.T) {
	echo := newEchoTool()
	if !echo.DynamicSchemaApplies(map[string]any{"tool_name": "renamed"}) {
		t.Error("a config the dynamic schema accepts must report true")
	}
	if echo.DynamicSchemaApplies(map[string]any{}) {
		t.Error("a config the dynamic schema declines must report false")
	}
	static := NewTool("static", "No dynamic schema.", greetHandler)
	if static.DynamicSchemaApplies(map[string]any{"tool_name": "renamed"}) {
		t.Error("a tool with no dynamic schema must report false")
	}
}
