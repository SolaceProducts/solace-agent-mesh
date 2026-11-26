// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httputil //nolint:revive // internal package, no conflict risk

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

func TestAPIVersionIsADate(t *testing.T) {
	declared, err := time.Parse(apiVersionLayout, samversion.APIVersion)
	if err != nil {
		t.Fatalf("samversion.APIVersion %q is not YYYY-MM-DD: %v", samversion.APIVersion, err)
	}
	if latest := time.Now().UTC().AddDate(0, 1, 0); declared.After(latest) {
		t.Fatalf("samversion.APIVersion %q is more than a month in the future; check the year", samversion.APIVersion)
	}
}

func TestAPIVersionLogMiddleware(t *testing.T) {
	tests := []struct {
		name     string
		declared string
		want     string
		omit     string
	}{
		{name: "valid date", declared: "2026-09-29", want: "clientAPIVersion=2026-09-29"},
		{name: "malformed", declared: "secret-ish-value", want: "length=16", omit: "secret-ish-value"},
		{name: "absent", declared: "", omit: "API version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			originalDefault := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
			t.Cleanup(func() { slog.SetDefault(originalDefault) })

			handler := Chain(RequestIDMiddleware, APIVersionLogMiddleware)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest("GET", "/api/v1/platform/agents", nil)
			if tt.declared != "" {
				req.Header.Set(HeaderAPIVersion, tt.declared)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want request to pass through", rec.Code)
			}
			if tt.want != "" && !strings.Contains(buf.String(), tt.want) {
				t.Errorf("log missing %q: %q", tt.want, buf.String())
			}
			if tt.omit != "" && strings.Contains(buf.String(), tt.omit) {
				t.Errorf("log must not contain %q: %q", tt.omit, buf.String())
			}
		})
	}
}
