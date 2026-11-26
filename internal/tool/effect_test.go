// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"
)

type classifiedTool struct {
	*mockTool
	class EffectClass
}

func (c *classifiedTool) EffectClass() EffectClass { return c.class }

type perCallTool struct {
	*classifiedTool
	perCall func(args map[string]any) EffectClass
}

func (p *perCallTool) CallEffectClass(args map[string]any) EffectClass { return p.perCall(args) }

func TestEffectClass_ReadOnlyOnlyWhenPositivelyReadOnly(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		class EffectClass
		want  bool
	}{
		{"declared read_only", DeclaredEffect(EffectReadOnly), true},
		{"derived read_only", DerivedEffect(EffectReadOnly), true},
		{"hinted read_only", HintedEffect(EffectReadOnly), true},
		{"declared side_effecting", DeclaredEffect(EffectSideEffecting), false},
		{"default", DefaultEffectClass, false},
		{"zero value", EffectClass{}, false},
		{"read_only with no source", EffectClass{Effect: EffectReadOnly}, false},
		{"read_only with an unknown source", EffectClass{Effect: EffectReadOnly, Source: "vibes"}, false},
		{"unknown effect", EffectClass{Effect: "readonly", Source: EffectSourceDeclared}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.class.ReadOnly(); got != tc.want {
				t.Fatalf("ReadOnly() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEffectClassOf_UnclassifiedToolIsDefaultSideEffecting(t *testing.T) {
	t.Parallel()
	got := EffectClassOf(newMockTool("plain"))
	if got != DefaultEffectClass {
		t.Fatalf("EffectClassOf(unclassified) = %+v, want %+v", got, DefaultEffectClass)
	}
	if got.ReadOnly() {
		t.Fatal("an unclassified tool must not read as read-only")
	}
}

func TestEffectClassOf_InvalidSelfClassificationFallsBackToDefault(t *testing.T) {
	t.Parallel()
	bad := &classifiedTool{mockTool: newMockTool("bad"), class: EffectClass{Effect: "maybe", Source: EffectSourceDeclared}}
	if got := EffectClassOf(bad); got != DefaultEffectClass {
		t.Fatalf("EffectClassOf(invalid) = %+v, want default", got)
	}
}

func TestResolveEffect_PrecedenceOrder(t *testing.T) {
	t.Parallel()
	operator := EffectClass{Effect: EffectSideEffecting, Source: EffectSourceOperator}
	declared := DeclaredEffect(EffectReadOnly)
	derived := DerivedEffect(EffectSideEffecting)
	hint := HintedEffect(EffectReadOnly)

	for _, tc := range []struct {
		name       string
		candidates []EffectClass
		want       EffectClass
	}{
		{"operator beats everything", []EffectClass{hint, derived, declared, operator}, operator},
		{"declared beats derived and hint", []EffectClass{hint, derived, declared}, declared},
		{"derived beats hint", []EffectClass{hint, derived}, derived},
		{"hint beats default", []EffectClass{DefaultEffectClass, hint}, hint},
		{"nothing valid is default", []EffectClass{{}, {Effect: "x", Source: EffectSourceDeclared}}, DefaultEffectClass},
		{"no candidates is default", nil, DefaultEffectClass},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ResolveEffect(tc.candidates...); got != tc.want {
				t.Fatalf("ResolveEffect = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCallEffectClassOf_PerCallRefinesAndFallsBack(t *testing.T) {
	t.Parallel()
	tl := &perCallTool{
		classifiedTool: &classifiedTool{mockTool: newMockTool("query"), class: DeclaredEffect(EffectSideEffecting)},
		perCall: func(args map[string]any) EffectClass {
			if args["mode"] == "read" {
				return DeclaredEffect(EffectReadOnly)
			}
			return EffectClass{}
		},
	}
	if got := CallEffectClassOf(tl, map[string]any{"mode": "read"}); !got.ReadOnly() {
		t.Fatalf("per-call read = %+v, want read_only", got)
	}
	if got := CallEffectClassOf(tl, map[string]any{"mode": "write"}); got != DeclaredEffect(EffectSideEffecting) {
		t.Fatalf("per-call with no answer = %+v, want the static class", got)
	}
	if got := CallEffectClassOf(newMockTool("plain"), nil); got != DefaultEffectClass {
		t.Fatalf("CallEffectClassOf(unclassified) = %+v, want default", got)
	}
}

func TestSet_EffectClassFor_OperatorOverrideTightens(t *testing.T) {
	t.Parallel()
	ts := NewSet()
	reader := &classifiedTool{mockTool: newMockTool("reader"), class: DeclaredEffect(EffectReadOnly)}
	other := &classifiedTool{mockTool: newMockTool("other"), class: DeclaredEffect(EffectReadOnly)}
	for _, tl := range []Tool{reader, other} {
		if err := ts.Register(tl); err != nil {
			t.Fatal(err)
		}
	}
	overrides, err := ParseEffectOverrides(nil, []HILEntry{{
		ToolMap:   map[string]any{"tool_type": "builtin", "tool_name": "reader", EffectConfigKey: "side_effecting"},
		ToolNames: []string{"reader"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ts.SetEffectOverrides(overrides)

	got, ok := ts.EffectClassFor("reader")
	want := EffectClass{Effect: EffectSideEffecting, Source: EffectSourceOperator}
	if !ok || got != want {
		t.Fatalf("EffectClassFor(reader) = %+v, %v; want %+v", got, ok, want)
	}
	if got, _ := ts.EffectClassFor("other"); got != DeclaredEffect(EffectReadOnly) {
		t.Fatalf("EffectClassFor(other) = %+v, want its own declaration", got)
	}
	if _, ok := ts.EffectClassFor("missing"); ok {
		t.Fatal("EffectClassFor(missing) reported a registered tool")
	}

	ts.SetEffectOverrides(nil)
	if got, _ := ts.EffectClassFor("reader"); got != DeclaredEffect(EffectReadOnly) {
		t.Fatalf("after clearing overrides, EffectClassFor(reader) = %+v, want its declaration", got)
	}
}

func TestSet_CallEffectClassFor_OperatorBeatsPerCall(t *testing.T) {
	t.Parallel()
	ts := NewSet()
	tl := &perCallTool{
		classifiedTool: &classifiedTool{mockTool: newMockTool("query"), class: DeclaredEffect(EffectSideEffecting)},
		perCall:        func(map[string]any) EffectClass { return DeclaredEffect(EffectReadOnly) },
	}
	if err := ts.Register(tl); err != nil {
		t.Fatal(err)
	}
	if got, _ := ts.CallEffectClassFor("query", nil); !got.ReadOnly() {
		t.Fatalf("without override, per-call = %+v, want read_only", got)
	}
	ts.SetEffectOverrides(map[string]EffectClass{"query": {Effect: EffectSideEffecting, Source: EffectSourceOperator}})
	if got, _ := ts.CallEffectClassFor("query", nil); got.ReadOnly() || got.Source != EffectSourceOperator {
		t.Fatalf("with override, per-call = %+v, want operator side_effecting", got)
	}
}

type resolvedEffectProbe struct {
	*classifiedTool
	seen []EffectClass
}

func (p *resolvedEffectProbe) ExecuteResolved(_ context.Context, _ map[string]any, _ *Context, effect EffectClass) (*Result, error) {
	p.seen = append(p.seen, effect)
	return OK("ok"), nil
}

func TestSet_Dispatch_HandsAResolvedEffectExecutorTheOperatorResolvedClass(t *testing.T) {
	t.Parallel()
	ts := NewSet()
	probe := &resolvedEffectProbe{classifiedTool: &classifiedTool{mockTool: newMockTool("reader"), class: DeclaredEffect(EffectReadOnly)}}
	if err := ts.Register(probe); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Dispatch(t.Context(), Call{Name: "reader", Arguments: "{}"}, nil); err != nil {
		t.Fatal(err)
	}
	ts.SetEffectOverrides(map[string]EffectClass{"reader": {Effect: EffectSideEffecting, Source: EffectSourceOperator}})
	if _, err := ts.Dispatch(t.Context(), Call{Name: "reader", Arguments: "{}"}, nil); err != nil {
		t.Fatal(err)
	}
	want := []EffectClass{DeclaredEffect(EffectReadOnly), {Effect: EffectSideEffecting, Source: EffectSourceOperator}}
	if !slices.Equal(probe.seen, want) {
		t.Fatalf("effects handed to the tool = %+v, want %+v", probe.seen, want)
	}
}

func TestSet_ExecuteBuiltin_ResolvesTheOperatorOverrideForAToolHeldByTheCaller(t *testing.T) {
	t.Parallel()
	ts := NewSet()
	probe := &resolvedEffectProbe{classifiedTool: &classifiedTool{mockTool: newMockTool("reader"), class: DeclaredEffect(EffectReadOnly)}}
	if err := ts.Register(probe); err != nil {
		t.Fatal(err)
	}
	ts.SetEffectOverrides(map[string]EffectClass{"reader": {Effect: EffectSideEffecting, Source: EffectSourceOperator}})
	if _, err := ts.ExecuteBuiltin(t.Context(), "reader", probe, map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	want := EffectClass{Effect: EffectSideEffecting, Source: EffectSourceOperator}
	if len(probe.seen) != 1 || probe.seen[0] != want {
		t.Fatalf("effects handed to the tool = %+v, want [%+v]", probe.seen, want)
	}
}

type plainBuiltin struct {
	*mockTool
	ran bool
}

func (p *plainBuiltin) Execute(context.Context, map[string]any, *Context) (*Result, error) {
	p.ran = true
	return OK("ok"), nil
}

func TestExecuteWithEffect_AToolThatIgnoresTheClassStillRuns(t *testing.T) {
	t.Parallel()
	plain := &plainBuiltin{mockTool: newMockTool("plain")}
	if _, err := ExecuteWithEffect(t.Context(), plain, nil, nil, DefaultEffectClass); err != nil || !plain.ran {
		t.Fatalf("ran = %v, err = %v, want the tool's own Execute to run", plain.ran, err)
	}
}

func TestParseEffectOverrides(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		value   any
		wantErr string
	}{
		{name: "side_effecting applies to every registered name", value: "side_effecting"},
		{name: "read_only is refused", value: "read_only", wantErr: `may only be "side_effecting"`},
		{name: "unknown value is refused", value: "write", wantErr: `effect "write" is not one of`},
		{name: "non-string is refused", value: true, wantErr: "effect must be a string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseEffectOverrides(nil, []HILEntry{
				{ToolMap: map[string]any{"tool_type": "mcp", EffectConfigKey: tc.value}, ToolNames: []string{"a", "b"}},
				{ToolMap: map[string]any{"tool_type": "builtin", "tool_name": "c"}, ToolNames: []string{"c"}},
			})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := EffectClass{Effect: EffectSideEffecting, Source: EffectSourceOperator}
			if len(got) != 2 || got["a"] != want || got["b"] != want {
				t.Fatalf("overrides = %+v, want a and b side_effecting by operator only", got)
			}
		})
	}
}

func TestParseEffectOverrides_WarnsWhenAnEntryWithEffectRegisteredNoTools(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		entry    HILEntry
		wantWarn bool
	}{
		{
			name:     "an entry that registered no tools",
			entry:    HILEntry{ToolMap: map[string]any{"tool_type": "mcp", EffectConfigKey: "side_effecting"}, Degraded: true},
			wantWarn: true,
		},
		{
			name:     "an entry carrying no tool names",
			entry:    HILEntry{ToolMap: map[string]any{"tool_type": "mcp", EffectConfigKey: "side_effecting"}},
			wantWarn: true,
		},
		{
			name: "a degraded entry that registered some tools",
			entry: HILEntry{
				ToolMap: map[string]any{"tool_type": "mcp", EffectConfigKey: "side_effecting"}, ToolNames: []string{"a"}, Degraded: true,
			},
			wantWarn: true,
		},
		{
			name:  "a healthy entry",
			entry: HILEntry{ToolMap: map[string]any{"tool_type": "mcp", EffectConfigKey: "side_effecting"}, ToolNames: []string{"a"}},
		},
		{
			name:  "a degraded entry with no effect",
			entry: HILEntry{ToolMap: map[string]any{"tool_type": "mcp"}, Degraded: true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var logged strings.Builder
			log := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))
			if _, err := ParseEffectOverrides(log, []HILEntry{tc.entry}); err != nil {
				t.Fatalf("ParseEffectOverrides(%+v): %v", tc.entry, err)
			}
			if got := strings.Contains(logged.String(), "effect"); got != tc.wantWarn {
				t.Fatalf("warned = %v, want %v; log: %q", got, tc.wantWarn, logged.String())
			}
		})
	}
}

func TestParseEffect(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"read_only", "side_effecting"} {
		if e, err := ParseEffect(s); err != nil || string(e) != s {
			t.Fatalf("ParseEffect(%q) = %q, %v", s, e, err)
		}
	}
	for _, s := range []string{"", "READ_ONLY", "readonly", "unknown"} {
		if _, err := ParseEffect(s); err == nil {
			t.Fatalf("ParseEffect(%q) accepted an invalid value", s)
		}
	}
}
