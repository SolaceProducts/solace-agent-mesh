// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const dcrCanary = "SECRET-MUST-NOT-APPEAR"

func captureLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

func TestRegisterClient_MalformedBodyNotLogged(t *testing.T) {
	body := `{"client_secret":"` + dcrCanary + `","client_id":"cid"`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	logger, buf := captureLogger()
	t.Setenv("TOOL_OAUTH_CLIENT_ID", "env-cid")
	t.Setenv("TOOL_OAUTH_CLIENT_SECRET", "env-secret")

	_, _ = RegisterClientWithConfig(context.Background(), srv.URL+"/register",
		"http://localhost/cb", nil, RegistrationRequest{}, logger)

	got := buf.String()
	if !strings.Contains(got, "not valid JSON") {
		t.Fatalf("the parse-failure warning no longer fires; fixture is stale: %q", got)
	}
	if strings.Contains(got, dcrCanary) {
		t.Errorf("DCR response body reached the log: %q", got)
	}
	if !strings.Contains(got, "bodyLen") {
		t.Errorf("lost the body length, which distinguishes an empty response from a malformed one: %q", got)
	}
}

func TestLogDCRServerError_BodyNotLogged(t *testing.T) {
	logger, buf := captureLogger()

	logDCRServerError("https://idp.example.com/register", http.StatusBadRequest,
		[]byte(`{"error":"invalid_client","client_secret":"`+dcrCanary+`"}`), logger)

	got := buf.String()
	if !strings.Contains(got, "rejected") {
		t.Fatalf("the rejection warning no longer fires; fixture is stale: %q", got)
	}
	if strings.Contains(got, dcrCanary) {
		t.Errorf("rejected-DCR body reached the log: %q", got)
	}
	for _, want := range []string{"httpStatus=400", "idp.example.com"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q, which is the actionable part: %q", want, got)
		}
	}
}

func TestLogDCRServerError_RFC6749FieldsStillLogged(t *testing.T) {
	logger, buf := captureLogger()

	logDCRServerError("https://idp.example.com/register", http.StatusBadRequest,
		[]byte(`{"error":"invalid_redirect_uri","error_description":"redirect_uri not registered"}`), logger)

	got := buf.String()
	if !strings.Contains(got, "invalid_redirect_uri") || !strings.Contains(got, "not registered") {
		t.Errorf("the RFC 6749 diagnostic branch stopped reporting: %q", got)
	}
}
