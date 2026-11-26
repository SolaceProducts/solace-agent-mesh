// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const (
	ownerTestAgent      = "test-agent"
	ownerTestPrompt     = "bob-private-prompt-text"
	submitRefusalLogMsg = "submit message: session belongs to another user"
	submitLookupLogMsg  = "submit message: look up session"
	autoCreateLogMsg    = "auto-create session"
)

func logLinesFor(logs *safeBuffer, msg, sessionID string) []string {
	var lines []string
	for l := range strings.SplitSeq(logs.String(), "\n") {
		if strings.Contains(l, msg) && strings.Contains(l, `"sessionID":"`+sessionID+`"`) {
			lines = append(lines, l)
		}
	}
	return lines
}

var messageRoutes = []string{"/api/v1/message:stream", "/api/v1/message:send"}

type messageOwnerFixture struct {
	handler  *messageHandler
	sse      *SSEManager
	mux      *http.ServeMux
	core     *entrypoint.GatewayCore
	sessRepo *store.SessionRepo
	chatRepo *store.ChatTaskRepo
	brk      broker.Broker
	logs     *safeBuffer
}

func newMessageOwnerFixture(t *testing.T) (*messageOwnerFixture, broker.Queue) {
	t.Helper()
	logs := &safeBuffer{}
	adapter, core, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: ownerTestAgent, URL: "solace:test"})

	h := &messageHandler{
		core: core, sseManager: adapter.sseManager, sessRepo: sessRepo, chatRepo: chatRepo,
		logger: slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	sessH := &sessionHandler{core: core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/message:stream", h.handleMessageStream)
	mux.HandleFunc("POST /api/v1/message:send", h.handleMessageSend)
	mux.HandleFunc("GET /api/v1/sessions/{id}", sessH.handleGet)

	brk := core.Services().Broker()
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", ownerTestAgent)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return &messageOwnerFixture{handler: h, sse: adapter.sseManager, mux: mux, core: core, sessRepo: sessRepo, chatRepo: chatRepo, brk: brk, logs: logs}, q
}

func (f *messageOwnerFixture) post(route, userID, contextID string) *httptest.ResponseRecorder {
	return f.postCtx(context.Background(), route, userID, contextID, nil)
}

func (f *messageOwnerFixture) postCtx(ctx context.Context, route, userID, contextID string, extraMetadata map[string]any) *httptest.ResponseRecorder {
	metadata := map[string]any{"agent_name": ownerTestAgent}
	maps.Copy(metadata, extraMetadata)
	message := map[string]any{
		"kind":     "message",
		"role":     "user",
		"parts":    []map[string]any{{"kind": "text", "text": ownerTestPrompt}},
		"metadata": metadata,
	}
	if contextID != "" {
		message["contextId"] = contextID
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "req1", "params": map[string]any{"message": message}})
	if err != nil {
		panic(err)
	}
	req := httptest.NewRequest(http.MethodPost, route, bytes.NewReader(body)).WithContext(ctx)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, asUser(req, userID))
	return rec
}

func publishedTask(t *testing.T, brk broker.Broker, q broker.Queue, wait time.Duration) *broker.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), wait)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		return nil
	}
	return msg
}

func decodeRPCError(t *testing.T, rec *httptest.ResponseRecorder) *ErrorDTO {
	t.Helper()
	var resp JSONRPCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode JSON-RPC response %q: %v", rec.Body.String(), err)
	}
	return resp.Error
}

