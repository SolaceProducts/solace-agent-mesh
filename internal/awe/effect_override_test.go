// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const ordersSpec = `
openapi: 3.0.0
info: {title: Orders, version: "1"}
servers: [{url: "https://orders.example.com"}]
paths:
  /orders:
    get:
      operationId: listOrders
      responses: {"200": {description: ok}}
    post:
      operationId: createOrder
      responses: {"201": {description: created}}
`

var invoicesSpec = strings.NewReplacer("Orders", "Invoices", "/orders", "/invoices", "listOrders", "listInvoices", "createOrder", "createInvoice").Replace(ordersSpec)

func registerForEffects(t *testing.T, toolsCfg []any) (*agent.Agent, error) {
	t.Helper()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "effects-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	_, err := RegisterTools(ag, toolsCfg, svc)
	return ag, err
}

func TestRegisterTools_EffectClassesThroughTheRealLoader(t *testing.T) {
	t.Parallel()
	ag, err := registerForEffects(t, []any{
		map[string]any{"tool_type": "builtin", "tool_name": "artifact_grep"},
		map[string]any{"tool_type": "builtin", "tool_name": "get_current_time", "effect": "side_effecting"},
		map[string]any{"tool_type": "builtin", "tool_name": "notify_user"},
		map[string]any{"tool_type": "openapi", "specification": ordersSpec},
		map[string]any{"tool_type": "openapi", "specification": invoicesSpec, "effect": "side_effecting"},
	})
	if err != nil {
		t.Fatal(err)
	}
	operator := tool.EffectClass{Effect: tool.EffectSideEffecting, Source: tool.EffectSourceOperator}
	for name, want := range map[string]tool.EffectClass{
		"artifact_grep":    tool.DeclaredEffect(tool.EffectReadOnly),
		"get_current_time": operator,
		"notify_user":      tool.DeclaredEffect(tool.EffectSideEffecting),
		"list_orders":      tool.DerivedEffect(tool.EffectReadOnly),
		"create_order":     tool.DerivedEffect(tool.EffectSideEffecting),
		"list_invoices":    operator,
		"create_invoice":   operator,
	} {
		got, ok := ag.Tools().EffectClassFor(name)
		if !ok {
			t.Errorf("%s is not registered", name)
			continue
		}
		if got != want {
			t.Errorf("%s: EffectClassFor = %+v, want %+v", name, got, want)
		}
	}
}

func mcpManifestEntry(host, toolName string, readOnlyHint bool, extra map[string]any) map[string]any {
	entry := map[string]any{
		"tool_type":         "mcp",
		"connection_params": map[string]any{"type": "streamable-http", "url": "http://" + host + ".invalid/mcp"},
		"manifest": []any{map[string]any{
			"name":        toolName,
			"description": toolName,
			"annotations": map[string]any{"readOnlyHint": readOnlyHint, "openWorldHint": false},
		}},
	}
	maps.Copy(entry, extra)
	return entry
}

func TestRegisterTools_EffectClassesOfRemoteMCPEntriesThroughTheRealLoader(t *testing.T) {
	t.Parallel()
	ag, err := registerForEffects(t, []any{
		mcpManifestEntry("untrusted.example", "lookup_order", true, nil),
		mcpManifestEntry("trusted.example", "lookup_invoice", true, map[string]any{"trust_annotations": true}),
		mcpManifestEntry("overridden.example", "lookup_receipt", true, map[string]any{"trust_annotations": true, "effect": "side_effecting"}),
		mcpManifestEntry("overridden-untrusted.example", "lookup_quote", true, map[string]any{"effect": "side_effecting"}),
	})
	if err != nil {
		t.Fatalf("RegisterTools with manifest MCP entries: %v", err)
	}
	operator := tool.EffectClass{Effect: tool.EffectSideEffecting, Source: tool.EffectSourceOperator}
	for name, want := range map[string]tool.EffectClass{
		"lookup_order":   tool.UntrustedHintEffect(tool.EffectSideEffecting),
		"lookup_invoice": tool.HintedEffect(tool.EffectReadOnly),
		"lookup_receipt": operator,
		"lookup_quote":   operator,
	} {
		got, ok := ag.Tools().EffectClassFor(name)
		if !ok {
			t.Errorf("%s is not registered", name)
			continue
		}
		if got != want {
			t.Errorf("%s: EffectClassFor = %+v, want %+v", name, got, want)
		}
	}
}

func TestRegisterTools_AnEffectOnAnEntryThatRegistersNoToolsIsNotAnError(t *testing.T) {
	t.Parallel()
	ag, err := registerForEffects(t, []any{
		map[string]any{
			"tool_type":         "mcp",
			"connection_params": map[string]any{"type": "stdio", "command": unreachableMCPCommand, "timeout": 2},
			"effect":            "side_effecting",
		},
		map[string]any{"tool_type": "builtin", "tool_name": "artifact_grep"},
	})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	if got, ok := ag.Tools().EffectClassFor("artifact_grep"); !ok || got != tool.DeclaredEffect(tool.EffectReadOnly) {
		t.Fatalf("artifact_grep: EffectClassFor = %+v, %v; want its declaration, untouched by the failed entry's effect", got, ok)
	}
}

func TestRegisterSkillTools_AnEffectOnASkillToolEntryIsHonoured(t *testing.T) {
	t.Parallel()
	ag, r := newSkillTestAgent(t)
	got, retry := r.RegisterSkillTools("orders-kit", []map[string]any{
		{"tool_type": "builtin", "tool_name": "artifact_grep", "effect": "side_effecting"},
		{"tool_type": "openapi", "specification": ordersSpec, "effect": "side_effecting"},
		{"tool_type": "builtin", "tool_name": "get_current_time"},
	})
	if len(retry) != 0 {
		t.Fatalf("retry = %v, want none", retry)
	}
	operator := tool.EffectClass{Effect: tool.EffectSideEffecting, Source: tool.EffectSourceOperator}
	for name, want := range map[string]tool.EffectClass{
		"artifact_grep":    operator,
		"list_orders":      operator,
		"create_order":     operator,
		"get_current_time": tool.DeclaredEffect(tool.EffectReadOnly),
	} {
		class, ok := ag.Tools().EffectClassFor(name)
		if !ok {
			t.Errorf("%s is not registered; registered %v", name, got)
			continue
		}
		if class != want {
			t.Errorf("%s: EffectClassFor = %+v, want %+v", name, class, want)
		}
	}
}

func TestRegisterSkillTools_ARegistrationKeepsTheAgentsOwnEffectOverrides(t *testing.T) {
	t.Parallel()
	ag, r := newSkillTestAgent(t)
	ag.Tools().SetEffectOverrides(map[string]tool.EffectClass{
		"get_current_time": {Effect: tool.EffectSideEffecting, Source: tool.EffectSourceOperator},
	})
	r.RegisterSkillTools("orders-kit", []map[string]any{
		{"tool_type": "builtin", "tool_name": "artifact_grep", "effect": "side_effecting"},
		{"tool_type": "builtin", "tool_name": "get_current_time"},
	})
	for _, name := range []string{"artifact_grep", "get_current_time"} {
		if class, _ := ag.Tools().EffectClassFor(name); class.Source != tool.EffectSourceOperator {
			t.Errorf("%s: EffectClassFor = %+v, want the operator override kept", name, class)
		}
	}
}

func TestRegisterSkillTools_ASkillCannotTrustAServersAnnotations(t *testing.T) {
	t.Parallel()
	var logged bytes.Buffer
	ag, r := newSkillTestAgent(t)
	r.logger = slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))
	trusting := mcpManifestEntry("trusting.example", "lookup_order", true, map[string]any{"trust_annotations": true})
	got, retry := r.RegisterSkillTools("orders-kit", []map[string]any{
		trusting,
		mcpManifestEntry("plain.example", "lookup_invoice", true, nil),
	})
	if len(retry) != 0 || !slices.Equal(slices.Sorted(slices.Values(got)), []string{"lookup_invoice", "lookup_order"}) {
		t.Fatalf("registered = %v, retry = %v; want both tools registered and none retried", got, retry)
	}
	for _, name := range []string{"lookup_order", "lookup_invoice"} {
		class, ok := ag.Tools().EffectClassFor(name)
		if !ok {
			t.Errorf("%s is not registered", name)
			continue
		}
		if want := tool.UntrustedHintEffect(tool.EffectSideEffecting); class != want {
			t.Errorf("%s: EffectClassFor = %+v, want %+v", name, class, want)
		}
	}
	if _, stillThere := trusting["trust_annotations"]; !stillThere {
		t.Error("the skill's own entry was modified; the registrar must strip the key from a copy")
	}
	if !strings.Contains(logged.String(), "trust_annotations") {
		t.Errorf("no warning named trust_annotations; log: %q", logged.String())
	}
}

func TestRegisterSkillTools_AnInvalidEffectSkipsTheEntry(t *testing.T) {
	t.Parallel()
	ag, r := newSkillTestAgent(t)
	got, retry := r.RegisterSkillTools("orders-kit", []map[string]any{
		{"tool_type": "builtin", "tool_name": "notify_user", "effect": "read_only"},
		{"tool_type": "builtin", "tool_name": "artifact_grep", "effect": true},
		{"tool_type": "builtin", "tool_name": "get_current_time"},
	})
	if len(retry) != 0 {
		t.Fatalf("retry = %v, want an invalid entry never retried", retry)
	}
	if !slices.Equal(got, []string{"get_current_time"}) {
		t.Fatalf("registered = %v, want only the entry without an invalid effect", got)
	}
	for _, name := range []string{"notify_user", "artifact_grep"} {
		if ag.Tools().GetTool(name) != nil {
			t.Errorf("%s registered although its entry carries an invalid effect", name)
		}
	}
}

func TestRegisterSkillTools_AnInvalidEffectIsLoggedByLengthNeverByValue(t *testing.T) {
	t.Parallel()
	const authorsValue = "maybe-read-only-canary"
	var logged bytes.Buffer
	_, r := newSkillTestAgent(t)
	r.logger = slog.New(slog.NewJSONHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))
	r.RegisterSkillTools("orders-kit", []map[string]any{
		{"tool_type": "builtin", "tool_name": "notify_user", "effect": authorsValue},
	})
	var record map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logged.String()), "\n") {
		var candidate map[string]any
		if json.Unmarshal([]byte(line), &candidate) == nil && candidate["msg"] == "skipping skill tool entry: invalid effect" {
			record = candidate
		}
	}
	if record == nil {
		t.Fatalf("no invalid-effect warning; log: %s", logged.String())
	}
	if record["level"] != "WARN" || record["valueLength"] != float64(len(authorsValue)) {
		t.Errorf("warning fields = %v, want level WARN and valueLength %d", record, len(authorsValue))
	}
	if strings.Contains(logged.String(), authorsValue) {
		t.Errorf("the skill author's effect string reached the log: %s", logged.String())
	}
}

func TestRegisterTools_OperatorCannotLoosenAnEffect(t *testing.T) {
	t.Parallel()
	_, err := registerForEffects(t, []any{
		map[string]any{"tool_type": "builtin", "tool_name": "notify_user", "effect": "read_only"},
	})
	if err == nil || !strings.Contains(err.Error(), `may only be "side_effecting"`) {
		t.Fatalf("err = %v, want the read_only override refused", err)
	}
}
