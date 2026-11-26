// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
)

func TestWebUIBaseURL_Sources(t *testing.T) {
	tests := []struct {
		name        string
		env         string
		redirectURL string
		want        string
	}{
		{name: "neither configured yields no address", want: ""},
		{name: "cluster WebUI base wins", env: "https://sam.example.com", redirectURL: "https://other.example.com/cb", want: "https://sam.example.com"},
		{name: "trailing slash trimmed", env: "https://sam.example.com/", want: "https://sam.example.com"},
		{name: "redirect URL falls back to its origin", redirectURL: "https://sam.example.com/api/v1/auth/callback", want: "https://sam.example.com"},
		{name: "a schemeless value is not a usable link", env: "sam.example.com", want: ""},
		{name: "an empty redirect URL yields nothing", redirectURL: "", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WEBUI_FRONTEND_SERVER_URL", tc.env)
			core := &GatewayCore{cfg: &GatewayConfig{FrontendRedirectURL: tc.redirectURL}}
			if got := core.webUIBaseURL(); got != tc.want {
				t.Errorf("webUIBaseURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAccessHint_OffersSignInOnlyWhenItWouldWork(t *testing.T) {
	const base = "https://sam.example.com"

	t.Run("email-keyed surface with a configured address offers sign-in", func(t *testing.T) {
		t.Setenv("WEBUI_FRONTEND_SERVER_URL", base)
		got := (&GatewayCore{cfg: &GatewayConfig{}}).accessHint(true)
		if !strings.Contains(got, base) {
			t.Errorf("hint should carry the sign-in address, got %q", got)
		}
		if !strings.Contains(got, "administrator") {
			t.Errorf("hint should still name the administrator as the other path, got %q", got)
		}
	})

	t.Run("no configured address never prompts a sign-in", func(t *testing.T) {
		t.Setenv("WEBUI_FRONTEND_SERVER_URL", "")
		got := (&GatewayCore{cfg: &GatewayConfig{}}).accessHint(true)
		if strings.Contains(strings.ToLower(got), "sign in") {
			t.Errorf("must not tell a user to sign in with nowhere to send them: %q", got)
		}
		if got != accessHintAdmin {
			t.Errorf("hint = %q, want %q", got, accessHintAdmin)
		}
	})

	t.Run("phone-keyed surface never prompts a sign-in even with an address", func(t *testing.T) {
		t.Setenv("WEBUI_FRONTEND_SERVER_URL", base)
		got := (&GatewayCore{cfg: &GatewayConfig{}}).accessHint(false)
		if strings.Contains(strings.ToLower(got), "sign in") {
			t.Errorf("a WhatsApp identity keys on a verified phone claim, so a sign-in does not link it: %q", got)
		}
	})
}

func TestSubmitFailureText_SeparatesDenialFromFailure(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "")
	core := &GatewayCore{cfg: &GatewayConfig{}}

	denial := core.SubmitFailureText(fmt.Errorf("submit: %w", ErrAgentDelegationDenied), true)
	internal := core.SubmitFailureText(errors.New("broker publish timed out"), true)

	if denial == internal {
		t.Fatalf("a permission denial and an infrastructure failure must read differently, both were %q", denial)
	}
	if internal != SubmitFailedText {
		t.Errorf("internal failure = %q, want the retryable message %q", internal, SubmitFailedText)
	}
	if strings.Contains(denial, "try again") {
		t.Errorf("denial invites a retry that can never succeed: %q", denial)
	}
	if strings.Contains(denial, ErrAgentDelegationDenied.Error()) {
		t.Errorf("denial exposes the raw Go error: %q", denial)
	}
}

func TestSubmitFailureText_ABudgetRefusalSaysWhenItResetsAndNotToRetry(t *testing.T) {
	core := &GatewayCore{cfg: &GatewayConfig{}}
	resetsAt := time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC)

	text := core.SubmitFailureText(fmt.Errorf("submit: %w", &budget.ExhaustedError{Tier: budget.TierMesh, ResetsAt: resetsAt}), true)

	if !strings.Contains(text, "1 November 2099 00:00 UTC") {
		t.Errorf("budget refusal %q does not say when the budget resets", text)
	}
	if text == SubmitFailedText || strings.Contains(text, "try again") {
		t.Errorf("budget refusal invites a retry that cannot succeed before the reset: %q", text)
	}
}

func TestBudgetExhaustedText_SaysWhoseBudgetIsSpentWithoutNamingIt(t *testing.T) {
	resetsAt := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	mesh := BudgetExhaustedText(&budget.ExhaustedError{Tier: budget.TierMesh, ResetsAt: resetsAt})
	user := BudgetExhaustedText(&budget.ExhaustedError{Tier: budget.TierUser, ResetsAt: resetsAt})

	if !strings.Contains(mesh, "this deployment's usage budget") {
		t.Errorf("mesh refusal %q does not say the deployment's budget is spent", mesh)
	}
	if !strings.Contains(user, "your usage budget") {
		t.Errorf("user refusal %q does not say the user's own budget is spent", user)
	}
	for _, text := range []string{mesh, user} {
		if !strings.Contains(text, "1 November 2026 00:00 UTC") {
			t.Errorf("refusal %q does not say when the budget resets", text)
		}
	}
}

func TestAgentLabel_PrefersDisplayNameOverWireName(t *testing.T) {
	deployed := cardWithDisplayName("agent_019fe224_dd07_764b_ae4c_125a04fb3ab2", "Orchestrator")
	if got := AgentLabel(deployed); got != "Orchestrator" {
		t.Errorf("AgentLabel() = %q, want the display name", got)
	}
	if got := AgentLabel(a2a.AgentCard{Name: "MyAgent"}); got != "MyAgent" {
		t.Errorf("AgentLabel() = %q, want the broker name when no display name is published", got)
	}
}

func TestSubmitFailureText_AnAgentThatIsNotDeployed(t *testing.T) {
	core := &GatewayCore{cfg: &GatewayConfig{}}
	if got := core.SubmitFailureText(fmt.Errorf("submit: %w", ErrAgentNotAvailable), false); got != AgentNotAvailableText {
		t.Errorf("SubmitFailureText = %q, want %q", got, AgentNotAvailableText)
	}
}
