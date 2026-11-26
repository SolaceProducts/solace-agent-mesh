// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestResolveReasoningTierScoped(t *testing.T) {
	tests := []struct {
		name       string
		override   reasoningTier
		static     reasoningTier
		binding    reasoningTier
		wantEffort string
		wantBudget int
	}{
		{
			name:       "override budget beats a static effort",
			override:   reasoningTier{budget: 4096},
			static:     reasoningTier{effort: "high"},
			wantBudget: 4096,
		},
		{
			name:       "override effort beats a static budget",
			override:   reasoningTier{effort: "low"},
			static:     reasoningTier{budget: 4096},
			wantEffort: "low",
		},
		{
			name:       "effort wins inside the tier that speaks",
			override:   reasoningTier{effort: "medium", budget: 4096},
			wantEffort: "medium",
		},
		{
			name:       "static tier answers when no override speaks",
			static:     reasoningTier{effort: "high"},
			binding:    reasoningTier{budget: 8192},
			wantEffort: "high",
		},
		{
			name:       "binding answers when nothing above it speaks",
			binding:    reasoningTier{budget: 8192},
			wantBudget: 8192,
		},
		{
			name:       "override none turns reasoning off rather than falling through",
			override:   reasoningTier{effort: "none"},
			static:     reasoningTier{budget: 4096},
			wantEffort: "none",
		},
		{
			name: "silence everywhere leaves reasoning unset",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := resolveReasoningTier(tc.override, tc.static, tc.binding)
			if tc.wantEffort == "" && tc.wantBudget == 0 {
				if got != nil {
					t.Fatalf("reasoning = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("reasoning = nil, want effort %q budget %d",
					tc.wantEffort, tc.wantBudget)
			}
			if got.Effort != tc.wantEffort {
				t.Errorf("effort = %q, want %q", got.Effort, tc.wantEffort)
			}
			if got.BudgetTokens != tc.wantBudget {
				t.Errorf("budget = %d, want %d", got.BudgetTokens, tc.wantBudget)
			}
		})
	}
}

func TestResolveReasoningNeverSendsBoth(t *testing.T) {
	got, _ := resolveReasoningTier(reasoningTier{effort: "high", budget: 4096})
	if got == nil {
		t.Fatal("reasoning = nil, want a config")
	}
	if got.Effort != "high" {
		t.Errorf("effort = %q, want %q", got.Effort, "high")
	}
	if got.BudgetTokens != 0 {
		t.Errorf("budget = %d, want 0; the provider would silently drop the effort", got.BudgetTokens)
	}
}

func TestResolveReasoningTierSource(t *testing.T) {
	override := reasoningTier{source: "taskOverride", effort: "low"}
	static := reasoningTier{source: "agentConfig", effort: "high"}
	binding := reasoningTier{source: "modelBinding", budget: 8192}
	silent := func(source string) reasoningTier { return reasoningTier{source: source} }

	tests := []struct {
		name       string
		tiers      []reasoningTier
		wantEffort string
		wantBudget int
		wantSource string
	}{
		{
			name:       "override answers",
			tiers:      []reasoningTier{override, static, binding},
			wantEffort: "low",
			wantSource: "taskOverride",
		},
		{
			name:       "static answers",
			tiers:      []reasoningTier{silent("taskOverride"), static, binding},
			wantEffort: "high",
			wantSource: "agentConfig",
		},
		{
			name:       "binding answers",
			tiers:      []reasoningTier{silent("taskOverride"), silent("agentConfig"), binding},
			wantBudget: 8192,
			wantSource: "modelBinding",
		},
		{
			name:       "nothing answers",
			tiers:      []reasoningTier{silent("taskOverride"), silent("agentConfig"), silent("modelBinding")},
			wantSource: reasoningSourceUnset,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, answering := resolveReasoningTier(tc.tiers...)
			if answering.source != tc.wantSource {
				t.Errorf("source = %q, want %q", answering.source, tc.wantSource)
			}
			if tc.wantEffort == "" && tc.wantBudget == 0 {
				if got != nil {
					t.Fatalf("reasoning = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("reasoning = nil, want effort %q budget %d", tc.wantEffort, tc.wantBudget)
			}
			if got.Effort != tc.wantEffort {
				t.Errorf("effort = %q, want %q", got.Effort, tc.wantEffort)
			}
			if got.BudgetTokens != tc.wantBudget {
				t.Errorf("budget = %d, want %d", got.BudgetTokens, tc.wantBudget)
			}
		})
	}
}

type warnRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (w *warnRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (w *warnRecorder) Handle(_ context.Context, r slog.Record) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	value := ""
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "value" {
			value = a.Value.String()
		}
		return true
	})
	w.lines = append(w.lines, r.Message+"|"+value)
	return nil
}

func (w *warnRecorder) WithAttrs([]slog.Attr) slog.Handler { return w }
func (w *warnRecorder) WithGroup(string) slog.Handler      { return w }

func (w *warnRecorder) rejections() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, l := range w.lines {
		if strings.HasPrefix(l, "invalid thinking.effort, ignoring|") {
			out = append(out, strings.TrimPrefix(l, "invalid thinking.effort, ignoring|"))
		}
	}
	return out
}

func TestBindingEffortWarningDebouncesByValue(t *testing.T) {
	rec := &warnRecorder{}
	agent := &Agent{}
	agent.logger = slog.New(rec)
	agent.cfg.ModelAlias = "planning"

	for i := 0; i < 5; i++ {
		agent.warnOnNewRejectedBindingEffort("hgih", true)
	}
	if got := rec.rejections(); len(got) != 1 || got[0] != "hgih" {
		t.Fatalf("repeats of one typo warned %v, want exactly one naming the value", got)
	}

	agent.warnOnNewRejectedBindingEffort("medum", true)
	if got := rec.rejections(); len(got) != 2 || got[1] != "medum" {
		t.Fatalf("a second, different typo warned %v, want it reported", got)
	}

	agent.warnOnNewRejectedBindingEffort("", false)
	agent.warnOnNewRejectedBindingEffort("medum", true)
	if got := rec.rejections(); len(got) != 3 {
		t.Fatalf("re-broken binding warned %v, want the earlier value reported again", got)
	}
}

func TestRejectedBindingEffortRenderingEmptyStillWarns(t *testing.T) {
	rec := &warnRecorder{}
	agent := &Agent{}
	agent.logger = slog.New(rec)
	agent.cfg.ModelAlias = "planning"

	for i := 0; i < 3; i++ {
		agent.warnOnNewRejectedBindingEffort("", true)
	}
	if got := rec.rejections(); len(got) != 1 || got[0] != "" {
		t.Fatalf("empty-rendering rejection warned %v, want exactly one warning", got)
	}
}

func TestTaskModelOverrideWarnsWithTheOffendingEffort(t *testing.T) {
	agent := boundAgentForCacheTest(t, map[string]any{
		"model":    "openai/claude-sonnet-4-6",
		"thinking": map[string]any{"effort": "hgih"},
	})

	rec := &warnRecorder{}
	_, _ = agent.resolveTaskModelOverride(t.Context(), map[string]any{
		"model_override": map[string]any{"model_id": "planning"},
	}, slog.New(rec))

	got := rec.rejections()
	if len(got) != 1 || got[0] != "hgih" {
		t.Errorf("rejections = %q, want exactly one naming the offending value", got)
	}
}
