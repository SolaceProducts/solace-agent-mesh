// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

func newEnterpriseAgentHarness(t *testing.T) *Agent {
	t.Helper()
	agent := New(AgentConfig{Name: "caller", Namespace: "ns"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.agentAuth = rbac.NewAgentAuth(nil, slog.Default())
	if err := agent.Init(context.Background(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return agent
}

type mockLLM struct {
	responses   []*llm.ChatResponse
	calls       []llm.ChatRequest
	mu          sync.Mutex
	callIndex   int
	preCallHook func(callIndex int)
}

func newMockLLM(responses ...*llm.ChatResponse) *mockLLM {
	return &mockLLM{responses: responses}
}

func (m *mockLLM) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, *req)
	if m.callIndex >= len(m.responses) {
		return &llm.ChatResponse{Content: "default response"}, nil
	}
	resp := m.responses[m.callIndex]
	m.callIndex++
	return resp, nil
}

func (m *mockLLM) ChatCompletionStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	idx := m.callIndex
	hook := m.preCallHook
	m.calls = append(m.calls, *req)
	var resp *llm.ChatResponse
	if m.callIndex >= len(m.responses) {
		resp = &llm.ChatResponse{Content: "default response"}
	} else {
		resp = m.responses[m.callIndex]
		m.callIndex++
	}
	m.mu.Unlock()

	if hook != nil {
		hook(idx)
	}

	ch := make(chan llm.StreamChunk, 10)
	go func() {
		defer close(ch)
		if resp.Content != "" {
			ch <- llm.StreamChunk{ContentDelta: resp.Content}
		}
		if len(resp.ToolCalls) > 0 {
			var deltas []llm.ToolCallDelta
			for i, tc := range resp.ToolCalls {
				deltas = append(deltas, llm.ToolCallDelta{
					Index:          i,
					ID:             tc.ID,
					Name:           tc.Name,
					ArgumentsDelta: tc.Arguments,
				})
			}
			ch <- llm.StreamChunk{ToolCallDeltas: deltas}
		}
		finishReason := resp.FinishReason
		if finishReason == "" {
			finishReason = "stop"
		}
		ch <- llm.StreamChunk{
			FinishReason: finishReason,
			Usage:        resp.Usage,
		}
	}()
	return ch, nil
}

func (m *mockLLM) getCalls() []llm.ChatRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]llm.ChatRequest, len(m.calls))
	copy(out, m.calls)
	return out
}

type memSessionStore struct {
	mu       sync.Mutex
	sessions map[string]*session.Session
	durable  *session.MemoryStore
}

func newMemSessionStore() *memSessionStore {
	return &memSessionStore{
		sessions: make(map[string]*session.Session),
		durable:  session.NewMemoryStore(),
	}
}

func sessionKey(appName, userID, sessionID string) string {
	return appName + "/" + userID + "/" + sessionID
}

func (s *memSessionStore) Get(_ context.Context, appName, userID, sessionID string) (*session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[sessionKey(appName, userID, sessionID)], nil
}

func (s *memSessionStore) GetByContextAndTask(_ context.Context, appName, userID, contextID, taskID string) (*session.Session, error) {
	if taskID == "" {
		return nil, fmt.Errorf("GetByContextAndTask: taskID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sess := range s.sessions {
		if sess.AppName != appName || sess.UserID != userID || sess.ContextID != contextID {
			continue
		}
		if sess.TaskID == nil || *sess.TaskID != taskID {
			continue
		}
		return sess, nil
	}
	return nil, nil
}

func (s *memSessionStore) Create(_ context.Context, sess *session.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionKey(sess.AppName, sess.UserID, sess.SessionID)
	if _, exists := s.sessions[key]; exists {
		return fmt.Errorf("session %q: %w", key, session.ErrSessionExists)
	}
	if sess.TaskID != nil {
		for _, other := range s.sessions {
			if other.AppName == sess.AppName && other.UserID == sess.UserID &&
				other.ContextID == sess.ContextID && other.TaskID != nil && *other.TaskID == *sess.TaskID {
				return fmt.Errorf("session for task %q: %w", *sess.TaskID, session.ErrSessionExists)
			}
		}
	}
	s.sessions[key] = sess
	return nil
}

func (s *memSessionStore) Save(_ context.Context, sess *session.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess.Version++
	sess.UpdatedAt = time.Now()
	s.sessions[sessionKey(sess.AppName, sess.UserID, sess.SessionID)] = sess
	return nil
}

func (s *memSessionStore) AppendMessages(_ context.Context, appName, userID, sessionID string, msgs []session.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionKey(appName, userID, sessionID)
	existing, ok := s.sessions[key]
	if !ok {
		return fmt.Errorf("append messages: session %q not found", key)
	}
	updated := *existing
	updated.Messages = append(append([]session.Message(nil), existing.Messages...), msgs...)
	s.sessions[key] = &updated
	return nil
}

func (s *memSessionStore) Delete(ctx context.Context, appName, userID, sessionID string) error {
	s.mu.Lock()
	delete(s.sessions, sessionKey(appName, userID, sessionID))
	s.mu.Unlock()
	return s.durable.Delete(ctx, appName, userID, sessionID)
}

func (s *memSessionStore) SaveCheckpoint(ctx context.Context, cp *session.TaskCheckpoint) error {
	return s.durable.SaveCheckpoint(ctx, cp)
}

func (s *memSessionStore) RecordToolResult(ctx context.Context, taskID, corrID string, r *session.ToolCallResult) (int, error) {
	return s.durable.RecordToolResult(ctx, taskID, corrID, r)
}

func (s *memSessionStore) AdoptCheckpoint(ctx context.Context, taskID, ownerID string, now time.Time, leaseFor, silentFor time.Duration, releasedOnly bool) (*session.TaskCheckpoint, error) {
	return s.durable.AdoptCheckpoint(ctx, taskID, ownerID, now, leaseFor, silentFor, releasedOnly)
}

func (s *memSessionStore) UpdateCheckpoint(ctx context.Context, cp *session.TaskCheckpoint) error {
	return s.durable.UpdateCheckpoint(ctx, cp)
}

func (s *memSessionStore) MarkAnswering(ctx context.Context, taskID, ownerID string, final *session.FinalMessage, until time.Time) (bool, error) {
	return s.durable.MarkAnswering(ctx, taskID, ownerID, final, until)
}

func (s *memSessionStore) RenewLeases(ctx context.Context, ownerID string, taskIDs []string, until time.Time) (int, error) {
	return s.durable.RenewLeases(ctx, ownerID, taskIDs, until)
}

func (s *memSessionStore) CheckpointOwner(ctx context.Context, taskID string) (string, bool, error) {
	return s.durable.CheckpointOwner(ctx, taskID)
}

func (s *memSessionStore) GetCheckpoint(ctx context.Context, taskID string) (*session.TaskCheckpoint, error) {
	return s.durable.GetCheckpoint(ctx, taskID)
}

func (s *memSessionStore) PopCheckpoint(ctx context.Context, taskID string) (*session.TaskCheckpoint, error) {
	return s.durable.PopCheckpoint(ctx, taskID)
}

func (s *memSessionStore) ReclaimUnowned(ctx context.Context, taskID, ownerID string, until time.Time) (bool, error) {
	return s.durable.ReclaimUnowned(ctx, taskID, ownerID, until)
}

func (s *memSessionStore) ReleaseOwnership(ctx context.Context, taskID, ownerID string) error {
	return s.durable.ReleaseOwnership(ctx, taskID, ownerID)
}

func (s *memSessionStore) RegisterRunningTask(ctx context.Context, task session.RunningTask, ownerID string, until time.Time) error {
	return s.durable.RegisterRunningTask(ctx, task, ownerID, until)
}

func (s *memSessionStore) RenewRunningTasks(ctx context.Context, ownerID string, tasks []session.RunningTask, until time.Time) (int, error) {
	return s.durable.RenewRunningTasks(ctx, ownerID, tasks, until)
}

func (s *memSessionStore) CheckpointedTasks(ctx context.Context, sessionID string, taskIDs []string) ([]string, error) {
	return s.durable.CheckpointedTasks(ctx, sessionID, taskIDs)
}

func (s *memSessionStore) DeleteLapsedRunningTasks(ctx context.Context, appName string, lapsedBefore time.Time) (int, error) {
	return s.durable.DeleteLapsedRunningTasks(ctx, appName, lapsedBefore)
}

func (s *memSessionStore) RemoveRunningTask(ctx context.Context, taskID, ownerID string) error {
	return s.durable.RemoveRunningTask(ctx, taskID, ownerID)
}

func (s *memSessionStore) ListRunningTasks(ctx context.Context, appName, userID, sessionID string, liveAfter time.Time) ([]string, error) {
	return s.durable.ListRunningTasks(ctx, appName, userID, sessionID, liveAfter)
}

func (s *memSessionStore) DeleteCheckpoint(ctx context.Context, taskID string) error {
	return s.durable.DeleteCheckpoint(ctx, taskID)
}

func (s *memSessionStore) DeleteCheckpointIfOwned(ctx context.Context, taskID, ownerID string) (bool, error) {
	return s.durable.DeleteCheckpointIfOwned(ctx, taskID, ownerID)
}

func (s *memSessionStore) DeleteAbandonedCheckpoint(ctx context.Context, taskID string, lapsedBefore time.Time) (bool, error) {
	return s.durable.DeleteAbandonedCheckpoint(ctx, taskID, lapsedBefore)
}

func (s *memSessionStore) PurgeAbandonedCheckpoints(ctx context.Context, agentName string, lapsedBefore time.Time, limit int) (int, error) {
	return s.durable.PurgeAbandonedCheckpoints(ctx, agentName, lapsedBefore, limit)
}

func (s *memSessionStore) RegisterPendingCalls(ctx context.Context, taskID, ownerID string, calls []session.PendingCall) error {
	return s.durable.RegisterPendingCalls(ctx, taskID, ownerID, calls)
}

func (s *memSessionStore) LookupPendingCall(ctx context.Context, agentName, corrID string) (*session.PendingCall, error) {
	return s.durable.LookupPendingCall(ctx, agentName, corrID)
}

func (s *memSessionStore) PendingCallTask(ctx context.Context, corrID string) (string, bool, error) {
	return s.durable.PendingCallTask(ctx, corrID)
}

func (s *memSessionStore) DeletePendingCall(ctx context.Context, corrID string) error {
	return s.durable.DeletePendingCall(ctx, corrID)
}

func (s *memSessionStore) ListExpiredPendingCalls(ctx context.Context, agentName string, now time.Time, limit int) ([]session.PendingCall, error) {
	return s.durable.ListExpiredPendingCalls(ctx, agentName, now, limit)
}

func (s *memSessionStore) ListResumableCheckpoints(ctx context.Context, agentName string, now time.Time, silentFor time.Duration, limit int, releasedOnly bool) ([]session.TaskCheckpoint, error) {
	return s.durable.ListResumableCheckpoints(ctx, agentName, now, silentFor, limit, releasedOnly)
}

func (s *memSessionStore) ListSessions(_ context.Context, appName, userID string) ([]*session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := appName + "/" + userID + "/"
	var result []*session.Session
	for k, v := range s.sessions {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			result = append(result, v)
		}
	}
	return result, nil
}

type mockServices struct {
	budgetGate             *budget.Gate
	brk                    broker.Broker
	llmMap                 map[string]llm.Client
	arts                   *artifact.ScopedService
	sessions               session.Store
	sessionsDieWithProcess bool
	volumes                volume.Service
	featureRegistry        *features.Registry
	modelResolver          runtime.ModelAliasResolver
	bindings               runtime.ModelBindings
	agentAuth              auth.AgentAuthorizer
	authn                  auth.Authenticator
	auditLog               auth.AuditLogger
	metrics                runtime.Metrics
	credSvc                auth.CredentialService
}

func newMockServices(llmClient llm.Client, store session.Store) *mockServices {
	return &mockServices{
		llmMap:   map[string]llm.Client{"": llmClient},
		sessions: store,
	}
}

func (s *mockServices) Broker() broker.Broker    { return s.brk }
func (s *mockServices) Config() config.Config    { return nil }
func (s *mockServices) KVStore() runtime.KVStore { return nil }
func (s *mockServices) Cache() runtime.Cache     { return nil }
func (s *mockServices) Metrics() runtime.Metrics {
	if s.metrics != nil {
		return s.metrics
	}
	return runtime.NoopMetrics{}
}
func (s *mockServices) Tracer() runtime.Tracer             { return runtime.NoopTracer{} }
func (s *mockServices) Artifacts() *artifact.ScopedService { return s.arts }
func (s *mockServices) Sessions() session.Store {
	if mem, ok := s.sessions.(*session.MemoryStore); ok && !s.sessionsDieWithProcess {
		return sharedDatabase{mem}
	}
	return s.sessions
}

type sharedDatabase struct{ *session.MemoryStore }

func (sharedDatabase) OutlivesProcess() bool { return true }

func (s *mockServices) LLM(model string) llm.Client {
	if c, ok := s.llmMap[model]; ok {
		return c
	}
	return s.llmMap[""]
}

func (s *mockServices) Authenticator() auth.Authenticator {
	if s.authn != nil {
		return s.authn
	}
	return auth.NoopAuthenticator{}
}
func (s *mockServices) ToolAuthorizer() auth.ToolAuthorizer { return auth.NoopToolAuthorizer{} }
func (s *mockServices) AgentAuthorizer() auth.AgentAuthorizer {
	if s.agentAuth != nil {
		return s.agentAuth
	}
	return auth.NoopAgentAuthorizer{}
}
func (s *mockServices) AuditLogger() auth.AuditLogger {
	if s.auditLog != nil {
		return s.auditLog
	}
	return auth.NoopAuditLogger{}
}
func (s *mockServices) ControlAuthorizer() auth.ControlAuthorizer {
	return auth.NoopControlAuthorizer{}
}
func (s *mockServices) CredentialService() auth.CredentialService {
	if s.credSvc != nil {
		return s.credSvc
	}
	return auth.NoopCredentialService{}
}
func (s *mockServices) Volumes() volume.Service             { return s.volumes }
func (s *mockServices) BudgetGate() *budget.Gate            { return s.budgetGate }
func (s *mockServices) HTTPClient() *http.Client            { return http.DefaultClient }
func (s *mockServices) FeatureRegistry() *features.Registry { return s.featureRegistry }

func (s *mockServices) UsageReporter() runtime.Component { return nil }
func (s *mockServices) ModelResolver() runtime.ModelAliasResolver {
	return s.modelResolver
}
func (s *mockServices) Bindings() runtime.ModelBindings                { return s.bindings }
func (s *mockServices) ConnectorSpecStore() runtime.ConnectorSpecStore { return nil }
func (s *mockServices) Logger() *slog.Logger                           { return slog.Default() }

func newTestAgent(mockResp ...*llm.ChatResponse) (*Agent, *mockLLM, *memSessionStore) {
	mock := newMockLLM(mockResp...)
	store := newMemSessionStore()

	agent := New(AgentConfig{
		Name:         "test-agent",
		SystemPrompt: "You are a helpful assistant.",
		Model:        "test-model",
		MaxTokens:    1000,
		MaxLLMCalls:  5,
		Namespace:    "test-ns",
	})

	svc := newMockServices(mock, store)
	agent.svc = svc
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test helper

	return agent, mock, store
}

