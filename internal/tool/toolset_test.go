// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"context"
	"log/slog"
	"slices"
	"sync/atomic"
	"testing"
)

type mockTool struct {
	name        string
	description string
	params      *JSONSchema
	execFn      func(ctx context.Context, args map[string]any, toolCtx *Context) (*Result, error)
}

func (m *mockTool) Name() string                                 { return m.name }
func (m *mockTool) Description() string                          { return m.description }
func (m *mockTool) Parameters() *JSONSchema                      { return m.params }
func (m *mockTool) Instructions() string                         { return "" }
func (m *mockTool) IsLongRunning() bool                          { return false }
func (m *mockTool) RawStringArgs() []string                      { return nil }
func (m *mockTool) ArtifactParams() map[string]ArtifactParamInfo { return nil }
func (m *mockTool) ContextParam() string                         { return "" }

func (m *mockTool) Execute(ctx context.Context, args map[string]any, toolCtx *Context) (*Result, error) {
	if m.execFn != nil {
		return m.execFn(ctx, args, toolCtx)
	}
	return OK("mock result"), nil
}

func newMockTool(name string) *mockTool {
	return &mockTool{
		name:        name,
		description: "A mock tool: " + name,
		params: &JSONSchema{
			Type: "object",
			Properties: map[string]any{
				"input": map[string]any{"type": "string"},
			},
		},
	}
}

func TestToLLMDefinitions_CoercesEmptyDescriptionToName(t *testing.T) {
	ts := NewSet()
	empty := newMockTool("no_desc_tool")
	empty.description = "   "
	full := newMockTool("has_desc_tool")
	if err := ts.Register(empty); err != nil {
		t.Fatal(err)
	}
	if err := ts.Register(full); err != nil {
		t.Fatal(err)
	}

	byName := make(map[string]LLMToolDefinition)
	for _, d := range ts.ToLLMDefinitions() {
		byName[d.Name] = d
	}

	if got := byName["no_desc_tool"].Description; got != "no_desc_tool" {
		t.Errorf("empty description should fall back to name, got %q", got)
	}
	if got := byName["has_desc_tool"].Description; got != "A mock tool: has_desc_tool" {
		t.Errorf("non-empty description should be preserved, got %q", got)
	}
}

func TestToLLMDefinitions_SortedByNameForStableCache(t *testing.T) {
	ts := NewSet()
	for _, name := range []string{"delta", "alpha", "charlie", "bravo"} {
		if err := ts.Register(newMockTool(name)); err != nil {
			t.Fatal(err)
		}
	}

	defs := ts.ToLLMDefinitions()
	got := make([]string, len(defs))
	for i, d := range defs {
		got[i] = d.Name
	}

	want := []string{"alpha", "bravo", "charlie", "delta"}
	if !slices.Equal(got, want) {
		t.Errorf("tool definitions must be sorted by name for a stable cache prefix; got %v want %v", got, want)
	}
}

func TestToLLMDefinitions_EmptyDescriptionWarnsOncePerTool(t *testing.T) {
	var warnings atomic.Int64
	handler := countingHandler{onRecord: func(r slog.Record) {
		if r.Level == slog.LevelWarn {
			warnings.Add(1)
		}
	}}
	ts := NewSet()
	ts.SetLogger(slog.New(handler))
	empty := newMockTool("no_desc_tool")
	empty.description = ""
	if err := ts.Register(empty); err != nil {
		t.Fatal(err)
	}

	for range 5 {
		ts.ToLLMDefinitions()
	}

	if got := warnings.Load(); got != 1 {
		t.Errorf("empty-description WARN should fire once per tool, got %d", got)
	}
}

type countingHandler struct {
	onRecord func(slog.Record)
}

func (h countingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h countingHandler) Handle(_ context.Context, r slog.Record) error {
	h.onRecord(r)
	return nil
}
func (h countingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h countingHandler) WithGroup(string) slog.Handler      { return h }

type availabilityMockTool struct {
	*mockTool
	available bool
}

func (m *availabilityMockTool) Available() bool { return m.available }

