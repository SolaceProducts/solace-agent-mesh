// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/extensions/sessioncontrol"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2arpc"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type fakeAgentReply func(req *a2a.Request) *a2a.Response

func compactSetup(t *testing.T, agentName string, reply fakeAgentReply) (
	http.Handler, *sessionHandler, *store.SessionRepo, *store.ChatTaskRepo, *store.TaskRepo,
) {
	t.Helper()

	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)
	core := adapter.core
	db := core.Store()
	taskRepo := store.NewTaskRepo(db)

	if agentName != "" {
		core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
			Name: agentName,
			Capabilities: a2a.AgentCapabilities{
				Extensions: []a2a.AgentExtension{{URI: sessioncontrol.URI}},
			},
		})
	}

	if reply != nil {
		brk := broker.NewDevBroker()
		if err := brk.Connect(t.Context()); err != nil {
			t.Fatalf("dev broker connect: %v", err)
		}
		t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

		const ns = "test-ns"
		const gw = "test-gw"

		agentQ, qErr := brk.CreateQueue("agent-req", broker.QueueOptions{Named: true})
		if qErr != nil {
			t.Fatalf("create agent queue: %v", qErr)
		}
		if subErr := brk.Subscribe(agentQ, a2a.AgentRequestTopic(ns, agentName)); subErr != nil {
			t.Fatalf("subscribe agent topic: %v", subErr)
		}
		client := a2arpc.New(a2arpc.Config{Broker: brk, Namespace: ns, GatewayID: gw, Timeout: 2 * time.Second})
		core.SetRPCClient(client)

		loopCtx, cancelLoops := context.WithCancel(t.Context())

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				msg, err := brk.Receive(loopCtx, agentQ)
				if err != nil {
					return
				}
				if msg == nil {
					continue
				}
				var req a2a.Request
				if jErr := json.Unmarshal(msg.Payload, &req); jErr != nil {
					msg.Ack()
					continue
				}
				props := a2a.DecodeUserProperties(msg.UserProperties)
				resp := reply(&req)
				resp.JSONRPC = "2.0"
				resp.ID = req.ID
				payload, _ := json.Marshal(resp)
				_ = brk.Publish(loopCtx, props.ReplyTo, &broker.Message{Payload: payload})
				msg.Ack()
			}
		}()
		t.Cleanup(wg.Wait)
		t.Cleanup(cancelLoops)
	}

	h := &sessionHandler{
		core: core, sessRepo: sessRepo, chatRepo: chatRepo, taskRepo: taskRepo,
		sseRepo: sseRepo, logger: slog.Default(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/compact", h.handleCompact)
	return mux, h, sessRepo, chatRepo, taskRepo
}

func seedCompactSession(t *testing.T, sessRepo *store.SessionRepo, agentName, userID string) string {
	t.Helper()
	sid := idgen.New()
	if err := sessRepo.Create(context.Background(), &store.Session{
		ID:      sid,
		UserID:  userID,
		AgentID: ptrTo(agentName),
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return sid
}

func ptrTo[T any](v T) *T { return &v }

func TestCompact_SessionNotFound(t *testing.T) {
	mux, _, _, _, _ := compactSetup(t, "", nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/missing/compact", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestCompact_TargetPercentValidation(t *testing.T) {
	mux, _, sessRepo, _, _ := compactSetup(t, "agent-a", nil)
	sid := seedCompactSession(t, sessRepo, "agent-a", "sam_dev_user")

	body := strings.NewReader(`{"targetPercent": 150}`)
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+sid+"/compact", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestCompact_AgentDoesNotSupportExtension(t *testing.T) {
	mux, _, sessRepo, _, _ := compactSetup(t, "", nil)
	sid := seedCompactSession(t, sessRepo, "agent-without-ext", "sam_dev_user")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+sid+"/compact", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestCompact_HappyPath_WritesSyntheticRows(t *testing.T) {
	const agentName = "agent-happy"
	reply := func(_ *a2a.Request) *a2a.Response {
		result := sessioncontrol.Result{
			SummarizedMessagesCount:       7,
			TokensFreed:                   2200,
			PostCompactionRemainingTokens: 1500,
			SummarizerInputTokens:         300,
			SummarizerOutputTokens:        80,
		}
		return &a2a.Response{Result: result}
	}
	mux, _, sessRepo, chatRepo, taskRepo := compactSetup(t, agentName, reply)

	sid := seedCompactSession(t, sessRepo, agentName, "sam_dev_user")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+sid+"/compact",
		strings.NewReader(`{"targetPercent": 50}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var resp compactResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.SessionID != sid || resp.EventsCompacted != 7 {
		t.Errorf("response fields mismatch: %+v", resp)
	}

	tasks, err := taskRepo.ListBySessionForUserWithTokens(context.Background(), sid, "sam_dev_user")
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Kind != store.TaskKindCompactionCost {
		t.Fatalf("expected one compaction-cost task, got %+v", tasks)
	}
	if got := *tasks[0].PostCompactionRemainingTokens; got != 1500 {
		t.Errorf("PostCompactionRemainingTokens = %d, want 1500", got)
	}

	chats, err := chatRepo.ListBySession(context.Background(), sid)
	if err != nil {
		t.Fatalf("list chat tasks: %v", err)
	}
	found := false
	for _, c := range chats {
		if c.Kind == store.ChatTaskKindNotification {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected a manual-compaction chat task")
	}
}

func TestCompact_MintsTheCostTaskAndTraceBeforeTheCall(t *testing.T) {
	const agentName = "agent-ids"
	sent := make(chan sessioncontrol.CompactParams, 1)
	reply := func(req *a2a.Request) *a2a.Response {
		raw, _ := json.Marshal(req.Params)
		var params sessioncontrol.CompactParams
		_ = json.Unmarshal(raw, &params)
		sent <- params
		return &a2a.Response{Result: sessioncontrol.Result{SummarizerInputTokens: 10, SummarizerOutputTokens: 5}}
	}
	mux, _, sessRepo, _, taskRepo := compactSetup(t, agentName, reply)

	sid := seedCompactSession(t, sessRepo, agentName, "sam_dev_user")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/sessions/"+sid+"/compact", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	params := <-sent
	if params.TaskID == "" || params.TraceID == "" || params.TaskID == params.TraceID {
		t.Fatalf("agent received task %q and trace %q, want two distinct minted ids", params.TaskID, params.TraceID)
	}
	tasks, err := taskRepo.ListBySessionForUserWithTokens(context.Background(), sid, "sam_dev_user")
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != params.TaskID {
		t.Fatalf("cost row = %+v, want its id to be the task id %q the agent received", tasks, params.TaskID)
	}
}

func TestCompact_CarriesTheOriginATaskSubmissionWould(t *testing.T) {
	const (
		agentName  = "agent-origin"
		userRow    = "0197a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
		gatewayRow = "0197a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5c"
	)
	sent := make(chan sessioncontrol.CompactParams, 1)
	reply := func(req *a2a.Request) *a2a.Response {
		raw, _ := json.Marshal(req.Params)
		var params sessioncontrol.CompactParams
		_ = json.Unmarshal(raw, &params)
		sent <- params
		return &a2a.Response{Result: sessioncontrol.Result{}}
	}
	mux, h, sessRepo, _, _ := compactSetup(t, agentName, reply)
	h.core.Config().GatewayUUID = gatewayRow

	sid := seedCompactSession(t, sessRepo, agentName, "alice")
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+sid+"/compact", nil)
	req.Header.Set(httputil.HeaderClientSurface, httputil.SurfaceCLI)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: "alice", PlatformUserID: userRow})))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	want := a2a.Origin{Channel: httputil.SurfaceCLI, UserUUID: userRow, EntrypointUUID: gatewayRow}
	if got := (<-sent).Origin; got != want {
		t.Errorf("agent received origin %+v, want %+v", got, want)
	}
}

func TestCompact_AgentRPCError(t *testing.T) {
	const agentName = "agent-err"
	reply := func(_ *a2a.Request) *a2a.Response {
		return &a2a.Response{
			Error: &a2a.Error{Code: -32603, Message: "summariser failed"},
		}
	}
	mux, _, sessRepo, _, _ := compactSetup(t, agentName, reply)

	sid := seedCompactSession(t, sessRepo, agentName, "sam_dev_user")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+sid+"/compact", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestCompact_AgentNotFoundMapsTo404(t *testing.T) {
	const agentName = "agent-nf"
	reply := func(_ *a2a.Request) *a2a.Response {
		return &a2a.Response{
			Error: &a2a.Error{Code: -32001, Message: "session not found"},
		}
	}
	mux, _, sessRepo, _, _ := compactSetup(t, agentName, reply)
	sid := seedCompactSession(t, sessRepo, agentName, "sam_dev_user")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+sid+"/compact", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestCompact_AgentConflictMapsTo409(t *testing.T) {
	const agentName = "agent-conflict"
	reply := func(_ *a2a.Request) *a2a.Response {
		return &a2a.Response{
			Error: &a2a.Error{Code: -32002, Message: "already compacting"},
		}
	}
	mux, _, sessRepo, _, _ := compactSetup(t, agentName, reply)
	sid := seedCompactSession(t, sessRepo, agentName, "sam_dev_user")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+sid+"/compact", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestCompact_LocalLockReturns409ForConcurrentRequest(t *testing.T) {
	mux, h, sessRepo, _, _ := compactSetup(t, "agent-lock", nil)
	sid := seedCompactSession(t, sessRepo, "agent-lock", "sam_dev_user")

	release, ok := h.compactLocks.tryLock(sid)
	if !ok {
		t.Fatal("could not acquire seed lock")
	}
	defer release()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+sid+"/compact", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestCompact_RPCTimeoutMapsTo504(t *testing.T) {
	restore := setCompactRPCTimeout(100 * time.Millisecond)
	t.Cleanup(restore)

	const agentName = "agent-slow"
	stop := make(chan struct{})
	reply := func(_ *a2a.Request) *a2a.Response {
		<-stop
		return &a2a.Response{Result: sessioncontrol.Result{}}
	}
	mux, _, sessRepo, _, _ := compactSetup(t, agentName, reply)
	t.Cleanup(func() { close(stop) })

	sid := seedCompactSession(t, sessRepo, agentName, "sam_dev_user")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+sid+"/compact", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestMapCompactError_MapsAllErrorCodes(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
	}{
		{"deadline", context.DeadlineExceeded, http.StatusGatewayTimeout},
		{"not found", &a2arpc.RPCError{Code: -32001, Message: "x"}, http.StatusNotFound},
		{"conflict", &a2arpc.RPCError{Code: -32002, Message: "x"}, http.StatusConflict},
		{"forbidden", &a2arpc.RPCError{Code: -32003, Message: "x"}, http.StatusNotFound},
		{"other rpc", &a2arpc.RPCError{Code: -32603, Message: "x"}, http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, _ := mapCompactError(tc.err)
			if status != tc.wantCode {
				t.Errorf("status = %d, want %d", status, tc.wantCode)
			}
		})
	}
}
