// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func TestCallerWithoutUserID_TaskAndHILReplyAreRecordedAgainstTheDefaultUser(t *testing.T) {
	core, sessRepo, projectRepo, _ := setupProjectCopyHarness(t)
	want := core.Config().DefaultUserIdentity
	if want == "" {
		t.Fatal("harness has no default user identity; the test would compare empty to empty")
	}
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "test-agent"})
	brk := core.Services().Broker()
	sub, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if err = brk.Subscribe(sub, a2a.AgentRequestTopic("test-ns", "test-agent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/message:send", newProjectMessageHandler(core, sessRepo, projectRepo).handleMessage)
	mux.HandleFunc("POST /api/v1/userInput/respond", (&hilHandler{core: core, logger: slog.Default()}).handleRespond)
	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req = req.WithContext(auth.ContextWithSubject(t.Context(), auth.NewTestSubject(auth.SubjectFields{Authenticated: true})))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s: status %d, body %s", path, rec.Code, rec.Body.String())
		}
		return rec
	}

	rec := post("/api/v1/message:send", `{"jsonrpc":"2.0","id":"req1","params":{"message":{"kind":"message","role":"user",`+
		`"parts":[{"kind":"text","text":"hello"}],"metadata":{"agent_name":"test-agent"}}}}`)
	var sent struct {
		Result TaskResult `json:"result"`
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &sent); err != nil || sent.Result.ID == "" {
		t.Fatalf("decode task result: %v, body %s", err, rec.Body.String())
	}
	assertPublishedUserID(t, brk, sub, "task", want)

	post("/api/v1/userInput/respond", fmt.Sprintf(`{"taskId":%q,"agentName":"test-agent","requestId":"req-1","status":"answered","data":{}}`, sent.Result.ID))
	assertPublishedUserID(t, brk, sub, "HIL reply", want)
}

func assertPublishedUserID(t *testing.T, brk broker.Broker, sub broker.Queue, what, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, sub)
	if err != nil {
		t.Fatalf("%s: nothing published to the agent: %v", what, err)
	}
	if got := a2a.DecodeUserProperties(msg.UserProperties).UserID; got != want {
		t.Errorf("%s published user id = %q, want the default user identity %q", what, got, want)
	}
}