func TestToLLMDefinitions_SkipsUnavailableTools(t *testing.T) {
	ts := NewSet()
	unavailable := &availabilityMockTool{mockTool: newMockTool("unhosted_tool"), available: false}
	available := &availabilityMockTool{mockTool: newMockTool("hosted_tool"), available: true}
	plain := newMockTool("plain_tool")
	for _, tl := range []Tool{unavailable, available, plain} {
		if err := ts.Register(tl); err != nil {
			t.Fatal(err)
		}
	}

	byName := make(map[string]LLMToolDefinition)
	for _, d := range ts.ToLLMDefinitions() {
		byName[d.Name] = d
	}

	if _, ok := byName["unhosted_tool"]; ok {
		t.Error("unavailable tool should not be advertised")
	}
	if _, ok := byName["hosted_tool"]; !ok {
		t.Error("available tool should be advertised")
	}
	if _, ok := byName["plain_tool"]; !ok {
		t.Error("tool without Available() should be advertised")
	}
}

func TestRegisterAndGetTool(t *testing.T) {
	ts := NewSet()
	mt := newMockTool("test_tool")

	if err := ts.Register(mt); err != nil {
		t.Fatal(err)
	}

	got := ts.GetTool("test_tool")
	if got == nil {
		t.Fatal("GetTool returned nil")
	}
	if got.Name() != "test_tool" {
		t.Errorf("name = %q", got.Name())
	}
}

func TestRegisterDuplicate(t *testing.T) {
	ts := NewSet()
	mt := newMockTool("test_tool")

	if err := ts.Register(mt); err != nil {
		t.Fatal(err)
	}
	if err := ts.Register(mt); err == nil {
		t.Error("expected error for duplicate registration")
	}
}

func TestUnregister(t *testing.T) {
	ts := NewSet()
	mt := newMockTool("test_tool")
	if err := ts.Register(mt); err != nil {
		t.Fatal(err)
	}

	ts.Unregister("test_tool")
	if ts.GetTool("test_tool") != nil {
		t.Error("tool should be unregistered")
	}
}

func TestGetToolNotFound(t *testing.T) {
	ts := NewSet()
	if ts.GetTool("nonexistent") != nil {
		t.Error("expected nil for nonexistent tool")
	}
}

func TestListTools(t *testing.T) {
	ts := NewSet()
	for _, name := range []string{"a", "b", "c"} {
		if err := ts.Register(newMockTool(name)); err != nil {
			t.Fatal(err)
		}
	}

	tools := ts.ListTools()
	if len(tools) != 3 {
		t.Errorf("ListTools returned %d tools", len(tools))
	}
}

func TestDispatchBuiltinTool(t *testing.T) {
	ts := NewSet()
	mt := newMockTool("greet")
	mt.execFn = func(_ context.Context, args map[string]any, _ *Context) (*Result, error) {
		name := args["name"].(string)
		return OK("Hello, " + name), nil
	}
	if err := ts.Register(mt); err != nil {
		t.Fatal(err)
	}

	r, err := ts.Dispatch(context.Background(), Call{
		ID:        "call-1",
		Name:      "greet",
		Arguments: `{"name": "World"}`,
	}, &Context{})
	if err != nil {
		t.Fatal(err)
	}

	if r.Status != StatusSuccess || r.Message != "Hello, World" {
		t.Errorf("result = %+v", r)
	}
}