func newUserMessage(text string) a2a.Message {
	return a2a.Message{
		Kind:      "message",
		MessageID: "msg-user-1",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart(text)},
		ContextID: "ctx-1",
	}
}

func TestHandleTaskBasicTextResponse(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{
		Content:      "Hello! How can I help you?",
		FinishReason: "stop",
	})

	resp, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("Hi there"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.ID != "task-001" {
		t.Errorf("response ID = %q", resp.ID)
	}
}

func TestHandleTaskLLMReceivesSystemPrompt(t *testing.T) {
	agent, mock, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
	agent.cfg.InjectCurrentTime = new(bool)

	_, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("Hello"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(calls))
	}

	msgs := calls[0].Messages
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "system" || !strings.Contains(msgs[0].Content, "You are a helpful assistant.") {
		t.Errorf("system message = %+v", msgs[0])
	}
	if msgs[1].Role != "user" || msgs[1].Content != "Hello" {
		t.Errorf("user message = %+v", msgs[1])
	}
}

func TestHandleTaskSavesHistory(t *testing.T) {
	agent, _, store := newTestAgent(&llm.ChatResponse{Content: "response 1"})
	agent.cfg.InjectCurrentTime = new(bool)

	_, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("Hello"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}

	sess, err := store.Get(context.Background(), "test-agent", "user-1", "ctx-1")
	if err != nil {
		t.Fatal(err)
	}
	if sess == nil {
		t.Fatal("session not found")
	} else if len(sess.Messages) != 2 {
		t.Fatalf("expected 2 messages in history, got %d", len(sess.Messages))
	}
	if sess.Messages[0].Role != "user" || sess.Messages[0].Content != "Hello" {
		t.Errorf("user message = %+v", sess.Messages[0])
	}
	if sess.Messages[1].Role != "assistant" || sess.Messages[1].Content != "response 1" {
		t.Errorf("assistant message = %+v", sess.Messages[1])
	}
}

func TestHandleTaskMultiTurn(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{Content: "Hello!"},
		&llm.ChatResponse{Content: "I'm doing well!"},
	)
	agent.cfg.InjectCurrentTime = new(bool)

	_, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("Hi"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = agent.HandleTask(
		context.Background(),
		"task-002",
		newUserMessage("How are you?"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}

	msgs := calls[1].Messages
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages in second call, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("msg[0] role = %q", msgs[0].Role)
	}
	if msgs[1].Role != "user" || msgs[1].Content != "Hi" {
		t.Errorf("msg[1] = %+v", msgs[1])
	}
	if msgs[2].Role != "assistant" || msgs[2].Content != "Hello!" {
		t.Errorf("msg[2] = %+v", msgs[2])
	}
	if msgs[3].Role != "user" || msgs[3].Content != "How are you?" {
		t.Errorf("msg[3] = %+v", msgs[3])
	}
}

func TestHandleTaskResponseFormat(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "The answer is 42."})

	resp, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("What is the answer?"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}

	if resp.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q", resp.JSONRPC)
	}
	if resp.ID != "task-001" {
		t.Errorf("id = %q", resp.ID)
	}
	if resp.Result == nil {
		t.Fatal("result is nil")
	}
}

func TestHandleTaskContextID(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})

	msg := newUserMessage("test")
	msg.ContextID = "custom-ctx"

	resp, err := agent.HandleTask(
		context.Background(),
		"task-001",
		msg,
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestExtractText(t *testing.T) {
	msg := a2a.Message{
		Parts: []a2a.Part{
			a2a.NewTextPart("Hello"),
			a2a.NewDataPart(map[string]any{"type": "signal"}),
			a2a.NewTextPart("World"),
		},
	}

	text := extractText(msg)
	if text != "Hello\nWorld" {
		t.Errorf("extractText = %q", text)
	}
}

func TestExtractTextEmpty(t *testing.T) {
	msg := a2a.Message{
		Parts: []a2a.Part{
			a2a.NewDataPart(map[string]any{"type": "signal"}),
		},
	}

	text := extractText(msg)
	if text != "" {
		t.Errorf("extractText = %q, want empty", text)
	}
}

type testBuiltinTool struct {
	name         string
	description  string
	params       *tool.JSONSchema
	instructions string
	execFn       func(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error)
}

func (t *testBuiltinTool) Name() string                                      { return t.name }
func (t *testBuiltinTool) Description() string                               { return t.description }
func (t *testBuiltinTool) Parameters() *tool.JSONSchema                      { return t.params }
func (t *testBuiltinTool) Instructions() string                              { return t.instructions }
func (t *testBuiltinTool) IsLongRunning() bool                               { return false }
func (t *testBuiltinTool) RequiredScopes() []string                          { return nil }
func (t *testBuiltinTool) RawStringArgs() []string                           { return nil }
func (t *testBuiltinTool) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }
func (t *testBuiltinTool) ContextParam() string                              { return "" }

func (t *testBuiltinTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	if t.execFn != nil {
		return t.execFn(ctx, args, toolCtx)
	}
	return tool.OK("mock result"), nil
}

func TestHandleTaskSingleToolCall(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Name: "add", Arguments: `{"a": 2, "b": 3}`},
			},
		},
		&llm.ChatResponse{
			Content:      "The sum is 5.",
			FinishReason: "stop",
		},
	)

	addTool := &testBuiltinTool{
		name:        "add",
		description: "Adds two numbers",
		params: &tool.JSONSchema{
			Type: "object",
			Properties: map[string]any{
				"a": map[string]any{"type": "number"},
				"b": map[string]any{"type": "number"},
			},
		},
		execFn: func(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
			a := args["a"].(float64)
			b := args["b"].(float64)
			return tool.OK(fmt.Sprintf("%.0f", a+b)), nil
		},
	}
	if err := agent.Tools().Register(addTool); err != nil {
		t.Fatal(err)
	}

	resp, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("What is 2 + 3?"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}

	if len(calls[0].Tools) != 4 {
		t.Errorf("expected 4 tool definitions (add + _notify_artifact_save + _continue_generation + sub_task), got %d", len(calls[0].Tools))
	}
	hasAddTool := false
	for _, td := range calls[0].Tools {
		if td.Name == "add" {
			hasAddTool = true
			break
		}
	}
	if !hasAddTool {
		t.Errorf("expected 'add' tool in definitions")
	}

	msgs := calls[1].Messages
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages in second call, got %d", len(msgs))
	}
	if msgs[2].Role != "assistant" {
		t.Errorf("msg[2] role = %q, want assistant", msgs[2].Role)
	}
	if len(msgs[2].ToolCalls) != 1 {
		t.Errorf("msg[2] tool calls = %d, want 1", len(msgs[2].ToolCalls))
	}
	if msgs[3].Role != "tool" {
		t.Errorf("msg[3] role = %q, want tool", msgs[3].Role)
	}
	if msgs[3].ToolCallID != "call-1" {
		t.Errorf("msg[3] tool call ID = %q", msgs[3].ToolCallID)
	}
	if !strings.Contains(msgs[3].Content, `"status":"success"`) || !strings.Contains(msgs[3].Content, `"message":"5"`) {
		t.Errorf("msg[3] content = %q, want JSON with status=success and message=5", msgs[3].Content)
	}
}

func TestHandleTask_RollingCacheBreakpointReachesWireAcrossTurns(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls:    []llm.ToolCall{{ID: "call-1", Name: "add", Arguments: `{"a":2,"b":3}`}},
		},
		&llm.ChatResponse{Content: "The sum is 5.", FinishReason: "stop"},
	)
	agent.cfg.CacheStrategy = "5m"

	addTool := &testBuiltinTool{
		name:        "add",
		description: "Adds two numbers",
		params: &tool.JSONSchema{Type: "object", Properties: map[string]any{
			"a": map[string]any{"type": "number"},
			"b": map[string]any{"type": "number"},
		}},
		execFn: func(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
			return tool.OK(fmt.Sprintf("%.0f", args["a"].(float64)+args["b"].(float64))), nil
		},
	}
	if err := agent.Tools().Register(addTool); err != nil {
		t.Fatal(err)
	}

	resp, err := agent.HandleTask(
		context.Background(), "task-rolling-cache",
		newUserMessage("What is 2 + 3?"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}

	msgs := calls[1].Messages
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages on the second call, got %d", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[0].CacheControl == nil {
		t.Error("system message should keep its static cache breakpoint")
	}
	if msgs[3].Role != "tool" || msgs[3].CacheControl == nil {
		t.Error("tool-result message should carry a rolling cache breakpoint on the second turn")
	}
	if msgs[2].CacheControl != nil {
		t.Error("content-less assistant tool-call message should not be marked")
	}
}

func TestHandleTask_CacheBreakpointsStayUnder4AcrossManyTurns(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "noop", Arguments: `{}`}}},
		&llm.ChatResponse{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "c2", Name: "noop", Arguments: `{}`}}},
		&llm.ChatResponse{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "c3", Name: "noop", Arguments: `{}`}}},
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	)
	agent.cfg.CacheStrategy = "5m"
	agent.cfg.MaxLLMCalls = 10

	noop := &testBuiltinTool{
		name:        "noop",
		description: "no-op tool",
		params:      &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
		execFn: func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
			return tool.OK("ok"), nil
		},
	}
	if err := agent.Tools().Register(noop); err != nil {
		t.Fatal(err)
	}

	resp, err := agent.HandleTask(
		context.Background(), "task-accum",
		newUserMessage("run the noop tool a few times"),
		a2a.UserProperties{ClientID: "u1", UserID: "u1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) < 4 {
		t.Fatalf("expected >=4 LLM calls (3 tool turns + final), got %d", len(calls))
	}
	for i, c := range calls {
		n := 0
		for _, m := range c.Messages {
			if m.CacheControl != nil {
				n++
			}
		}
		for _, td := range c.Tools {
			if td.CacheControl != nil {
				n++
			}
		}
		if n > 4 {
			t.Errorf("call %d: %d cache_control breakpoints, exceeds Anthropic's limit of 4", i, n)
		}
	}
}

func TestHandleTaskToolDefinitionsPassedToLLM(t *testing.T) {
	agent, mock, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})

	for _, name := range []string{"tool_a", "tool_b"} {
		err := agent.Tools().Register(&testBuiltinTool{
			name:        name,
			description: "Tool " + name,
			params:      &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	_, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("hello"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}

	calls := mock.getCalls()
	if len(calls[0].Tools) != 5 {
		t.Errorf("expected 5 tool definitions (2 user + _notify_artifact_save + _continue_generation + sub_task), got %d", len(calls[0].Tools))
	}
}

func TestHandleTaskNoToolsNilDefinitions(t *testing.T) {
	agent, mock, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})

	_, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("hello"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}

	calls := mock.getCalls()
	if len(calls[0].Tools) != 3 {
		t.Errorf("expected 3 tools (_notify_artifact_save + _continue_generation + sub_task), got %d", len(calls[0].Tools))
	}
	toolNames := make(map[string]bool)
	for _, td := range calls[0].Tools {
		toolNames[td.Name] = true
	}
	for _, expected := range []string{"_notify_artifact_save", "_continue_generation", "sub_task"} {
		if !toolNames[expected] {
			t.Errorf("expected tool %q not found in definitions", expected)
		}
	}
}

