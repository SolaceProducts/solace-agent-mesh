// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"io"
	"log/slog"
	"testing"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestResolveOnboarding_HeadlessNeverBlocks(t *testing.T) {
	tests := []struct {
		name         string
		generalModel string
		noOnboarding bool
	}{
		{name: "llm env present", generalModel: "openai/gpt-4o", noOnboarding: false},
		{name: "no env, onboarding disabled", generalModel: "", noOnboarding: true},
		{name: "no env, no tty falls through to placeholder", generalModel: "", noOnboarding: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.generalModel != "" {
				t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", tc.generalModel)
			}
			s, err := resolveOnboarding(tc.noOnboarding, quietLogger())
			if err != nil {
				t.Fatalf("resolveOnboarding returned error: %v", err)
			}
			if s == nil {
				t.Fatal("resolveOnboarding returned nil settings; expected a value to inject")
			}
			if !s.Onboarded {
				t.Error("expected Onboarded=true so the wizard is not re-triggered next boot")
			}
		})
	}
}

func TestHasLLMEnv(t *testing.T) {
	t.Run("planning model satisfies", func(t *testing.T) {
		t.Setenv("LLM_SERVICE_PLANNING_MODEL_NAME", "openai/gpt-4o")
		if !hasLLMEnv() {
			t.Error("expected hasLLMEnv true when planning model is set")
		}
	})
}