func TestDispatchUnknownTool(t *testing.T) {
	ts := NewSet()
	r, err := ts.Dispatch(context.Background(), Call{
		ID:   "call-1",
		Name: "nonexistent",
	}, &Context{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusError {
		t.Errorf("expected error result, got %+v", r)
	}
}

func TestDispatchBadJSON(t *testing.T) {
	ts := NewSet()
	if err := ts.Register(newMockTool("test")); err != nil {
		t.Fatal(err)
	}

	r, err := ts.Dispatch(context.Background(), Call{
		ID:        "call-1",
		Name:      "test",
		Arguments: "not-json",
	}, &Context{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusError {
		t.Errorf("expected error for bad JSON, got %+v", r)
	}
}

func TestDispatchEmptyArgs(t *testing.T) {
	ts := NewSet()
	if err := ts.Register(newMockTool("test")); err != nil {
		t.Fatal(err)
	}

	for _, args := range []string{"", "{}"} {
		r, err := ts.Dispatch(context.Background(), Call{
			ID:        "call-1",
			Name:      "test",
			Arguments: args,
		}, &Context{})
		if err != nil {
			t.Fatal(err)
		}
		if r.Status != StatusSuccess {
			t.Errorf("args=%q: expected success, got %+v", args, r)
		}
	}
}

func TestDispatchCoercesJSONStringArgs(t *testing.T) {
	var gotItems any
	ts := NewSet()
	mt := &mockTool{
		name:        "typed",
		description: "A tool with typed parameters",
		params: &JSONSchema{
			Type: "object",
			Properties: map[string]any{
				"label": map[string]any{"type": "string"},
				"items": map[string]any{"type": "array"},
				"meta":  map[string]any{"type": "object"},
			},
		},
		execFn: func(_ context.Context, args map[string]any, _ *Context) (*Result, error) {
			gotItems = args["items"]
			return OK("ok"), nil
		},
	}
	if err := ts.Register(mt); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		args    string
		wantErr bool
	}{
		{"stringified_array_coerced", `{"items":"[1,2,3]"}`, false},
		{"stringified_object_coerced", `{"meta":"{\"k\":\"v\"}"}`, false},
		{"stringified_empty_array_coerced", `{"items":"[]"}`, false},
		{"native_array_still_ok", `{"items":[1,2]}`, false},
		{"non_json_string_still_rejected", `{"items":"not json"}`, true},
		{"json_string_wrong_kind_rejected", `{"items":"\"hello\""}`, true},
		{"plain_string_param_untouched", `{"label":"[1,2]"}`, false},
	}
	ctx := t.Context()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ts.Dispatch(ctx, Call{Name: "typed", Arguments: tc.args}, &Context{})
			if err != nil {
				t.Fatalf("Dispatch returned err: %v", err)
			}
			if gotErr := res.Status == StatusError; gotErr != tc.wantErr {
				t.Errorf("args=%s: status=%q message=%q, wantErr=%v", tc.args, res.Status, res.Message, tc.wantErr)
			}
		})
	}

	if _, err := ts.Dispatch(ctx, Call{Name: "typed", Arguments: `{"items":"[1,2,3]"}`}, &Context{}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if _, ok := gotItems.([]any); !ok {
		t.Errorf("tool received items of type %T, want []any (coerced from JSON string)", gotItems)
	}
}

func TestDispatchParallel(t *testing.T) {
	ts := NewSet()

	var callCount atomic.Int32
	mt := newMockTool("counter")
	mt.execFn = func(_ context.Context, args map[string]any, _ *Context) (*Result, error) {
		callCount.Add(1)
		return OK("counted"), nil
	}
	if err := ts.Register(mt); err != nil {
		t.Fatal(err)
	}

	calls := make([]Call, 5)
	for i := range calls {
		calls[i] = Call{ID: "call", Name: "counter", Arguments: "{}"}
	}

	results := ts.DispatchParallel(context.Background(), calls, &Context{})
	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(results))
	}

	for i, r := range results {
		if r.Status != StatusSuccess {
			t.Errorf("result[%d] = %+v", i, r)
		}
	}

	if callCount.Load() != 5 {
		t.Errorf("call count = %d, want 5", callCount.Load())
	}
}

func TestDispatchParallelWithErrors(t *testing.T) {
	ts := NewSet()
	if err := ts.Register(newMockTool("good")); err != nil {
		t.Fatal(err)
	}

	calls := []Call{
		{ID: "1", Name: "good", Arguments: "{}"},
		{ID: "2", Name: "missing", Arguments: "{}"},
		{ID: "3", Name: "good", Arguments: "{}"},
	}

	results := ts.DispatchParallel(context.Background(), calls, &Context{})
	if results[0].Status != StatusSuccess {
		t.Errorf("result[0] should be success")
	}
	if results[1].Status != StatusError {
		t.Errorf("result[1] should be error for missing tool")
	}
	if results[2].Status != StatusSuccess {
		t.Errorf("result[2] should be success")
	}
}

func TestDispatchRespectsContextCancellation(t *testing.T) {
	ts := NewSet()
	mt := newMockTool("slow")
	mt.execFn = func(ctx context.Context, _ map[string]any, _ *Context) (*Result, error) {
		<-ctx.Done()
		return Error("cancelled"), ctx.Err()
	}
	if err := ts.Register(mt); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := ts.Dispatch(ctx, Call{Name: "slow", Arguments: "{}"}, &Context{})
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestToLLMDefinitions(t *testing.T) {
	ts := NewSet()
	if err := ts.Register(newMockTool("tool_a")); err != nil {
		t.Fatal(err)
	}
	if err := ts.Register(newMockTool("tool_b")); err != nil {
		t.Fatal(err)
	}

	defs := ts.ToLLMDefinitions()
	if len(defs) != 2 {
		t.Fatalf("expected 2 definitions, got %d", len(defs))
	}

	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
		if d.Parameters == nil {
			t.Errorf("tool %q has nil parameters", d.Name)
		}
	}
	if !names["tool_a"] || !names["tool_b"] {
		t.Errorf("missing tools: %v", names)
	}
}