func TestHandleTaskMaxLLMCallsLimit(t *testing.T) {
	toolCallResp := &llm.ChatResponse{
		Content:      "",
		FinishReason: "tool_calls",
		ToolCalls: []llm.ToolCall{
			{ID: "call-x", Name: "noop", Arguments: "{}"},
		},
	}

	agent, mock, _ := newTestAgent(
		toolCallResp, toolCallResp, toolCallResp, toolCallResp, toolCallResp,
		toolCallResp, toolCallResp, toolCallResp, toolCallResp, toolCallResp,
	)

	agent.cfg.MaxLLMCalls = 3

	err := agent.Tools().Register(&testBuiltinTool{
		name:        "noop",
		description: "Does nothing",
		params:      &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("test"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 3 {
		t.Errorf("expected 3 LLM calls (max), got %d", len(calls))
	}
}

func TestHandleTaskToolErrorFormatted(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Name: "fail_tool", Arguments: "{}"},
			},
		},
		&llm.ChatResponse{Content: "The tool failed."},
	)

	err := agent.Tools().Register(&testBuiltinTool{
		name:        "fail_tool",
		description: "Always fails",
		params:      &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
		execFn: func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
			return tool.Error("something went wrong"), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, handleErr := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("do it"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if handleErr != nil {
		t.Fatal(handleErr)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	msgs := calls[1].Messages
	toolMsg := msgs[3]
	if toolMsg.Role != "tool" {
		t.Errorf("tool msg role = %q", toolMsg.Role)
	}
	if !strings.Contains(toolMsg.Content, "something went wrong") || !strings.Contains(toolMsg.Content, `"status":"error"`) {
		t.Errorf("tool msg content = %q, want JSON with status=error and message about something went wrong", toolMsg.Content)
	}
}

func TestHandleTaskToolWithDataResult(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Name: "lookup", Arguments: `{"key": "foo"}`},
			},
		},
		&llm.ChatResponse{Content: "The value is bar."},
	)

	err := agent.Tools().Register(&testBuiltinTool{
		name:        "lookup",
		description: "Looks up a value",
		params:      &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
		execFn: func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
			return &tool.Result{
				Status:  tool.StatusSuccess,
				Message: "found",
				Data:    map[string]any{"value": "bar"},
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, handleErr := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("lookup foo"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if handleErr != nil {
		t.Fatal(handleErr)
	}

	calls := mock.getCalls()
	toolMsg := calls[1].Messages[3]
	if toolMsg.Role != "tool" {
		t.Errorf("tool msg role = %q", toolMsg.Role)
	}
	if !strings.Contains(toolMsg.Content, `"value":"bar"`) && !strings.Contains(toolMsg.Content, `"value": "bar"`) {
		t.Errorf("tool msg content = %q, want JSON data", toolMsg.Content)
	}
}

func TestHandleTaskMultiTurnToolCalling(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls:    []llm.ToolCall{{ID: "c1", Name: "tool_a", Arguments: `{"x": 1}`}},
		},
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls:    []llm.ToolCall{{ID: "c2", Name: "tool_b", Arguments: `{"y": 10}`}},
		},
		&llm.ChatResponse{Content: "Final answer: 11", FinishReason: "stop"},
	)

	for _, name := range []string{"tool_a", "tool_b"} {
		n := name
		err := agent.Tools().Register(&testBuiltinTool{
			name:   n,
			params: &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
			execFn: func(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
				return tool.OK(fmt.Sprintf("%s result", n)), nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	resp, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("compute"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 3 {
		t.Fatalf("expected 3 LLM calls, got %d", len(calls))
	}

	msgs := calls[2].Messages
	if len(msgs) != 6 {
		t.Fatalf("expected 6 messages in call 3, got %d", len(msgs))
	}
	if msgs[3].Role != "tool" || msgs[3].ToolCallID != "c1" {
		t.Errorf("msg[3] = %+v", msgs[3])
	}
	if msgs[5].Role != "tool" || msgs[5].ToolCallID != "c2" {
		t.Errorf("msg[5] = %+v", msgs[5])
	}
}

func TestHandleTaskParallelToolCalls(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "echo", Arguments: `{"msg": "a"}`},
				{ID: "c2", Name: "echo", Arguments: `{"msg": "b"}`},
				{ID: "c3", Name: "echo", Arguments: `{"msg": "c"}`},
			},
		},
		&llm.ChatResponse{Content: "Got all three.", FinishReason: "stop"},
	)

	var callOrder sync.Mutex
	var callArgs []string
	err := agent.Tools().Register(&testBuiltinTool{
		name:   "echo",
		params: &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
		execFn: func(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
			callOrder.Lock()
			callArgs = append(callArgs, args["msg"].(string))
			callOrder.Unlock()
			return tool.OK(args["msg"].(string)), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, handleErr := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("parallel"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if handleErr != nil {
		t.Fatal(handleErr)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	callOrder.Lock()
	if len(callArgs) != 3 {
		t.Errorf("expected 3 tool calls, got %d", len(callArgs))
	}
	callOrder.Unlock()

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}
	msgs := calls[1].Messages
	if len(msgs) != 6 {
		t.Fatalf("expected 6 messages, got %d", len(msgs))
	}
	for i := 3; i <= 5; i++ {
		if msgs[i].Role != "tool" {
			t.Errorf("msgs[%d].Role = %q, want tool", i, msgs[i].Role)
		}
	}
}

func TestHandleTaskLastCallWarningLeavesCachedSystemMessageUnchanged(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls:    []llm.ToolCall{{ID: "c1", Name: "noop", Arguments: "{}"}},
		},
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	)
	agent.cfg.MaxLLMCalls = 2

	err := agent.Tools().Register(&testBuiltinTool{
		name:   "noop",
		params: &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, handleErr := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("test"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if handleErr != nil {
		t.Fatal(handleErr)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}

	if present, idx := requestContainsWarning(calls[0]); present {
		t.Errorf("first call should not have last-call warning (found at message %d)", idx)
	}

	present, idx := requestContainsWarning(calls[1])
	if !present {
		t.Fatal("second call should have last-call warning")
	}
	if want := len(calls[1].Messages) - 1; idx != want {
		t.Errorf("warning at message %d, want the trailing message %d", idx, want)
	}
	if role := calls[1].Messages[idx].Role; role != "user" {
		t.Errorf("warning message Role = %q, want user", role)
	}

	firstSystem, lastSystem := calls[0].Messages[0], calls[1].Messages[0]
	if firstSystem.Role != "system" || lastSystem.Role != "system" {
		t.Fatalf("expected system message first, got %q and %q", firstSystem.Role, lastSystem.Role)
	}
	if firstSystem.Content != lastSystem.Content {
		t.Errorf("system prompt changed on the last call (%d -> %d bytes)",
			len(firstSystem.Content), len(lastSystem.Content))
	}
}

func TestHandleTaskLastCallWarningNotOnFirstCall(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{Content: "ok", FinishReason: "stop"},
	)
	agent.cfg.MaxLLMCalls = 1

	_, handleErr := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("test"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if handleErr != nil {
		t.Fatal(handleErr)
	}

	calls := mock.getCalls()
	if present, idx := requestContainsWarning(calls[0]); present {
		t.Errorf("should not inject warning on the very first call (found at message %d)", idx)
	}
}

func TestHandleTaskSavesToolCallHistory(t *testing.T) {
	agent, _, store := newTestAgent(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls:    []llm.ToolCall{{ID: "c1", Name: "add", Arguments: `{"a": 1, "b": 2}`}},
		},
		&llm.ChatResponse{Content: "The answer is 3.", FinishReason: "stop"},
	)
	agent.cfg.InjectCurrentTime = new(bool)

	err := agent.Tools().Register(&testBuiltinTool{
		name:   "add",
		params: &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
		execFn: func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
			return tool.OK("3"), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, handleErr := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("What is 1+2?"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if handleErr != nil {
		t.Fatal(handleErr)
	}

	sess, err := store.Get(context.Background(), "test-agent", "user-1", "ctx-1")
	if err != nil {
		t.Fatal(err)
	}
	if sess == nil {
		t.Fatal("session not found")
	} else if len(sess.Messages) != 4 {
		t.Fatalf("expected 4 session messages, got %d", len(sess.Messages))
	}

	if sess.Messages[0].Role != "user" || sess.Messages[0].Content != "What is 1+2?" {
		t.Errorf("msg[0] = %+v", sess.Messages[0])
	}

	if sess.Messages[1].Role != "assistant" {
		t.Errorf("msg[1] role = %q", sess.Messages[1].Role)
	}
	if len(sess.Messages[1].ToolCalls) != 1 {
		t.Fatalf("msg[1] tool calls = %d, want 1", len(sess.Messages[1].ToolCalls))
	}
	if sess.Messages[1].ToolCalls[0].ID != "c1" || sess.Messages[1].ToolCalls[0].Name != "add" {
		t.Errorf("msg[1] tool call = %+v", sess.Messages[1].ToolCalls[0])
	}

	if sess.Messages[2].Role != "tool" {
		t.Errorf("msg[2] role = %q", sess.Messages[2].Role)
	}
	if sess.Messages[2].ToolCallID != "c1" {
		t.Errorf("msg[2] ToolCallID = %q", sess.Messages[2].ToolCallID)
	}

	if sess.Messages[3].Role != "assistant" || sess.Messages[3].Content != "The answer is 3." {
		t.Errorf("msg[3] = %+v", sess.Messages[3])
	}
}

func TestHandleTaskToolHistoryRestoredInMultiTurn(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls:    []llm.ToolCall{{ID: "c1", Name: "search", Arguments: `{"q": "test"}`}},
		},
		&llm.ChatResponse{Content: "Found: test result.", FinishReason: "stop"},
		&llm.ChatResponse{Content: "You asked about test result.", FinishReason: "stop"},
	)
	agent.cfg.InjectCurrentTime = new(bool)

	err := agent.Tools().Register(&testBuiltinTool{
		name:   "search",
		params: &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
		execFn: func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
			return tool.OK("test result"), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, handleErr := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("Search for test"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if handleErr != nil {
		t.Fatal(handleErr)
	}

	_, handleErr = agent.HandleTask(
		context.Background(),
		"task-002",
		newUserMessage("What did you find?"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if handleErr != nil {
		t.Fatal(handleErr)
	}

	calls := mock.getCalls()
	if len(calls) != 3 {
		t.Fatalf("expected 3 LLM calls total, got %d", len(calls))
	}

	msgs := calls[2].Messages
	if len(msgs) != 6 {
		t.Fatalf("expected 6 messages in turn 2, got %d", len(msgs))
	}

	if msgs[0].Role != "system" {
		t.Errorf("msg[0] role = %q", msgs[0].Role)
	}
	if msgs[1].Role != "user" || msgs[1].Content != "Search for test" {
		t.Errorf("msg[1] = %+v", msgs[1])
	}
	if msgs[2].Role != "assistant" || len(msgs[2].ToolCalls) != 1 {
		t.Errorf("msg[2] = role=%q toolCalls=%d", msgs[2].Role, len(msgs[2].ToolCalls))
	}
	if msgs[2].ToolCalls[0].ID != "c1" || msgs[2].ToolCalls[0].Name != "search" {
		t.Errorf("msg[2] tool call = %+v", msgs[2].ToolCalls[0])
	}
	if msgs[3].Role != "tool" || msgs[3].ToolCallID != "c1" {
		t.Errorf("msg[3] = role=%q callID=%q", msgs[3].Role, msgs[3].ToolCallID)
	}
	if msgs[4].Role != "assistant" || msgs[4].Content != "Found: test result." {
		t.Errorf("msg[4] = %+v", msgs[4])
	}
	if msgs[5].Role != "user" || msgs[5].Content != "What did you find?" {
		t.Errorf("msg[5] = %+v", msgs[5])
	}
}

func TestHandleTaskMultiToolSavesAllResults(t *testing.T) {
	agent, _, store := newTestAgent(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "echo", Arguments: `{"msg": "a"}`},
				{ID: "c2", Name: "echo", Arguments: `{"msg": "b"}`},
			},
		},
		&llm.ChatResponse{Content: "Got both.", FinishReason: "stop"},
	)

	err := agent.Tools().Register(&testBuiltinTool{
		name:   "echo",
		params: &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
		execFn: func(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
			return tool.OK(args["msg"].(string)), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, handleErr := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("echo both"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if handleErr != nil {
		t.Fatal(handleErr)
	}

	sess, err := store.Get(context.Background(), "test-agent", "user-1", "ctx-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Messages) != 5 {
		t.Fatalf("expected 5 session messages, got %d", len(sess.Messages))
	}
	if len(sess.Messages[1].ToolCalls) != 2 {
		t.Errorf("expected 2 tool calls in assistant msg, got %d", len(sess.Messages[1].ToolCalls))
	}
	if sess.Messages[2].ToolCallID != "c1" || sess.Messages[3].ToolCallID != "c2" {
		t.Errorf("tool call IDs: %q, %q", sess.Messages[2].ToolCallID, sess.Messages[3].ToolCallID)
	}
}

func TestLLMMessagesToSessionMessages(t *testing.T) {
	msgs := []llm.ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "tool_a", Arguments: `{"x": 1}`},
		}},
		{Role: "tool", Content: "result", ToolCallID: "c1"},
		{Role: "assistant", Content: "done"},
	}

	out := llmMessagesToSessionMessages(msgs, "task-1")

	if len(out) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(out))
	}
	for i, m := range out {
		if m.TaskID != "task-1" {
			t.Errorf("msg[%d].TaskID = %q, want the writing task", i, m.TaskID)
		}
	}

	if out[0].Role != "user" || out[0].Content != "hello" {
		t.Errorf("msg[0] = %+v", out[0])
	}

	if out[1].Role != "assistant" || len(out[1].ToolCalls) != 1 {
		t.Errorf("msg[1] = %+v", out[1])
	}
	if out[1].ToolCalls[0].ID != "c1" || out[1].ToolCalls[0].Name != "tool_a" {
		t.Errorf("msg[1] tool call = %+v", out[1].ToolCalls[0])
	}

	if out[2].Role != "tool" || out[2].ToolCallID != "c1" || out[2].Content != "result" {
		t.Errorf("msg[2] = role=%q callID=%q content=%q", out[2].Role, out[2].ToolCallID, out[2].Content)
	}

	if out[3].Role != "assistant" || out[3].Content != "done" {
		t.Errorf("msg[3] = %+v", out[3])
	}
}

func TestFormatToolResult(t *testing.T) {
	agent := New(AgentConfig{Name: "test-agent"})
	toolCtx := &tool.Context{AgentName: "test-agent"}

	tests := []struct {
		name   string
		result *tool.Result
		want   string
	}{
		{"nil", nil, "no result"},
		{"success message", tool.OK("hello"), `{"message":"hello","status":"success"}`},
		{"error", tool.Error("bad"), `{"message":"bad","status":"error"}`},
		{"with data", &tool.Result{Status: tool.StatusSuccess, Message: "ok", Data: map[string]any{"x": 1}}, `{"message":"ok","status":"success","x":1}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := agent.formatToolResult(context.Background(), tc.result, toolCtx, nil)
			if got != tc.want {
				t.Errorf("formatToolResult = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAutoContinuationConcatenatesContent(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			Content:      "Hello, here is part one",
			FinishReason: "max_tokens",
		},
		&llm.ChatResponse{
			Content:      " and here is part two.",
			FinishReason: "stop",
		},
	)

	resp, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("say hello"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}

	msgs := calls[1].Messages
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages in second call, got %d", len(msgs))
	}
	if msgs[2].Role != "assistant" {
		t.Errorf("msg[2] role = %q, want assistant", msgs[2].Role)
	}
	if msgs[2].Content != "Hello, here is part one" {
		t.Errorf("msg[2] content = %q", msgs[2].Content)
	}
	if len(msgs[2].ToolCalls) != 1 || msgs[2].ToolCalls[0].Name != "_continue_generation" {
		t.Errorf("msg[2] should have _continue_generation tool call, got %+v", msgs[2].ToolCalls)
	}
	if msgs[3].Role != "tool" {
		t.Errorf("msg[3] role = %q, want tool", msgs[3].Role)
	}
	if msgs[3].ToolName != "_continue_generation" {
		t.Errorf("msg[3] tool name = %q, want _continue_generation", msgs[3].ToolName)
	}
	if !strings.Contains(msgs[3].Content, `"status":"continue"`) {
		t.Errorf("msg[3] should contain continue status JSON, got %q", msgs[3].Content)
	}

	task, ok := resp.Result.(a2a.Task)
	if !ok {
		t.Fatalf("expected Task result, got %T", resp.Result)
	}
	if len(task.Status.Message.Parts) == 0 {
		t.Fatal("expected response parts")
	}
	fullText := task.Status.Message.Parts[0].Text
	if fullText != "Hello, here is part one and here is part two." {
		t.Errorf("full response = %q", fullText)
	}
}

func assertPairedToolHistory(t *testing.T, msgs []llm.ChatMessage) {
	t.Helper()
	calls := map[string]bool{}
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			calls[tc.ID] = true
		}
	}
	results := map[string]bool{}
	for _, m := range msgs {
		if m.Role != "tool" {
			continue
		}
		results[m.ToolCallID] = true
		if !calls[m.ToolCallID] {
			t.Errorf("orphan tool result for call ID %q", m.ToolCallID)
		}
	}
	for id := range calls {
		if !results[id] {
			t.Errorf("tool call %q has no paired result", id)
		}
	}
}

func TestImplicitContinueGenerationCall(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			Content:      "Hello, here is part one",
			FinishReason: "tool_calls",
			ToolCalls:    []llm.ToolCall{{ID: "cg-1", Name: "_continue_generation", Arguments: `{}`}},
		},
		&llm.ChatResponse{
			Content:      " and here is part two.",
			FinishReason: "stop",
		},
	)

	resp, err := agent.HandleTask(
		context.Background(),
		"task-implicit-cg",
		newUserMessage("say hello"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}

	msgs := calls[1].Messages
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages in second call, got %d", len(msgs))
	}
	if len(msgs[2].ToolCalls) != 1 || msgs[2].ToolCalls[0].Name != "_continue_generation" {
		t.Fatalf("assistant message should carry the host-injected _continue_generation call, got %+v", msgs[2].ToolCalls)
	}
	if !strings.HasPrefix(msgs[2].ToolCalls[0].ID, "host-continue-") {
		t.Errorf("LLM-emitted call should be replaced by a host-injected one, got ID %q", msgs[2].ToolCalls[0].ID)
	}
	if msgs[3].Role != "tool" || !strings.Contains(msgs[3].Content, `"status":"continue"`) {
		t.Errorf("expected continue tool result, got role %q content %q", msgs[3].Role, msgs[3].Content)
	}

	task, ok := resp.Result.(a2a.Task)
	if !ok {
		t.Fatalf("expected Task result, got %T", resp.Result)
	}
	if got := task.Status.Message.Parts[0].Text; got != "Hello, here is part one and here is part two." {
		t.Errorf("full response = %q", got)
	}
}

func TestImplicitContinueGenerationCallMixedWithRealTool(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			Content:      "working on it",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "cg-1", Name: "_continue_generation", Arguments: `{}`},
				{ID: "real-1", Name: "noop", Arguments: `{}`},
			},
		},
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	)

	noopExecuted := false
	noop := &testBuiltinTool{
		name:        "noop",
		description: "no-op tool",
		params:      &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
		execFn: func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
			noopExecuted = true
			return tool.OK("ok"), nil
		},
	}
	if err := agent.Tools().Register(noop); err != nil {
		t.Fatal(err)
	}

	resp, err := agent.HandleTask(
		context.Background(),
		"task-mixed-cg",
		newUserMessage("do the thing"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if !noopExecuted {
		t.Error("real tool call should have been dispatched")
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}

	var assistantMsg *llm.ChatMessage
	var continueResult, noopResult *llm.ChatMessage
	for i := range calls[1].Messages {
		m := &calls[1].Messages[i]
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			assistantMsg = m
		}
		if m.Role != "tool" {
			continue
		}
		if strings.Contains(m.Content, "unknown tool") {
			t.Errorf("no tool result should report an unknown tool, got %q", m.Content)
		}
		switch m.ToolCallID {
		case "cg-1":
			continueResult = m
		case "real-1":
			noopResult = m
		}
	}
	if assistantMsg == nil {
		t.Fatal("expected an assistant message carrying the tool calls")
	}
	gotIDs := make([]string, len(assistantMsg.ToolCalls))
	for i, tc := range assistantMsg.ToolCalls {
		gotIDs[i] = tc.ID
	}
	if !slices.Contains(gotIDs, "cg-1") || !slices.Contains(gotIDs, "real-1") {
		t.Fatalf("assistant message must carry both original calls, got %v", gotIDs)
	}
	if noopResult == nil {
		t.Fatal("expected a tool result for the real tool call")
	}
	if continueResult == nil {
		t.Fatal("expected a synthetic tool result for the continuation call")
	}
	if !strings.Contains(continueResult.Content, "must never invoke it yourself") {
		t.Errorf("continuation call should be answered with the corrective message, got %q", continueResult.Content)
	}
	assertPairedToolHistory(t, calls[1].Messages)

	task, ok := resp.Result.(a2a.Task)
	if !ok {
		t.Fatalf("expected Task result, got %T", resp.Result)
	}
	if got := task.Status.Message.Parts[0].Text; !strings.Contains(got, "done") {
		t.Errorf("final response should contain the closing text, got %q", got)
	}
}

func TestImplicitContinueMultipleRounds(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			Content:      "part one,",
			FinishReason: "tool_calls",
			ToolCalls:    []llm.ToolCall{{ID: "cg-1", Name: "_continue_generation", Arguments: `{}`}},
		},
		&llm.ChatResponse{
			Content:      " part two,",
			FinishReason: "tool_calls",
			ToolCalls:    []llm.ToolCall{{ID: "cg-2", Name: "_continue_generation", Arguments: `{}`}},
		},
		&llm.ChatResponse{Content: " part three.", FinishReason: "stop"},
	)

	resp, err := agent.HandleTask(
		context.Background(),
		"task-multi-cg",
		newUserMessage("go"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if calls := mock.getCalls(); len(calls) != 3 {
		t.Fatalf("expected 3 LLM calls, got %d", len(calls))
	}
	task := resp.Result.(a2a.Task)
	if got := task.Status.Message.Parts[0].Text; got != "part one, part two, part three." {
		t.Errorf("full response = %q", got)
	}
}

func TestImplicitContinueWithDroppedCompanionCall(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			Content:      "thinking",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "cg-1", Name: "_continue_generation", Arguments: `{}`},
				{ID: "bad-1", Name: "$FUNCTION_NAME", Arguments: `{}`},
			},
		},
		&llm.ChatResponse{Content: "recovered", FinishReason: "stop"},
	)

	resp, err := agent.HandleTask(
		context.Background(),
		"task-dropped-companion",
		newUserMessage("go"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}
	results := map[string]string{}
	for _, m := range calls[1].Messages {
		if m.Role == "tool" {
			results[m.ToolCallID] = m.Content
		}
	}
	if !strings.Contains(results["cg-1"], "must never invoke it yourself") {
		t.Errorf("continuation call should get the corrective result, got %q", results["cg-1"])
	}
	if !strings.Contains(results["bad-1"], "Invalid tool name") {
		t.Errorf("dropped companion should keep its corrective result, got %q", results["bad-1"])
	}
	assertPairedToolHistory(t, calls[1].Messages)
}

func TestImplicitContinueAutoContinuationDisabled(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			Content:      "partial",
			FinishReason: "tool_calls",
			ToolCalls:    []llm.ToolCall{{ID: "cg-1", Name: "_continue_generation", Arguments: `{}`}},
		},
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	)
	disabled := false
	agent.cfg.EnableAutoContinuation = &disabled

	resp, err := agent.HandleTask(
		context.Background(),
		"task-cg-disabled",
		newUserMessage("go"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls (dispatch error + retry), got %d", len(calls))
	}
	var sawUnknownTool bool
	for _, m := range calls[1].Messages {
		if m.Role == "tool" && m.ToolCallID == "cg-1" && strings.Contains(m.Content, "unknown tool") {
			sawUnknownTool = true
		}
	}
	if !sawUnknownTool {
		t.Error("with auto-continuation disabled the call should dispatch and fail loudly")
	}
	task := resp.Result.(a2a.Task)
	if got := task.Status.Message.Parts[0].Text; !strings.Contains(got, "done") {
		t.Errorf("final response should contain the recovery text, got %q", got)
	}
}

func TestImplicitContinueCapReturnsAccumulatedText(t *testing.T) {
	var responses []*llm.ChatResponse
	var want strings.Builder
	for i := 0; i <= maxAutoContinuations; i++ {
		part := fmt.Sprintf("part%d ", i)
		want.WriteString(part)
		responses = append(responses, &llm.ChatResponse{
			Content:      part,
			FinishReason: "tool_calls",
			ToolCalls:    []llm.ToolCall{{ID: fmt.Sprintf("cg-%d", i), Name: "_continue_generation", Arguments: `{}`}},
		})
	}
	agent, mock, _ := newTestAgent(responses...)
	agent.cfg.MaxLLMCalls = maxAutoContinuations + 5

	resp, err := agent.HandleTask(
		context.Background(),
		"task-cg-cap",
		newUserMessage("go"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("cutoff must not surface an error: %+v", resp.Error)
	}
	if calls := mock.getCalls(); len(calls) != maxAutoContinuations+1 {
		t.Fatalf("expected %d LLM calls, got %d", maxAutoContinuations+1, len(calls))
	}
	task := resp.Result.(a2a.Task)
	if got := task.Status.Message.Parts[0].Text; got != want.String() {
		t.Errorf("full response = %q, want %q", got, want.String())
	}
}

func TestImplicitContinueWithFencedArtifact(t *testing.T) {
	artifactContent := BlockPrefixSaveArtifact + " filename=\"notes.md\" description=\"Notes\"\nSome notes.\n" + BlockDelimiterClose + "\nSaved your notes."
	agent, mock, store := newTestAgent(
		&llm.ChatResponse{
			Content:      artifactContent,
			FinishReason: "tool_calls",
			ToolCalls:    []llm.ToolCall{{ID: "cg-1", Name: "_continue_generation", Arguments: `{}`}},
		},
		&llm.ChatResponse{Content: "All set.", FinishReason: "stop"},
	)

	brk := newMockBroker()
	memStore := memoryArtifactStore()
	svc := newMockServices(agent.svc.LLM(""), store)
	svc.brk = brk
	svc.arts = &artifact.ScopedService{
		Inner:     memStore,
		Scope:     "namespace",
		Namespace: "test-ns",
	}
	agent.svc = svc

	resp, err := agent.HandleTask(
		context.Background(),
		"task-cg-artifact",
		newUserMessage("save my notes"),
		a2a.UserProperties{
			ClientID:       "user-1",
			UserID:         "user-1",
			A2AStatusTopic: "test-ns/a2a/v1/agent/status/test-agent",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}

	var sawNotify bool
	for _, m := range calls[1].Messages {
		for _, tc := range m.ToolCalls {
			if tc.Name == "_continue_generation" {
				t.Errorf("the LLM-emitted continuation call must not reach history, got %+v", tc)
			}
		}
		if m.Role != "tool" {
			continue
		}
		if m.ToolName == "_notify_artifact_save" {
			sawNotify = true
		}
		if strings.Contains(m.Content, "unknown tool") {
			t.Errorf("no tool result should report an unknown tool, got %q", m.Content)
		}
	}
	if !sawNotify {
		t.Error("artifact notification should be injected on the continuation turn")
	}
	assertPairedToolHistory(t, calls[1].Messages)
}

func TestImplicitContinueCapDefeatsMaxTokensPassthrough(t *testing.T) {
	var responses []*llm.ChatResponse
	var want strings.Builder
	for i := 0; i <= maxAutoContinuations; i++ {
		part := fmt.Sprintf("part%d ", i)
		want.WriteString(part)
		responses = append(responses, &llm.ChatResponse{
			Content:      part,
			FinishReason: "max_tokens",
			ToolCalls:    []llm.ToolCall{{ID: fmt.Sprintf("cg-%d", i), Name: "_continue_generation", Arguments: `{}`}},
		})
	}
	agent, mock, _ := newTestAgent(responses...)
	agent.cfg.MaxLLMCalls = maxAutoContinuations + 5

	resp, err := agent.HandleTask(
		context.Background(),
		"task-cg-cap-maxtokens",
		newUserMessage("go"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("cutoff must not surface an error: %+v", resp.Error)
	}
	if calls := mock.getCalls(); len(calls) != maxAutoContinuations+1 {
		t.Fatalf("expected %d LLM calls, got %d", maxAutoContinuations+1, len(calls))
	}
	task := resp.Result.(a2a.Task)
	if got := task.Status.Message.Parts[0].Text; got != want.String() {
		t.Errorf("full response = %q, want %q", got, want.String())
	}
}

func TestImplicitContinueCapClampedToLowBudget(t *testing.T) {
	var responses []*llm.ChatResponse
	var want strings.Builder
	for i := 0; i < 4; i++ {
		part := fmt.Sprintf("part%d ", i)
		want.WriteString(part)
		responses = append(responses, &llm.ChatResponse{
			Content:      part,
			FinishReason: "tool_calls",
			ToolCalls:    []llm.ToolCall{{ID: fmt.Sprintf("cg-%d", i), Name: "_continue_generation", Arguments: `{}`}},
		})
	}
	agent, mock, _ := newTestAgent(responses...)

	resp, err := agent.HandleTask(
		context.Background(),
		"task-cg-cap-lowbudget",
		newUserMessage("go"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("clamped cutoff must not surface an error: %+v", resp.Error)
	}
	if calls := mock.getCalls(); len(calls) != 4 {
		t.Fatalf("expected 4 LLM calls, got %d", len(calls))
	}
	task := resp.Result.(a2a.Task)
	if got := task.Status.Message.Parts[0].Text; got != want.String() {
		t.Errorf("full response = %q, want %q", got, want.String())
	}
}

func TestImplicitContinueDisabledWithOtherWork(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			Content:      "working",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "cg-1", Name: "_continue_generation", Arguments: `{}`},
				{ID: "real-1", Name: "noop", Arguments: `{}`},
			},
		},
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	)
	disabled := false
	agent.cfg.EnableAutoContinuation = &disabled

	noopExecuted := false
	noop := &testBuiltinTool{
		name:        "noop",
		description: "no-op tool",
		params:      &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
		execFn: func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
			noopExecuted = true
			return tool.OK("ok"), nil
		},
	}
	if err := agent.Tools().Register(noop); err != nil {
		t.Fatal(err)
	}

	resp, err := agent.HandleTask(
		context.Background(),
		"task-cg-disabled-mixed",
		newUserMessage("go"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if !noopExecuted {
		t.Error("real tool call should have been dispatched")
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}
	var sawUnknownTool bool
	for _, m := range calls[1].Messages {
		if m.Role == "tool" && m.ToolCallID == "cg-1" && strings.Contains(m.Content, "unknown tool") {
			sawUnknownTool = true
		}
	}
	if !sawUnknownTool {
		t.Error("with auto-continuation disabled the continuation call must dispatch and fail loudly")
	}
	assertPairedToolHistory(t, calls[1].Messages)
}

func TestAutoContinuationDisabled(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			Content:      "partial",
			FinishReason: "max_tokens",
		},
	)

	disabled := false
	agent.cfg.EnableAutoContinuation = &disabled

	resp, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("hello"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(calls))
	}

	task := resp.Result.(a2a.Task)
	fullText := task.Status.Message.Parts[0].Text
	if fullText != "partial" {
		t.Errorf("response = %q, want partial", fullText)
	}
}

func TestAutoContinuationMultipleRounds(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{Content: "part1", FinishReason: "max_tokens"},
		&llm.ChatResponse{Content: "part2", FinishReason: "max_tokens"},
		&llm.ChatResponse{Content: "part3", FinishReason: "stop"},
	)

	resp, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("generate long text"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(mock.getCalls()) != 3 {
		t.Fatalf("expected 3 LLM calls, got %d", len(mock.getCalls()))
	}

	task := resp.Result.(a2a.Task)
	fullText := task.Status.Message.Parts[0].Text
	if fullText != "part1part2part3" {
		t.Errorf("response = %q, want part1part2part3", fullText)
	}
}

func TestAutoContinuationDefaultEnabled(t *testing.T) {
	agent := New(AgentConfig{Name: "test"})
	if !agent.autoContinuationEnabled() {
		t.Error("auto-continuation should default to enabled")
	}

	enabled := true
	agent.cfg.EnableAutoContinuation = &enabled
	if !agent.autoContinuationEnabled() {
		t.Error("auto-continuation should be enabled when explicitly true")
	}

	disabled := false
	agent.cfg.EnableAutoContinuation = &disabled
	if agent.autoContinuationEnabled() {
		t.Error("auto-continuation should be disabled when explicitly false")
	}
}

type mockBroker struct {
	mu                  sync.Mutex
	published           []publishedMsg
	requested           []publishedMsg
	queues              map[string]*mockQueue
	receiveChs          map[string]chan *broker.Message
	deletedQueues       []string
	deprovisionedQueues []string
	createdQueues       []string
	createdDirectSub    int
	directSubSeq        uint64
	deprovisionErr      error
	publishErr          error
}

type publishedMsg struct {
	topic     string
	payload   []byte
	userProps map[string]string
}

type mockQueue struct {
	name   string
	direct bool
}

func (q *mockQueue) Name() string               { return q.name }
func (q *mockQueue) IsDirectSubscription() bool { return q.direct }

func newMockBroker() *mockBroker {
	return &mockBroker{
		queues:     make(map[string]*mockQueue),
		receiveChs: make(map[string]chan *broker.Message),
	}
}

func (b *mockBroker) Connect(_ context.Context) error    { return nil }
func (b *mockBroker) Disconnect(_ context.Context) error { return nil }
func (b *mockBroker) IsConnected() bool                  { return true }
func (b *mockBroker) DeleteQueue(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deletedQueues = append(b.deletedQueues, name)
	return nil
}
func (b *mockBroker) DeprovisionQueue(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deprovisionedQueues = append(b.deprovisionedQueues, name)
	return b.deprovisionErr
}

func (b *mockBroker) getDeprovisionedQueues() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.deprovisionedQueues))
	copy(out, b.deprovisionedQueues)
	return out
}

func (b *mockBroker) getDeletedQueues() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.deletedQueues))
	copy(out, b.deletedQueues)
	return out
}

func (b *mockBroker) Subscribe(_ broker.Queue, _ string) error   { return nil }
func (b *mockBroker) Unsubscribe(_ broker.Queue, _ string) error { return nil }

func (b *mockBroker) CreateQueue(name string, _ broker.QueueOptions) (broker.Queue, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	q := &mockQueue{name: name}
	b.queues[name] = q
	b.receiveChs[name] = make(chan *broker.Message, 10)
	b.createdQueues = append(b.createdQueues, name)
	return q, nil
}

func (b *mockBroker) CreateTemporaryQueue() (broker.Queue, error) { return nil, nil }
func (b *mockBroker) RequestReplyInbox(context.Context, string, *broker.Message, time.Duration) (*broker.Message, error) {
	return nil, nil
}
func (b *mockBroker) CreateDirectSubscription() (broker.Queue, error) {
	b.mu.Lock()
	b.directSubSeq++
	name := fmt.Sprintf("direct-mock-%d", b.directSubSeq)
	b.createdDirectSub++
	b.mu.Unlock()
	q, err := b.CreateQueue(name, broker.QueueOptions{})
	if err != nil {
		return nil, err
	}
	if mq, ok := q.(*mockQueue); ok {
		mq.direct = true
	}
	return q, nil
}

func (b *mockBroker) Publish(_ context.Context, topic string, msg *broker.Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.publishErr != nil {
		return b.publishErr
	}
	b.published = append(b.published, publishedMsg{topic: topic, payload: msg.Payload, userProps: msg.UserProperties})
	return nil
}

func (b *mockBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	return b.Publish(ctx, topic, msg)
}

func (b *mockBroker) Receive(ctx context.Context, queue broker.Queue) (*broker.Message, error) {
	b.mu.Lock()
	ch := b.receiveChs[queue.Name()]
	b.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg := <-ch:
		return msg, nil
	}
}

func (b *mockBroker) Request(_ context.Context, topic string, msg *broker.Message, _ time.Duration) (*broker.Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.requested = append(b.requested, publishedMsg{topic: topic, payload: msg.Payload, userProps: msg.UserProperties})
	return nil, fmt.Errorf("not implemented")
}

func (b *mockBroker) getRequested() []publishedMsg {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.requested)
}

func (b *mockBroker) getPublished() []publishedMsg {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]publishedMsg, len(b.published))
	copy(out, b.published)
	return out
}

func TestBuildAgentCard(t *testing.T) {
	agent := New(AgentConfig{
		Name:        "test-agent",
		Description: "A test agent",
		Version:     "2.0.0",
		Namespace:   "test-ns",
	})

	card := agent.BuildAgentCard()

	if card.Name != "test-agent" {
		t.Errorf("name = %q", card.Name)
	}
	if card.Description != "A test agent" {
		t.Errorf("description = %q", card.Description)
	}
	if card.Version != "2.0.0" {
		t.Errorf("version = %q", card.Version)
	}
	if card.ProtocolVersion != "0.3.0" {
		t.Errorf("protocolVersion = %q", card.ProtocolVersion)
	}
	if card.URL != "solace:test-ns/a2a/v1/agent/request/test-agent" {
		t.Errorf("url = %q", card.URL)
	}
	if len(card.DefaultInputModes) != 1 || card.DefaultInputModes[0] != "text" {
		t.Errorf("inputModes = %v", card.DefaultInputModes)
	}
}

func TestBuildAgentCardDefaultVersion(t *testing.T) {
	agent := New(AgentConfig{Name: "test"})
	card := agent.BuildAgentCard()
	if card.Version != "1.0.0" {
		t.Errorf("default version = %q, want 1.0.0", card.Version)
	}
}

func TestBuildAgentCardWithTools(t *testing.T) {
	agent := New(AgentConfig{
		Name:      "test-agent",
		Namespace: "ns",
	})

	_ = agent.Tools().Register(&testBuiltinTool{
		name:        "my_tool",
		description: "a tool",
		params:      &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
	})

	card := agent.BuildAgentCard()

	var toolsExt *a2a.AgentExtension
	for i := range card.Capabilities.Extensions {
		if card.Capabilities.Extensions[i].URI == "https://solace.com/a2a/extensions/sam/tools" {
			toolsExt = &card.Capabilities.Extensions[i]
			break
		}
	}
	if toolsExt == nil {
		t.Fatal("missing tools extension")
	} else {
		tools, ok := toolsExt.Params["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Fatalf("tools extension = %v", toolsExt.Params)
		}
		toolMap, ok := tools[0].(map[string]any)
		if !ok || toolMap["name"] != "my_tool" {
			t.Errorf("tool[0] = %v", tools[0])
		}
	}
}

func TestBuildAgentCardAgentTypeExtension(t *testing.T) {
	agent := New(AgentConfig{Name: "test", Namespace: "ns"})
	card := agent.BuildAgentCard()

	var found bool
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == "https://solace.com/a2a/extensions/agent-type" {
			found = true
			if ext.Params["type"] != "standard" {
				t.Errorf("agent type = %v", ext.Params["type"])
			}
		}
	}
	if !found {
		t.Error("missing agent-type extension")
	}
}

type fakeSkillManager struct {
	available    []AvailableSkill
	instructions map[string]string
	toolOwner    map[string]string
	toolOwners   map[string][]string
	hasResources bool
	hasAssets    bool
}

func (f *fakeSkillManager) IsHeld(name string) bool {
	_, held := f.instructions[name]
	return held
}

func (f *fakeSkillManager) GetInstructions(names []string) []LoadedSkillInfo {
	var out []LoadedSkillInfo
	for _, name := range names {
		if content := f.instructions[name]; content != "" {
			out = append(out, LoadedSkillInfo{Name: name, InstructionContent: content})
		}
	}
	return out
}

func (f *fakeSkillManager) SkillToolOwners(toolName string) []string {
	if owners, ok := f.toolOwners[toolName]; ok {
		return owners
	}
	if owner, ok := f.toolOwner[toolName]; ok {
		return []string{owner}
	}
	return nil
}

func (f *fakeSkillManager) SkillToolNames(skillName string) []string {
	var names []string
	for toolName, owner := range f.toolOwner {
		if owner == skillName {
			names = append(names, toolName)
		}
	}
	for toolName, owners := range f.toolOwners {
		if slices.Contains(owners, skillName) {
			names = append(names, toolName)
		}
	}
	slices.Sort(names)
	return names
}

func (f *fakeSkillManager) HasAnyResources() bool                    { return f.hasResources }
func (f *fakeSkillManager) HasAnyAssets() bool                       { return f.hasAssets }
func (f *fakeSkillManager) AvailableSkillsForCard() []AvailableSkill { return f.available }

func findExtension(card a2a.AgentCard, uri string) *a2a.AgentExtension {
	for i := range card.Capabilities.Extensions {
		if card.Capabilities.Extensions[i].URI == uri {
			return &card.Capabilities.Extensions[i]
		}
	}
	return nil
}

const samSkillsExtURI = "https://solace.com/a2a/extensions/sam/skills"

func TestBuildAgentCardLoadableSkillsExcludedFromCardSkills(t *testing.T) {
	agent := New(AgentConfig{
		Name:      "test",
		Namespace: "ns",
		Skills: []a2a.AgentSkill{
			{ID: "search", Name: "Search", Description: "Find things"},
		},
	})
	agent.SetSkillManager(&fakeSkillManager{available: []AvailableSkill{
		{Name: "pptx", Description: "PowerPoint authoring"},
	}})

	card := agent.BuildAgentCard()

	if len(card.Skills) != 1 || card.Skills[0].ID != "search" {
		t.Fatalf("card.Skills = %v, want only curated 'search'", card.Skills)
	}
	for _, s := range card.Skills {
		if s.ID == "pptx" || s.Name == "pptx" {
			t.Errorf("loadable skill leaked into card.Skills: %v", s)
		}
	}
}

func TestBuildAgentCardLoadableSkillsExtensionLeavesProviderSliceUnsorted(t *testing.T) {
	agent := New(AgentConfig{Name: "test", Namespace: "ns"})
	fake := &fakeSkillManager{available: []AvailableSkill{
		{Name: "zebra", Description: "z skill"},
		{Name: "alpha", Description: "a skill"},
	}}
	agent.SetSkillManager(fake)

	card := agent.BuildAgentCard()

	if fake.available[0].Name != "zebra" {
		t.Errorf("provider slice was reordered in place: %v", fake.available)
	}

	ext := findExtension(card, samSkillsExtURI)
	if ext == nil {
		t.Fatal("missing sam/skills extension")
	}
	skills, ok := ext.Params["skills"].([]any)
	if !ok || len(skills) != 2 {
		t.Fatalf("skills param = %v", ext.Params["skills"])
	}
	first, _ := skills[0].(map[string]any)
	second, _ := skills[1].(map[string]any)
	if first["name"] != "alpha" || second["name"] != "zebra" {
		t.Errorf("skills not sorted by name: %v", skills)
	}
	if first["description"] != "a skill" {
		t.Errorf("alpha description = %v", first["description"])
	}
}

func TestBuildAgentCardNoLoadableSkillsExtensionWhenEmpty(t *testing.T) {
	agent := New(AgentConfig{Name: "test", Namespace: "ns"})
	if ext := findExtension(agent.BuildAgentCard(), samSkillsExtURI); ext != nil {
		t.Error("sam/skills extension present with no skill manager")
	}

	agent.SetSkillManager(&fakeSkillManager{})
	if ext := findExtension(agent.BuildAgentCard(), samSkillsExtURI); ext != nil {
		t.Error("sam/skills extension present with empty available list")
	}
}

func TestBuildAgentCardNoHealthExtensionWhenNoProblems(t *testing.T) {
	agent := New(AgentConfig{Name: "test", Namespace: "ns"})
	if ext := findExtension(agent.BuildAgentCard(), a2a.ExtensionURIHealth); ext != nil {
		t.Error("sam/health extension present with no active problems")
	}
}

func TestBuildAgentCardHealthExtension(t *testing.T) {
	agent := New(AgentConfig{Name: "test", Namespace: "ns"})
	agent.health.Set("queue:test-ns/a2a/v1/agent/request/test", health.Degraded, "provision cooldown")

	problems := a2a.HealthProblemsFromCard(agent.BuildAgentCard())
	if len(problems) != 1 {
		t.Fatalf("HealthProblemsFromCard() = %v, want one problem", problems)
	}
	if problems[0].Severity != health.Degraded || problems[0].Detail != "provision cooldown" {
		t.Errorf("problem = %+v, want Degraded/\"provision cooldown\"", problems[0])
	}

	agent.health.Clear("queue:test-ns/a2a/v1/agent/request/test")
	if ext := findExtension(agent.BuildAgentCard(), a2a.ExtensionURIHealth); ext != nil {
		t.Error("sam/health extension still present after Clear")
	}
}

func TestBuildAgentCardWithWelcome(t *testing.T) {
	agent := New(AgentConfig{
		Name:      "test",
		Namespace: "ns",
		Welcome: &WelcomeConfig{
			Message: "Hello! I'm a test agent.",
			Suggestions: []WelcomeSuggestion{
				{Label: "Tell me more", Prompt: "Tell me more about yourself", AutoSend: true},
				{Label: "Get started", Prompt: "Help me get started", AutoSend: false},
			},
		},
	})
	card := agent.BuildAgentCard()

	var found bool
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == ExtensionURIWelcome {
			found = true
			if ext.Params["welcome_message"] != "Hello! I'm a test agent." {
				t.Errorf("welcome_message = %v", ext.Params["welcome_message"])
			}
			suggestions, ok := ext.Params["suggestions"].([]map[string]any)
			if !ok || len(suggestions) != 2 {
				t.Fatalf("suggestions = %v", ext.Params["suggestions"])
			}
			if suggestions[0]["label"] != "Tell me more" {
				t.Errorf("suggestion[0].label = %v", suggestions[0]["label"])
			}
			if suggestions[0]["auto_send"] != true {
				t.Errorf("suggestion[0].auto_send = %v", suggestions[0]["auto_send"])
			}
			if suggestions[1]["auto_send"] != false {
				t.Errorf("suggestion[1].auto_send = %v", suggestions[1]["auto_send"])
			}
		}
	}
	if !found {
		t.Error("missing welcome extension")
	}
}

func TestBuildAgentCardWithoutWelcome(t *testing.T) {
	agent := New(AgentConfig{Name: "test", Namespace: "ns"})
	card := agent.BuildAgentCard()

	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == ExtensionURIWelcome {
			t.Error("welcome extension should not be present when Welcome is nil")
		}
	}
}

func TestBuildAgentCardWithPriority(t *testing.T) {
	agent := New(AgentConfig{Name: "test", Namespace: "ns", Priority: 100})
	card := agent.BuildAgentCard()

	var found bool
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == ExtensionURIPriority {
			found = true
			if ext.Params["priority"] != 100 {
				t.Errorf("priority = %v, want 100", ext.Params["priority"])
			}
		}
	}
	if !found {
		t.Error("missing priority extension")
	}
}

func TestBuildAgentCardWithoutPriority(t *testing.T) {
	agent := New(AgentConfig{Name: "test", Namespace: "ns"})
	card := agent.BuildAgentCard()

	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == ExtensionURIPriority {
			t.Error("priority extension should not be present when Priority is zero")
		}
	}
}

func TestBuildAgentCardDeploymentID(t *testing.T) {
	agent := New(AgentConfig{Name: "agent", Namespace: "ns"})
	card := agent.BuildAgentCard()
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == ExtensionURIDeployment {
			t.Fatal("sam/deployment extension should be absent when DeploymentID is empty")
		}
	}

	agent = New(AgentConfig{Name: "agent", Namespace: "ns", DeploymentID: "deploy-xyz-123"})
	card = agent.BuildAgentCard()
	var ext *a2a.AgentExtension
	for i := range card.Capabilities.Extensions {
		if card.Capabilities.Extensions[i].URI == ExtensionURIDeployment {
			ext = &card.Capabilities.Extensions[i]
			break
		}
	}
	if ext == nil {
		t.Fatal("missing sam/deployment extension")
	}
	if ext.URI != "https://solace.com/a2a/extensions/sam/deployment" {
		t.Errorf("URI = %q", ext.URI)
	}
	if got := ext.Params["deployment_id"]; got != "deploy-xyz-123" {
		t.Errorf("deployment_id = %v", got)
	}
}

func TestBuildAgentCardPeerTopology(t *testing.T) {
	agent := New(AgentConfig{Name: "orchestrator", Namespace: "ns"})
	agent.Init(context.Background(), nil, newMockServices(newMockLLM(), newMemSessionStore())) //nolint:errcheck // test

	card := agent.BuildAgentCard()
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == ExtensionURIPeerAgentTopology {
			t.Fatal("peer-agent-topology extension should be absent when peer registry is empty")
		}
	}

	agent.InjectPeerCard(a2a.AgentCard{Name: "math-agent"})
	agent.InjectPeerCard(a2a.AgentCard{Name: "data-agent"})

	card = agent.BuildAgentCard()
	var ext *a2a.AgentExtension
	for i := range card.Capabilities.Extensions {
		if card.Capabilities.Extensions[i].URI == ExtensionURIPeerAgentTopology {
			ext = &card.Capabilities.Extensions[i]
			break
		}
	}
	if ext == nil {
		t.Fatal("missing peer-agent-topology extension")
	}
	if ext.URI != "https://solace.com/a2a/extensions/peer-agent-topology" {
		t.Errorf("URI = %q, want Python-parity URI", ext.URI)
	}
	names, ok := ext.Params["peer_agent_names"].([]string)
	if !ok {
		t.Fatalf("peer_agent_names = %T, want []string", ext.Params["peer_agent_names"])
	}
	if len(names) != 2 || names[0] != "data-agent" || names[1] != "math-agent" {
		t.Errorf("peer_agent_names = %v, want [data-agent math-agent]", names)
	}

	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"peer_agent_names":["data-agent","math-agent"]`) {
		t.Errorf("wire JSON missing peer_agent_names array: %s", raw)
	}
}

func TestBuildAgentCardWithSkills(t *testing.T) {
	agent := New(AgentConfig{
		Name:      "test",
		Namespace: "ns",
		Skills: []a2a.AgentSkill{
			{ID: "skill-1", Name: "Summarize", Description: "Summarize text"},
		},
	})
	card := agent.BuildAgentCard()
	if len(card.Skills) != 1 || card.Skills[0].ID != "skill-1" {
		t.Errorf("skills = %v", card.Skills)
	}
}

func TestPublishAgentCard(t *testing.T) {
	brk := newMockBroker()
	mock := newMockLLM(&llm.ChatResponse{Content: "ok"})
	store := newMemSessionStore()

	agent := New(AgentConfig{
		Name:      "pub-agent",
		Namespace: "test-ns",
	})
	svc := newMockServices(mock, store)
	svc.brk = brk
	agent.svc = svc
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test helper

	err := agent.publishAgentCard(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	published := brk.getPublished()
	if len(published) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(published))
	}
	if published[0].topic != "test-ns/a2a/v1/discovery/agentcards" {
		t.Errorf("topic = %q", published[0].topic)
	}

	var card a2a.AgentCard
	if jsonErr := json.Unmarshal(published[0].payload, &card); jsonErr != nil {
		t.Fatal(jsonErr)
	}
	if card.Name != "pub-agent" {
		t.Errorf("card name = %q", card.Name)
	}
	if card.URL != "solace:test-ns/a2a/v1/agent/request/pub-agent" {
		t.Errorf("card url = %q", card.URL)
	}
}

func TestHandleTaskArtifactBlockDetection_SavingNoticePublishedBeforePersist(t *testing.T) {
	artifactContent := BlockPrefixSaveArtifact + " filename=\"poem.md\" description=\"A poem\"\nRoses are red,\nViolets are blue.\n" + BlockDelimiterClose + "\nHere is your poem!"
	agent, _, store := newTestAgent(&llm.ChatResponse{
		Content:      artifactContent,
		FinishReason: "stop",
	})

	brk := newMockBroker()
	memStore := memoryArtifactStore()
	svc := newMockServices(agent.svc.LLM(""), store)
	svc.brk = brk

	var savingBeforePersist bool
	spy := &saveOrderSpy{Service: memStore, onFirstSave: func() {
		for _, m := range brk.getPublished() {
			if strings.Contains(string(m.payload), "Saving artifact") {
				savingBeforePersist = true
				return
			}
		}
	}}
	svc.arts = &artifact.ScopedService{
		Inner:     spy,
		Scope:     "namespace",
		Namespace: "test-ns",
	}
	agent.svc = svc

	resp, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("Write me a poem"),
		a2a.UserProperties{
			ClientID:       "user-1",
			UserID:         "user-1",
			A2AStatusTopic: "test-ns/a2a/v1/agent/status/test-agent",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	task := resp.Result.(a2a.Task)
	responseText := task.Status.Message.Parts[len(task.Status.Message.Parts)-1].Text
	if strings.Contains(responseText, "Roses are red") {
		t.Errorf("response should not contain artifact content, got: %q", responseText)
	}
	if !strings.Contains(responseText, "Here is your poem!") {
		t.Errorf("response should contain text after artifact block, got: %q", responseText)
	}

	loc := svc.arts.Resolve("test-agent", "user-1", "ctx-1", "poem.md")
	content, mimeType, loadErr := memStore.Load(context.Background(), loc, nil)
	if loadErr != nil {
		t.Fatalf("failed to load artifact: %v", loadErr)
	}
	if content == nil {
		t.Fatal("artifact not saved")
	}
	if mimeType != "text/plain" {
		t.Errorf("artifact mime type = %q", mimeType)
	}
	if !strings.Contains(string(content), "Roses are red") {
		t.Errorf("artifact content = %q", string(content))
	}

	published := brk.getPublished()
	var hasArtifactProgress, hasArtifactSaved, hasCompleted bool
	for _, msg := range published {
		payload := string(msg.payload)
		if strings.Contains(payload, "artifact_creation_progress") {
			hasArtifactProgress = true
			if strings.Contains(payload, `"status":"completed"`) {
				hasCompleted = true
			}
		}
		if strings.Contains(payload, "artifact_saved") {
			hasArtifactSaved = true
		}
	}
	if !hasArtifactProgress {
		t.Error("expected artifact_creation_progress status event")
	}
	if !hasArtifactSaved {
		t.Error("expected artifact_saved status event")
	}
	if !hasCompleted {
		t.Error("expected an artifact_creation_progress completed event")
	}
	if !savingBeforePersist {
		t.Error("expected \"Saving artifact\" progress update to be published before the artifact is persisted")
	}
}

type saveOrderSpy struct {
	artifact.Service
	once        sync.Once
	onFirstSave func()
	saveErr     error
}

func (s *saveOrderSpy) Save(ctx context.Context, loc artifact.Locator, content []byte, mimeType string) (int, error) {
	s.once.Do(func() {
		if s.onFirstSave != nil {
			s.onFirstSave()
		}
	})
	if s.saveErr != nil {
		return 0, s.saveErr
	}
	return s.Service.Save(ctx, loc, content, mimeType)
}

func TestHandleTaskArtifactBlockSaveFailure(t *testing.T) {
	artifactContent := BlockPrefixSaveArtifact + " filename=\"poem.md\" description=\"A poem\"\nRoses are red,\nViolets are blue.\n" + BlockDelimiterClose + "\nHere is your poem!"
	agent, _, store := newTestAgent(&llm.ChatResponse{
		Content:      artifactContent,
		FinishReason: "stop",
	})

	brk := newMockBroker()
	memStore := memoryArtifactStore()
	svc := newMockServices(agent.svc.LLM(""), store)
	svc.brk = brk
	svc.arts = &artifact.ScopedService{
		Inner:     &saveOrderSpy{Service: memStore, saveErr: errors.New("disk full")},
		Scope:     "namespace",
		Namespace: "test-ns",
	}
	agent.svc = svc

	_, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("Write me a poem"),
		a2a.UserProperties{
			ClientID:       "user-1",
			UserID:         "user-1",
			A2AStatusTopic: "test-ns/a2a/v1/agent/status/test-agent",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	var hasSaving, hasFailed, hasCompleted, hasSaved bool
	for _, msg := range brk.getPublished() {
		payload := string(msg.payload)
		if strings.Contains(payload, "Saving artifact") {
			hasSaving = true
		}
		if strings.Contains(payload, "artifact_creation_progress") && strings.Contains(payload, `"status":"failed"`) {
			hasFailed = true
		}
		if strings.Contains(payload, "artifact_creation_progress") && strings.Contains(payload, `"status":"completed"`) {
			hasCompleted = true
		}
		if strings.Contains(payload, "artifact_saved") {
			hasSaved = true
		}
	}
	if !hasSaving {
		t.Error("expected a \"Saving artifact\" progress update before the failed save")
	}
	if !hasFailed {
		t.Error("expected an artifact_creation_progress failed event on save failure")
	}
	if hasCompleted {
		t.Error("did not expect a completed artifact_creation_progress event on save failure")
	}
	if hasSaved {
		t.Error("did not expect an artifact_saved event on save failure")
	}
}

func TestHandleTaskArtifactNotification(t *testing.T) {
	artifactContent := BlockPrefixSaveArtifact + " filename=\"hello.txt\" description=\"A greeting\"\nHello World\n" + BlockDelimiterClose
	followUp := "The artifact has been saved successfully."

	mock := newMockLLM(
		&llm.ChatResponse{Content: artifactContent, FinishReason: "stop"},
		&llm.ChatResponse{Content: followUp, FinishReason: "stop"},
	)
	store := newMemSessionStore()
	brk := newMockBroker()
	memStore := memoryArtifactStore()

	agent := New(AgentConfig{
		Name:         "test-agent",
		SystemPrompt: "You are a helpful assistant.",
		Model:        "test-model",
		MaxTokens:    1000,
		MaxLLMCalls:  5,
		Namespace:    "test-ns",
	})

	svc := newMockServices(mock, store)
	svc.brk = brk
	svc.arts = &artifact.ScopedService{
		Inner:     memStore,
		Scope:     "namespace",
		Namespace: "test-ns",
	}
	agent.svc = svc
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test setup, error checked via agent behavior

	resp, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("Create hello.txt"),
		a2a.UserProperties{
			ClientID:       "user-1",
			UserID:         "user-1",
			A2AStatusTopic: "test-ns/a2a/v1/agent/status/test-agent",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}

	secondCallMsgs := calls[1].Messages
	var hasNotifyCall, hasNotifyResult bool
	var notifyResultContent string
	for _, m := range secondCallMsgs {
		for _, tc := range m.ToolCalls {
			if tc.Name == "_notify_artifact_save" && strings.HasPrefix(tc.ID, hostNotifyCallIDPrefix) {
				hasNotifyCall = true
			}
		}
		if m.Role == "tool" && m.ToolCallID != "" && strings.HasPrefix(m.ToolCallID, hostNotifyCallIDPrefix) {
			hasNotifyResult = true
			notifyResultContent = m.Content
		}
	}
	if !hasNotifyCall {
		t.Error("second LLM call should contain _notify_artifact_save tool call")
	}
	if !hasNotifyResult {
		t.Error("second LLM call should contain _notify_artifact_save tool result")
	}

	if !strings.Contains(notifyResultContent, "Do NOT repeat or re-summarize") {
		t.Errorf("LLM-facing notify result should contain anti-repeat instruction, got: %q", notifyResultContent)
	}
	if !strings.Contains(notifyResultContent, "provided to the requester") {
		t.Errorf("LLM-facing notify result should retain the success message, got: %q", notifyResultContent)
	}

	published := brk.getPublished()
	var hasToolStart, hasToolResult bool
	for _, msg := range published {
		payload := string(msg.payload)
		if strings.Contains(payload, "tool_invocation_start") && strings.Contains(payload, "_notify_artifact_save") {
			hasToolStart = true
		}
		if strings.Contains(payload, "tool_result") && strings.Contains(payload, "_notify_artifact_save") {
			hasToolResult = true
			if strings.Contains(payload, `"type":"tool_result"`) && strings.Contains(payload, "Do NOT repeat or re-summarize") {
				t.Error("tool_result status event leaked the LLM-only anti-repeat instruction")
			}
		}
	}
	if !hasToolStart {
		t.Error("expected tool_invocation_start status event for _notify_artifact_save")
	}
	if !hasToolResult {
		t.Error("expected tool_result status event for _notify_artifact_save")
	}

	task := resp.Result.(a2a.Task)
	responseText := task.Status.Message.Parts[len(task.Status.Message.Parts)-1].Text
	if !strings.Contains(responseText, followUp) {
		t.Errorf("response should contain follow-up text, got: %q", responseText)
	}
}

func TestAppendResponseText(t *testing.T) {
	for _, tc := range []struct {
		name, prev, text, want string
	}{
		{"first response", "", "Hello.", "Hello."},
		{"after a sentence", "One.", "Two.", "One.\n\nTwo."},
		{"after a line break", "One.\n", "Two.", "One.\n\nTwo."},
		{"after a blank line", "One.\n\n", "Two.", "One.\n\nTwo."},
		{"empty response", "One.", "", "One."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reply strings.Builder
			reply.WriteString(tc.prev)
			appendResponseText(&reply, tc.text)
			if got := reply.String(); got != tc.want {
				t.Errorf("appendResponseText(%q, %q) = %q, want %q", tc.prev, tc.text, got, tc.want)
			}
		})
	}
}

func TestHandleTaskDropsLLMGeneratedNotifyCall(t *testing.T) {
	mock := newMockLLM(
		&llm.ChatResponse{
			Content: "Let me notify about the artifact.",
			ToolCalls: []llm.ToolCall{{
				ID:        "call_abc123",
				Name:      "_notify_artifact_save",
				Arguments: `{"filename":"test.txt","version":1,"status":"saved"}`,
			}},
			FinishReason: "stop",
		},
		&llm.ChatResponse{
			Content:      "Saved the artifact for you.",
			FinishReason: "stop",
		},
	)
	store := newMemSessionStore()

	agent := New(AgentConfig{
		Name:         "test-agent",
		SystemPrompt: "You are a helpful assistant.",
		Model:        "test-model",
		MaxTokens:    1000,
		MaxLLMCalls:  5,
		Namespace:    "test-ns",
	})

	svc := newMockServices(mock, store)
	agent.svc = svc
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test setup, error checked via agent behavior

	resp, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("Hello"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls (drop + retry), got %d", len(calls))
	}

	task := resp.Result.(a2a.Task)
	responseText := task.Status.Message.Parts[0].Text
	if !strings.Contains(responseText, "Saved the artifact for you.") {
		t.Errorf("unexpected response text: %q", responseText)
	}
}

func memoryArtifactStore() artifact.Service {
	return memory.New()
}

func TestBuildSystemPromptTeachesFilesOnlyToAgentsWithArtifactTools(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})

	without := agent.buildSystemPrompt(context.Background(), nil, gatewayContext{}, promptSkillState{}, false)
	if strings.Contains(without, BlockPrefixSaveArtifact) {
		t.Error("an agent without the artifact tools must not be taught the save_artifact block")
	}

	if err := agent.Tools().Register(&testBuiltinTool{name: loadArtifactToolName, description: "Load an artifact"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	with := agent.buildSystemPrompt(context.Background(), nil, gatewayContext{}, promptSkillState{}, false)
	for _, want := range []string{BlockPrefixSaveArtifact, `filename=`, "artifact_return:filename"} {
		if !strings.Contains(with, want) {
			t.Errorf("prompt for an agent with the artifact tools is missing %q", want)
		}
	}
}

func TestStartCardPublisherOnStart(t *testing.T) {
	brk := newMockBroker()
	mock := newMockLLM(&llm.ChatResponse{Content: "ok"})
	store := newMemSessionStore()

	agent := New(AgentConfig{
		Name:      "card-agent",
		Namespace: "test-ns",
	})
	svc := newMockServices(mock, store)
	svc.brk = brk
	agent.svc = svc
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test helper

	ctx := t.Context()

	err := agent.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Stop(context.Background()) //nolint:errcheck // test helper

	time.Sleep(50 * time.Millisecond)

	published := brk.getPublished()
	if len(published) < 1 {
		t.Error("expected at least 1 published agent card on start")
	}
}

func TestBuildAgentCardCustomInputModes(t *testing.T) {
	agent := New(AgentConfig{
		Name:              "test",
		Namespace:         "ns",
		DefaultInputModes: []string{"text", "file", "data"},
	})
	card := agent.BuildAgentCard()
	if len(card.DefaultInputModes) != 3 {
		t.Fatalf("DefaultInputModes length = %d, want 3", len(card.DefaultInputModes))
	}
	for i, want := range []string{"text", "file", "data"} {
		if card.DefaultInputModes[i] != want {
			t.Errorf("DefaultInputModes[%d] = %q, want %q", i, card.DefaultInputModes[i], want)
		}
	}
}

func TestBuildAgentCardCustomOutputModes(t *testing.T) {
	agent := New(AgentConfig{
		Name:               "test",
		Namespace:          "ns",
		DefaultOutputModes: []string{"text", "artifact"},
	})
	card := agent.BuildAgentCard()
	if len(card.DefaultOutputModes) != 2 {
		t.Fatalf("DefaultOutputModes length = %d, want 2", len(card.DefaultOutputModes))
	}
	if card.DefaultOutputModes[0] != "text" || card.DefaultOutputModes[1] != "artifact" {
		t.Errorf("DefaultOutputModes = %v, want [text artifact]", card.DefaultOutputModes)
	}
}

func TestBuildAgentCardDefaultModesFallback(t *testing.T) {
	agent := New(AgentConfig{Name: "test", Namespace: "ns"})
	card := agent.BuildAgentCard()

	if len(card.DefaultInputModes) != 1 || card.DefaultInputModes[0] != "text" {
		t.Errorf("DefaultInputModes = %v, want [text]", card.DefaultInputModes)
	}
	if len(card.DefaultOutputModes) != 1 || card.DefaultOutputModes[0] != "text" {
		t.Errorf("DefaultOutputModes = %v, want [text]", card.DefaultOutputModes)
	}
}

func TestBuildAgentCardDocumentationURL(t *testing.T) {
	agent := New(AgentConfig{
		Name:             "test",
		Namespace:        "ns",
		DocumentationURL: "https://docs.example.com/my-agent",
	})
	card := agent.BuildAgentCard()
	if card.DocumentationURL != "https://docs.example.com/my-agent" {
		t.Errorf("DocumentationURL = %q, want %q", card.DocumentationURL, "https://docs.example.com/my-agent")
	}
}

func TestBuildAgentCardProvider(t *testing.T) {
	agent := New(AgentConfig{
		Name:      "test",
		Namespace: "ns",
		Provider: &a2a.AgentProvider{
			Organization: "Acme Corp",
			URL:          "https://acme.example.com",
		},
	})
	card := agent.BuildAgentCard()
	if card.Provider == nil {
		t.Fatal("Provider should not be nil")
	}
	if card.Provider.Organization != "Acme Corp" {
		t.Errorf("Provider.Organization = %q, want %q", card.Provider.Organization, "Acme Corp")
	}
	if card.Provider.URL != "https://acme.example.com" {
		t.Errorf("Provider.URL = %q, want %q", card.Provider.URL, "https://acme.example.com")
	}
}

func TestBuildAgentCardProviderNilByDefault(t *testing.T) {
	agent := New(AgentConfig{Name: "test", Namespace: "ns"})
	card := agent.BuildAgentCard()
	if card.Provider != nil {
		t.Errorf("Provider should be nil when not configured, got %+v", card.Provider)
	}
}

func TestBuildAgentCardJSONRoundtrip(t *testing.T) {
	agent := New(AgentConfig{
		Name:               "roundtrip-agent",
		Namespace:          "ns",
		DefaultInputModes:  []string{"text", "file"},
		DefaultOutputModes: []string{"text", "artifact"},
		DocumentationURL:   "https://example.com/docs",
		Provider: &a2a.AgentProvider{
			Organization: "Test Org",
			URL:          "https://example.com",
		},
	})
	card := agent.BuildAgentCard()

	data, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}

	var decoded a2a.AgentCard
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}

	if len(decoded.DefaultInputModes) != 2 || decoded.DefaultInputModes[1] != "file" {
		t.Errorf("roundtrip DefaultInputModes = %v", decoded.DefaultInputModes)
	}
	if len(decoded.DefaultOutputModes) != 2 || decoded.DefaultOutputModes[1] != "artifact" {
		t.Errorf("roundtrip DefaultOutputModes = %v", decoded.DefaultOutputModes)
	}
	if decoded.DocumentationURL != "https://example.com/docs" {
		t.Errorf("roundtrip DocumentationURL = %q", decoded.DocumentationURL)
	}
	if decoded.Provider == nil || decoded.Provider.Organization != "Test Org" {
		t.Errorf("roundtrip Provider = %+v", decoded.Provider)
	}
}

func TestStartDiscoveryDisabled(t *testing.T) {
	brk := newMockBroker()
	mock := newMockLLM(&llm.ChatResponse{Content: "ok"})
	store := newMemSessionStore()

	disabled := false
	agent := New(AgentConfig{
		Name:             "no-disc-agent",
		Namespace:        "test-ns",
		DiscoveryEnabled: &disabled,
	})
	svc := newMockServices(mock, store)
	svc.brk = brk
	agent.svc = svc
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test helper

	ctx := t.Context()

	err := agent.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Stop(context.Background()) //nolint:errcheck // test helper

	if n := directSubscriptions(brk); n != 1 {
		t.Errorf("direct subscriptions = %d, want 1: no discovery subscription when discovery is disabled", n)
	}

	time.Sleep(50 * time.Millisecond)
	published := brk.getPublished()
	if len(published) < 1 {
		t.Error("agent card should still be published even with discovery disabled")
	}
}

func TestStartDiscoveryEnabledByDefault(t *testing.T) {
	brk := newMockBroker()
	mock := newMockLLM(&llm.ChatResponse{Content: "ok"})
	store := newMemSessionStore()

	agent := New(AgentConfig{
		Name:      "disc-agent",
		Namespace: "test-ns",
	})
	svc := newMockServices(mock, store)
	svc.brk = brk
	agent.svc = svc
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test helper

	ctx := t.Context()

	err := agent.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Stop(context.Background()) //nolint:errcheck // test helper

	if n := directSubscriptions(brk); n != 3 {
		t.Errorf("direct subscriptions = %d, want 3: discovery should subscribe when enabled (default)", n)
	}
}

func TestDiscoveryDisabledIgnoresPeerCards(t *testing.T) {
	brk := newMockBroker()
	mock := newMockLLM(&llm.ChatResponse{Content: "ok"})
	store := newMemSessionStore()

	disabled := false
	agent := New(AgentConfig{
		Name:             "isolated-agent",
		Namespace:        "test-ns",
		DiscoveryEnabled: &disabled,
	})
	svc := newMockServices(mock, store)
	svc.brk = brk
	agent.svc = svc
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test helper

	ctx := t.Context()

	err := agent.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Stop(context.Background()) //nolint:errcheck // test helper

	card := a2a.AgentCard{
		Name:        "peer-agent",
		Description: "A peer",
		URL:         "solace:test-ns/a2a/v1/agent/request/peer-agent",
	}
	data, _ := json.Marshal(card)
	agent.handleDiscoveryMessage(&broker.Message{Payload: data})

	if n := directSubscriptions(brk); n != 1 {
		t.Errorf("direct subscriptions = %d, want 1: no discovery subscription when discovery is disabled", n)
	}
}

func TestHandleTaskInvalidToolName_DollarPrefix(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Name: "$invalid", Arguments: `{}`},
			},
		},
		&llm.ChatResponse{
			Content:      "Sorry about that, let me help you differently.",
			FinishReason: "stop",
		},
	)

	resp, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("Hello"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls (invalid tool rejected → retry), got %d", len(calls))
	}

	secondMsgs := calls[1].Messages
	hasToolError := false
	for _, m := range secondMsgs {
		if m.Role == "tool" && strings.Contains(m.Content, "Invalid tool name") {
			hasToolError = true
		}
	}
	if !hasToolError {
		t.Error("second LLM call should contain tool error message about invalid name")
	}
}

func TestHandleTaskInvalidToolName_SpecialChars(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Name: "foo bar!", Arguments: `{}`},
			},
		},
		&llm.ChatResponse{
			Content:      "Let me try again.",
			FinishReason: "stop",
		},
	)

	resp, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("Hello"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}

	secondMsgs := calls[1].Messages
	hasToolError := false
	for _, m := range secondMsgs {
		if m.Role == "tool" && strings.Contains(m.Content, "Invalid tool name") {
			hasToolError = true
		}
	}
	if !hasToolError {
		t.Error("second LLM call should contain tool error about invalid name")
	}
}

