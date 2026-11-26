// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"log/slog"
	"sync"
	"testing"
)

type effortWarnRecorder struct {
	mu     sync.Mutex
	values []string
}

func (r *effortWarnRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *effortWarnRecorder) Handle(_ context.Context, rec slog.Record) error {
	if rec.Message != "invalid thinking.effort, ignoring" {
		return nil
	}
	value := ""
	rec.Attrs(func(a slog.Attr) bool {
		if a.Key == "value" {
			value = a.Value.String()
		}
		return true
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values = append(r.values, value)
	return nil
}

func (r *effortWarnRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *effortWarnRecorder) WithGroup(string) slog.Handler      { return r }

func (r *effortWarnRecorder) rejections() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.values...)
}

func captureEffortWarnings(t *testing.T) *effortWarnRecorder {
	t.Helper()
	rec := &effortWarnRecorder{}
	previous := slog.Default()
	slog.SetDefault(slog.New(rec))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return rec
}

func TestExtractReasoningEffortWarnsOnNonString(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"int", 3, "3"},
		{"bool", true, "true"},
		{"nested map", map[string]any{"level": "high"}, "map[level:high]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := captureEffortWarnings(t)
			if got := extractReasoningEffort(map[string]any{
				"thinking": map[string]any{"effort": tc.value},
			}); got != "" {
				t.Fatalf("extractReasoningEffort() = %q, want %q", got, "")
			}
			got := rec.rejections()
			if len(got) != 1 {
				t.Fatalf("warned %v, want exactly one rejection warning", got)
			}
			if got[0] != tc.want {
				t.Errorf("warning named value %q, want %q", got[0], tc.want)
			}
		})
	}
}

func TestExtractReasoningEffortDoesNotWarnOnUnset(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  any
	}{
		{"explicit nil", map[string]any{"thinking": map[string]any{"effort": nil}}},
		{"empty string", map[string]any{"thinking": map[string]any{"effort": ""}}},
		{"whitespace", map[string]any{"thinking": map[string]any{"effort": "  "}}},
		{"no effort key", map[string]any{"thinking": map[string]any{"budget_tokens": 2048}}},
		{"no thinking block", map[string]any{"model": "claude"}},
		{"not a map", "claude-opus"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := captureEffortWarnings(t)
			extractReasoningEffort(tc.raw)
			if got := rec.rejections(); len(got) != 0 {
				t.Errorf("warned %v, want no warning for an unset effort", got)
			}
		})
	}
}

func TestExtractReasoningEffort(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  any
		want string
	}{
		{"high", map[string]any{"thinking": map[string]any{"effort": "high"}}, "high"},
		{"minimal", map[string]any{"thinking": map[string]any{"effort": "minimal"}}, "minimal"},
		{"none is a level", map[string]any{"thinking": map[string]any{"effort": "none"}}, "none"},
		{"case and space tolerated", map[string]any{"thinking": map[string]any{"effort": " High "}}, "high"},
		{"unknown level dropped", map[string]any{"thinking": map[string]any{"effort": "extreme"}}, ""},
		{"empty is unset, not a typo", map[string]any{"thinking": map[string]any{"effort": ""}}, ""},
		{"whitespace is unset, not a typo", map[string]any{"thinking": map[string]any{"effort": "  "}}, ""},
		{"wrong type dropped", map[string]any{"thinking": map[string]any{"effort": 3}}, ""},
		{"bool dropped", map[string]any{"thinking": map[string]any{"effort": true}}, ""},
		{"nested map dropped", map[string]any{"thinking": map[string]any{"effort": map[string]any{"level": "high"}}}, ""},
		{"explicit nil is unset", map[string]any{"thinking": map[string]any{"effort": nil}}, ""},
		{"no effort key", map[string]any{"thinking": map[string]any{"budget_tokens": 2048}}, ""},
		{"no thinking block", map[string]any{"model": "claude"}, ""},
		{"not a map", "claude-opus", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractReasoningEffort(tc.raw); got != tc.want {
				t.Errorf("extractReasoningEffort() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEffortAndBudgetParseIndependently(t *testing.T) {
	raw := map[string]any{"thinking": map[string]any{
		"effort":        "low",
		"budget_tokens": 4096,
	}}
	if got := extractReasoningEffort(raw); got != "low" {
		t.Errorf("effort = %q, want %q", got, "low")
	}
	if got := extractReasoningBudgetTokens(raw); got != 4096 {
		t.Errorf("budget = %d, want 4096", got)
	}
}
