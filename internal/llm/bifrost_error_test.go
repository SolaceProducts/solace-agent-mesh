// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestBifrostError_Nil(t *testing.T) {
	err := bifrostErrorToError(nil, "")
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestBifrostError_WithMessageAndStatus(t *testing.T) {
	bfErr := &schemas.BifrostError{
		Error:      &schemas.ErrorField{Message: "rate limited"},
		StatusCode: schemas.Ptr(429),
	}
	llmErr := bifrostErrorToError(bfErr, "")
	if llmErr == nil {
		t.Fatal("expected non-nil error")
	}
	if llmErr.Message != "rate limited" {
		t.Errorf("Message = %q, want %q", llmErr.Message, "rate limited")
	}
	if llmErr.StatusCode != 429 {
		t.Errorf("StatusCode = %d, want 429", llmErr.StatusCode)
	}
}

func TestBifrostError_IncludesUnderlyingCause(t *testing.T) {
	cause := errors.New("tls: failed to verify certificate: x509: certificate signed by unknown authority")
	bfErr := &schemas.BifrostError{
		Error: &schemas.ErrorField{
			Message: "failed to execute HTTP request to provider API",
			Error:   cause,
			Type:    schemas.Ptr("network_error"),
		},
		//nolint:staticcheck // the shape core's PopulateExtraFields produces; see bifrostErrorProvider.
		ExtraFields: schemas.BifrostErrorExtraFields{Provider: "openai"},
	}
	llmErr := bifrostErrorToError(bfErr, "")
	if !strings.Contains(llmErr.Message, "tls") || !strings.Contains(llmErr.Message, "x509") {
		t.Errorf("Message = %q, want underlying TLS error details", llmErr.Message)
	}
	if llmErr.Type != "network_error" {
		t.Errorf("Type = %q, want network_error", llmErr.Type)
	}
	if llmErr.Provider != "openai" {
		t.Errorf("Provider = %q, want openai", llmErr.Provider)
	}
	s := llmErr.Error()
	if !strings.Contains(s, "openai") {
		t.Errorf("Error() = %q, want provider mentioned", s)
	}
	if !strings.Contains(s, "network_error") {
		t.Errorf("Error() = %q, want type mentioned", s)
	}
}

func TestBifrostError_SkipsDuplicateCause(t *testing.T) {
	cause := errors.New("tls handshake failed")
	bfErr := &schemas.BifrostError{
		Error: &schemas.ErrorField{
			Message: "oops: tls handshake failed",
			Error:   cause,
		},
	}
	llmErr := bifrostErrorToError(bfErr, "")
	if strings.Count(llmErr.Message, "tls handshake failed") != 1 {
		t.Errorf("Message = %q, expected cause mentioned exactly once", llmErr.Message)
	}
}

func TestBifrostError_NilInnerFields(t *testing.T) {
	bfErr := &schemas.BifrostError{}
	llmErr := bifrostErrorToError(bfErr, "")
	if llmErr == nil {
		t.Fatal("expected non-nil error")
	}
	if llmErr.Message != "bifrost error" {
		t.Errorf("Message = %q, want %q", llmErr.Message, "bifrost error")
	}
	if llmErr.StatusCode != 0 {
		t.Errorf("StatusCode = %d, want 0", llmErr.StatusCode)
	}
}

func TestIsRetryable_StatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		want       bool
	}{
		{"429_rate_limit", 429, true},
		{"500_server_error", 500, true},
		{"503_service_unavailable", 503, true},
		{"400_client_error", 400, false},
		{"401_auth_error", 401, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Error{Message: "test", StatusCode: tt.statusCode}
			if got := e.IsRetryable(); got != tt.want {
				t.Errorf("IsRetryable() with status %d = %v, want %v", tt.statusCode, got, tt.want)
			}
		})
	}
}

