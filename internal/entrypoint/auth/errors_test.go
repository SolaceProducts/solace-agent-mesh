// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		errType ErrorType
		detail  string
	}{
		{"401 missing token", http.StatusUnauthorized, ErrorAuthenticationRequired, "Not authenticated"},
		{"401 bad token", http.StatusUnauthorized, ErrorInvalidToken, "Invalid or expired token"},
		{"403 scope", http.StatusForbidden, ErrorAuthorizationFailure, "Missing required scope"},
		{"502 refresh", http.StatusBadGateway, ErrorTokenRefreshFailed, "Upstream refresh failed"},
		{"500 config", http.StatusInternalServerError, ErrorServerConfiguration, "Auth service not configured"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteError(rec, tc.status, tc.errType, tc.detail)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			ct := rec.Header().Get("Content-Type")
			if !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("Content-Type = %q, want application/json", ct)
			}

			var body errorBody
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Message != tc.detail {
				t.Errorf("message = %q, want %q", body.Message, tc.detail)
			}
			if body.ErrorType != tc.errType {
				t.Errorf("errorType = %q, want %q", body.ErrorType, tc.errType)
			}
		})
	}
}

type levelCaptureHandler struct{ level slog.Level }

func (h *levelCaptureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *levelCaptureHandler) Handle(_ context.Context, r slog.Record) error {
	h.level = r.Level
	return nil
}
func (h *levelCaptureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *levelCaptureHandler) WithGroup(string) slog.Handler      { return h }

type attrCaptureHandler struct {
	msg   string
	attrs map[string]string
}

func (h *attrCaptureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *attrCaptureHandler) Handle(_ context.Context, r slog.Record) error {
	h.msg = r.Message
	h.attrs = make(map[string]string, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		h.attrs[a.Key] = a.Value.String()
		return true
	})
	return nil
}
func (h *attrCaptureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *attrCaptureHandler) WithGroup(string) slog.Handler      { return h }

func TestWriteErrorReqAttribution(t *testing.T) {
	capture := &attrCaptureHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(prev) })

	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	r.Header.Set("User-Agent", "test-agent/1.0")
	WriteErrorReq(httptest.NewRecorder(), r, http.StatusUnauthorized, ErrorInvalidToken, "Invalid or expired token")

	for k, want := range map[string]string{
		"method":    "GET",
		"path":      "/api/v1/sessions",
		"clientIP":  "203.0.113.7",
		"userAgent": "test-agent/1.0",
		"errorType": string(ErrorInvalidToken),
	} {
		if got := capture.attrs[k]; got != want {
			t.Errorf("attr %q = %q, want %q", k, got, want)
		}
	}
	if capture.attrs["errorId"] == "" {
		t.Errorf("errorId should still be logged")
	}
}

func TestWriteErrorReqNamesTheKey(t *testing.T) {
	capture := &attrCaptureHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(prev) })

	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	r = r.WithContext(ContextWithSubject(r.Context(), NewTestSubject(SubjectFields{UserID: "system:ci", AuthMethod: APIKeyAuthMethod, CredentialID: "key-1"})))
	WriteErrorReq(httptest.NewRecorder(), r, http.StatusForbidden, ErrorAuthorizationFailure, MsgAPIKeyRouteNotAllowed)
	if got := capture.attrs["credentialID"]; got != "key-1" {
		t.Errorf("credentialID = %q, want %q", got, "key-1")
	}

	anonymous := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	WriteErrorReq(httptest.NewRecorder(), anonymous, http.StatusUnauthorized, ErrorAuthenticationRequired, "Not authenticated")
	if got, ok := capture.attrs["credentialID"]; ok {
		t.Errorf("request with no identity logged credentialID %q", got)
	}
}

