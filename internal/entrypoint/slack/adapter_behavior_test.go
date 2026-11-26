// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

func newAdapterForBehaviorTest(t *testing.T, cfg *SlackConfig) (*SlackGatewayAdapter, *artifact.ScopedService) {
	t.Helper()
	scoped := &artifact.ScopedService{Inner: artmem.New()}
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	rt := runtime.NewRuntime(brk, nil, runtime.WithArtifacts(scoped))
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{})
	core.SetServices(rt.Services())

	a := &SlackGatewayAdapter{
		core:   core,
		cfg:    cfg,
		logger: slog.Default(),
	}
	return a, scoped
}

func cardWithDisplayName(brokerName, display string) a2a.AgentCard {
	return a2a.AgentCard{
		Name: brokerName,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    "https://solace.com/a2a/extensions/display-name",
				Params: map[string]any{"display_name": display},
			}},
		},
	}
}

func TestResolveAgentName_AtMention(t *testing.T) {
	t.Parallel()
	a, _ := newAdapterForBehaviorTest(t, &SlackConfig{DefaultAgentName: "DefaultAgent"})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "AlphaAgent"})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "BetaAgent"})

	if got := a.resolveTarget(t.Context(), nil, "hey @BetaAgent please help"); got.Name != "BetaAgent" {
		t.Errorf("resolveAgentName at-mention = %q, want BetaAgent", got.Name)
	}
	if got := a.resolveTarget(t.Context(), nil, "hey @betaagent (case-insensitive)"); got.Name != "BetaAgent" {
		t.Errorf("resolveAgentName case-insensitive at-mention = %q, want BetaAgent", got.Name)
	}
}

func TestResolveAgentName_AtMention_DisplayName(t *testing.T) {
	t.Parallel()
	a, _ := newAdapterForBehaviorTest(t, &SlackConfig{})
	a.core.AgentRegistry().AddOrUpdate(cardWithDisplayName(
		"workflow_019e394b_528f_737e_a032_45f04b8c1a2c", "shipment-request-workflow",
	))

	res := a.resolveTarget(t.Context(), nil, "hey @shipment-request-workflow please parse this")
	if res.Name != "workflow_019e394b_528f_737e_a032_45f04b8c1a2c" {
		t.Errorf("resolveAgentName display-name mention = %q, want UUID-derived instance name", res.Name)
	}
}

func TestResolveAgentName_DefaultPresent(t *testing.T) {
	t.Parallel()
	a, _ := newAdapterForBehaviorTest(t, &SlackConfig{DefaultAgentName: "DefaultAgent"})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "DefaultAgent"})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "OtherAgent"})

	if got := a.resolveTarget(t.Context(), nil, "hello"); got.Name != "DefaultAgent" {
		t.Errorf("resolveAgentName default = %q, want DefaultAgent", got.Name)
	}
}

func TestResolveAgentName_DefaultViaDisplayName(t *testing.T) {
	t.Parallel()
	a, _ := newAdapterForBehaviorTest(t, &SlackConfig{DefaultAgentName: "shipment-request-workflow"})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})
	a.core.AgentRegistry().AddOrUpdate(cardWithDisplayName(
		"workflow_019e394b_528f_737e_a032_45f04b8c1a2c", "shipment-request-workflow",
	))

	res := a.resolveTarget(t.Context(), nil, "hello")
	if res.Misconfigured {
		t.Fatalf("expected match via display-name, got misconfigured=true")
	}
	if res.Name != "workflow_019e394b_528f_737e_a032_45f04b8c1a2c" {
		t.Errorf("resolveAgentName via display-name = %q, want UUID-derived instance name", res.Name)
	}
}

func TestResolveAgentName_MisconfiguredDoesNotFallBack(t *testing.T) {
	t.Parallel()
	a, _ := newAdapterForBehaviorTest(t, &SlackConfig{DefaultAgentName: "MissingAgent"})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "DiscoveredAgent"})

	res := a.resolveTarget(t.Context(), nil, "hello")
	if !res.Misconfigured {
		t.Fatalf("expected misconfigured=true, got name=%q", res.Name)
	}
	if res.Name != "" {
		t.Errorf("expected empty name on misconfig, got %q", res.Name)
	}
}

