// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import "testing"

func TestReasoningEffortAndBudgetAreMutuallyExclusive(t *testing.T) {
	for _, tc := range []struct {
		name       string
		reasoning  *ReasoningConfig
		wantEffort string
		wantBudget int
	}{
		{"effort only", &ReasoningConfig{Effort: "high"}, "high", 0},
		{"budget only", &ReasoningConfig{BudgetTokens: 2048}, "", 2048},
		{"both set, effort wins", &ReasoningConfig{Effort: "low", BudgetTokens: 2048}, "low", 0},
		{"neither set", &ReasoningConfig{}, "", 0},
		{"nil", nil, "", 0},
		{"unrecognised level alone", &ReasoningConfig{Effort: "ludicrous"}, "", 0},
		{"unrecognised level falls through to budget", &ReasoningConfig{Effort: "ludicrous", BudgetTokens: 2048}, "", 2048},
		{"level is trimmed and case folded", &ReasoningConfig{Effort: " High "}, "high", 0},
		{"xhigh survives to the reasoning params", &ReasoningConfig{Effort: "xhigh"}, "xhigh", 0},
		{"max wins over a co-present budget", &ReasoningConfig{Effort: "max", BudgetTokens: 8192}, "max", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := buildReasoningParams(tc.reasoning)
			if params == nil {
				if tc.wantEffort != "" || tc.wantBudget != 0 {
					t.Fatalf("no reasoning params emitted, want effort %q budget %d",
						tc.wantEffort, tc.wantBudget)
				}
				return
			}
			gotEffort := ""
			if params.Effort != nil {
				gotEffort = *params.Effort
			}
			gotBudget := 0
			if params.MaxTokens != nil {
				gotBudget = *params.MaxTokens
			}
			if gotEffort != tc.wantEffort {
				t.Errorf("effort = %q, want %q", gotEffort, tc.wantEffort)
			}
			if gotBudget != tc.wantBudget {
				t.Errorf("budget = %d, want %d", gotBudget, tc.wantBudget)
			}
			if (params.Effort != nil) == (params.MaxTokens != nil) {
				t.Error("exactly one reasoning field must be set")
			}
		})
	}
}

func TestNormalizeReasoningEffortCoversTheProviderVocabulary(t *testing.T) {
	for _, level := range []string{"low", "medium", "high", "xhigh", "max"} {
		if got := NormalizeReasoningEffort(level); got != level {
			t.Errorf("NormalizeReasoningEffort(%q) = %q, want it accepted: the provider takes it",
				level, got)
		}
	}

	for _, level := range []string{"minimal", "none"} {
		if got := NormalizeReasoningEffort(level); got != level {
			t.Errorf("NormalizeReasoningEffort(%q) = %q, want it kept", level, got)
		}
	}

	for _, level := range []string{"adaptive", "ludicrous", "", "  "} {
		if got := NormalizeReasoningEffort(level); got != "" {
			t.Errorf("NormalizeReasoningEffort(%q) = %q, want it dropped", level, got)
		}
	}

	if got := NormalizeReasoningEffort(" XHigh "); got != "xhigh" {
		t.Errorf("NormalizeReasoningEffort(\" XHigh \") = %q, want trimmed and folded", got)
	}
}