func TestDispatchValidatesArgTypes(t *testing.T) {
	ts := NewSet()
	mt := &mockTool{
		name:        "typed",
		description: "A tool with typed parameters",
		params: &JSONSchema{
			Type: "object",
			Properties: map[string]any{
				"count":   map[string]any{"type": "integer"},
				"ratio":   map[string]any{"type": "number"},
				"label":   map[string]any{"type": "string"},
				"enabled": map[string]any{"type": "boolean"},
				"items":   map[string]any{"type": "array"},
				"meta":    map[string]any{"type": "object"},
			},
		},
		execFn: func(_ context.Context, _ map[string]any, _ *Context) (*Result, error) {
			return OK("ok"), nil
		},
	}
	if err := ts.Register(mt); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		args    string
		wantErr bool
	}{
		{"integer_ok", `{"count":5}`, false},
		{"integer_from_whole_float", `{"count":5.0}`, false},
		{"integer_rejects_string", `{"count":"5"}`, true},
		{"integer_rejects_bool", `{"count":true}`, true},
		{"integer_rejects_fractional", `{"count":5.5}`, true},
		{"number_ok", `{"ratio":0.25}`, false},
		{"number_rejects_string", `{"ratio":"half"}`, true},
		{"string_ok", `{"label":"hello"}`, false},
		{"string_rejects_integer", `{"label":42}`, true},
		{"boolean_ok", `{"enabled":true}`, false},
		{"boolean_rejects_string", `{"enabled":"yes"}`, true},
		{"array_ok", `{"items":[1,2]}`, false},
		{"array_rejects_object", `{"items":{"a":1}}`, true},
		{"object_ok", `{"meta":{"k":"v"}}`, false},
		{"object_rejects_array", `{"meta":[1,2]}`, true},
		{"null_skipped", `{"count":null}`, false},
		{"absent_skipped", `{}`, false},
	}
	ctx := t.Context()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ts.Dispatch(ctx, Call{Name: "typed", Arguments: tc.args}, &Context{})
			if err != nil {
				t.Fatalf("Dispatch returned err: %v", err)
			}
			gotErr := res.Status == StatusError
			if gotErr != tc.wantErr {
				t.Errorf("args=%s: status=%q message=%q, wantErr=%v", tc.args, res.Status, res.Message, tc.wantErr)
			}
		})
	}
}

func TestDispatchCoerceSatisfiesRequired(t *testing.T) {
	ts := NewSet()
	mt := &mockTool{
		name:        "needsItems",
		description: "A tool requiring an array parameter",
		params: &JSONSchema{
			Type:       "object",
			Properties: map[string]any{"items": map[string]any{"type": "array"}},
			Required:   []string{"items"},
		},
		execFn: func(_ context.Context, _ map[string]any, _ *Context) (*Result, error) {
			return OK("ok"), nil
		},
	}
	if err := ts.Register(mt); err != nil {
		t.Fatal(err)
	}
	res, err := ts.Dispatch(t.Context(), Call{Name: "needsItems", Arguments: `{"items":"[1,2,3]"}`}, &Context{})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Status == StatusError {
		t.Errorf("stringified required array rejected: %q", res.Message)
	}
}

func TestListTools_SortedByNameForStableCache(t *testing.T) {
	ts := NewSet()
	for _, name := range []string{"delta", "alpha", "charlie", "bravo"} {
		if err := ts.Register(newMockTool(name)); err != nil {
			t.Fatal(err)
		}
	}

	tools := ts.ListTools()
	got := make([]string, len(tools))
	for i, tl := range tools {
		got[i] = tl.Name()
	}

	want := []string{"alpha", "bravo", "charlie", "delta"}
	if !slices.Equal(got, want) {
		t.Errorf("ListTools must be sorted by name — it feeds the system prompt's tool-instructions section, part of the cache prefix; got %v want %v", got, want)
	}
}