func TestResolveAgentName_NoAgentsReturnsEmpty(t *testing.T) {
	t.Parallel()
	a, _ := newAdapterForBehaviorTest(t, &SlackConfig{DefaultAgentName: ""})
	res := a.resolveTarget(t.Context(), nil, "anybody home?")
	if res.Name != "" || res.Misconfigured {
		t.Errorf("resolveAgentName empty registry = {%q, misconfig=%v}, want empty/false", res.Name, res.Misconfigured)
	}
}

func TestResolveAgentName_EmptyDefaultFallsBackToFirstAlphabetic(t *testing.T) {
	t.Parallel()
	a, _ := newAdapterForBehaviorTest(t, &SlackConfig{})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Zeta"})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Manager"})

	res := a.resolveTarget(t.Context(), nil, "hello")
	if res.Misconfigured {
		t.Fatalf("expected name fallback, got misconfigured=true")
	}
	if res.Name != "Builder" {
		t.Errorf("first alphabetic = %q, want Builder", res.Name)
	}
}

type slackHTTPMock struct {
	mu          sync.Mutex
	postedTexts []string
	server      *httptest.Server
}

func newSlackHTTPMock(t *testing.T) *slackHTTPMock {
	t.Helper()
	m := &slackHTTPMock{}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth.test", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user_id": "U-bot"})
	})
	mux.HandleFunc("/users.profile.get", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "profile": map[string]any{"email": ""}})
	})
	mux.HandleFunc("/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		vals, _ := url.ParseQuery(string(body))
		m.mu.Lock()
		m.postedTexts = append(m.postedTexts, vals.Get("text"))
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "channel": vals.Get("channel"), "ts": "1700000000.000001",
		})
	})
	m.server = httptest.NewServer(mux)
	t.Cleanup(m.server.Close)
	return m
}

func (m *slackHTTPMock) posts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.postedTexts))
	copy(out, m.postedTexts)
	return out
}

func TestSubmitSlackTask_MisconfigPostsChannelError(t *testing.T) {
	t.Parallel()
	mock := newSlackHTTPMock(t)
	a, _ := newAdapterForBehaviorTest(t, &SlackConfig{DefaultAgentName: "does-not-exist"})
	client := goslack.New("xoxb-test", goslack.OptionAPIURL(mock.server.URL+"/"))
	a.api = slackapi.NewSlackAPIAdapter(client, "xoxb-test", slackapi.WithDisabledRateLimiting())
	a.emailCache = NewEmailCache(a.api, 0, slog.Default())
	a.ctx = t.Context()
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})

	a.submitSlackTask(slackInbound{channelID: "C123", threadTS: "1700000000.000001", userID: "U-alice", teamID: "T-team", text: "what is the capital of France?", isDM: true})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(mock.posts()) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	posts := mock.posts()
	if len(posts) != 1 {
		t.Fatalf("expected exactly one chat.postMessage call, got %d (%v)", len(posts), posts)
	}
	if !strings.Contains(posts[0], "does-not-exist") {
		t.Errorf("posted text = %q, want it to mention the misconfigured name", posts[0])
	}
}

