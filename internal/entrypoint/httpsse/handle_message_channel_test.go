// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
)

func TestHandleMessage_StampsTheChannelFromTheClosedHeaderSetAndAnEvalSessionSource(t *testing.T) {
	cases := []struct {
		name        string
		header      string
		metadata    string
		wantChannel string
		wantTrigger string
	}{
		{"cli header", httputil.SurfaceCLI, "", httputil.SurfaceCLI, a2a.TriggerInteractive},
		{"no header", "", "", httputil.SurfaceAPI, a2a.TriggerInteractive},
		{"a relay surface cannot be claimed", httputil.SurfaceSlack, "", httputil.SurfaceAPI, a2a.TriggerInteractive},
		{"the scheduler cannot be claimed by header", a2a.ChannelScheduler, "", httputil.SurfaceAPI, a2a.TriggerInteractive},
		{"a forged scheduler source is stripped", httputil.SurfaceWebUI, `,"source":"scheduler"`, httputil.SurfaceWebUI, a2a.TriggerInteractive},
		{"an eval session books the eval channel", "", `,"session_source":"eval"`, a2a.ChannelEval, a2a.TriggerInteractive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
				`"parts":[{"kind":"text","text":"hello"}],"metadata":{"agent_name":"test-agent"` + tc.metadata + `}}}}`
			mux := http.NewServeMux()
			mux.HandleFunc("POST /api/v1/message:stream", h.handleMessageStream)
			req := httptest.NewRequest("POST", "/api/v1/message:stream", strings.NewReader(body))
			if tc.header != "" {
				req.Header.Set(httputil.HeaderClientSurface, tc.header)
			}
			req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})))
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("send message: %d %s", rec.Code, rec.Body.String())
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			msg, err := brk.Receive(ctx, q)
			if err != nil {
				t.Fatalf("no task published: %v", err)
			}
			props := a2a.DecodeUserProperties(msg.UserProperties)
			if props.Channel != tc.wantChannel || props.Trigger != tc.wantTrigger {
				t.Errorf("published channel %q trigger %q, want %q %q", props.Channel, props.Trigger, tc.wantChannel, tc.wantTrigger)
			}
		})
	}
}

func TestHandleMessage_StampsTheAuthenticatedCallersPlatformUserRow(t *testing.T) {
	const platformUserID = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c40"
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
		`"parts":[{"kind":"text","text":"hello"}],"metadata":{"agent_name":"test-agent"}}}}`
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/message:stream", h.handleMessageStream)
	req := httptest.NewRequest("POST", "/api/v1/message:stream", strings.NewReader(body))
	req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: "user-1", PlatformUserID: platformUserID})))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("send message: %d %s", rec.Code, rec.Body.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("no task published: %v", err)
	}
	if got := a2a.DecodeUserProperties(msg.UserProperties).UserUUID; got != platformUserID {
		t.Errorf("published userUuid = %q, want the caller's users.id %s", got, platformUserID)
	}
}
