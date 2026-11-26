// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const (
	secretUser  = "svcuser"
	secretPass  = "hunter2"
	secretToken = "abc123secret"
)

func captureDefaultLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func logRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		records = append(records, rec)
	}
	return records
}

func TestSSRF_LogsCarryNoCredentialsAndAreWarnings(t *testing.T) {
	closed := newInternalTarget(t, nil)
	closedAddr := closed.srv.Listener.Addr().String()
	closed.srv.Close()

	tests := []struct {
		name         string
		allowPrivate string
		urlFmt       string
		wantStatus   map[string]int
	}{
		{"blocked loopback", "", "http://" + secretUser + ":" + secretPass + "@" + closedAddr + "/x?token=" + secretToken,
			map[string]int{"remote agent card": 502, "openapi detectAuth": 502, "openapi parse": 502, "connector specification read": 502, "mcp discovery": 502}},
		{"blocked metadata", "", "http://" + secretUser + ":" + secretPass + "@169.254.169.254/x?token=" + secretToken,
			map[string]int{"remote agent card": 502, "openapi detectAuth": 502, "openapi parse": 502, "connector specification read": 502, "mcp discovery": 502}},
		{"refused after opt-out", "true", "http://" + secretUser + ":" + secretPass + "@" + closedAddr + "/x?token=" + secretToken,
			map[string]int{"remote agent card": 422, "openapi detectAuth": 502, "openapi parse": 502, "connector specification read": 502, "mcp discovery": 502}},
	}
	for _, tt := range tests {
		for _, ep := range shapeEndpoints() {
			t.Run(tt.name+"/"+ep.name, func(t *testing.T) {
				t.Setenv(allowPrivateMCPEnvVar, tt.allowPrivate)
				logs := captureDefaultLogs(t)
				endpoints := newSSRFEndpoints(t, nil, nil)

				res := ep.call(t, endpoints, tt.urlFmt)

				if res.status != tt.wantStatus[ep.name] {
					t.Fatalf("status = %d, want %d; body: %s", res.status, tt.wantStatus[ep.name], res.body)
				}
				for _, secret := range []string{secretUser, secretPass, secretToken} {
					if strings.Contains(logs.String(), secret) {
						t.Errorf("logs contain %q: %s", secret, logs.String())
					}
					if strings.Contains(string(res.body), secret) {
						t.Errorf("response contains %q: %s", secret, res.body)
					}
				}
				records := logRecords(t, logs)
				if len(records) != 1 {
					t.Fatalf("logged %d lines for one failed request, want 1: %s", len(records), logs.String())
				}
				if records[0]["level"] != "WARN" {
					t.Errorf("level = %v, want WARN", records[0]["level"])
				}
				if records[0]["errorId"] == nil {
					t.Error("log line has no errorId to join with the response")
				}
				if cause, _ := records[0]["error"].(string); cause == "" {
					t.Error("log line carries no cause to diagnose the failure with")
				}
			})
		}
	}
}

func TestSSRF_StoredCardRefreshLogsOnceWithoutCredentials(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "")
	var logs bytes.Buffer
	handler := slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})
	endpoints := newSSRFEndpoints(t, nil, nil)
	endpoints.svc.logger = slog.New(handler)

	created, err := endpoints.svc.Create(t.Context(), CreateRemoteAgentRequest{
		Description:       "remote agent whose stored URL carries credentials",
		AgentURL:          "http://" + secretUser + ":" + secretPass + "@127.0.0.1:1",
		Name:              "stored-credentials",
		AgentCardLocation: "well_known",
		AgentCardAuthType: "none",
		AgentAuthConfig:   map[string]any{},
	}, "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	logs.Reset()

	got, err := endpoints.svc.GetByIDWithCard(t.Context(), created.ID, true)
	if err != nil {
		t.Fatalf("GetByIDWithCard: %v", err)
	}
	if got.CardFetchError != cardBlockedMessage {
		t.Fatalf("CardFetchError = %q", got.CardFetchError)
	}
	for _, secret := range []string{secretUser, secretPass} {
		if strings.Contains(logs.String(), secret) || strings.Contains(got.CardFetchError, secret) {
			t.Errorf("credential %q leaked: %s", secret, logs.String())
		}
	}
	if n := len(logRecords(t, &logs)); n != 1 {
		t.Errorf("logged %d lines for one stored refresh, want 1: %s", n, logs.String())
	}
}

func TestWriteErrorWithCause_CallerTargetFailuresAreWarnings(t *testing.T) {
	cause := newOutboundCause(&url.Error{Op: "Get", URL: "http://x/?token=" + secretToken, Err: errors.New("boom")})
	tests := []struct {
		name      string
		status    int
		cause     error
		wantLevel string
	}{
		{"caller target at 502", http.StatusBadGateway, cause, "WARN"},
		{"unrelated 502 stays an error", http.StatusBadGateway, errors.New("plain"), "ERROR"},
		{"nil cause at 502 stays an error", http.StatusBadGateway, nil, "ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureDefaultLogs(t)
			writeErrorWithCause(httptest.NewRecorder(), tt.status, "message", nil, tt.cause)
			records := logRecords(t, logs)
			if len(records) != 1 || records[0]["level"] != tt.wantLevel {
				t.Fatalf("records = %v, want one %s", records, tt.wantLevel)
			}
			if strings.Contains(logs.String(), secretToken) {
				t.Errorf("log carries the query token: %s", logs.String())
			}
		})
	}
}
