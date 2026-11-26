// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func graphErrorBody(code, subcode int, message string) string {
	if subcode == 0 {
		return `{"error":{"message":"` + message + `","code":` + itoa(code) + `,"type":"OAuthException","fbtrace_id":"Ax4"}}`
	}
	return `{"error":{"message":"` + message + `","code":` + itoa(code) + `,"error_subcode":` + itoa(subcode) + `,"type":"OAuthException","fbtrace_id":"Ax4"}}`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func TestProbeCredentials_Success(t *testing.T) {
	t.Parallel()
	var gotPath, gotAuth, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotQuery = r.URL.Path, r.Header.Get("Authorization"), r.URL.RawQuery
		_, _ = w.Write([]byte(`{"id":"555111","display_phone_number":"+1 555 0100","verified_name":"Acme","quality_rating":"GREEN"}`))
	}))
	defer srv.Close()

	res := ProbeCredentials(t.Context(), "tok-abc", "555111", ProbeOptions{BaseURL: srv.URL})
	if !res.Success {
		t.Fatalf("Success = false (class %q, hint %q), want true", res.ErrorClass, res.Hint)
	}
	if res.ErrorClass != "" || res.Hint != "" {
		t.Errorf("a successful probe must carry no class or hint, got %q / %q", res.ErrorClass, res.Hint)
	}
	if want := "/" + defaultGraphAPIVersion + "/555111"; gotPath != want {
		t.Errorf("probe path = %q, want %q", gotPath, want)
	}
	if gotAuth != "Bearer tok-abc" {
		t.Errorf("Authorization = %q, want the bearer token", gotAuth)
	}
	if want := "fields=" + probeField; gotQuery != want {
		t.Errorf("probe query = %q, want exactly %q", gotQuery, want)
	}
	if strings.Contains(gotQuery, "tok-abc") || strings.Contains(gotQuery, "access_token") {
		t.Errorf("probe query carries a credential: %q", gotQuery)
	}
}

func TestProbeCredentials_RejectsNonPhoneNumberNode(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		body string
	}{
		{"WABA node answers 200 with no phone number", `{"id":"102290129340398","name":"Acme Business"}`},
		{"field selector filtered everything out", `{"id":"555111"}`},
		{"empty display_phone_number", `{"id":"555111","display_phone_number":""}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			res := ProbeCredentials(t.Context(), "tok", "102290129340398", ProbeOptions{BaseURL: srv.URL})
			if res.Success {
				t.Fatal("Success = true for a 200 that carries no phone number")
			}
			if res.ErrorClass != ProbeClassPhoneNumberUnknown {
				t.Errorf("ErrorClass = %q, want %q", res.ErrorClass, ProbeClassPhoneNumberUnknown)
			}
			if !strings.Contains(res.Hint, "Business Account") {
				t.Errorf("Hint should name the id it was probably confused with: %q", res.Hint)
			}
		})
	}
}

func TestProbeCredentials_NonJSON200IsNotAWrongID(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body>Access blocked by network policy</body></html>`))
	}))
	defer srv.Close()

	res := ProbeCredentials(t.Context(), "tok", "555111", ProbeOptions{BaseURL: srv.URL})
	if res.Success {
		t.Fatal("Success = true for a non-Meta 200")
	}
	if res.ErrorClass != ProbeClassUnknown {
		t.Errorf("ErrorClass = %q, want %q — an interstitial says nothing about the id", res.ErrorClass, ProbeClassUnknown)
	}
	if !strings.Contains(res.Hint, "egress proxy") {
		t.Errorf("Hint = %q, want it to name the network path", res.Hint)
	}
	if strings.Contains(res.Hint, "Business Account") {
		t.Errorf("Hint blames the Phone Number ID for a network answer: %q", res.Hint)
	}
}