func TestHandleTaskInvalidToolName_Empty(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Name: "", Arguments: `{}`},
			},
		},
		&llm.ChatResponse{
			Content:      "OK",
			FinishReason: "stop",
		},
	)

	resp, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("Hello"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}
}

func TestHandleTaskPanicInToolDispatch(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Name: "panic_tool", Arguments: `{}`},
			},
		},
		&llm.ChatResponse{
			Content:      "I recovered from the error.",
			FinishReason: "stop",
		},
	)

	panicTool := &testBuiltinTool{
		name:        "panic_tool",
		description: "A tool that panics",
		params:      &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
		execFn: func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
			panic("unexpected nil pointer")
		},
	}
	if err := agent.Tools().Register(panicTool); err != nil {
		t.Fatal(err)
	}

	brk := newMockBroker()
	svc := newMockServices(agent.svc.LLM(""), newMemSessionStore())
	svc.brk = brk
	agent.svc = svc

	resp, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("Call the panic tool"),
		a2a.UserProperties{
			ClientID:       "user-1",
			UserID:         "user-1",
			A2AStatusTopic: "test-ns/a2a/v1/agent/status/test-agent",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls (panic → error result → text), got %d", len(calls))
	}

	secondMsgs := calls[1].Messages
	hasPanicError := false
	for _, m := range secondMsgs {
		if m.Role == "tool" && strings.Contains(m.Content, "panic") {
			hasPanicError = true
		}
	}
	if !hasPanicError {
		t.Error("second LLM call should contain panic error result")
	}
}

