// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import "net/http"

const (
	ErrorTypeTermsAcceptanceRequired = "terms_acceptance_required"
	ErrorTypeFreeTierPaused          = "free_tier_paused"
	ErrorTypeFreeTierExhausted       = "free_tier_exhausted"
)

const (
	FreeLLMTermsRequiredUserMessage = "Terms not accepted. Accept the free model's terms to continue."
	FreeLLMPausedUserMessage        = "The free model is paused. Add a model to replace it."
	FreeTierExhaustedUserMessage    = "The free model's allowance is used up. Add a model to replace it."
)

func (e *Error) IsTermsAcceptanceRequired() bool {
	return e != nil && e.StatusCode == http.StatusForbidden && e.Type == ErrorTypeTermsAcceptanceRequired
}

func (e *Error) IsFreeLLMPaused() bool {
	return e != nil && e.StatusCode == http.StatusServiceUnavailable && e.Type == ErrorTypeFreeTierPaused
}

func (e *Error) IsFreeTierExhausted() bool {
	return e != nil && e.Type == ErrorTypeFreeTierExhausted
}

func noWaitClears(errType string) bool {
	return errType == ErrorTypeFreeTierPaused
}
