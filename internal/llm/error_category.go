// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"net/http"
	"slices"
	"strings"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
)

const (
	ErrorCategoryAuthFailure     = "auth_failure"
	ErrorCategoryRateLimit       = "rate_limit"
	ErrorCategoryTimeout         = "timeout"
	ErrorCategoryContextOverflow = "context_overflow"
	ErrorCategoryContentFiltered = "content_filtered"
	ErrorCategoryUnknown         = "unknown"

	ErrorCategoryFreeLLMTermsRequired = "free_llm_terms_required"
	ErrorCategoryFreeLLMPaused        = "free_llm_paused"
	ErrorCategoryAllowanceExhausted   = "allowance_exhausted"
)

const ProviderOther = "other"

var contentFilterPatterns = []string{
	"content filter",
	"content_filter",
	"content policy",
	"content_policy",
	"content management policy",
	"safety system",
	"safety filter",
	"responsible ai",
	"guardrail",
	"moderation",
}

var authFailurePatterns = []string{
	"invalid api key",
	"incorrect api key",
	"invalid_api_key",
	"missing api key",
	"no api key",
	"api key not",
	"invalid x-api-key",
	"authentication",
	"unauthenticated",
	"unauthorized",
	"permission denied",
	"access denied",
	"forbidden",
	"invalid credentials",
	"invalid_client",
	"invalid_grant",
	"invalid grant",
	"expired token",
	"security token",
}

var timeoutPatterns = []string{
	"timed out",
	"timeout",
	"deadline exceeded",
}

func (e *Error) Category() string {
	if e == nil {
		return ErrorCategoryUnknown
	}
	if e.Type == ErrorTypeAzureSignIn && e.Code != SignInEntraUnreachable {
		return ErrorCategoryAuthFailure
	}
	if e.IsReauthorizationRequired() {
		return ErrorCategoryAuthFailure
	}
	if e.IsGrantTokenUnavailable() {
		return ErrorCategoryUnknown
	}
	if e.IsTermsAcceptanceRequired() {
		return ErrorCategoryFreeLLMTermsRequired
	}
	if e.IsFreeLLMPaused() {
		return ErrorCategoryFreeLLMPaused
	}
	if e.IsFreeTierExhausted() {
		return ErrorCategoryAllowanceExhausted
	}
	switch e.StatusCode {
	case http.StatusTooManyRequests:
		return ErrorCategoryRateLimit
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrorCategoryAuthFailure
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return ErrorCategoryTimeout
	}
	if e.IsContextLimitError() {
		return ErrorCategoryContextOverflow
	}
	if e.matchesAny(contentFilterPatterns) {
		return ErrorCategoryContentFiltered
	}
	if e.matchesAny(authFailurePatterns) {
		return ErrorCategoryAuthFailure
	}
	if e.matchesAny(timeoutPatterns) {
		return ErrorCategoryTimeout
	}
	if e.namesRateLimit() {
		return ErrorCategoryRateLimit
	}
	return ErrorCategoryUnknown
}

var throttleWordings = []string{
	"rate limit",
	"rate_limit",
	"ratelimit",
	"too many requests",
	"throttled",
	"throttling",
	"rate exceeded",
	"requests per",
	"tokens per min",
	"per minute",
	"per-minute",
	"per second",
	"rpm exceeded",
	"tpm exceeded",
	"please wait before trying again",
}

var accountRefusalWordings = []string{
	"credit balance", "more credits", "insufficient credits", "insufficient balance", "max budget",
	"over budget", "exceededbudget", "budget has been exceeded", "budget_exceeded", "billing details",
	"billing_hard_limit", "billing hard limit", "payment required", "insufficient funds", "insufficient_quota",
	"exceeded your current quota", "quota exceeded", "service quota", "usage limits", "spend limit",
}

var oversizedRequestWordings = []string{"request too large", "must be reduced"}

func AccountRefusalPatterns() []string { return slices.Clone(accountRefusalWordings) }

func (e *Error) refusesTheAccount() bool {
	return e.matchesAny(accountRefusalWordings)
}

func (e *Error) statusCanCarryRateLimit() bool {
	switch {
	case e.StatusCode == http.StatusTooManyRequests:
		return true
	case e.StatusCode == 0 || e.StatusCode >= http.StatusInternalServerError:
		return e.namesTimeRate()
	default:
		return e.typeOrCodeNamesThrottle()
	}
}

func (e *Error) typeOrCodeNamesThrottle() bool {
	return (&Error{Type: e.Type, Code: e.Code}).namesTimeRate()
}

func (e *Error) namesTimeRate() bool {
	return e.matchesAny(throttleWordings)
}

func (e *Error) namesRateLimit() bool {
	return bifrost.IsRateLimitErrorMessage(e.Message) ||
		bifrost.IsRateLimitErrorMessage(e.Type) ||
		bifrost.IsRateLimitErrorMessage(e.Code) ||
		e.namesTimeRate()
}

func (e *Error) matchesAny(patterns []string) bool {
	for _, field := range [...]string{e.Message, e.Type, e.Code} {
		if field == "" {
			continue
		}
		lower := strings.ToLower(field)
		for _, pat := range patterns {
			if strings.Contains(lower, pat) {
				return true
			}
		}
	}
	return false
}

var knownProviders = func() map[string]struct{} {
	set := make(map[string]struct{}, len(schemas.StandardProviders))
	for _, p := range schemas.StandardProviders {
		set[strings.ToLower(string(p))] = struct{}{}
	}
	return set
}()

func ProviderLabel(provider string) string {
	name := strings.ToLower(strings.TrimSpace(provider))
	if name == "" {
		return ""
	}
	if _, ok := knownProviders[name]; ok {
		return name
	}
	return ProviderOther
}

func ProviderLabels() []string {
	labels := make([]string, 0, len(knownProviders)+1)
	for name := range knownProviders {
		labels = append(labels, name)
	}
	labels = append(labels, ProviderOther)
	slices.Sort(labels)
	return labels
}