func TestIsBadRequest(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		want       bool
	}{
		{"400_lower_bound", 400, true},
		{"499_upper_bound", 499, true},
		{"500_server_error", 500, false},
		{"399_below_range", 399, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Error{Message: "test", StatusCode: tt.statusCode}
			if got := e.IsBadRequest(); got != tt.want {
				t.Errorf("IsBadRequest() with status %d = %v, want %v", tt.statusCode, got, tt.want)
			}
		})
	}
}

func TestIsRejectedWithHTMLPage(t *testing.T) {
	const htmlPage = "<html><body>blocked</body></html>"
	tests := []struct {
		name       string
		statusCode int
		message    string
		want       bool
	}{
		{"html_400", http.StatusBadRequest, htmlPage, true},
		{"html_403", http.StatusForbidden, htmlPage, true},
		{"html_413", http.StatusRequestEntityTooLarge, htmlPage, true},
		{"bifrost_html_marker_403", http.StatusForbidden, schemas.ErrProviderResponseHTML + ": 403", true},
		{"html_401_expired_credential", http.StatusUnauthorized, htmlPage, false},
		{"html_408_timeout", http.StatusRequestTimeout, htmlPage, false},
		{"html_429_rate_limit", http.StatusTooManyRequests, htmlPage, false},
		{"html_500_server_error", http.StatusInternalServerError, htmlPage, false},
		{"structured_403", http.StatusForbidden, `{"error":"forbidden"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Error{Message: tt.message, StatusCode: tt.statusCode}
			if got := e.IsRejectedWithHTMLPage(); got != tt.want {
				t.Errorf("IsRejectedWithHTMLPage() with status %d = %v, want %v", tt.statusCode, got, tt.want)
			}
		})
	}
}

func TestIsPayloadRejection(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		message    string
		want       bool
	}{
		{"content_policy_400", http.StatusBadRequest, "request blocked by content policy", true},
		{"html_403", http.StatusForbidden, "<html><body>blocked</body></html>", true},
		{"json_413", http.StatusRequestEntityTooLarge, `{"error":"payload too large"}`, true},
		{"context_limit_400", http.StatusBadRequest, "maximum context length exceeded", true},
		{"content_filter_422", http.StatusUnprocessableEntity, "rejected by the content filter", true},
		{"auth_403", http.StatusForbidden, `{"error":"invalid api key"}`, false},
		{"model_access_403", http.StatusForbidden, "you do not have access to this model", false},
		{"invalid_model_400", http.StatusBadRequest, "invalid model: gpt-nope", false},
		{"invalid_parameter_400", http.StatusBadRequest, "unsupported parameter: temperature", false},
		{"unauthorized_html", http.StatusUnauthorized, "<html><body>expired</body></html>", false},
		{"rate_limited", http.StatusTooManyRequests, "slow down", false},
		{"server_error", http.StatusInternalServerError, "boom", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Error{Message: tt.message, StatusCode: tt.statusCode}
			if got := e.IsPayloadRejection(); got != tt.want {
				t.Errorf("IsPayloadRejection() with status %d = %v, want %v", tt.statusCode, got, tt.want)
			}
		})
	}
}

func TestIsContentFilterRejection(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		message    string
		want       bool
	}{
		{"content_filter_400", http.StatusBadRequest, "blocked by the content filter", true},
		{"guardrail_403", http.StatusForbidden, "guardrail intervened", true},
		{"size_413", http.StatusRequestEntityTooLarge, "payload too large", false},
		{"context_limit_400", http.StatusBadRequest, "maximum context length exceeded", false},
		{"html_403", http.StatusForbidden, "<html><body>blocked</body></html>", false},
		{"auth_403", http.StatusForbidden, "invalid api key", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Error{Message: tt.message, StatusCode: tt.statusCode}
			if got := e.IsContentFilterRejection(); got != tt.want {
				t.Errorf("IsContentFilterRejection() with status %d = %v, want %v", tt.statusCode, got, tt.want)
			}
		})
	}
}

func TestContextLimit_MaxContextLength(t *testing.T) {
	e := &Error{Message: "maximum context length exceeded", StatusCode: 400}
	if !e.IsContextLimitError() {
		t.Error("expected IsContextLimitError() = true for 'maximum context length exceeded'")
	}
}

func TestContextLimit_TokenLimit(t *testing.T) {
	e := &Error{Message: "request exceeds token limit", StatusCode: 400}
	if !e.IsContextLimitError() {
		t.Error("expected IsContextLimitError() = true for 'request exceeds token limit'")
	}
}

func TestContextLimit_NonBadRequest(t *testing.T) {
	e := &Error{Message: "maximum context length", StatusCode: 500}
	if e.IsContextLimitError() {
		t.Error("expected IsContextLimitError() = false when status is 500")
	}
}

func TestContextLimit_UnrelatedMessage(t *testing.T) {
	e := &Error{Message: "invalid model name", StatusCode: 400}
	if e.IsContextLimitError() {
		t.Error("expected IsContextLimitError() = false for unrelated 400 error")
	}
}

func TestIsTemperatureUnsupportedError(t *testing.T) {
	tests := []struct {
		name       string
		message    string
		statusCode int
		want       bool
	}{
		{
			name:       "anthropic_opus_deprecated",
			message:    "`temperature` is deprecated for this model.",
			statusCode: 400,
			want:       true,
		},
		{
			name:       "anthropic_opus_deprecated_json_wire",
			message:    "{\"message\":\"`temperature` is deprecated for this model.\"}",
			statusCode: 400,
			want:       true,
		},
		{
			name:       "claude_with_thinking_takes_only_the_default",
			message:    "{\"message\":\"The model returned the following errors: `temperature` may only be set to 1 when thinking is enabled.\"}",
			statusCode: 400,
			want:       true,
		},
		{
			name:       "openai_o_series_not_supported",
			message:    "Unsupported value: 'temperature' does not support 0 with this model.",
			statusCode: 400,
			want:       true,
		},
		{
			name:       "temperature_not_supported",
			message:    "temperature is not supported with this model",
			statusCode: 400,
			want:       true,
		},
		{
			name:       "not_a_bad_request",
			message:    "`temperature` is deprecated for this model.",
			statusCode: 500,
			want:       false,
		},
		{
			name:       "unrelated_bad_request",
			message:    "invalid model name",
			statusCode: 400,
			want:       false,
		},
		{
			name:       "temperature_mentioned_but_unrelated_reason",
			message:    "temperature must be between 0 and 2",
			statusCode: 400,
			want:       false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Error{Message: tt.message, StatusCode: tt.statusCode}
			if got := e.IsTemperatureUnsupportedError(); got != tt.want {
				t.Errorf("IsTemperatureUnsupportedError() = %v, want %v (message %q, status %d)", got, tt.want, tt.message, tt.statusCode)
			}
		})
	}
}

func TestUserFacing_ContextLimit(t *testing.T) {
	e := &Error{Message: "maximum context length exceeded", StatusCode: 400}
	msg := e.UserFacingMessage()
	if !strings.Contains(msg, "conversation history has become too long") {
		t.Errorf("UserFacingMessage() = %q, want it to mention conversation history too long", msg)
	}
	if !strings.Contains(msg, "start a new conversation") {
		t.Errorf("UserFacingMessage() = %q, want it to suggest starting a new conversation", msg)
	}
}

func TestUserFacing_BadRequest(t *testing.T) {
	e := &Error{Message: "invalid model name", StatusCode: 400}
	msg := e.UserFacingMessage()
	if !strings.HasPrefix(msg, "Bad request: ") {
		t.Errorf("UserFacingMessage() = %q, want prefix 'Bad request: '", msg)
	}
	if !strings.Contains(msg, "invalid model name") {
		t.Errorf("UserFacingMessage() = %q, want it to contain original message", msg)
	}
}

func TestUserFacing_ServerError(t *testing.T) {
	e := &Error{Message: "Internal server error", StatusCode: 500}
	msg := e.UserFacingMessage()
	if !strings.HasPrefix(msg, "LLM error: ") {
		t.Errorf("UserFacingMessage() = %q, want prefix 'LLM error: '", msg)
	}
	if !strings.Contains(msg, "Internal server error") {
		t.Errorf("UserFacingMessage() = %q, want it to contain original message", msg)
	}
}

func TestUserFacing_HTML403ContentFilter(t *testing.T) {
	htmlBody := "HTML response received from provider:\n<html>\n<head><title>403 Forbidden</title></head>\n<body><center><h1>403 Forbidden</h1></center></body>\n</html>"
	e := &Error{Message: htmlBody, StatusCode: 403}
	msg := e.UserFacingMessage()
	if strings.Contains(msg, "<html") || strings.Contains(msg, "<head") || strings.Contains(msg, "<body") {
		t.Errorf("UserFacingMessage() leaked raw HTML: %q", msg)
	}
	if !strings.Contains(msg, "403") {
		t.Errorf("UserFacingMessage() = %q, want it to mention the HTTP status code", msg)
	}
	if !strings.Contains(msg, "content filter") {
		t.Errorf("UserFacingMessage() = %q, want it to mention content filter", msg)
	}
}

func TestUserFacing_HTML502Transient(t *testing.T) {
	htmlBody := "HTML response received from provider:\n<html><head><title>502 Bad Gateway</title></head><body><h1>502 Bad Gateway</h1></body></html>"
	e := &Error{Message: htmlBody, StatusCode: 502}
	msg := e.UserFacingMessage()
	if strings.Contains(msg, "<html") {
		t.Errorf("UserFacingMessage() leaked raw HTML: %q", msg)
	}
	if !strings.Contains(msg, "502") {
		t.Errorf("UserFacingMessage() = %q, want it to mention the HTTP status code", msg)
	}
	if strings.Contains(msg, "content filter") {
		t.Errorf("UserFacingMessage() = %q, transient 502 should not mention content filter", msg)
	}
}

func TestUserFacing_RawHTMLFallback(t *testing.T) {
	e := &Error{Message: "<html><head><title>Bad Gateway</title></head></html>", StatusCode: 502}
	msg := e.UserFacingMessage()
	if strings.Contains(msg, "<html") {
		t.Errorf("UserFacingMessage() leaked raw HTML: %q", msg)
	}
}

func TestUserFacing_HTMLNoStatusCodeNeverShowsHTTP0(t *testing.T) {
	htmlBody := "HTML response received from provider:\n<html><body>Bad Gateway</body></html>"
	e := &Error{Message: htmlBody, StatusCode: 0}
	msg := e.UserFacingMessage()
	if strings.Contains(msg, "<html") {
		t.Errorf("UserFacingMessage() leaked raw HTML: %q", msg)
	}
	if strings.Contains(msg, "HTTP 0") {
		t.Errorf("UserFacingMessage() = %q, must not surface \"HTTP 0\"", msg)
	}
}

func TestUserFacing_UppercaseHTMLFallback(t *testing.T) {
	e := &Error{Message: "<!DOCTYPE HTML><HTML><BODY>403</BODY></HTML>", StatusCode: 403}
	msg := e.UserFacingMessage()
	if strings.Contains(strings.ToLower(msg), "<html") || strings.Contains(strings.ToLower(msg), "<!doctype") {
		t.Errorf("UserFacingMessage() leaked raw HTML: %q", msg)
	}
	if !strings.Contains(msg, "content filter") {
		t.Errorf("UserFacingMessage() = %q, want content-filter guidance for a 4xx HTML body", msg)
	}
}

func TestUserFacing_ContextLimitWinsOverHTML(t *testing.T) {
	e := &Error{Message: "<html>input is too long</html>", StatusCode: 400}
	msg := e.UserFacingMessage()
	if strings.Contains(msg, "<html") {
		t.Errorf("UserFacingMessage() leaked raw HTML: %q", msg)
	}
	if !strings.Contains(msg, "conversation history") {
		t.Errorf("UserFacingMessage() = %q, want context-limit guidance", msg)
	}
}

func TestUserFacingErrorMessage_UntypedHTMLIsSuppressed(t *testing.T) {
	raw := errors.New("provider API error: <html><head><title>504 Gateway Time-out</title></head></html>")
	msg := UserFacingErrorMessage(raw)
	if strings.Contains(strings.ToLower(msg), "<html") {
		t.Errorf("UserFacingErrorMessage() leaked raw HTML: %q", msg)
	}
	if !strings.Contains(msg, "try again") {
		t.Errorf("UserFacingErrorMessage() = %q, want transient-infrastructure guidance", msg)
	}
}

func TestUserFacingErrorMessage_UntypedPlainIsPreserved(t *testing.T) {
	if got := UserFacingErrorMessage(errors.New("connection refused")); got != "LLM error: connection refused" {
		t.Errorf("UserFacingErrorMessage() = %q, want %q", got, "LLM error: connection refused")
	}
	if got := UserFacingErrorMessage(nil); got != "" {
		t.Errorf("UserFacingErrorMessage(nil) = %q, want empty", got)
	}
}

func TestUserFacingErrorMessage_BoundsLongBodies(t *testing.T) {
	body := strings.Repeat("x", maxProviderMessageBytes*3)
	for name, msg := range map[string]string{
		"untyped": UserFacingErrorMessage(errors.New(body)),
		"typed5xx": UserFacingErrorMessage(&Error{
			Message: body, StatusCode: 500,
		}),
		"typed4xx": UserFacingErrorMessage(&Error{
			Message: body, StatusCode: 400,
		}),
	} {
		if len(msg) > maxProviderMessageBytes*2 {
			t.Errorf("%s: message is %d bytes, want it bounded near %d",
				name, len(msg), maxProviderMessageBytes)
		}
		if !strings.Contains(msg, "truncated") {
			t.Errorf("%s: want a truncation marker, got %q", name, msg)
		}
	}
}

func TestSanitizeErrorText_NoLLMPrefix(t *testing.T) {
	got := SanitizeErrorText(errors.New("no messages to compact"))
	if got != "no messages to compact" {
		t.Errorf("SanitizeErrorText() = %q, want the message unprefixed", got)
	}
	if strings.Contains(SanitizeErrorText(errors.New("<html>oops</html>")), "<html") {
		t.Error("SanitizeErrorText() leaked raw HTML")
	}
}

func TestError_StringWithStatusCode(t *testing.T) {
	e := &Error{Message: "rate limited", StatusCode: 429}
	s := e.Error()
	if !strings.Contains(s, "429") {
		t.Errorf("Error() = %q, want it to contain '429'", s)
	}
	if !strings.Contains(s, "rate limited") {
		t.Errorf("Error() = %q, want it to contain 'rate limited'", s)
	}
}

func TestError_StringWithoutStatusCode(t *testing.T) {
	e := &Error{Message: "unknown error"}
	s := e.Error()
	if strings.Contains(s, "status") {
		t.Errorf("Error() = %q, should not contain 'status' when StatusCode is 0", s)
	}
	if !strings.Contains(s, "unknown error") {
		t.Errorf("Error() = %q, want it to contain 'unknown error'", s)
	}
}

func TestBifrostError_ConfiguredProviderWinsOverBifrostsLabel(t *testing.T) {
	bfErr := &schemas.BifrostError{
		Error:       &schemas.ErrorField{Message: "boom"},
		ExtraFields: schemas.BifrostErrorExtraFields{RoutingInfo: schemas.RoutingInfo{Provider: schemas.OpenRouter}},
	}
	if got := bifrostErrorToError(bfErr, "openai").Provider; got != "openai" {
		t.Errorf("Provider = %q, want the configured provider over Bifrost's wire provider", got)
	}
	if got := bifrostErrorToError(bfErr, "").Provider; got != "openrouter" {
		t.Errorf("Provider = %q, want Bifrost's provider when none is configured", got)
	}
}