func TestHandleTaskToCompletion_ImmediateResponse(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{
		Content:      "Immediate response",
		FinishReason: "stop",
	})

	resp, err := agent.HandleTaskToCompletion(
		context.Background(),
		"task-001",
		newUserMessage("Hello"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	} else if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	task := resp.Result.(a2a.Task)
	responseText := task.Status.Message.Parts[0].Text
	if !strings.Contains(responseText, "Immediate response") {
		t.Errorf("response = %q", responseText)
	}
}

func TestAgentStop_DeletesAllQueues(t *testing.T) {
	brk := newMockBroker()
	mock := newMockLLM(&llm.ChatResponse{Content: "ok"})
	store := newMemSessionStore()

	agent := New(AgentConfig{
		Name:      "cleanup-agent",
		Namespace: "test-ns",
	})
	svc := newMockServices(mock, store)
	svc.brk = brk
	agent.svc = svc
	if err := agent.Init(context.Background(), nil, svc); err != nil {
		t.Fatal(err)
	}

	if err := agent.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := agent.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	deleted := brk.getDeletedQueues()

	const requestQueue = "test-ns/q/a2a/cleanup-agent"
	foundRequest := false
	directSubDeletes := 0
	for _, got := range deleted {
		switch {
		case got == requestQueue:
			foundRequest = true
		case strings.HasPrefix(got, "direct-mock-"):
			directSubDeletes++
		}
	}
	if !foundRequest {
		t.Errorf("Agent.Stop must DeleteQueue(%q) for the durable request queue. Got: %v",
			requestQueue, deleted)
	}

	if directSubDeletes < 2 {
		t.Errorf("Agent.Stop must terminate both direct-subscription handles (peer-direct + discovery). "+
			"Got %d direct-mock-* deletes. Full list: %v", directSubDeletes, deleted)
	}
}

