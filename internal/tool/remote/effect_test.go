// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestTool_EffectClassFromInit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		init string
		want tool.EffectClass
	}{
		{
			name: "declared read_only",
			init: `{"tool_name":"lookup","description":"d","parameters":{"type":"object"},"capabilities":null,"effect":"read_only"}`,
			want: tool.DeclaredEffect(tool.EffectReadOnly),
		},
		{
			name: "declared side_effecting",
			init: `{"tool_name":"lookup","description":"d","parameters":{"type":"object"},"capabilities":null,"effect":"side_effecting"}`,
			want: tool.DeclaredEffect(tool.EffectSideEffecting),
		},
		{
			name: "an older STR that sends no effect",
			init: `{"tool_name":"lookup","description":"d","parameters":{"type":"object"},"capabilities":null}`,
			want: tool.DefaultEffectClass,
		},
		{
			name: "an unrecognised value",
			init: `{"tool_name":"lookup","description":"d","parameters":{"type":"object"},"capabilities":null,"effect":"mostly_harmless"}`,
			want: tool.DefaultEffectClass,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var params remotetool.InitParams
			if err := json.Unmarshal([]byte(tc.init), &params); err != nil {
				t.Fatal(err)
			}
			rt := NewTool(&ToolConfig{ToolName: "lookup"}, nil)
			if got := tool.EffectClassOf(rt); got != tool.DefaultEffectClass {
				t.Fatalf("before init, EffectClassOf = %+v, want default", got)
			}
			rt.ApplyInitParams(&params)
			if got := tool.EffectClassOf(rt); got != tc.want {
				t.Fatalf("EffectClassOf = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestTool_AnInitWithoutEffectDropsAPreviouslyAppliedEffect(t *testing.T) {
	t.Parallel()
	rt := NewTool(&ToolConfig{ToolName: "lookup"}, nil)
	rt.ApplyInitParams(&remotetool.InitParams{ToolName: "lookup", Description: "d", Effect: remotetool.EffectReadOnly})
	if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectReadOnly) {
		t.Fatalf("after the declaring init, EffectClassOf = %+v, want declared read_only", got)
	}
	rt.ApplyInitParams(&remotetool.InitParams{ToolName: "lookup", Description: "d"})
	if got := tool.EffectClassOf(rt); got != tool.DefaultEffectClass {
		t.Fatalf("after an init without effect, EffectClassOf = %+v, want the default", got)
	}
}

func TestTool_ABroadcastTightensAConfigAwareEffectWithoutWaitingForTheRefresh(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		requested string
		broadcast string
		want      tool.EffectClass
	}{
		{"side_effecting tightens read_only", remotetool.EffectReadOnly, remotetool.EffectSideEffecting, tool.DeclaredEffect(tool.EffectSideEffecting)},
		{"a broadcast that drops the effect tightens read_only to the default", remotetool.EffectReadOnly, "", tool.DefaultEffectClass},
		{"read_only does not loosen side_effecting", remotetool.EffectSideEffecting, remotetool.EffectReadOnly, tool.DeclaredEffect(tool.EffectSideEffecting)},
		{"read_only does not loosen an undeclared tool", "", remotetool.EffectReadOnly, tool.DefaultEffectClass},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rt := NewTool(&ToolConfig{ToolName: "lookup"}, nil)
			rt.applyInit(&remotetool.InitParams{ToolName: "lookup", Description: "d", Effect: tc.requested}, true)
			refreshNeeded := rt.applyInit(&remotetool.InitParams{ToolName: "lookup", Description: "d2", Effect: tc.broadcast}, false)
			if !refreshNeeded {
				t.Fatal("a differing broadcast over a config-aware init asked for no refresh")
			}
			if got := tool.EffectClassOf(rt); got != tc.want {
				t.Fatalf("config-aware %q then broadcast %q: EffectClassOf = %+v, want %+v", tc.requested, tc.broadcast, got, tc.want)
			}
		})
	}
}

func TestComputeInitFingerprint_ChangesWithEffect(t *testing.T) {
	t.Parallel()
	a := &remotetool.InitParams{ToolName: "t", Description: "d"}
	b := &remotetool.InitParams{ToolName: "t", Description: "d", Effect: remotetool.EffectReadOnly}
	if computeInitFingerprint(a) == computeInitFingerprint(b) {
		t.Fatal("an init that only adds an effect was deduplicated away")
	}
}