func TestProbeCredentials_ClassifiesGraphErrors(t *testing.T) {
	t.Parallel()
	const metaText = "Error validating access token: the user 15551234567 is not valid"

	for _, tt := range []struct {
		name       string
		status     int
		body       string
		wantClass  ProbeErrorClass
		wantCode   int
		wantSub    int
		hintQuotes string
	}{
		{
			name:       "190 rejects the token",
			status:     http.StatusUnauthorized,
			body:       graphErrorBody(codeOAuthException, 0, metaText),
			wantClass:  ProbeClassTokenInvalid,
			wantCode:   190,
			hintQuotes: "System User token",
		},
		{
			name:       "190/463 says the token expired",
			status:     http.StatusUnauthorized,
			body:       graphErrorBody(codeOAuthException, subcodeTokenExpired, metaText),
			wantClass:  ProbeClassTokenExpired,
			wantCode:   190,
			wantSub:    463,
			hintQuotes: "expired",
		},
		{
			name:       "190/467 says Meta invalidated the token",
			status:     http.StatusUnauthorized,
			body:       graphErrorBody(codeOAuthException, subcodeTokenInvalid, metaText),
			wantClass:  ProbeClassTokenInvalid,
			wantCode:   190,
			wantSub:    467,
			hintQuotes: "revoked",
		},
		{
			name:       "100 points at the phone-number id",
			status:     http.StatusBadRequest,
			body:       graphErrorBody(codeUnknownObject, 33, "Unsupported get request"),
			wantClass:  ProbeClassPhoneNumberUnknown,
			wantCode:   100,
			wantSub:    33,
			hintQuotes: "Phone Number ID",
		},
		{
			name:       "200 points at the app permissions",
			status:     http.StatusForbidden,
			body:       graphErrorBody(200, 0, "Permissions error"),
			wantClass:  ProbeClassPermissionDenied,
			wantCode:   200,
			hintQuotes: "whatsapp_business_management",
		},
		{
			name:       "mid-band permission code",
			status:     http.StatusForbidden,
			body:       graphErrorBody(294, 0, "Permissions error"),
			wantClass:  ProbeClassPermissionDenied,
			wantCode:   294,
			hintQuotes: "whatsapp_business_management",
		},
		{
			name:       "top-of-band permission code",
			status:     http.StatusForbidden,
			body:       graphErrorBody(299, 0, "Permissions error"),
			wantClass:  ProbeClassPermissionDenied,
			wantCode:   299,
			hintQuotes: "whatsapp_business_management",
		},
		{
			name:      "429 leaves the credentials unproven",
			status:    http.StatusTooManyRequests,
			body:      graphErrorBody(codeThroughputReached, 0, "rate limited"),
			wantClass: ProbeClassRateLimited,
			wantCode:  130429,
		},
		{
			name:      "5xx is reported as unreachable, not as bad credentials",
			status:    http.StatusServiceUnavailable,
			body:      `<html>502 Bad Gateway</html>`,
			wantClass: ProbeClassNetworkUnreachable,
		},
		{
			name:      "an unrecognized code still names the numbers",
			status:    http.StatusBadRequest,
			body:      graphErrorBody(133010, 0, "Account not registered"),
			wantClass: ProbeClassUnknown,
			wantCode:  133010,
		},
		{
			name:       "a bodyless 403 does not blame the token",
			status:     http.StatusForbidden,
			body:       `<html>403 Forbidden</html>`,
			wantClass:  ProbeClassUnknown,
			hintQuotes: "egress proxy",
		},
		{
			name:       "a bodyless 404 does not blame the phone-number id",
			status:     http.StatusNotFound,
			body:       ``,
			wantClass:  ProbeClassUnknown,
			hintQuotes: "egress proxy",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			res := ProbeCredentials(t.Context(), "tok", "555111", ProbeOptions{BaseURL: srv.URL})
			if res.Success {
				t.Fatal("Success = true for a rejected probe")
			}
			if res.ErrorClass != tt.wantClass {
				t.Errorf("ErrorClass = %q, want %q", res.ErrorClass, tt.wantClass)
			}
			if res.MetaErrorCode != tt.wantCode {
				t.Errorf("MetaErrorCode = %d, want %d", res.MetaErrorCode, tt.wantCode)
			}
			if res.MetaErrorSubcode != tt.wantSub {
				t.Errorf("MetaErrorSubcode = %d, want %d", res.MetaErrorSubcode, tt.wantSub)
			}
			if res.Hint == "" {
				t.Error("Hint is empty; the UI renders it as the whole failure message")
			}
			if tt.hintQuotes != "" && !strings.Contains(res.Hint, tt.hintQuotes) {
				t.Errorf("Hint = %q, want it to name %q", res.Hint, tt.hintQuotes)
			}
			if strings.Contains(res.Hint, "15551234567") || strings.Contains(res.Hint, metaText) {
				t.Errorf("Hint echoes Meta's free-text message: %q", res.Hint)
			}
		})
	}
}

