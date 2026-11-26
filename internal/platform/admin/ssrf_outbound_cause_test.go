// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestOutboundCause(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"url error", &url.Error{Op: "Get", URL: "http://h/?token=" + secretToken, Err: errors.New("boom")}},
		{"plain error", errors.New("boom")},
		{"nil does not panic", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cause := newOutboundCause(tt.err)
			if !isOutboundCause(cause) {
				t.Error("not recognised as an outbound cause")
			}
			if strings.Contains(cause.Error(), secretToken) || strings.Contains(cause.Error(), "http://h") {
				t.Errorf("text carries the request URL: %q", cause.Error())
			}
		})
	}
}

func TestUnreachableTargetError_NilCauseDoesNotPanic(t *testing.T) {
	err := &unreachableTargetError{ValidationError: &ValidationError{Message: "m"}}
	if _, ok := errors.AsType[*ValidationError](err); !ok || isOutboundCause(err) {
		t.Fatal("want the ValidationError and no cause")
	}
}

func TestCapLogDetail(t *testing.T) {
	long := strings.Repeat("a", maxCauseLogBytes*4)
	multibyte := strings.Repeat("é", maxCauseLogBytes)
	tests := []struct {
		name      string
		in        string
		wantCap   bool
		wantExact string
	}{
		{"short is untouched", "boom", false, "boom"},
		{"at the limit is untouched", strings.Repeat("a", maxCauseLogBytes), false, strings.Repeat("a", maxCauseLogBytes)},
		{"long is cut", long, true, ""},
		{"cut never splits a multibyte rune", multibyte, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := capLogDetail(tt.in)
			if !tt.wantCap {
				if got != tt.wantExact {
					t.Fatalf("got %q", got)
				}
				return
			}
			if len(got) > maxCauseLogBytes+len("…(truncated)") || !strings.HasSuffix(got, "…(truncated)") || !utf8.ValidString(got) {
				t.Fatalf("got %d bytes, valid=%v: %q", len(got), utf8.ValidString(got), got)
			}
		})
	}

	t.Run("a hostile response line is bounded in the cause", func(t *testing.T) {
		hostile := errors.New("malformed HTTP response " + strings.Repeat("X", 1<<20))
		if got := newOutboundCause(hostile).Error(); len(got) > maxCauseLogBytes+len("…(truncated)") {
			t.Fatalf("cause text is %d bytes", len(got))
		}
	})
}

func TestSSRF_UnparsableURLsDoNotEchoTheInput(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "")
	endpoints := newSSRFEndpoints(t, nil, nil)
	bad := "http://" + secretUser + ":" + secretPass + "@example.com:44x/x?api_key=" + secretToken
	portShaped := "http://" + secretUser + ":" + secretPass

	calls := map[string]func(t *testing.T) apiResult{
		"detectAuth spec URL": func(t *testing.T) apiResult { return endpoints.postDetectAuth(t, bad) },
		"card URL":            func(t *testing.T) apiResult { return endpoints.postCardURL(t, bad) },
		"detectAuth URL whose userinfo reads as a port": func(t *testing.T) apiResult { return endpoints.postDetectAuth(t, portShaped) },
		"card URL whose userinfo reads as a port":       func(t *testing.T) apiResult { return endpoints.postCardURL(t, portShaped) },
		"oauth2 token URL": func(t *testing.T) apiResult {
			return endpoints.postCard(t, oauthCardRequest(bad, unusedPublicLookingURL))
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			logs := captureDefaultLogs(t)
			res := call(t)
			if res.status != http.StatusUnprocessableEntity {
				t.Fatalf("got %d %s, want 422", res.status, res.body)
			}
			for _, secret := range []string{secretUser, secretPass, secretToken} {
				if strings.Contains(string(res.body), secret) || strings.Contains(logs.String(), secret) {
					t.Errorf("secret %q echoed: %s / %s", secret, res.body, logs.String())
				}
			}
		})
	}
}

func TestSSRF_OAuthTokenURLSchemeIsValidated(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "")
	endpoints := newSSRFEndpoints(t, nil, nil)
	for _, tokenURL := range []string{"ftp://idp.example/token", "file:///etc/passwd", "javascript:alert(1)"} {
		t.Run(tokenURL, func(t *testing.T) {
			res := endpoints.postCard(t, oauthCardRequest(tokenURL, unusedPublicLookingURL))
			if res.status != http.StatusUnprocessableEntity || !strings.Contains(res.message(t), "OAuth2 token URL") {
				t.Fatalf("got %d %s", res.status, res.body)
			}
		})
	}
}

func TestHandleServiceError_OnlyRedactedCausesAreLogged(t *testing.T) {
	raw := errors.New("GET http://x/?apikey=" + secretToken + " failed: upstream body " + secretPass)
	tests := []struct {
		name      string
		err       error
		wantLevel string
		wantCause bool
	}{
		{"external error with a raw cause", &ExternalServiceError{Message: "m", Cause: raw}, "ERROR", false},
		{"external error with an outbound cause", &ExternalServiceError{Message: "m", Cause: newOutboundCause(raw)}, "WARN", true},
		{"plain validation error", &ValidationError{Message: "m"}, "WARN", false},
		{"unreachable target validation error", &unreachableTargetError{ValidationError: &ValidationError{Message: "m"}, cause: newOutboundCause(raw)}, "WARN", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureDefaultLogs(t)
			rec := httptest.NewRecorder()
			handleServiceError(rec, tt.err)

			records := logRecords(t, logs)
			if len(records) != 1 || records[0]["level"] != tt.wantLevel {
				t.Fatalf("records = %v, want one %s", records, tt.wantLevel)
			}
			_, hasCause := records[0]["error"]
			if hasCause != tt.wantCause {
				t.Errorf("error attribute present = %v, want %v", hasCause, tt.wantCause)
			}
			if strings.Contains(logs.String(), secretToken) {
				t.Errorf("log carries the query: %s", logs.String())
			}
			if !tt.wantCause && strings.Contains(logs.String(), secretPass) {
				t.Errorf("log carries upstream text: %s", logs.String())
			}
			if strings.Contains(rec.Body.String(), secretToken) {
				t.Errorf("response carries upstream text: %s", rec.Body.String())
			}
		})
	}
}
