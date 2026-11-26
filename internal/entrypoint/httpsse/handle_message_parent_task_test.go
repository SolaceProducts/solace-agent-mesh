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
)

func TestHandleMessage_StripsClientParentTaskID(t *testing.T) {
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

	const forgedParent = "019f624e-9634-784d-9de0-901f494b4b2a"
	body := `{"jsonrpc":"2.0","id":"req1","params":{"message":{"kind":"message","role":"user",` +
		`"parts":[{"kind":"text","text":"hello"}],"metadata":{"agent_name":"test-agent",` +
		`"parentTaskId":"` + forgedParent + `"}}}}`

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/message:stream", h.handleMessageStream)
	req := httptest.NewRequest("POST", "/api/v1/message:stream", strings.NewReader(body))
	req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: "attacker"})))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("send message: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("expected a task published to the agent: %v", err)
	}
	var env struct {
		Params struct {
			Message struct {
				Metadata map[string]any `json:"metadata"`
			} `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(msg.Payload, &env); err != nil {
		t.Fatalf("decode published request: %v", err)
	}
	if v, ok := env.Params.Message.Metadata["parentTaskId"]; ok {
		t.Errorf("client-supplied parentTaskId must be stripped, got %v", v)
	}
}
