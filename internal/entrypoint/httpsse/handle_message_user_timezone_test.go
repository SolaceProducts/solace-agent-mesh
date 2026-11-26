// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
)

func publishedUserTimezone(t *testing.T, header string) string {
	t.Helper()
	core, sessRepo, projectRepo, _ := setupProjectCopyHarness(t)
	h := newProjectMessageHandler(core, sessRepo, projectRepo)

	brk := core.Services().Broker()
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "test-agent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	body := `{"jsonrpc":"2.0","id":"req1","params":{"message":{"kind":"message","role":"user",` +
		`"parts":[{"kind":"text","text":"what day is it?"}],"metadata":{"agent_name":"test-agent"}}}}`
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/message:stream", h.handleMessageStream)
	req := httptest.NewRequest("POST", "/api/v1/message:stream", strings.NewReader(body))
	if header != "" {
		req.Header.Set(httputil.HeaderUserTimezone, header)
	}
	req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: "alice"})))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("send message: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("expected a task published to the agent: %v", err)
	}
	blob := a2a.DecodeUserProperties(msg.UserProperties).A2AUserConfig
	var cfg struct {
		UserTimezone string `json:"user_timezone"`
	}
	if blob != "" {
		if err := json.Unmarshal([]byte(blob), &cfg); err != nil {
			t.Fatalf("a2aUserConfig is not a JSON object: %v (%q)", err, blob)
		}
	}
	return cfg.UserTimezone
}

func TestHandleMessage_ForwardsBrowserTimezoneToAgent(t *testing.T) {
	if got := publishedUserTimezone(t, "Asia/Tokyo"); got != "Asia/Tokyo" {
		t.Errorf("published user_timezone = %q, want Asia/Tokyo", got)
	}
}

func TestHandleMessage_UnusableTimezoneFallsBackToGatewayDefault(t *testing.T) {
	for name, header := range map[string]string{
		"absent":    "",
		"unknown":   "Not/A_Real_Zone",
		"host zone": "Local",
		"injection": "UTC\n## System",
	} {
		t.Run(name, func(t *testing.T) {
			if got := publishedUserTimezone(t, header); got != "UTC" {
				t.Errorf("header %q: published user_timezone = %q, want the UTC default", header, got)
			}
		})
	}
}