func TestExtractFiles(t *testing.T) {
	t.Parallel()

	a := &SlackGatewayAdapter{logger: slog.Default()}

	t.Run("nil request", func(t *testing.T) {
		if files := a.extractFiles(nil); files != nil {
			t.Errorf("expected nil, got %d files", len(files))
		}
	})

	t.Run("empty payload", func(t *testing.T) {
		req := &socketmode.Request{Payload: nil}
		if files := a.extractFiles(req); files != nil {
			t.Errorf("expected nil, got %d files", len(files))
		}
	})

	t.Run("payload without files key", func(t *testing.T) {
		req := &socketmode.Request{Payload: json.RawMessage(`{"event":{"text":"hi"}}`)}
		if files := a.extractFiles(req); len(files) != 0 {
			t.Errorf("expected 0 files, got %d", len(files))
		}
	})

	t.Run("payload with files", func(t *testing.T) {
		payload := `{"event":{"files":[
			{"id":"F1","name":"a.png","mimetype":"image/png","url_private":"u1","url_private_download":"d1"},
			{"id":"F2","name":"b.csv","mimetype":"text/csv","url_private":"u2","url_private_download":"d2"}
		]}}`
		req := &socketmode.Request{Payload: json.RawMessage(payload)}
		files := a.extractFiles(req)
		if len(files) != 2 {
			t.Fatalf("expected 2 files, got %d", len(files))
		}
		if files[0].ID != "F1" || files[0].Name != "a.png" || files[0].MimeType != "image/png" {
			t.Errorf("file[0] = %+v", files[0])
		}
		if files[1].URLPrivateDownload != "d2" {
			t.Errorf("file[1].URLPrivateDownload = %q", files[1].URLPrivateDownload)
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		req := &socketmode.Request{Payload: json.RawMessage(`{not json`)}
		if files := a.extractFiles(req); files != nil {
			t.Errorf("expected nil on malformed JSON, got %d files", len(files))
		}
	})
}

func TestIsMessageSubtypeAllowed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		subType string
		allowed bool
	}{
		{"", true},
		{"file_share", true},
		{"thread_broadcast", true},
		{"message_changed", false},
		{"message_deleted", false},
		{"channel_join", false},
		{"channel_topic", false},
		{"bot_message", false},
		{"reminder_add", false},
		{"random_unknown", false},
	}
	for _, tc := range cases {
		got := isMessageSubtypeAllowed(tc.subType)
		if got != tc.allowed {
			t.Errorf("isMessageSubtypeAllowed(%q) = %v, want %v", tc.subType, got, tc.allowed)
		}
	}
}

func TestLoadAndUploadArtifact_FalseBranches(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T) (a *SlackGatewayAdapter, taskID, filename string)
	}{
		{
			name: "no artifact service",
			setup: func(t *testing.T) (*SlackGatewayAdapter, string, string) {
				brk := broker.NewDevBroker()
				if err := brk.Connect(t.Context()); err != nil {
					t.Fatalf("connect dev broker: %v", err)
				}
				t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
				rt := runtime.NewRuntime(brk, nil)
				core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{})
				core.SetServices(rt.Services())
				return &SlackGatewayAdapter{core: core, cfg: &SlackConfig{}, logger: slog.Default()}, "task-1", "f.png"
			},
		},
		{
			name: "task not tracked",
			setup: func(t *testing.T) (*SlackGatewayAdapter, string, string) {
				a, _ := newAdapterForBehaviorTest(t, &SlackConfig{})
				return a, "unknown-task", "f.png"
			},
		},
		{
			name: "task meta missing",
			setup: func(t *testing.T) (*SlackGatewayAdapter, string, string) {
				a, _ := newAdapterForBehaviorTest(t, &SlackConfig{})
				_ = a.core.Tasks().Register("task-1", "AlphaAgent", "user-1", entrypoint.TaskIdentity{})
				return a, "task-1", "f.png"
			},
		},
		{
			name: "artifact missing returns empty content",
			setup: func(t *testing.T) (*SlackGatewayAdapter, string, string) {
				a, _ := newAdapterForBehaviorTest(t, &SlackConfig{})
				_ = a.core.Tasks().Register("task-1", "AlphaAgent", "user-1", entrypoint.TaskIdentity{})
				a.core.Tasks().SetMetadata("task-1", &SlackTaskMeta{
					ChannelID: "C1", ThreadTS: "t1", SessionID: "sess-1",
				})
				return a, "task-1", "missing.png"
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, taskID, filename := tc.setup(t)
			q := newRecordingQueue()
			if ok := a.loadAndUploadArtifact(t.Context(), taskID, filename, filename, nil, "comment", q); ok {
				t.Fatalf("expected false")
			}
			if got := recordedOpCount(q); got != 0 {
				t.Errorf("expected 0 ops queued, got %d", got)
			}
		})
	}
}

