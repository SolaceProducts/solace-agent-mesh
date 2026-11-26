// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func TestUserIDFromRequest_EnterpriseIdentity_PrefersEmail(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest("GET", "/test", nil)
	identity := auth.NewTestSubject(auth.SubjectFields{
		UserID: "user-uuid-12345",
		Email:  "alice@example.com",
	})
	req = req.WithContext(auth.ContextWithSubject(req.Context(), identity))

	got := userIDFromRequest(req)
	if got != "alice@example.com" {
		t.Errorf("userIDFromRequest = %q, want %q", got, "alice@example.com")
	}
}

func TestUserIDFromRequest_EnterpriseIdentity_FallsBackToUserID(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest("GET", "/test", nil)
	identity := auth.NewTestSubject(auth.SubjectFields{UserID: "enterprise-user@example.com"})
	req = req.WithContext(auth.ContextWithSubject(req.Context(), identity))

	got := userIDFromRequest(req)
	if got != "enterprise-user@example.com" {
		t.Errorf("userIDFromRequest = %q, want %q", got, "enterprise-user@example.com")
	}
}

func TestUserIDFromRequest_CommunityFallback(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest("GET", "/test", nil)
	var userID string
	platpkg.StubAuthMiddleware(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			userID = userIDFromRequest(r)
		}),
	).ServeHTTP(httptest.NewRecorder(), req)

	if userID != "stub@example.com" {
		t.Errorf("userIDFromRequest = %q, want %q", userID, "stub@example.com")
	}
}

func TestUserIDFromRequest_NoContext(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest("GET", "/test", nil)
	got := userIDFromRequest(req)
	if got != "unknown" {
		t.Errorf("userIDFromRequest = %q, want %q", got, "unknown")
	}
}

func TestHandleServiceError_ExternalServiceError(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	err := &ExternalServiceError{
		Service: "LLM",
		Message: "model not available",
	}
	handleServiceError(w, err)
	if w.Code != http.StatusBadGateway {
		t.Errorf("handleServiceError(ExternalServiceError) = %d, want %d", w.Code, http.StatusBadGateway)
	}
}

func TestHandleServiceError_ValidationError(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	err := &ValidationError{Message: "bad input"}
	handleServiceError(w, err)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("handleServiceError(ValidationError) = %d, want %d", w.Code, http.StatusUnprocessableEntity)
	}
}

func TestParsePagination_RejectsInvalidValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		query   string
		wantErr bool
	}{
		{"", false},
		{"pageNumber=1&pageSize=20", false},
		{"pageSize=0", true},
		{"pageSize=-1", true},
		{"pageSize=101", true},
		{"pageSize=abc", true},
		{"pageNumber=0", true},
		{"pageNumber=-3", true},
		{"pageNumber=abc", true},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/x?"+tt.query, nil)
			_, _, err := parsePagination(req)
			if tt.wantErr && err == nil {
				t.Errorf("parsePagination(%q) returned nil error, want error", tt.query)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("parsePagination(%q) returned %v, want nil", tt.query, err)
			}
		})
	}
}

type syncedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncedBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncedBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestWithLoggerName_StartsFreshChain(t *testing.T) {
	t.Parallel()

	buf := &syncedBuffer{}
	prevDefault := slog.Default()
	defer slog.SetDefault(prevDefault)
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))

	parent := slog.Default().With("logger_name", "platform")
	child := withLoggerName(parent, "enterprise-platform")
	grandchild := withLoggerName(child, "reconciler")

	parent.Info("p")
	child.Info("c")
	grandchild.Info("g")

	wantMsgs := map[string]struct{}{"msg=p": {}, "msg=c": {}, "msg=g": {}}
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		matched := ""
		for m := range wantMsgs {
			if strings.Contains(line, m) {
				matched = m
				break
			}
		}
		if matched == "" {
			continue
		}
		if strings.Count(line, "logger_name=") != 1 {
			t.Errorf("log line carries %d logger_name= attrs, want exactly 1: %s",
				strings.Count(line, "logger_name="), line)
		}
	}

	wantPairs := []struct{ msg, comp string }{
		{`msg=p`, "logger_name=platform"},
		{`msg=c`, "logger_name=enterprise-platform"},
		{`msg=g`, "logger_name=reconciler"},
	}
	for _, want := range wantPairs {
		if !strings.Contains(buf.String(), want.msg) || !strings.Contains(buf.String(), want.comp) {
			t.Errorf("output missing %q + %q. got:\n%s", want.msg, want.comp, buf.String())
		}
	}
}