func TestAgent_RemoveDeprovisionsRequestQueue(t *testing.T) {
	brk := newMockBroker()
	mock := newMockLLM(&llm.ChatResponse{Content: "ok"})
	store := newMemSessionStore()

	agent := New(AgentConfig{Name: "remove-agent", Namespace: "test-ns"})
	svc := newMockServices(mock, store)
	svc.brk = brk
	agent.svc = svc
	if err := agent.Init(context.Background(), nil, svc); err != nil {
		t.Fatal(err)
	}
	if err := agent.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := agent.Remove(stopCtx); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	deprovisioned := brk.getDeprovisionedQueues()
	want := "test-ns/q/a2a/remove-agent"
	found := false
	for _, name := range deprovisioned {
		if name == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Agent.Remove must DeprovisionQueue(%q) for the durable request queue. Got: %v",
			want, deprovisioned)
	}

	if len(deprovisioned) != 1 {
		t.Errorf("Agent.Remove deprovisioned %d queues, want exactly 1 (request). Got: %v",
			len(deprovisioned), deprovisioned)
	}
}

func TestAgent_RemoveSurfacesDeprovisionError(t *testing.T) {
	brk := newMockBroker()
	brk.deprovisionErr = errors.New("broker unreachable")
	mock := newMockLLM(&llm.ChatResponse{Content: "ok"})
	store := newMemSessionStore()

	agent := New(AgentConfig{Name: "deprov-fail", Namespace: "test-ns"})
	svc := newMockServices(mock, store)
	svc.brk = brk
	agent.svc = svc
	if err := agent.Init(context.Background(), nil, svc); err != nil {
		t.Fatal(err)
	}
	if err := agent.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := agent.Remove(stopCtx)
	if err == nil {
		t.Fatal("Remove returned nil, want error from failed DeprovisionQueue (silent-leak regression)")
	}
	if !strings.Contains(err.Error(), "broker unreachable") {
		t.Errorf("error chain doesn't include the underlying cause: %v", err)
	}
	deprovisioned := brk.getDeprovisionedQueues()
	if len(deprovisioned) != 1 || deprovisioned[0] != "test-ns/q/a2a/deprov-fail" {
		t.Errorf("expected DeprovisionQueue to be attempted once for the request queue; got %v", deprovisioned)
	}
}