func TestLoadAndUploadArtifact_Success(t *testing.T) {
	t.Parallel()
	a, scoped := newAdapterForBehaviorTest(t, &SlackConfig{})
	_ = a.core.Tasks().Register("task-1", "AlphaAgent", "user-1", entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata("task-1", &SlackTaskMeta{
		ChannelID: "C1", ThreadTS: "t1", SessionID: "sess-1",
	})

	loc := scoped.Resolve("AlphaAgent", "user-1", "sess-1", "report.csv")
	if _, err := scoped.Inner.Save(t.Context(), loc, []byte("a,b\n1,2\n"), "text/csv"); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	q := newRecordingQueue()
	if ok := a.loadAndUploadArtifact(t.Context(), "task-1", "report.csv", "report.csv", nil, "see file", q); !ok {
		t.Fatalf("expected success")
	}
	ops := drainRecordedOps(q)
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	fu, ok := ops[0].(FileUploadOp)
	if !ok {
		t.Fatalf("expected FileUploadOp, got %T", ops[0])
	}
	if fu.Filename != "report.csv" || string(fu.Content) != "a,b\n1,2\n" {
		t.Errorf("FileUploadOp = %+v", fu)
	}
	if fu.MimeType != "text/csv" {
		t.Errorf("MimeType = %q, want text/csv", fu.MimeType)
	}
	if fu.InitialComment != "see file" {
		t.Errorf("InitialComment = %q", fu.InitialComment)
	}
}

func TestHandleFilePart_ArtifactURIFilenameDiffersFromDisplayName(t *testing.T) {
	t.Parallel()
	a, scoped := newAdapterForBehaviorTest(t, &SlackConfig{})
	_ = a.core.Tasks().Register("task-1", "AlphaAgent", "user-1", entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata("task-1", &SlackTaskMeta{
		ChannelID: "C1", ThreadTS: "t1", SessionID: "sess-1",
	})

	loc := scoped.Resolve("AlphaAgent", "user-1", "sess-1", "report_final_v2.csv")
	if _, err := scoped.Inner.Save(t.Context(), loc, []byte("a,b\n1,2\n"), "text/csv"); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	q := newRecordingQueue()
	a.handleFilePart(t.Context(), "task-1", q, a2a.Part{Kind: "file", File: &a2a.FileContent{
		URI:  "artifact://AlphaAgent/user-1/sess-1/report_final_v2.csv",
		Name: "Quarterly Report.csv",
	}})

	ops := drainRecordedOps(q)
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d: %+v", len(ops), ops)
	}
	fu, ok := ops[0].(FileUploadOp)
	if !ok {
		t.Fatalf("artifact dropped from the reply: expected FileUploadOp, got %T (%+v)", ops[0], ops[0])
	}
	if string(fu.Content) != "a,b\n1,2\n" {
		t.Errorf("uploaded content = %q, want the seeded artifact bytes", fu.Content)
	}
	if fu.Filename != "Quarterly Report.csv" {
		t.Errorf("Filename = %q, want the display name", fu.Filename)
	}
	if !strings.Contains(fu.InitialComment, "Quarterly Report.csv") {
		t.Errorf("InitialComment = %q, want the display name", fu.InitialComment)
	}
}

func workingArtifactData(status string) map[string]any {
	return map[string]any{
		"type":     "artifact_creation_progress",
		"filename": "scratch.json",
		"status":   status,
		"tags":     []any{"__working"},
	}
}

func TestHandleArtifactProgress_WorkingInProgressSkipped(t *testing.T) {
	t.Parallel()
	a, _ := newAdapterForBehaviorTest(t, &SlackConfig{})
	_ = a.core.Tasks().Register("task-1", "AlphaAgent", "user-1", entrypoint.TaskIdentity{})
	meta := &SlackTaskMeta{ChannelID: "C1", ThreadTS: "t1", SessionID: "sess-1"}
	a.core.Tasks().SetMetadata("task-1", meta)

	q := newRecordingQueue()
	a.handleArtifactProgress(t.Context(), "task-1", meta, q, workingArtifactData("in-progress"))
	if got := recordedOpCount(q); got != 0 {
		t.Fatalf("expected 0 ops for __working in-progress, got %d", got)
	}
}

func TestHandleArtifactProgress_WorkingTerminalRetiresPlaceholder(t *testing.T) {
	t.Parallel()
	a, _ := newAdapterForBehaviorTest(t, &SlackConfig{})
	_ = a.core.Tasks().Register("task-1", "AlphaAgent", "user-1", entrypoint.TaskIdentity{})
	meta := &SlackTaskMeta{ChannelID: "C1", ThreadTS: "t1", SessionID: "sess-1"}
	a.core.Tasks().SetMetadata("task-1", meta)

	q := newRecordingQueue()
	a.handleArtifactProgress(t.Context(), "task-1", meta, q, map[string]any{
		"type":     "artifact_creation_progress",
		"filename": "scratch.json",
		"status":   "in-progress",
	})
	a.handleArtifactProgress(t.Context(), "task-1", meta, q, workingArtifactData("completed"))

	ops := drainRecordedOps(q)
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops (in-progress placeholder + completed delete), got %d: %+v", len(ops), ops)
	}
	inProg, ok := ops[0].(ArtifactProgressOp)
	if !ok || inProg.Status != "in-progress" {
		t.Fatalf("op[0] = %+v, want in-progress ArtifactProgressOp", ops[0])
	}
	del, ok := ops[1].(ArtifactProgressOp)
	if !ok || del.Status != "completed" {
		t.Fatalf("op[1] = %+v, want completed ArtifactProgressOp", ops[1])
	}
	if del.Text != "" || len(del.Blocks) != 0 {
		t.Errorf("completed delete op must be silent, got Text=%q Blocks=%d", del.Text, len(del.Blocks))
	}
}