func TestProbeCredentials_Timeout(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	start := time.Now()
	res := ProbeCredentials(t.Context(), "tok", "555111", ProbeOptions{BaseURL: srv.URL, Timeout: 100 * time.Millisecond})
	elapsed := time.Since(start)

	if res.Success {
		t.Fatal("Success = true against a hung Graph host")
	}
	if res.ErrorClass != ProbeClassNetworkUnreachable {
		t.Errorf("ErrorClass = %q, want %q", res.ErrorClass, ProbeClassNetworkUnreachable)
	}
	if !strings.Contains(res.Hint, "Timed out") {
		t.Errorf("Hint = %q, want it to say the probe timed out", res.Hint)
	}
	if elapsed > 2*time.Second {
		t.Errorf("probe took %v; the timeout must bound it", elapsed)
	}
}

func TestProbeCredentials_Unreachable(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	res := ProbeCredentials(t.Context(), "tok", "555111", ProbeOptions{BaseURL: url, Timeout: time.Second})
	if res.Success {
		t.Fatal("Success = true against a closed port")
	}
	if res.ErrorClass != ProbeClassNetworkUnreachable {
		t.Errorf("ErrorClass = %q, want %q", res.ErrorClass, ProbeClassNetworkUnreachable)
	}
	if strings.Contains(res.Hint, "Timed out") {
		t.Errorf("a refused connection should not report a timeout: %q", res.Hint)
	}
}

func TestProbeCredentials_MissingCredentials(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	for _, tt := range []struct{ name, token, phone string }{
		{"no token", "", "555111"},
		{"no phone number id", "tok", ""},
		{"whitespace only", "   ", "  "},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := ProbeCredentials(t.Context(), tt.token, tt.phone, ProbeOptions{BaseURL: srv.URL})
			if res.Success {
				t.Fatal("Success = true with an incomplete credential pair")
			}
			if res.ErrorClass != ProbeClassIncomplete {
				t.Errorf("ErrorClass = %q, want %q", res.ErrorClass, ProbeClassIncomplete)
			}
		})
	}
	if calls.Load() != 0 {
		t.Errorf("probe made %d HTTP calls with incomplete credentials, want 0", calls.Load())
	}
}

func TestProbeCredentials_PhoneNumberIDStaysOneSegment(t *testing.T) {
	t.Parallel()
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(graphErrorBody(codeUnknownObject, 33, "Unsupported get request")))
	}))
	defer srv.Close()

	res := ProbeCredentials(t.Context(), "tok", "../me", ProbeOptions{BaseURL: srv.URL})
	if res.ErrorClass != ProbeClassPhoneNumberUnknown {
		t.Errorf("ErrorClass = %q, want %q", res.ErrorClass, ProbeClassPhoneNumberUnknown)
	}
	if strings.Contains(gotPath, "/me") {
		t.Errorf("phone number id escaped its path segment: %q", gotPath)
	}
	if want := "/" + defaultGraphAPIVersion + "/..%2Fme"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

func TestProbeOptions_DefaultsToProductionGraph(t *testing.T) {
	t.Parallel()
	var opts ProbeOptions
	if got := opts.baseURL(); got != defaultGraphAPIBaseURL {
		t.Errorf("baseURL = %q, want %q", got, defaultGraphAPIBaseURL)
	}
	if got := opts.apiVersion(); got != defaultGraphAPIVersion {
		t.Errorf("apiVersion = %q, want %q", got, defaultGraphAPIVersion)
	}
	if got := opts.timeout(); got != probeTimeout {
		t.Errorf("timeout = %v, want %v", got, probeTimeout)
	}
	if got := (ProbeOptions{BaseURL: "https://mock.example/"}).baseURL(); got != "https://mock.example" {
		t.Errorf("baseURL = %q, want the trailing slash trimmed", got)
	}
}