func TestWriteErrorReqKeepsShareIDsOutOfTheLog(t *testing.T) {
	capture := &attrCaptureHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(prev) })

	const shareID = "Ab3_dE5-gH7iJ9kL1mN0p"
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/share/" + shareID},
		{http.MethodGet, "/api/v1/share/" + shareID + "/artifacts/report.txt"},
		{http.MethodPost, "/api/v1/share/" + shareID + "/fork"},
		{http.MethodPatch, "/api/v1/share/" + shareID},
		{http.MethodDelete, "/api/v1/share/" + shareID + "/users"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			WriteErrorReq(httptest.NewRecorder(), r, http.StatusUnauthorized, ErrorInvalidToken, "Invalid or expired token")

			got := capture.attrs["path"]
			if got != sharePathPlaceholder {
				t.Errorf("path = %q, want %q", got, sharePathPlaceholder)
			}
			if strings.Contains(got, shareID) {
				t.Errorf("path %q leaks the share id", got)
			}
		})
	}
}

func TestWriteErrorNoRequestAttribution(t *testing.T) {
	capture := &attrCaptureHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(prev) })

	WriteError(httptest.NewRecorder(), http.StatusUnauthorized, ErrorInvalidToken, "Invalid or expired token")

	for _, k := range []string{"method", "path", "clientIP", "userAgent"} {
		if _, ok := capture.attrs[k]; ok {
			t.Errorf("WriteError unexpectedly logged %q", k)
		}
	}
	if capture.attrs["errorId"] == "" || capture.attrs["httpStatus"] == "" {
		t.Errorf("WriteError must still log errorId and httpStatus")
	}
}

func TestWriteErrorReqCapsLongFields(t *testing.T) {
	capture := &attrCaptureHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(prev) })

	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	r.Header.Set("User-Agent", strings.Repeat("A", 1000))
	WriteErrorReq(httptest.NewRecorder(), r, http.StatusUnauthorized, ErrorInvalidToken, "x")

	if got := len(capture.attrs["userAgent"]); got != maxAuthLogFieldLen {
		t.Errorf("userAgent length = %d, want capped to %d", got, maxAuthLogFieldLen)
	}
}

func TestClientIP(t *testing.T) {
	for _, tc := range []struct {
		name, xff, xRealIP, remoteAddr, want string
	}{
		{"xff multi-hop takes left-most", "203.0.113.7, 10.0.0.1", "", "10.0.0.9:1234", "203.0.113.7"},
		{"xff single", "203.0.113.9", "", "10.0.0.9:1234", "203.0.113.9"},
		{"xff beats x-real-ip", "203.0.113.7", "198.51.100.4", "10.0.0.9:1234", "203.0.113.7"},
		{"x-real-ip when no xff", "", "198.51.100.4", "10.0.0.9:1234", "198.51.100.4"},
		{"blank xff falls through to remoteAddr", "   ", "", "192.0.2.5:55000", "192.0.2.5"},
		{"empty left-most xff hop falls through", ", 10.0.0.1", "", "192.0.2.5:55000", "192.0.2.5"},
		{"remoteAddr host when no headers", "", "", "192.0.2.5:55000", "192.0.2.5"},
		{"ipv6 remoteAddr strips brackets and port", "", "", "[2001:db8::1]:8080", "2001:db8::1"},
		{"remoteAddr raw when not host:port", "", "", "unixsocket", "unixsocket"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.xRealIP != "" {
				r.Header.Set("X-Real-IP", tc.xRealIP)
			}
			if got := clientIP(r); got != tc.want {
				t.Errorf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWriteErrorLogLevel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   slog.Level
	}{
		{"401 invalid token is warn", http.StatusUnauthorized, slog.LevelWarn},
		{"403 authorization failure is warn", http.StatusForbidden, slog.LevelWarn},
		{"400 bad request is warn", http.StatusBadRequest, slog.LevelWarn},
		{"500 server configuration is error", http.StatusInternalServerError, slog.LevelError},
		{"502 refresh failure is error", http.StatusBadGateway, slog.LevelError},
		{"503 service unavailable is error", http.StatusServiceUnavailable, slog.LevelError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := &levelCaptureHandler{}
			prev := slog.Default()
			slog.SetDefault(slog.New(capture))
			t.Cleanup(func() { slog.SetDefault(prev) })

			WriteError(httptest.NewRecorder(), tc.status, ErrorInvalidToken, "detail")

			if capture.level != tc.want {
				t.Errorf("level = %v, want %v", capture.level, tc.want)
			}
		})
	}
}