func TestHandleArtifactProgress_DeliverableUploads(t *testing.T) {
	t.Parallel()
	a, scoped := newAdapterForBehaviorTest(t, &SlackConfig{})
	_ = a.core.Tasks().Register("task-1", "AlphaAgent", "user-1", entrypoint.TaskIdentity{})
	meta := &SlackTaskMeta{ChannelID: "C1", ThreadTS: "t1", SessionID: "sess-1"}
	a.core.Tasks().SetMetadata("task-1", meta)

	loc := scoped.Resolve("AlphaAgent", "user-1", "sess-1", "report.csv")
	if _, err := scoped.Inner.Save(t.Context(), loc, []byte("a,b\n1,2\n"), "text/csv"); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	q := newRecordingQueue()
	a.handleArtifactProgress(t.Context(), "task-1", meta, q, map[string]any{
		"type":     "artifact_creation_progress",
		"filename": "report.csv",
		"status":   "completed",
	})

	ops := drainRecordedOps(q)
	var uploaded bool
	for _, op := range ops {
		if fu, ok := op.(FileUploadOp); ok && fu.Filename == "report.csv" {
			uploaded = true
		}
	}
	if !uploaded {
		t.Fatalf("expected a FileUploadOp for the deliverable, got ops %+v", ops)
	}
}

func newRecordingQueue() *MessageQueue {
	q := NewMessageQueue("test", "C1", "t1", nil, false, slog.Default())
	q.ops = make(chan Operation, 64)
	return q
}

func recordedOpCount(q *MessageQueue) int { return len(q.ops) }

func drainRecordedOps(q *MessageQueue) []Operation {
	var ops []Operation
	for {
		select {
		case op := <-q.ops:
			ops = append(ops, op)
		default:
			return ops
		}
	}
}

func registerSlackTask(a *SlackGatewayAdapter, taskID string) {
	_ = a.core.Tasks().Register(taskID, "AlphaAgent", "user-1", entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskID, &SlackTaskMeta{
		ChannelID: "C123", ThreadTS: "t1", SessionID: "sess-1", StatusTS: "1700000000.000001",
	})
}

func registerSlackDMTask(a *SlackGatewayAdapter, taskID string) {
	_ = a.core.Tasks().Register(taskID, "AlphaAgent", "user-1", entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskID, &SlackTaskMeta{
		ChannelID: "C123", ThreadTS: "t1", SessionID: "sess-1", StatusTS: "1700000000.000001", IsDM: true,
	})
}