func decodeTaskResult(t *testing.T, rec *httptest.ResponseRecorder) TaskResult {
	t.Helper()
	var resp struct {
		Result TaskResult `json:"result"`
		Error  *ErrorDTO  `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode JSON-RPC response %q: %v", rec.Body.String(), err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}
	return resp.Result
}

func TestHandleMessage_ContextID_ByOwnership(t *testing.T) {
	for _, route := range messageRoutes {
		t.Run(route, func(t *testing.T) {
			f, q := newMessageOwnerFixture(t)
			aliceCtx := idgen.New()
			seedAliceSession(t, f.sessRepo, aliceCtx)

			t.Run("owner continues own context", func(t *testing.T) {
				rec := f.post(route, createOwnerAlice, aliceCtx)
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
				}
				if got := decodeTaskResult(t, rec); got.ContextID != aliceCtx {
					t.Errorf("task contextId = %q, want %q", got.ContextID, aliceCtx)
				}
				if publishedTask(t, f.brk, q, 2*time.Second) == nil {
					t.Error("owner's task was not published")
				}
				assertSessionUntouched(t, f.sessRepo, aliceCtx)
				if lines := logLinesFor(f.logs, autoCreateLogMsg, aliceCtx); len(lines) > 0 {
					t.Errorf("continuing an existing session tried to create it: %v", lines)
				}
			})

			t.Run("non-owner is refused and nothing is submitted", func(t *testing.T) {
				trackedBefore := f.sse.totalTasks()
				rec := f.post(route, createOwnerBob, aliceCtx)
				if rec.Code != http.StatusNotFound {
					t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
				}
				if got := f.sse.totalTasks(); got != trackedBefore {
					t.Errorf("a refused submit left %d tasks registered in the SSE manager, want %d", got, trackedBefore)
				}
				rpcErr := decodeRPCError(t, rec)
				if rpcErr == nil || rpcErr.Message != sessionNotFoundMsg || rpcErr.Code != codeInvalidParams {
					t.Fatalf("error = %+v, want code %d message %q", rpcErr, codeInvalidParams, sessionNotFoundMsg)
				}
				if data, _ := rpcErr.Data.(map[string]any); data["traceId"] == nil {
					t.Errorf("refusal must carry the trace id like every other refused submit: %+v", rpcErr.Data)
				}
				assertNoSessionData(t, rec.Body.String())
				if msg := publishedTask(t, f.brk, q, 200*time.Millisecond); msg != nil {
					t.Errorf("a task was published for a context the caller does not own: %s", msg.Payload)
				}
				assertSessionUntouched(t, f.sessRepo, aliceCtx)
			})

			t.Run("unknown context is created for the caller", func(t *testing.T) {
				fresh := idgen.New()
				rec := f.post(route, createOwnerBob, fresh)
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
				}
				decodeTaskResult(t, rec)
				if publishedTask(t, f.brk, q, 2*time.Second) == nil {
					t.Error("task for a new context was not published")
				}
				got, err := f.sessRepo.GetByID(t.Context(), fresh)
				if err != nil || got == nil {
					t.Fatalf("session for the new context: %v, %v", got, err)
				}
				if got.UserID != createOwnerBob {
					t.Errorf("new session owner = %q, want %q", got.UserID, createOwnerBob)
				}
			})

			t.Run("no contextId mints one for the caller", func(t *testing.T) {
				rec := f.post(route, createOwnerBob, "")
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
				}
				minted := decodeTaskResult(t, rec).ContextID
				if minted == "" || minted == aliceCtx {
					t.Fatalf("minted contextId = %q", minted)
				}
				if publishedTask(t, f.brk, q, 2*time.Second) == nil {
					t.Error("task for a minted context was not published")
				}
			})
		})
	}
}

func TestHandleMessage_NonOwnerAnswerMatchesGetSession(t *testing.T) {
	f, _ := newMessageOwnerFixture(t)
	aliceCtx := idgen.New()
	seedAliceSession(t, f.sessRepo, aliceCtx)

	post := f.post("/api/v1/message:stream", createOwnerBob, aliceCtx)
	get := getSessionAs(f.mux, createOwnerBob, aliceCtx)

	if post.Code != get.Code {
		t.Fatalf("POST status %d != GET status %d", post.Code, get.Code)
	}
	var getErr ErrorResponse
	if err := json.Unmarshal(get.Body.Bytes(), &getErr); err != nil {
		t.Fatalf("decode GET body: %v", err)
	}
	if rpcErr := decodeRPCError(t, post); rpcErr == nil || rpcErr.Message != getErr.Message {
		t.Errorf("POST error %+v, want message %q as GET answers", rpcErr, getErr.Message)
	}
}

func TestHandleMessage_SchedulerSessionByOwnership(t *testing.T) {
	f, q := newMessageOwnerFixture(t)
	scheduler := "scheduler"
	sessionID := idgen.New()
	if err := f.sessRepo.Create(t.Context(), &store.Session{ID: sessionID, UserID: createOwnerAlice, Source: &scheduler}); err != nil {
		t.Fatalf("seed scheduler session: %v", err)
	}
	if err := f.chatRepo.Create(t.Context(), &store.ChatTask{
		ID: idgen.New(), SessionID: sessionID, UserID: createOwnerAlice, CreatedTime: 1,
		MessageBubbles: bubblesJSON(t, map[string]any{"type": "user", "text": "scheduled prior turn"}),
	}); err != nil {
		t.Fatalf("seed chat task: %v", err)
	}

	if rec := f.post("/api/v1/message:stream", createOwnerBob, sessionID); rec.Code != http.StatusNotFound {
		t.Fatalf("non-owner status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
	if msg := publishedTask(t, f.brk, q, 200*time.Millisecond); msg != nil {
		t.Fatalf("non-owner's task was published: %s", msg.Payload)
	}

	if rec := f.post("/api/v1/message:stream", createOwnerAlice, sessionID); rec.Code != http.StatusOK {
		t.Fatalf("owner status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	msg := publishedTask(t, f.brk, q, 2*time.Second)
	if msg == nil {
		t.Fatal("owner's task was not published")
	}
	if !strings.Contains(string(msg.Payload), "scheduled prior turn") {
		t.Errorf("scheduler history missing from the owner's task: %s", msg.Payload)
	}
}

func TestHandleMessage_NonOwnerRefusalIsLoggedAtWarn(t *testing.T) {
	f, _ := newMessageOwnerFixture(t)
	aliceCtx := idgen.New()
	seedAliceSession(t, f.sessRepo, aliceCtx)

	f.post("/api/v1/message:stream", createOwnerBob, aliceCtx)

	lines := logLinesFor(f.logs, submitRefusalLogMsg, aliceCtx)
	if len(lines) != 1 {
		t.Fatalf("refusal logged %d times under %q, want once: %s", len(lines), submitRefusalLogMsg, f.logs.String())
	}
	refusal := lines[0]
	var line map[string]any
	if err := json.Unmarshal([]byte(refusal), &line); err != nil {
		t.Fatalf("decode log line %q: %v", refusal, err)
	}
	if line["level"] != "WARN" {
		t.Errorf("refusal logged at %v, want WARN", line["level"])
	}
	if line["sessionID"] != aliceCtx || line["userID"] != createOwnerBob || line["traceID"] == nil {
		t.Errorf("refusal log lacks ids: %v", line)
	}
	for _, leaked := range []string{ownerTestPrompt, alicePrivateName, aliceAgent, "@example.com"} {
		if strings.Contains(refusal, leaked) {
			t.Errorf("refusal log carries %q: %s", leaked, refusal)
		}
	}
}

func closedSessionRepo(t *testing.T) *store.SessionRepo {
	t.Helper()
	db, err := store.Open(testTemplate.Clone(t), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err = db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	return store.NewSessionRepo(db)
}

func TestHandleMessage_LookupErrorIsServerError(t *testing.T) {
	for _, route := range messageRoutes {
		t.Run(route, func(t *testing.T) {
			f, q := newMessageOwnerFixture(t)
			aliceCtx := idgen.New()
			seedAliceSession(t, f.sessRepo, aliceCtx)
			f.handler.sessRepo = closedSessionRepo(t)

			for _, caller := range []string{createOwnerAlice, createOwnerBob} {
				trackedBefore := f.sse.totalTasks()
				rec := f.post(route, caller, aliceCtx)
				if got := f.sse.totalTasks(); got != trackedBefore {
					t.Errorf("%s: a failed submit left %d tasks registered, want %d", caller, got, trackedBefore)
				}
				if rec.Code != http.StatusInternalServerError {
					t.Fatalf("%s: status = %d, want 500, body=%s", caller, rec.Code, rec.Body.String())
				}
				rpcErr := decodeRPCError(t, rec)
				if rpcErr == nil || rpcErr.Code != codeInternalError || strings.Contains(rpcErr.Message, sessionNotFoundMsg) {
					t.Errorf("%s: error = %+v, want an internal error that does not read as not-found", caller, rpcErr)
				}
				if msg := publishedTask(t, f.brk, q, 200*time.Millisecond); msg != nil {
					t.Errorf("%s: a task was published although the session lookup failed: %s", caller, msg.Payload)
				}
			}
			if len(logLinesFor(f.logs, submitLookupLogMsg, aliceCtx)) == 0 {
				t.Errorf("lookup failure not logged under %q: %s", submitLookupLogMsg, f.logs.String())
			}
		})
	}
}

type failingConnector struct{ err error }

func (c failingConnector) Connect(context.Context) (driver.Conn, error) { return nil, c.err }
func (c failingConnector) Driver() driver.Driver                        { return nil }

func TestHandleMessage_LiveRequestDeadlineExceededLookupIsServerError(t *testing.T) {
	f, q := newMessageOwnerFixture(t)
	aliceCtx := idgen.New()
	seedAliceSession(t, f.sessRepo, aliceCtx)
	timedOut := fmt.Errorf("dial db: %w", context.DeadlineExceeded)
	f.handler.sessRepo = store.NewSessionRepo(&store.DB{DB: &dbutil.DB{DB: sql.OpenDB(failingConnector{err: timedOut})}})

	rec := f.post("/api/v1/message:stream", createOwnerAlice, aliceCtx)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%q", rec.Code, rec.Body.String())
	}
	if rpcErr := decodeRPCError(t, rec); rpcErr == nil || rpcErr.Code != codeInternalError {
		t.Errorf("error = %+v, want an internal error", rpcErr)
	}
	if msg := publishedTask(t, f.brk, q, 200*time.Millisecond); msg != nil {
		t.Errorf("a task was published although the session lookup failed: %s", msg.Payload)
	}
	lines := logLinesFor(f.logs, submitLookupLogMsg, aliceCtx)
	if len(lines) != 1 || !strings.Contains(lines[0], `"level":"ERROR"`) {
		t.Errorf("lookup failure logged as %v, want one ERROR line", lines)
	}
}

func nonCanonicalSpellings(id string) map[string]string {
	return map[string]string{
		"urn":        "urn:uuid:" + id,
		"braced":     "{" + id + "}",
		"upper":      strings.ToUpper(id),
		"unhyphened": strings.ReplaceAll(id, "-", ""),
	}
}

func TestHandleMessage_NonCanonicalContextIDResolvesToTheSameSession(t *testing.T) {
	f, q := newMessageOwnerFixture(t)
	aliceCtx := idgen.New()
	seedAliceSession(t, f.sessRepo, aliceCtx)
	for name, spelling := range nonCanonicalSpellings(aliceCtx) {
		t.Run(name, func(t *testing.T) {
			if rec := f.post("/api/v1/message:stream", createOwnerBob, spelling); rec.Code != http.StatusNotFound {
				t.Fatalf("non-owner status = %d, want 404, body=%s", rec.Code, rec.Body.String())
			}
			if msg := publishedTask(t, f.brk, q, 200*time.Millisecond); msg != nil {
				t.Fatalf("non-owner's task was published: %s", msg.Payload)
			}
			rec := f.post("/api/v1/message:stream", createOwnerAlice, spelling)
			if rec.Code != http.StatusOK {
				t.Fatalf("owner status = %d, want 200, body=%s", rec.Code, rec.Body.String())
			}
			if got := decodeTaskResult(t, rec).ContextID; got != aliceCtx {
				t.Errorf("task contextId = %q, want the canonical %q", got, aliceCtx)
			}
			published := publishedTask(t, f.brk, q, 2*time.Second)
			if published == nil {
				t.Fatal("owner's task was not published")
			}
			if !strings.Contains(string(published.Payload), `"contextId":"`+aliceCtx+`"`) {
				t.Errorf("the agent was addressed under another spelling of the context: %s", published.Payload)
			}
		})
	}
}

func TestHandleMessage_NonCanonicalFreshContextIsCreatedUnderTheCanonicalID(t *testing.T) {
	f, _ := newMessageOwnerFixture(t)
	for name := range nonCanonicalSpellings("") {
		t.Run(name, func(t *testing.T) {
			id := idgen.New()
			spelling := nonCanonicalSpellings(id)[name]
			got := decodeTaskResult(t, f.post("/api/v1/message:stream", createOwnerBob, spelling)).ContextID
			if got != id {
				t.Fatalf("task contextId = %q, want the canonical %q", got, id)
			}
			created, err := f.sessRepo.GetByID(t.Context(), id)
			if err != nil || created == nil || created.UserID != createOwnerBob {
				t.Fatalf("session under the canonical id %q: %+v, %v", id, created, err)
			}
		})
	}
}

func TestHandleMessage_BackfillsTheAgentOfAnUploadMintedSession(t *testing.T) {
	f, _ := newMessageOwnerFixture(t)
	id := idgen.New()
	if err := f.sessRepo.Create(t.Context(), &store.Session{ID: id, UserID: createOwnerAlice}); err != nil {
		t.Fatalf("seed upload-minted session: %v", err)
	}

	rec := f.post("/api/v1/message:stream", createOwnerAlice, "{"+strings.ToUpper(id)+"}")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	got, err := f.sessRepo.GetByID(t.Context(), id)
	if err != nil || got == nil || got.AgentID == nil || *got.AgentID != ownerTestAgent {
		t.Fatalf("session after the first message: %+v, %v; want agent %q", got, err, ownerTestAgent)
	}
	if lines := logLinesFor(f.logs, autoCreateLogMsg, id); len(lines) > 0 {
		t.Errorf("continuing an existing session tried to create it: %v", lines)
	}
}

func TestEnsureSessionAgent_CreateThatMeetsAnExistingRow(t *testing.T) {
	adapter, core, sessRepo, _, _ := setupTestAdapterWithDB(t)
	h := &messageHandler{core: core, sseManager: adapter.sseManager, sessRepo: sessRepo}
	ensure := func(id string) *safeBuffer {
		logs := &safeBuffer{}
		logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})).With("sessionID", id)
		h.ensureSessionAgent(t.Context(), nil, chatSession{contextID: id, userID: createOwnerBob, agentName: "AgentB"}, logger)
		return logs
	}

	t.Run("another user's row with an agent is left alone", func(t *testing.T) {
		id := idgen.New()
		seedAliceSession(t, sessRepo, id)
		logs := ensure(id)
		assertSessionUntouched(t, sessRepo, id)
		lines := logLinesFor(logs, autoCreateLogMsg, id)
		if len(lines) != 1 || !strings.Contains(lines[0], `"level":"WARN"`) {
			t.Errorf("a create that lost to someone else's row logged %v, want one WARN", lines)
		}
	})

	t.Run("another user's row without an agent is left alone", func(t *testing.T) {
		id := idgen.New()
		if err := sessRepo.Create(t.Context(), &store.Session{ID: id, UserID: createOwnerAlice}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		logs := ensure(id)
		got, err := sessRepo.GetByID(t.Context(), id)
		if err != nil || got == nil || got.UserID != createOwnerAlice || got.AgentID != nil {
			t.Fatalf("alice's agentless session after bob's create: %+v, %v; want it still hers and agentless", got, err)
		}
		lines := logLinesFor(logs, autoCreateLogMsg, id)
		if len(lines) != 1 || !strings.Contains(lines[0], `"level":"WARN"`) {
			t.Errorf("a create that lost to someone else's row logged %v, want one WARN", lines)
		}
	})

	t.Run("the caller's own row without an agent gets one", func(t *testing.T) {
		id := idgen.New()
		if err := sessRepo.Create(t.Context(), &store.Session{ID: id, UserID: createOwnerBob}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		logs := ensure(id)
		got, err := sessRepo.GetByID(t.Context(), id)
		if err != nil || got == nil || got.AgentID == nil || *got.AgentID != "AgentB" {
			t.Fatalf("session after ensureSessionAgent: %+v, %v; want agent AgentB", got, err)
		}
		lines := logLinesFor(logs, autoCreateLogMsg, id)
		if len(lines) != 1 || !strings.Contains(lines[0], `"level":"DEBUG"`) {
			t.Errorf("a benign create race logged %v, want one DEBUG and no warning", lines)
		}
	})
}

func TestHandleMessage_StripsClientParentSessionID(t *testing.T) {
	f, q := newMessageOwnerFixture(t)
	aliceCtx := idgen.New()
	seedAliceSession(t, f.sessRepo, aliceCtx)

	rec := f.postCtx(context.Background(), "/api/v1/message:stream", createOwnerBob, idgen.New(), map[string]any{"parentSessionID": aliceCtx})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	published := publishedTask(t, f.brk, q, 2*time.Second)
	if published == nil {
		t.Fatal("bob's task was not published")
	}
	if strings.Contains(string(published.Payload), "parentSessionID") {
		t.Errorf("a client-supplied parentSessionID reached the agent: %s", published.Payload)
	}
}

func TestHandleMessage_DeletedSessionByOwnership(t *testing.T) {
	f, q := newMessageOwnerFixture(t)
	id := idgen.New()
	seedAliceSession(t, f.sessRepo, id)
	softDeleteForTest(t, f.sessRepo, id, createOwnerAlice)

	trackedBefore := f.sse.totalTasks()
	rec := f.post("/api/v1/message:stream", createOwnerBob, id)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bob into alice's deleted session: status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
	if got := f.sse.totalTasks(); got != trackedBefore {
		t.Errorf("a refused submit left %d tasks registered, want %d", got, trackedBefore)
	}
	if msg := publishedTask(t, f.brk, q, 200*time.Millisecond); msg != nil {
		t.Fatalf("a task was published into someone else's deleted session: %s", msg.Payload)
	}

	rec = f.post("/api/v1/message:stream", createOwnerAlice, id)
	if rec.Code != http.StatusOK {
		t.Fatalf("alice into her own deleted session: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if publishedTask(t, f.brk, q, 2*time.Second) == nil {
		t.Error("alice's task was not published")
	}
	if live, err := f.sessRepo.GetByID(t.Context(), id); err != nil || live != nil {
		t.Errorf("the tombstone was brought back: %+v, %v", live, err)
	}
	lines := logLinesFor(f.logs, autoCreateLogMsg+": id already held", id)
	if len(lines) != 1 || !strings.Contains(lines[0], `"level":"WARN"`) {
		t.Errorf("creating over the tombstone logged %v, want one WARN", lines)
	}
}

func TestHandleMessage_ClientGoneDuringLookupIsQuiet(t *testing.T) {
	f, q := newMessageOwnerFixture(t)
	aliceCtx := idgen.New()
	seedAliceSession(t, f.sessRepo, aliceCtx)
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	trackedBefore := f.sse.totalTasks()

	rec := f.postCtx(gone, "/api/v1/message:stream", createOwnerAlice, aliceCtx, nil)

	if rec.Body.Len() != 0 {
		t.Errorf("a client that was gone was answered: %d %s", rec.Code, rec.Body.String())
	}
	if msg := publishedTask(t, f.brk, q, 200*time.Millisecond); msg != nil {
		t.Errorf("a task was published for a client that was gone: %s", msg.Payload)
	}
	if got := f.sse.totalTasks(); got != trackedBefore {
		t.Errorf("tasks registered = %d, want %d", got, trackedBefore)
	}
	if lines := logLinesFor(f.logs, submitLookupLogMsg, aliceCtx); len(lines) > 0 {
		t.Errorf("a client disconnect was logged as a lookup failure: %v", lines)
	}
}

func TestMessageContextOwnership_ThroughTheComposedHandler(t *testing.T) {
	adapter, core, sessRepo, _, _ := setupTestAdapterWithDB(t)
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: ownerTestAgent, URL: "solace:test"})
	brk := core.Services().Broker()
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", ownerTestAgent)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	do := func(userID, method, path string, body any) *httptest.ResponseRecorder {
		raw, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-SAM-User-ID", userID)
		rec := httptest.NewRecorder()
		adapter.Handler().ServeHTTP(rec, req)
		return rec
	}
	send := func(userID, contextID string) *httptest.ResponseRecorder {
		return do(userID, http.MethodPost, "/api/v1/message:stream", map[string]any{
			"jsonrpc": "2.0", "id": "req1",
			"params": map[string]any{"message": map[string]any{
				"kind": "message", "role": "user", "contextId": contextID,
				"parts":    []map[string]any{{"kind": "text", "text": ownerTestPrompt}},
				"metadata": map[string]any{"agent_name": ownerTestAgent},
			}},
		})
	}

	aliceCtx := idgen.New()
	if rec := do(createOwnerAlice, http.MethodPost, "/api/v1/sessions", map[string]any{"id": aliceCtx, "agentId": aliceAgent}); rec.Code != http.StatusCreated {
		t.Fatalf("alice creates her session: %d %s", rec.Code, rec.Body.String())
	}

	if rec := send(createOwnerBob, aliceCtx); rec.Code != http.StatusNotFound {
		t.Fatalf("bob into alice's context: status %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
	if msg := publishedTask(t, brk, q, 200*time.Millisecond); msg != nil {
		t.Fatalf("bob's task was published into alice's context: %s", msg.Payload)
	}
	if rec := send(createOwnerAlice, aliceCtx); rec.Code != http.StatusOK {
		t.Fatalf("alice into her own context: status %d, body=%s", rec.Code, rec.Body.String())
	}
	if publishedTask(t, brk, q, 2*time.Second) == nil {
		t.Fatal("alice's task was not published")
	}
	if rec := send(createOwnerBob, idgen.New()); rec.Code != http.StatusOK {
		t.Fatalf("bob into a fresh context: status %d, body=%s", rec.Code, rec.Body.String())
	}
	if publishedTask(t, brk, q, 2*time.Second) == nil {
		t.Fatal("bob's task for a fresh context was not published")
	}

	got, err := sessRepo.GetByID(t.Context(), aliceCtx)
	if err != nil || got == nil || got.UserID != createOwnerAlice {
		t.Fatalf("alice's session after the refused submit: %+v, %v", got, err)
	}
	if rec := do(createOwnerBob, http.MethodGet, "/api/v1/sessions/"+aliceCtx, nil); rec.Code != http.StatusNotFound {
		t.Errorf("bob reading alice's session: status %d, want 404", rec.Code)
	}
}