func TestAgent_OnlyOneDurableQueue(t *testing.T) {
	brk := newMockBroker()
	mock := newMockLLM(&llm.ChatResponse{Content: "ok"})
	store := newMemSessionStore()

	agent := New(AgentConfig{Name: "one-q-agent", Namespace: "test-ns"})
	svc := newMockServices(mock, store)
	svc.brk = brk
	agent.svc = svc
	if err := agent.Init(context.Background(), nil, svc); err != nil {
		t.Fatal(err)
	}
	if err := agent.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = agent.Stop(context.Background()) })

	brk.mu.Lock()
	defer brk.mu.Unlock()

	var durable []string
	for _, name := range brk.createdQueues {
		if !strings.HasPrefix(name, "direct-mock-") {
			durable = append(durable, name)
		}
	}
	if len(durable) != 1 {
		t.Fatalf("Agent must create exactly 1 durable queue (request). Got %d: %v",
			len(durable), durable)
	}
	if durable[0] != "test-ns/q/a2a/one-q-agent" {
		t.Errorf("durable queue name = %q, want %q", durable[0], "test-ns/q/a2a/one-q-agent")
	}
	if brk.createdDirectSub < 2 {
		t.Errorf("Agent must create >=2 direct subscriptions (peer-direct + discovery). Got %d",
			brk.createdDirectSub)
	}
}

