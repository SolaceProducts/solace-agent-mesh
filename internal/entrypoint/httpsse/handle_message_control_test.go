// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const (
	controlPartRefusalLogMsg = "submit message: control response part refused"
	controlAnswerMarker      = "answer-content-marker"
)

func controlParts() map[string]map[string]any {
	return map[string]map[string]any{
		"user_input_response": {"kind": "data", "data": map[string]any{
			"type": "user_input_response", "request_id": "req-of-alice", "status": "answered",
			"data": map[string]any{"field": controlAnswerMarker},
		}},
		"auth_response": {"kind": "data", "data": map[string]any{
			"type": "auth_response", "target_agent_task_id": "task-of-alice", "function_call_id": "call-1", "code": controlAnswerMarker,
		}},
	}
}

func assertControlPartRefused(t *testing.T, f *messageOwnerFixture, rec *httptest.ResponseRecorder, trackedBefore int) {
	t.Helper()
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	rpcErr := decodeRPCError(t, rec)
	if rpcErr == nil || rpcErr.Message != controlResponseRefusedMsg || rpcErr.Code != codeInvalidParams {
		t.Fatalf("error = %+v, want code %d message %q", rpcErr, codeInvalidParams, controlResponseRefusedMsg)
	}
	if strings.Contains(rec.Body.String(), "req-of-alice") || strings.Contains(rec.Body.String(), "task-of-alice") {
		t.Errorf("the refusal echoed the id the part named: %s", rec.Body.String())
	}
	if got := f.sse.totalTasks(); got != trackedBefore {
		t.Errorf("a refused submit left %d tasks registered in the SSE manager, want %d", got, trackedBefore)
	}
}

func TestMessageRoutesRefuseControlResponseParts(t *testing.T) {
	for _, route := range messageRoutes {
		for name, part := range controlParts() {
			for _, caller := range []string{createOwnerAlice, createOwnerBob} {
				t.Run(route+"/"+name+"/"+caller, func(t *testing.T) {
					f, q := newMessageOwnerFixture(t)
					before := f.sse.totalTasks()

					rec := f.postParts(route, caller, textPart(ownerTestPrompt), part)

					assertControlPartRefused(t, f, rec, before)
					if msg := publishedTask(t, f.brk, q, 200*time.Millisecond); msg != nil {
						t.Errorf("a task carrying a control response was published: %s", msg.Payload)
					}
					refusals := strings.Count(f.logs.String(), controlPartRefusalLogMsg)
					if refusals != 1 {
						t.Fatalf("want exactly one refusal WARN, got %d:\n%s", refusals, f.logs.String())
					}
					for _, want := range []string{`"level":"WARN"`, `"userID":"` + caller + `"`, `"responseType":"` + name + `"`} {
						if !strings.Contains(f.logs.String(), want) {
							t.Errorf("refusal log lacks %s:\n%s", want, f.logs.String())
						}
					}
					for _, secret := range []string{controlAnswerMarker, "req-of-alice", "task-of-alice", ownerTestPrompt} {
						if strings.Contains(f.logs.String(), secret) {
							t.Errorf("the refusal logged %q", secret)
						}
					}
				})
			}
		}
	}
}

func TestMessageRoutesRefuseControlResponsePartsWhateverElseTheRequestSays(t *testing.T) {
	f, q := newMessageOwnerFixture(t)
	ownContext := idgen.New()
	if rec := f.post("/api/v1/message:send", createOwnerAlice, ownContext); rec.Code != http.StatusOK {
		t.Fatalf("seed alice's session: %d %s", rec.Code, rec.Body.String())
	}
	if publishedTask(t, f.brk, q, 2*time.Second) == nil {
		t.Fatal("seed task was not published")
	}

	for name, body := range map[string]map[string]any{
		"in the caller's own context":      {"contextId": ownContext, "metadata": map[string]any{"agent_name": ownerTestAgent}},
		"for an agent that does not exist": {"metadata": map[string]any{"agent_name": "no-such-agent"}},
		"with no agent named":              {},
	} {
		t.Run(name, func(t *testing.T) {
			before := f.sse.totalTasks()
			message := map[string]any{"kind": "message", "role": "user", "parts": []map[string]any{controlParts()["user_input_response"]}}
			for k, v := range body {
				message[k] = v
			}
			raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "req1", "params": map[string]any{"message": message}})
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			f.mux.ServeHTTP(rec, asUser(httptest.NewRequest(http.MethodPost, "/api/v1/message:stream", bytes.NewReader(raw)), createOwnerAlice))
			assertControlPartRefused(t, f, rec, before)
			if msg := publishedTask(t, f.brk, q, 200*time.Millisecond); msg != nil {
				t.Errorf("a task carrying a control response was published: %s", msg.Payload)
			}
		})
	}
}

func TestMessageRoutesStillAcceptOtherDataParts(t *testing.T) {
	f, q := newMessageOwnerFixture(t)
	for name, part := range map[string]map[string]any{
		"a data part of another type":              {"kind": "data", "data": map[string]any{"type": "agent_progress_update", "status": "x"}},
		"a data part with no type":                 {"kind": "data", "data": map[string]any{"field": "value"}},
		"text that names a control response":       textPart("user_input_response auth_response"),
		"a text part carrying control-shaped data": {"kind": "text", "text": "hi", "data": map[string]any{"type": "user_input_response"}},
	} {
		t.Run(name, func(t *testing.T) {
			rec := f.postParts("/api/v1/message:send", createOwnerBob, part)
			assertArtifactAccepted(t, f, q, rec)
		})
	}
}

func TestControlResponsePartsAreRefusedThroughTheComposedHandler(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: ownerTestAgent, URL: "solace:test"})
	brk := core.Services().Broker()
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", ownerTestAgent)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	send := func(route string, parts ...map[string]any) *httptest.ResponseRecorder {
		raw, marshalErr := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "req1", "params": map[string]any{"message": map[string]any{
			"kind": "message", "role": "user", "parts": parts,
			"metadata": map[string]any{"agent_name": ownerTestAgent},
		}}})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		req := httptest.NewRequest(http.MethodPost, route, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-SAM-User-ID", createOwnerBob)
		rec := httptest.NewRecorder()
		adapter.Handler().ServeHTTP(rec, req)
		return rec
	}

	for _, route := range messageRoutes {
		for name, part := range controlParts() {
			if rec := send(route, part); rec.Code != http.StatusBadRequest {
				t.Errorf("%s with %s: status %d, want 400, body=%s", route, name, rec.Code, rec.Body.String())
			}
			if msg := publishedTask(t, brk, q, 200*time.Millisecond); msg != nil {
				t.Errorf("%s with %s published a task: %s", route, name, msg.Payload)
			}
		}
	}
	if rec := send("/api/v1/message:send", textPart(ownerTestPrompt)); rec.Code != http.StatusOK {
		t.Fatalf("an ordinary message: status %d, body=%s", rec.Code, rec.Body.String())
	}
	if publishedTask(t, brk, q, 2*time.Second) == nil {
		t.Fatal("an ordinary message was not published")
	}
}
