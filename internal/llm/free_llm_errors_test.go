// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"errors"
	"net/http"
	"testing"
)

const (
	termsRequiredBody  = `{"error":{"message":"Accept the free model terms to use it.","type":"terms_acceptance_required","code":"terms_acceptance_required"}}`
	freeTierPausedBody = `{"error":{"message":"The free model is paused. Add your own API key to keep going.","type":"free_tier_paused","code":"free_tier_paused"}}`
	freeTierSpentBody  = `{"error":{"message":"Your free credits are used up.","type":"free_tier_exhausted","code":"free_tier_exhausted"}}`
	litellmBudgetBody  = `{"error":{"message":"Budget has been exceeded! Current cost: 5.01, Max budget: 5.0","type":"budget_exceeded","param":null,"code":"402"}}`
)

func TestFreeLLMRefusalsAreClassifiedAndNotRetried(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		body         string
		wantCategory string
		wantMessage  string
	}{
		{"the free LLM asks for its terms", http.StatusForbidden, termsRequiredBody,
			ErrorCategoryFreeLLMTermsRequired, FreeLLMTermsRequiredUserMessage},
		{"the free LLM is paused", http.StatusServiceUnavailable, freeTierPausedBody,
			ErrorCategoryFreeLLMPaused, FreeLLMPausedUserMessage},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			llmErr, calls := failedCall(t, c.status, c.body)
			if calls != 1 {
				t.Errorf("handler called %d times, want 1: no retry within the call changes this refusal", calls)
			}
			if got := llmErr.Category(); got != c.wantCategory {
				t.Errorf("Category() = %q, want %q", got, c.wantCategory)
			}
			if got := UserFacingErrorMessage(llmErr); got != c.wantMessage {
				t.Errorf("user-facing message = %q, want %q", got, c.wantMessage)
			}
		})
	}
}

func TestTheFreeTiersSpentAllowanceIsClassifiedAndNotRetried(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusPaymentRequired, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			handler, calls := slowFailHandler(0, status, freeTierSpentBody)
			server := mockOpenAIServer(t, handler)
			t.Cleanup(server.Close)
			client := newGrantClient(t, server.URL, &fakeGrants{})

			var llmErr *Error
			if err := chatOnce(t, client); !errors.As(err, &llmErr) {
				t.Fatalf("err = %v, want an *llm.Error", err)
			}
			if got := calls.Load(); got != 1 {
				t.Errorf("handler called %d times, want 1: no retry within the call refills an allowance", got)
			}
			if got := llmErr.Category(); got != ErrorCategoryAllowanceExhausted {
				t.Errorf("Category() = %q, want %q", got, ErrorCategoryAllowanceExhausted)
			}
			if got := UserFacingErrorMessage(llmErr); got != FreeTierExhaustedUserMessage {
				t.Errorf("user-facing message = %q, want %q", got, FreeTierExhaustedUserMessage)
			}
		})
	}
}

func TestAnotherGatewaysSpentBudgetIsNotTheFreeTiers(t *testing.T) {
	want := BadRequestMessagePrefix + "Budget has been exceeded! Current cost: 5.01, Max budget: 5.0"
	assertNotTheFreeTiers := func(t *testing.T, llmErr *Error) {
		t.Helper()
		if got := llmErr.Category(); got != ErrorCategoryUnknown {
			t.Errorf("Category() = %q, want %q", got, ErrorCategoryUnknown)
		}
		if got := UserFacingErrorMessage(llmErr); got != want {
			t.Errorf("user-facing message = %q, want %q", got, want)
		}
	}

	t.Run("called with a key", func(t *testing.T) {
		llmErr, _ := failedCall(t, http.StatusBadRequest, litellmBudgetBody)
		assertNotTheFreeTiers(t, llmErr)
	})
	t.Run("called with an OAuth grant", func(t *testing.T) {
		handler, _ := slowFailHandler(0, http.StatusBadRequest, litellmBudgetBody)
		server := mockOpenAIServer(t, handler)
		t.Cleanup(server.Close)
		var llmErr *Error
		if err := chatOnce(t, newGrantClient(t, server.URL, &fakeGrants{})); !errors.As(err, &llmErr) {
			t.Fatalf("err = %v, want an *llm.Error", err)
		}
		assertNotTheFreeTiers(t, llmErr)
	})
}

func TestAnotherProvidersRefusalIsNotTheFreeLLMs(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		body         string
		wantCategory string
		wantCalls    int32
	}{
		{"a gateway's own 403", http.StatusForbidden,
			`{"error":{"message":"Forbidden","type":"permission_error"}}`, ErrorCategoryAuthFailure, 1},
		{"the terms type under another status", http.StatusBadRequest, termsRequiredBody, ErrorCategoryUnknown, 1},
		{"a provider's own 503", http.StatusServiceUnavailable,
			`{"error":{"message":"Service Unavailable","type":"server_error"}}`, ErrorCategoryUnknown, defaultLLMMaxRetries + 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			llmErr, calls := failedCall(t, c.status, c.body)
			if got := llmErr.Category(); got != c.wantCategory {
				t.Errorf("Category() = %q, want %q", got, c.wantCategory)
			}
			if calls != c.wantCalls {
				t.Errorf("handler called %d times, want %d", calls, c.wantCalls)
			}
		})
	}
}

func failedCall(t *testing.T, status int, body string) (*Error, int32) {
	t.Helper()
	handler, calls := slowFailHandler(0, status, body)
	server := mockOpenAIServer(t, handler)
	t.Cleanup(server.Close)
	client := newRetryTestClient(t, server.URL, nil)

	_, err := client.ChatCompletion(t.Context(), &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}})
	var llmErr *Error
	if !errors.As(err, &llmErr) {
		t.Fatalf("err = %v, want an *llm.Error", err)
	}
	return llmErr, calls.Load()
}