func TestWriteErrorWithCause_ContextCanceledLogsAtWarn(t *testing.T) {
	causes := []error{
		context.Canceled,
		context.DeadlineExceeded,
		fmt.Errorf("list users: %w", context.Canceled),
	}
	for _, cause := range causes {
		t.Run(cause.Error(), func(t *testing.T) {
			buf := &syncedBuffer{}
			prevDefault := slog.Default()
			defer slog.SetDefault(prevDefault)
			slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))

			rec := httptest.NewRecorder()
			writeErrorWithCause(rec, http.StatusInternalServerError, "internal error", nil, cause)

			if rec.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500 unchanged (this is a log-level-only fix)", rec.Code)
			}
			out := buf.String()
			if strings.Contains(out, "level=ERROR") {
				t.Errorf("log = %q, want no ERROR line for a canceled/timed-out context", out)
			}
			if !strings.Contains(out, "level=WARN") {
				t.Errorf("log = %q, want a WARN line", out)
			}
		})
	}
}

func TestWriteErrorWithCause_OtherServerErrorsStillLogAtError(t *testing.T) {
	buf := &syncedBuffer{}
	prevDefault := slog.Default()
	defer slog.SetDefault(prevDefault)
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))

	rec := httptest.NewRecorder()
	writeErrorWithCause(rec, http.StatusInternalServerError, "internal error", nil, errors.New("boom"))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(buf.String(), "level=ERROR") {
		t.Errorf("log = %q, want an ERROR line for a genuine server fault", buf.String())
	}
}

func TestValidatePathUUID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"empty passes", "", false},
		{"valid v4 UUID", "550e8400-e29b-41d4-a716-446655440000", false},
		{"valid v7 UUID", "018f3a3b-2c1d-7000-8000-000000000000", false},
		{"random word rejected", "not-a-uuid", true},
		{"sql injection attempt rejected", "1; DROP TABLE connectors;", true},
		{"truncated uuid rejected", "550e8400-e29b-41d4", true},
		{"urn spelling rejected", "urn:uuid:550e8400-e29b-41d4-a716-446655440000", true},
		{"braced spelling rejected", "{550e8400-e29b-41d4-a716-446655440000}", true},
		{"unhyphenated spelling rejected", "550e8400e29b41d4a716446655440000", true},
		{"uppercase canonical accepted", "550E8400-E29B-41D4-A716-446655440000", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validatePathUUID(tt.id)
			if tt.wantErr && err == nil {
				t.Errorf("validatePathUUID(%q) = nil, want ValidationError", tt.id)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validatePathUUID(%q) = %v, want nil", tt.id, err)
			}
			if tt.wantErr && err != nil {
				if _, ok := err.(*ValidationError); !ok {
					t.Errorf("validatePathUUID(%q) = %T, want *ValidationError (so handleServiceError maps to 400)", tt.id, err)
				}
			}
		})
	}
}

func TestHandleServiceError_ConflictLogsLogMessage(t *testing.T) {
	buf := &syncedBuffer{}
	prevDefault := slog.Default()
	defer slog.SetDefault(prevDefault)
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))

	rec := httptest.NewRecorder()
	handleServiceError(rec, &ConflictError{Message: `A budget named "canary-Ω" already exists.`, LogMessage: "budget name already exists"})

	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "canary-Ω") {
		t.Errorf("response = %d %s, want a 409 naming the budget", rec.Code, rec.Body.String())
	}
	if logged := buf.String(); strings.Contains(logged, "canary-Ω") || !strings.Contains(logged, "budget name already exists") {
		t.Errorf("log = %q, want LogMessage and no name", logged)
	}
}

func TestParseExpandPlatform_ReadsRepeatedValues(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/x?expand=a,b&expand=c&expand=", nil)
	if got := parseExpandPlatform(r); !maps.Equal(got, map[string]bool{"a": true, "b": true, "c": true}) {
		t.Errorf("parseExpandPlatform = %v, want a, b and c", got)
	}
}