func failedTask(taskID string, state a2a.TaskState) *a2a.Task {
	return &a2a.Task{ID: taskID, Kind: "task", Status: a2a.TaskStatus{State: state}}
}

func TestOnFinalResponse_Completed_DeletesStatus(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	registerSlackTask(a, "task-ok")

	a.OnFinalResponse(t.Context(), "task-ok", failedTask("task-ok", a2a.TaskStateCompleted), nil)

	if got := calls.Deletes(); got != 1 {
		t.Errorf("chat.delete calls = %d, want 1", got)
	}
	if got := calls.Updates(); got != 0 {
		t.Errorf("chat.update calls = %d, want 0 (no terminal text on success)", got)
	}
}

func TestOnFinalResponse_FailedState_PostsErrorNotDelete(t *testing.T) {
	for _, state := range []a2a.TaskState{a2a.TaskStateFailed, a2a.TaskStateRejected} {
		t.Run(string(state), func(t *testing.T) {
			a, calls, _ := newHandlerTestAdapter(t)
			taskID := "task-" + string(state)
			registerSlackTask(a, taskID)

			a.OnFinalResponse(t.Context(), taskID, failedTask(taskID, state), nil)

			if got := calls.Deletes(); got != 0 {
				t.Errorf("chat.delete calls = %d, want 0 (failure must not silently delete)", got)
			}
			if got := calls.Updates(); got != 1 {
				t.Fatalf("chat.update calls = %d, want 1 (error finalize)", got)
			}
			if txt := calls.LastUpdateText(); !strings.Contains(txt, "error occurred") {
				t.Errorf("finalize text = %q, want it to contain an error message", txt)
			}
		})
	}
}

func TestOnFinalResponse_AfterError_DoesNotDelete(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	registerSlackTask(a, "task-race")

	a.OnError(t.Context(), "task-race", &a2a.Error{Message: "boom"}, nil)
	updatesAfterError := calls.Updates()

	a.OnFinalResponse(t.Context(), "task-race", failedTask("task-race", a2a.TaskStateCompleted), nil)

	if got := calls.Deletes(); got != 0 {
		t.Errorf("chat.delete calls = %d, want 0 (must not delete the error message)", got)
	}
	if got := calls.Updates(); got != updatesAfterError {
		t.Errorf("chat.update calls = %d, want %d (no extra finalize after error)", got, updatesAfterError)
	}
}

func TestOnFinalResponse_DM_ClearsSessionProcessing(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	registerSlackDMTask(a, "task-dm")

	a.OnFinalResponse(t.Context(), "task-dm", failedTask("task-dm", a2a.TaskStateCompleted), nil)

	if got := calls.SessionStatuses(); !slices.Equal(got, []string{"active"}) {
		t.Errorf("agents.sessions.setStatus calls = %v, want [active]", got)
	}
}

func TestOnFinalResponse_ChannelMention_NeverSetsSessionStatus(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	registerSlackTask(a, "task-mention")

	a.OnFinalResponse(t.Context(), "task-mention", failedTask("task-mention", a2a.TaskStateCompleted), nil)

	if got := calls.SessionStatuses(); len(got) != 0 {
		t.Errorf("agents.sessions.setStatus calls = %v, want none", got)
	}
}

func TestOnError_DM_ClearsSessionProcessing(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	registerSlackDMTask(a, "task-dm-err")

	a.OnError(t.Context(), "task-dm-err", &a2a.Error{Message: "boom"}, nil)

	if got := calls.SessionStatuses(); !slices.Equal(got, []string{"active"}) {
		t.Errorf("agents.sessions.setStatus calls = %v, want [active]", got)
	}
}

func TestOnFinalResponse_Streaming_ClearsSessionProcessing(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	a.cfg.ShowActivityTimeline = true
	registerSlackDMTask(a, "task-dm-stream")

	a.OnFinalResponse(t.Context(), "task-dm-stream", failedTask("task-dm-stream", a2a.TaskStateCompleted), nil)

	if got := calls.SessionStatuses(); !slices.Equal(got, []string{"active"}) {
		t.Errorf("agents.sessions.setStatus calls = %v, want [active] (streaming branch)", got)
	}
}