func TestAgentInternalLLMCallMultimodal_WireFormat(t *testing.T) {
	mock := newMockLLM(&llm.ChatResponse{Content: "a red cat barking"})
	agent := New(AgentConfig{Name: "test-agent", Model: "gpt-4o-mini"})
	svc := newMockServices(mock, newMemSessionStore())
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	imgBytes := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	audioBytes := []byte{0x52, 0x49, 0x46, 0x46}

	parts := []tool.MultimodalPart{
		tool.TextPart{Text: "describe these"},
		tool.ImagePart{Data: imgBytes, MIMEType: "image/png"},
		tool.AudioPart{Data: audioBytes, Format: "wav"},
	}

	temp := 0.2
	got, err := agent.internalLLMCallMultimodal(t.Context(), internalCallSinks{}, "system prompt", parts, tool.InternalLLMCallMultimodalOpts{Temperature: &temp})
	if err != nil {
		t.Fatalf("internalLLMCallMultimodal: %v", err)
	}
	if got != "a red cat barking" {
		t.Errorf("response = %q, want 'a red cat barking'", got)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(calls))
	}
	req := calls[0]
	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content != "system prompt" {
		t.Errorf("message[0] = %+v, want system prompt", req.Messages[0])
	}
	user := req.Messages[1]
	if user.Role != "user" {
		t.Errorf("message[1].Role = %q, want user", user.Role)
	}
	if user.Content != "" {
		t.Errorf("message[1].Content = %q, want empty (content must travel via ContentBlocks)", user.Content)
	}
	if len(user.ContentBlocks) != 3 {
		t.Fatalf("expected 3 content blocks (text, image, audio), got %d", len(user.ContentBlocks))
	}

	if user.ContentBlocks[0].Type != "text" || user.ContentBlocks[0].Text != "describe these" {
		t.Errorf("block[0] = %+v, want text/describe these", user.ContentBlocks[0])
	}
	if user.ContentBlocks[1].Type != "image_url" {
		t.Errorf("block[1].Type = %q, want image_url", user.ContentBlocks[1].Type)
	}
	if !strings.HasPrefix(user.ContentBlocks[1].ImageURL, "data:image/png;base64,") {
		t.Errorf("block[1].ImageURL = %q, want data:image/png;base64,... prefix", user.ContentBlocks[1].ImageURL)
	}
	if user.ContentBlocks[2].Type != "input_audio" {
		t.Errorf("block[2].Type = %q, want input_audio", user.ContentBlocks[2].Type)
	}
	if user.ContentBlocks[2].AudioFormat != "wav" {
		t.Errorf("block[2].AudioFormat = %q, want wav", user.ContentBlocks[2].AudioFormat)
	}
	if user.ContentBlocks[2].AudioData == "" {
		t.Error("block[2].AudioData must not be empty")
	}
}

func TestAgentInternalLLMCallMultimodal_RejectsEmptyParts(t *testing.T) {
	agent := New(AgentConfig{Name: "test-agent", Model: "gpt-4o-mini"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	temp := 0.2
	_, err := agent.internalLLMCallMultimodal(t.Context(), internalCallSinks{}, "sys", nil, tool.InternalLLMCallMultimodalOpts{Temperature: &temp})
	if err == nil {
		t.Fatal("expected error when called with no parts")
	}
	if !strings.Contains(err.Error(), "no content parts") {
		t.Errorf("error = %v, want to mention 'no content parts'", err)
	}
}

func TestGetOrCreateSession_NilStoreReturnsError(t *testing.T) {
	a, _, _ := newTestAgent()
	a.svc = newMockServices(newMockLLM(), nil)

	if _, err := a.getOrCreateSession(t.Context(), "user", "ctx-1", ""); err == nil {
		t.Fatal("expected error when session store is nil; got nil (would have nil-panicked)")
	}
}

type blindFirstGetStore struct {
	*memSessionStore
	barrier sync.WaitGroup
}

func (s *blindFirstGetStore) Get(ctx context.Context, appName, userID, sessionID string) (*session.Session, error) {
	sess, err := s.memSessionStore.Get(ctx, appName, userID, sessionID)
	if sess == nil && err == nil {
		s.barrier.Done()
		s.barrier.Wait()
	}
	return sess, err
}

func (s *blindFirstGetStore) GetByContextAndTask(ctx context.Context, appName, userID, contextID, taskID string) (*session.Session, error) {
	sess, err := s.memSessionStore.GetByContextAndTask(ctx, appName, userID, contextID, taskID)
	if sess == nil && err == nil {
		s.barrier.Done()
		s.barrier.Wait()
	}
	return sess, err
}

func TestGetOrCreateSession_ConcurrentFirstMessagesConverge(t *testing.T) {
	const callers = 8
	for name, taskID := range map[string]string{"normal": "", "run_based": "task-1"} {
		t.Run(name, func(t *testing.T) {
			a, _, _ := newTestAgent()
			store := &blindFirstGetStore{memSessionStore: newMemSessionStore()}
			store.barrier.Add(callers)
			a.svc = newMockServices(newMockLLM(), store)

			ids := make([]string, callers)
			errs := make([]error, callers)
			var wg sync.WaitGroup
			for i := range callers {
				wg.Go(func() {
					sess, err := a.getOrCreateSession(t.Context(), "user", "ctx-1", taskID)
					errs[i] = err
					if sess != nil {
						ids[i] = sess.SessionID
					}
				})
			}
			wg.Wait()

			for i := range callers {
				if errs[i] != nil {
					t.Fatalf("caller %d: unexpected error: %v", i, errs[i])
				}
				if ids[i] != ids[0] {
					t.Errorf("caller %d got session %q, want %q", i, ids[i], ids[0])
				}
			}
		})
	}
}

func TestAgentStart_ClearsStoppingLatch(t *testing.T) {
	brk := newMockBroker()
	mock := newMockLLM(&llm.ChatResponse{Content: "ok"})
	store := newMemSessionStore()

	agent := New(AgentConfig{
		Name:      "restart-agent",
		Namespace: "test-ns",
	})
	svc := newMockServices(mock, store)
	svc.brk = brk
	agent.svc = svc
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test helper

	agent.stopping.Store(true)

	if err := agent.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer agent.Stop(context.Background()) //nolint:errcheck // test helper

	if agent.stopping.Load() {
		t.Error("stopping still true after Start — a restarted agent would silently downgrade its next genuine queue loss to Info")
	}
}

func TestAgentStart_ClearsHealth(t *testing.T) {
	brk := newMockBroker()
	mock := newMockLLM(&llm.ChatResponse{Content: "ok"})
	store := newMemSessionStore()

	agent := New(AgentConfig{
		Name:      "restart-agent",
		Namespace: "test-ns",
	})
	svc := newMockServices(mock, store)
	svc.brk = brk
	agent.svc = svc
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test helper

	agent.health.Set("queue:leftover", health.Fatal, "queue no longer exists")

	if err := agent.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer agent.Stop(context.Background()) //nolint:errcheck // test helper

	if problems := agent.health.Snapshot(); problems != nil {
		t.Errorf("health.Snapshot() after Start = %v, want nil — a restarted agent must not carry a permanently orphaned problem from its previous run", problems)
	}
}

type controllableReceiveBroker struct {
	broker.Broker
	mu  sync.Mutex
	err error
}

func (b *controllableReceiveBroker) setErr(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.err = err
}

func (b *controllableReceiveBroker) Receive(ctx context.Context, q broker.Queue) (*broker.Message, error) {
	b.mu.Lock()
	err := b.err
	b.mu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(2 * time.Millisecond):
		return nil, nil
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool, failMsg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(failMsg)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAgentReceiveLoop_ProvisionCooldownDegradesThenRecovers(t *testing.T) {
	brk := &controllableReceiveBroker{Broker: newMockBroker()}
	mock := newMockLLM(&llm.ChatResponse{Content: "ok"})
	store := newMemSessionStore()

	agent := New(AgentConfig{Name: "cooldown-agent", Namespace: "test-ns"})
	svc := newMockServices(mock, store)
	svc.brk = brk
	agent.svc = svc
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test helper

	if err := agent.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer agent.Stop(context.Background()) //nolint:errcheck // test helper

	brk.setErr(broker.ErrProvisionCooldown)
	waitForCondition(t, 2*time.Second, func() bool { return len(agent.health.Snapshot()) > 0 },
		"receiveLoop never reported Degraded health for a provision cooldown — Health option not wired")
	for _, p := range agent.health.Snapshot() {
		if p.Severity == health.Fatal {
			t.Errorf("cooldown reported as Fatal, want Degraded (a retryable condition must never be terminal): %+v", p)
		}
	}

	brk.setErr(nil)
	waitForCondition(t, 2*time.Second, func() bool { return len(agent.health.Snapshot()) == 0 },
		"health never cleared after the cooldown lifted")
}

func TestAgentReceiveLoop_TeardownVsUnplannedQueueLoss(t *testing.T) {
	t.Run("unplanned queue loss is Fatal", func(t *testing.T) {
		brk := &controllableReceiveBroker{Broker: newMockBroker()}
		mock := newMockLLM(&llm.ChatResponse{Content: "ok"})
		store := newMemSessionStore()

		agent := New(AgentConfig{Name: "unplanned-loss-agent", Namespace: "test-ns"})
		svc := newMockServices(mock, store)
		svc.brk = brk
		agent.svc = svc
		agent.Init(context.Background(), nil, svc) //nolint:errcheck // test helper

		if err := agent.Start(t.Context()); err != nil {
			t.Fatalf("Start: %v", err)
		}

		brk.setErr(broker.ErrQueueNotFound)
		waitForCondition(t, 2*time.Second, func() bool {
			for _, p := range agent.health.Snapshot() {
				if p.Severity == health.Fatal {
					return true
				}
			}
			return false
		}, "unplanned ErrQueueNotFound never reported as Fatal — Teardown option not wired, or wired backwards")

		agent.cancelFn()
	})

	t.Run("planned teardown clears health, no fault", func(t *testing.T) {
		brk := &controllableReceiveBroker{Broker: newMockBroker()}
		mock := newMockLLM(&llm.ChatResponse{Content: "ok"})
		store := newMemSessionStore()

		agent := New(AgentConfig{Name: "planned-teardown-agent", Namespace: "test-ns"})
		svc := newMockServices(mock, store)
		svc.brk = brk
		agent.svc = svc
		agent.Init(context.Background(), nil, svc) //nolint:errcheck // test helper

		if err := agent.Start(t.Context()); err != nil {
			t.Fatalf("Start: %v", err)
		}

		healthKey := "queue:" + agent.queue.Name()
		agent.health.Set(healthKey, health.Degraded, health.DetailProvisionCooldown)

		agent.stopping.Store(true)
		brk.setErr(broker.ErrQueueNotFound)

		waitForCondition(t, 2*time.Second, func() bool { return len(agent.health.Snapshot()) == 0 },
			"planned teardown left a stale health entry behind")
		time.Sleep(20 * time.Millisecond)
		if problems := agent.health.Snapshot(); problems != nil {
			t.Errorf("health.Snapshot() after planned teardown = %v, want nil", problems)
		}

		agent.cancelFn()
	})
}

func directSubscriptions(brk *mockBroker) int {
	brk.mu.Lock()
	defer brk.mu.Unlock()
	return brk.createdDirectSub
}

func TestAgentDiscoveryLoop_ProvisionCooldownDegradesThenRecovers(t *testing.T) {
	brk := &controllableReceiveBroker{Broker: newMockBroker()}
	mock := newMockLLM(&llm.ChatResponse{Content: "ok"})
	store := newMemSessionStore()

	agent := New(AgentConfig{Name: "discovery-cooldown-agent", Namespace: "test-ns"})
	svc := newMockServices(mock, store)
	svc.brk = brk
	agent.svc = svc
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test helper

	if err := agent.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer agent.Stop(context.Background()) //nolint:errcheck // test helper

	brk.setErr(broker.ErrProvisionCooldown)
	waitForCondition(t, 2*time.Second, func() bool {
		return discoveryProblemsOnCard(agent) > 0
	}, "discovery loop's Degraded health for a provision cooldown never reached the agent card")

	brk.setErr(nil)
	waitForCondition(t, 2*time.Second, func() bool {
		return len(agent.peerDiscoveryProblems()) == 0 && len(a2a.HealthProblemsFromCard(agent.BuildAgentCard())) == 0
	}, "discovery loop's health entry never cleared after the cooldown lifted")
}

func discoveryProblemsOnCard(agent *Agent) int {
	onCard := make(map[string]bool)
	for _, p := range a2a.HealthProblemsFromCard(agent.BuildAgentCard()) {
		onCard[p.ID] = true
	}
	n := 0
	for _, p := range agent.peerDiscoveryProblems() {
		if onCard[p.ID] {
			n++
		}
	}
	return n
}

func TestAgentDiscoveryLoop_ResubscribesAfterQueueLoss(t *testing.T) {
	mb := newMockBroker()
	brk := &controllableReceiveBroker{Broker: mb}
	mock := newMockLLM(&llm.ChatResponse{Content: "ok"})
	store := newMemSessionStore()

	agent := New(AgentConfig{Name: "discovery-unplanned-loss-agent", Namespace: "test-ns"})
	svc := newMockServices(mock, store)
	svc.brk = brk
	agent.svc = svc
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test helper

	if err := agent.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	defer agent.Stop(context.Background()) //nolint:errcheck // test helper
	subscribed := directSubscriptions(mb)

	brk.setErr(broker.ErrQueueNotFound)
	waitForCondition(t, 2*time.Second, func() bool {
		return discoveryProblemsOnCard(agent) > 0
	}, "the lost discovery subscription never showed on the agent card")

	brk.setErr(nil)
	waitForCondition(t, 5*time.Second, func() bool {
		return directSubscriptions(mb) > subscribed && len(agent.peerDiscoveryProblems()) == 0
	}, "the discovery directory never resubscribed after its queue was lost")
	resubscribed := directSubscriptions(mb)
	time.Sleep(1500 * time.Millisecond)
	if problems := agent.peerDiscoveryProblems(); len(problems) != 0 || directSubscriptions(mb) != resubscribed {
		t.Fatalf("after recovery: problems %+v, %d more subscriptions; want none of either",
			problems, directSubscriptions(mb)-resubscribed)
	}
}
