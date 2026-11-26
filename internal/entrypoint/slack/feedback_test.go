// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

func storeBackedFeedbackAdapter(t *testing.T) *SlackGatewayAdapter {
	t.Helper()
	a, _ := storeBackedFeedbackAdapterWithCalls(t)
	return a
}

type apiCallLog struct {
	mu     sync.Mutex
	calls  map[string]int
	bodies map[string][]string
}

func (l *apiCallLog) body(method, b string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.bodies == nil {
		l.bodies = map[string][]string{}
	}
	l.bodies[method] = append(l.bodies[method], b)
}

func (l *apiCallLog) lastBody(method string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	got := l.bodies[method]
	if len(got) == 0 {
		return ""
	}
	return got[len(got)-1]
}

func (l *apiCallLog) allBodies(method string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.bodies[method])
}

func (l *apiCallLog) hit(method string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls[method]++
}

func (l *apiCallLog) count(method string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls[method]
}

type feedbackAdapterOption func(*feedbackAdapterConfig)

type feedbackAdapterConfig struct {
	viewsOpenFails bool
}

func withFailingViewsOpen() feedbackAdapterOption {
	return func(c *feedbackAdapterConfig) { c.viewsOpenFails = true }
}

func storeBackedFeedbackAdapterWithCalls(t *testing.T, opts ...feedbackAdapterOption) (*SlackGatewayAdapter, *apiCallLog) {
	t.Helper()
	cfg := feedbackAdapterConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	log := &apiCallLog{calls: map[string]int{}}
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	mux := http.NewServeMux()
	mux.HandleFunc("/users.info", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"user": map[string]any{"id": "U1", "profile": map[string]any{"email": "alice@example.com"}},
		})
	})
	mux.HandleFunc("/views.open", func(w http.ResponseWriter, _ *http.Request) {
		log.hit("views.open")
		w.Header().Set("Content-Type", "application/json")
		if cfg.viewsOpenFails {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "expired_trigger_id"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "view": map[string]any{"id": "V1"}})
	})
	mux.HandleFunc("/conversations.open", func(w http.ResponseWriter, _ *http.Request) {
		log.hit("conversations.open")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": map[string]any{"id": "D1"}})
	})
	mux.HandleFunc("/chat.update", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		log.body("chat.update", string(body))
		log.hit("chat.update")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ts": "1700000000.000100"})
	})
	mux.HandleFunc("/chat.postEphemeral", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		log.body("chat.postEphemeral", string(body))
		log.hit("chat.postEphemeral")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "message_ts": "1700000000.000200"})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	core := entrypoint.NewGatewayCore(&recordingGWAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": store.TestSQLiteDBURL(t),
		},
	})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core.Init: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	api := slackapi.NewSlackAPIAdapter(goslack.New("xoxb-test", goslack.OptionAPIURL(srv.URL+"/")), "xoxb-test", slackapi.WithDisabledRateLimiting())
	return &SlackGatewayAdapter{
		core:       core,
		cfg:        &SlackConfig{},
		api:        api,
		emailCache: NewEmailCache(api, 0, testLogger()),
		logger:     testLogger(),
		ctx:        t.Context(),
	}, log
}

func seedFeedbackTask(t *testing.T, core *entrypoint.GatewayCore, taskID, agentName string) {
	t.Helper()
	task := &store.Task{
		ID: taskID, UserID: "alice@example.com",
		StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground",
	}
	if agentName != "" {
		handlerType := store.TaskHandlerTypeAgent
		if strings.HasPrefix(agentName, "workflow_") {
			handlerType = store.TaskHandlerTypeWorkflow
		}
		task.AgentName = &agentName
		task.HandlerName = &agentName
		task.HandlerType = &handlerType
	}
	if err := store.NewTaskRepo(core.Store()).Create(t.Context(), task); err != nil {
		t.Fatalf("seed task: %v", err)
	}
}

func TestRecordFeedback_PersistsAndFires(t *testing.T) {
	a := storeBackedFeedbackAdapter(t)

	var fired struct {
		task, rating, comment, user string
		ok                          bool
	}
	a.core.OnFeedback(func(taskID, rating, comment, userID string) {
		fired.task, fired.rating, fired.comment, fired.user, fired.ok = taskID, rating, comment, userID, true
	})

	taskID := "11111111-1111-1111-1111-111111111111"
	sessionID := "22222222-2222-2222-2222-222222222222"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")
	payload := FeedbackPayload{TaskID: taskID, SessionID: sessionID, Rating: "down"}
	var cb goslack.InteractionCallback
	cb.User.ID = "U1"
	cb.User.TeamID = "T1"

	if _, err := a.recordFeedback(cb, payload, "not helpful"); err != nil {
		t.Fatalf("recordFeedback: %v", err)
	}

	fbs, err := store.NewFeedbackRepo(a.core.Store()).Search(t.Context(), "*", 0, 0, taskID, "", "", 10, 0)
	if err != nil {
		t.Fatalf("search feedback: %v", err)
	}
	if len(fbs) != 1 {
		t.Fatalf("expected 1 persisted feedback row, got %d", len(fbs))
	}
	row := fbs[0]
	if row.Rating != "down" {
		t.Errorf("rating = %q, want down", row.Rating)
	}
	if row.Comment == nil || *row.Comment != "not helpful" {
		t.Errorf("comment = %v, want 'not helpful'", row.Comment)
	}
	if row.UserID == "" {
		t.Errorf("userID should be resolved (email or slack:<team>:<user>), got empty")
	}
	if row.ShareContent {
		t.Error("share_content must stay false: the consent checkbox is retired")
	}
	if row.EntrypointType != store.EntrypointTypeSlack {
		t.Errorf("entrypoint_type = %q, want %q", row.EntrypointType, store.EntrypointTypeSlack)
	}

	if !fired.ok {
		t.Fatal("feedback hook did not fire")
	}
	if fired.task != taskID || fired.rating != "down" || fired.comment != "not helpful" {
		t.Errorf("hook fired with wrong data: %+v", fired)
	}
	if fired.user != row.UserID {
		t.Errorf("hook userID %q != persisted userID %q", fired.user, row.UserID)
	}
}

func TestRecordFeedback_MissingIDsNotRecorded(t *testing.T) {
	a := storeBackedFeedbackAdapter(t)
	fired := false
	a.core.OnFeedback(func(_, _, _, _ string) { fired = true })

	taskID := "33333333-3333-3333-3333-333333333333"
	if _, err := a.recordFeedback(goslack.InteractionCallback{}, FeedbackPayload{TaskID: taskID, SessionID: "", Rating: "down"}, "x"); !errors.Is(err, errFeedbackNotRecorded) {
		t.Errorf("missing session id should report the drop, got %v", err)
	}

	fbs, err := store.NewFeedbackRepo(a.core.Store()).Search(t.Context(), "*", 0, 0, taskID, "", "", 10, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(fbs) != 0 {
		t.Errorf("missing session id should persist nothing, got %d rows", len(fbs))
	}
	if fired {
		t.Error("missing ids → feedback hook must not fire")
	}
}

func TestRecordFeedback_NoStoreIsNoop(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core := entrypoint.NewGatewayCore(&recordingGWAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "gw"})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}

	fired := false
	core.OnFeedback(func(_, _, _, _ string) { fired = true })
	a := &SlackGatewayAdapter{core: core, logger: testLogger(), ctx: t.Context()}

	if _, err := a.recordFeedback(goslack.InteractionCallback{}, FeedbackPayload{TaskID: "t", SessionID: "s", Rating: "down"}, "x"); err != nil {
		t.Errorf("a gateway without a store must no-op, got %v", err)
	}
	if fired {
		t.Error("no store → feedback hook must not fire")
	}
}

func TestRecordFeedback_DropPaths(t *testing.T) {
	a := storeBackedFeedbackAdapter(t)
	a.emailCache = nil
	fired := false
	a.core.OnFeedback(func(_, _, _, _ string) { fired = true })

	attributed := "33333333-3333-3333-3333-333333333333"
	seedFeedbackTask(t, a.core, attributed, "OrchestratorAgent")
	bare := "44444444-4444-4444-4444-444444444444"
	seedFeedbackTask(t, a.core, bare, "")

	sess := "22222222-2222-2222-2222-222222222222"
	cases := []struct {
		desc    string
		payload FeedbackPayload
	}{
		{"unresolved user", FeedbackPayload{TaskID: attributed, SessionID: sess, Rating: "down"}},
		{"invalid rating", FeedbackPayload{TaskID: attributed, SessionID: sess, Rating: "meh", UserID: "alice@example.com"}},
		{"missing task", FeedbackPayload{TaskID: "99999999-9999-9999-9999-999999999999", SessionID: sess, Rating: "up", UserID: "alice@example.com"}},
		{"task without entry-point", FeedbackPayload{TaskID: bare, SessionID: sess, Rating: "up", UserID: "alice@example.com"}},
	}
	for _, tc := range cases {
		if _, err := a.recordFeedback(goslack.InteractionCallback{}, tc.payload, ""); !errors.Is(err, errFeedbackNotRecorded) {
			t.Errorf("%s: want the drop reported, got %v", tc.desc, err)
		}
		fbs, err := store.NewFeedbackRepo(a.core.Store()).Search(t.Context(), "*", 0, 0, tc.payload.TaskID, "", "", 10, 0)
		if err != nil {
			t.Fatalf("%s: search: %v", tc.desc, err)
		}
		if len(fbs) != 0 {
			t.Errorf("%s: persisted %d rows, want 0", tc.desc, len(fbs))
		}
	}
	if fired {
		t.Error("dropped feedback must not fire the hook")
	}
}

func TestRecordFeedback_SubjectStamped(t *testing.T) {
	a := storeBackedFeedbackAdapter(t)

	wfTask := "55555555-5555-5555-5555-555555555555"
	seedFeedbackTask(t, a.core, wfTask, "workflow_019e47f4_1234_7abc_8def_0123456789ab")
	var cb goslack.InteractionCallback
	cb.User.ID = "U1"
	cb.User.TeamID = "T1"
	if _, err := a.recordFeedback(cb, FeedbackPayload{TaskID: wfTask, SessionID: "22222222-2222-2222-2222-222222222222", Rating: "up"}, ""); err != nil {
		t.Fatalf("recordFeedback: %v", err)
	}

	fbs, err := store.NewFeedbackRepo(a.core.Store()).Search(t.Context(), "*", 0, 0, wfTask, "", "", 10, 0)
	if err != nil || len(fbs) != 1 {
		t.Fatalf("search: %v n=%d", err, len(fbs))
	}
	if fbs[0].SubjectID != "019e47f4-1234-7abc-8def-0123456789ab" || fbs[0].SubjectType != store.SubjectTypeWorkflow {
		t.Errorf("subject = (%q, %q), want dashed uuid + workflow", fbs[0].SubjectID, fbs[0].SubjectType)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestHandleNativeFeedbackSubmit_CommentNeverLogged(t *testing.T) {
	a := storeBackedFeedbackAdapter(t)
	logs := &lockedBuffer{}
	a.logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	taskID := "11111111-1111-1111-1111-111111111111"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")
	const comment = "SECRET-FEEDBACK-COMMENT the agent exposed my account number"
	metadata, err := json.Marshal(nativeFeedbackModalMetadata{
		Payload:   FeedbackPayload{TaskID: taskID, SessionID: "22222222-2222-2222-2222-222222222222", Rating: "down"},
		ChannelID: "C1",
		MessageTS: "1700000000.000100",
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	var cb goslack.InteractionCallback
	cb.User.ID = "U1"
	cb.User.TeamID = "T1"
	cb.View.CallbackID = NativeFeedbackModalID
	cb.View.PrivateMetadata = string(metadata)
	cb.View.State = &goslack.ViewState{Values: map[string]map[string]goslack.BlockAction{
		FeedbackCommentBlockID: {FeedbackCommentInputID: {Value: comment}},
	}}

	a.handleNativeFeedbackSubmit(cb, func() {})
	a.wg.Wait()

	out := logs.String()
	if !strings.Contains(out, "native feedback submitted") {
		t.Fatalf("submit milestone log missing:\n%s", out)
	}
	if strings.Contains(out, "SECRET-FEEDBACK-COMMENT") {
		t.Fatalf("comment text leaked into logs:\n%s", out)
	}

	fbs, err := store.NewFeedbackRepo(a.core.Store()).Search(t.Context(), "*", 0, 0, taskID, "", "", 10, 0)
	if err != nil || len(fbs) != 1 {
		t.Fatalf("search: %v n=%d", err, len(fbs))
	}
	if fbs[0].Comment == nil || *fbs[0].Comment != comment {
		t.Errorf("comment must persist unchanged, got %v", fbs[0].Comment)
	}
}

func clickCallback(t *testing.T, taskID, rating string) (goslack.InteractionCallback, *goslack.BlockAction) {
	t.Helper()
	value, err := json.Marshal(FeedbackPayload{
		TaskID:    taskID,
		SessionID: "22222222-2222-2222-2222-222222222222",
		Rating:    rating,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var cb goslack.InteractionCallback
	cb.User.ID = "U1"
	cb.User.TeamID = "T1"
	cb.TriggerID = "trigger-1"
	cb.Container.ChannelID = "C1"
	cb.Container.MessageTs = "1700000000.000100"
	return cb, &goslack.BlockAction{ActionID: NativeFeedbackActionID, Value: string(value)}
}

func onlyFeedbackRow(t *testing.T, a *SlackGatewayAdapter, taskID string) *store.Feedback {
	t.Helper()
	fbs, err := store.NewFeedbackRepo(a.core.Store()).Search(t.Context(), "*", 0, 0, taskID, "", "", 10, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(fbs) != 1 {
		t.Fatalf("expected 1 feedback row, got %d", len(fbs))
	}
	return fbs[0]
}

func TestHandleNativeFeedbackClick_NoNoticeOnSuccess(t *testing.T) {
	a, calls := storeBackedFeedbackAdapterWithCalls(t)
	taskID := "11111111-1111-1111-1111-111111111111"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")

	cb, action := clickCallback(t, taskID, feedbackRatingUp)
	a.handleNativeFeedbackClick(cb, action, func() {})
	a.wg.Wait()

	if got := calls.count("chat.postEphemeral"); got != 0 {
		t.Errorf("chat.postEphemeral called %d times, want 0", got)
	}
}

func submitCallback(t *testing.T, taskID, comment string) goslack.InteractionCallback {
	t.Helper()
	return submitCallbackRated(t, taskID, comment, feedbackRatingDown)
}

func submitCallbackRated(t *testing.T, taskID, comment, rating string) goslack.InteractionCallback {
	t.Helper()
	metadata, err := json.Marshal(nativeFeedbackModalMetadata{
		Payload:   FeedbackPayload{TaskID: taskID, SessionID: "22222222-2222-2222-2222-222222222222", Rating: rating},
		ChannelID: "C1",
		MessageTS: "1700000000.000100",
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	var cb goslack.InteractionCallback
	cb.User.ID = "U1"
	cb.User.TeamID = "T1"
	cb.View.CallbackID = NativeFeedbackModalID
	cb.View.PrivateMetadata = string(metadata)
	cb.View.State = &goslack.ViewState{Values: map[string]map[string]goslack.BlockAction{
		FeedbackCommentBlockID: {FeedbackCommentInputID: {Value: comment}},
	}}
	return cb
}

func TestHandleNativeFeedbackSubmit_NotifiesUserWhenCaptureFails(t *testing.T) {
	a, calls := storeBackedFeedbackAdapterWithCalls(t)
	a.handleNativeFeedbackSubmit(submitCallback(t, "99999999-9999-9999-9999-999999999999", "lost"), func() {})
	a.wg.Wait()

	if got := calls.count("chat.postEphemeral"); got != 1 {
		t.Errorf("chat.postEphemeral called %d times, want 1", got)
	}
}

func TestHandleNativeFeedbackClick_NotifiesOnUnparseablePayload(t *testing.T) {
	a, calls := storeBackedFeedbackAdapterWithCalls(t)

	var cb goslack.InteractionCallback
	cb.User.ID = "U1"
	cb.Container.ChannelID = "C1"
	acked := 0
	a.handleNativeFeedbackClick(cb, &goslack.BlockAction{
		ActionID: NativeFeedbackActionID,
		Value:    "{not json",
	}, func() { acked++ })
	a.wg.Wait()

	if got := calls.count("chat.postEphemeral"); got != 1 {
		t.Errorf("chat.postEphemeral called %d times, want 1", got)
	}
	if acked != 1 {
		t.Errorf("acked %d times, want 1", acked)
	}
}

func TestHandleNativeFeedbackSubmit_NotifiesOnUnparseableMetadata(t *testing.T) {
	a, calls := storeBackedFeedbackAdapterWithCalls(t)

	var cb goslack.InteractionCallback
	cb.User.ID = "U1"
	cb.View.CallbackID = NativeFeedbackModalID
	cb.View.PrivateMetadata = "{not json"
	a.handleNativeFeedbackSubmit(cb, func() {})
	a.wg.Wait()

	if got := calls.count("chat.postEphemeral"); got != 1 {
		t.Errorf("chat.postEphemeral called %d times, want 1", got)
	}
}

func TestHandleNativeFeedbackSubmit_AcknowledgesOnMessage(t *testing.T) {
	a, calls := storeBackedFeedbackAdapterWithCalls(t)
	taskID := "11111111-1111-1111-1111-111111111111"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")

	a.handleNativeFeedbackSubmit(submitCallback(t, taskID, "some detail"), func() {})
	a.wg.Wait()

	if got := calls.count("chat.update"); got != 1 {
		t.Errorf("chat.update called %d times, want 1", got)
	}
}

func TestFeedbackAcknowledgedBlocks_RemovesButtons(t *testing.T) {
	blocks := FeedbackAcknowledgedBlocks(feedbackRatingUp)

	if len(blocks) != 1 {
		t.Fatalf("expected a single notice block, got %d", len(blocks))
	}
	if _, ok := blocks[0].(*goslack.ContextBlock); !ok {
		t.Errorf("notice is %T, want a context block", blocks[0])
	}
	for _, b := range blocks {
		if _, ok := b.(*goslack.ContextActionsBlock); ok {
			t.Error("feedback buttons survived the acknowledgement")
		}
	}
}

func TestFeedbackAckText_CarriesTheRating(t *testing.T) {
	up := feedbackAckText(feedbackRatingUp)
	down := feedbackAckText(feedbackRatingDown)

	if up == down {
		t.Fatalf("a positive and a negative rating must not read the same: %q", up)
	}
	for _, tc := range []struct{ name, got string }{{"up", up}, {"down", down}} {
		if !strings.Contains(tc.got, entrypoint.FeedbackAckNotice) {
			t.Errorf("%s notice dropped the shared wording: %q", tc.name, tc.got)
		}
	}

	if got := feedbackAckText("sideways"); got != entrypoint.FeedbackAckNotice {
		t.Errorf("unknown rating = %q, want the bare notice", got)
	}
}

func TestHandleNativeFeedbackSubmit_NoAcknowledgementWhenCaptureFails(t *testing.T) {
	a, calls := storeBackedFeedbackAdapterWithCalls(t)
	a.handleNativeFeedbackSubmit(submitCallback(t, "99999999-9999-9999-9999-999999999999", "lost"), func() {})
	a.wg.Wait()

	if got := calls.count("chat.update"); got != 0 {
		t.Errorf("chat.update called %d times, want 0 on a failed capture", got)
	}
	if got := calls.count("chat.postEphemeral"); got != 1 {
		t.Errorf("chat.postEphemeral called %d times, want 1", got)
	}
}

func TestAcknowledgement_SendsNoticeWithoutButtons(t *testing.T) {
	a, calls := storeBackedFeedbackAdapterWithCalls(t)
	taskID := "11111111-1111-1111-1111-111111111111"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")

	a.handleNativeFeedbackSubmit(submitCallback(t, taskID, "detail"), func() {})
	a.wg.Wait()

	body, err := url.QueryUnescape(calls.lastBody("chat.update"))
	if err != nil {
		t.Fatalf("decode update body: %v", err)
	}
	if !strings.Contains(body, entrypoint.FeedbackAckNotice) {
		t.Errorf("update body missing the notice: %s", body)
	}
	if strings.Contains(body, "feedback_buttons") {
		t.Errorf("update body still carries the feedback buttons: %s", body)
	}
	if !strings.Contains(body, feedbackAckText(feedbackRatingDown)) {
		t.Errorf("update body dropped the rating thumb: %s", body)
	}
	if !strings.Contains(body, "C1") {
		t.Errorf("update body lost the channel from the modal metadata: %s", body)
	}
	if !strings.Contains(body, "1700000000.000100") {
		t.Errorf("update body lost the message timestamp from the modal metadata: %s", body)
	}
}

func TestHandleInteractive_RoutesViewSubmissionToFeedback(t *testing.T) {
	a, _ := storeBackedFeedbackAdapterWithCalls(t)
	taskID := "11111111-1111-1111-1111-111111111111"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")

	client, srv := newTestSocketClient(t)
	defer srv.Close()

	const comment = "routed through the envelope"
	cb := submitCallback(t, taskID, comment)
	cb.Type = goslack.InteractionTypeViewSubmission
	a.handleInteractive(client, socketmode.Event{
		Type:    socketmode.EventTypeInteractive,
		Data:    cb,
		Request: &socketmode.Request{EnvelopeID: "env-submit"},
	})
	a.wg.Wait()

	got := onlyFeedbackRow(t, a, taskID)
	if got.Comment == nil || *got.Comment != comment {
		t.Errorf("comment = %v, want %q: view_submission did not route", got.Comment, comment)
	}
}

func TestHandleInteractive_ViewClosedDoesNothing(t *testing.T) {
	a, calls := storeBackedFeedbackAdapterWithCalls(t)
	taskID := "11111111-1111-1111-1111-111111111111"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")

	client, srv := newTestSocketClient(t)
	defer srv.Close()

	cb := submitCallback(t, taskID, "")
	cb.Type = goslack.InteractionTypeViewClosed
	a.handleInteractive(client, socketmode.Event{
		Type:    socketmode.EventTypeInteractive,
		Data:    cb,
		Request: &socketmode.Request{EnvelopeID: "env-closed"},
	})
	a.wg.Wait()

	if got := calls.count("chat.update"); got != 0 {
		t.Errorf("chat.update called %d times, want 0", got)
	}
	fbs, err := store.NewFeedbackRepo(a.core.Store()).Search(t.Context(), "*", 0, 0, taskID, "", "", 10, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(fbs) != 0 {
		t.Errorf("a dismissal persisted %d rows, want 0", len(fbs))
	}
}

func subscribeFeedback(t *testing.T, core *entrypoint.GatewayCore, name string) (broker.Broker, broker.Queue) {
	t.Helper()
	brk := core.Services().Broker()
	q, err := brk.CreateQueue(name, broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.FeedbackTopic(core.Config().Namespace)); err != nil {
		t.Fatal(err)
	}
	return brk, q
}

func receiveFeedbackEvent(t *testing.T, brk broker.Broker, q broker.Queue) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("expected broker message, got error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("unmarshal broker payload: %v", err)
	}
	fb, ok := payload["feedback"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'feedback' key in payload, got: %v", payload)
	}
	return fb
}

func assertNoFeedbackEvent(t *testing.T, brk broker.Broker, q broker.Queue) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if msg, err := brk.Receive(ctx, q); err == nil && msg != nil {
		t.Fatalf("expected no broker message, got one: %s", msg.Payload)
	}
}

func TestRecordFeedback_PublishesToBrokerWhenEnabled(t *testing.T) {
	a := storeBackedFeedbackAdapter(t)
	a.core.Config().FeedbackPublishingRaw = map[string]any{"enabled": true}
	brk, q := subscribeFeedback(t, a.core, "test-slack-feedback-pub")

	taskID := "11111111-1111-1111-1111-111111111111"
	sessionID := "22222222-2222-2222-2222-222222222222"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")
	payload := FeedbackPayload{TaskID: taskID, SessionID: sessionID, Rating: "down"}
	var cb goslack.InteractionCallback
	cb.User.ID = "U1"
	cb.User.TeamID = "T1"

	if _, err := a.recordFeedback(cb, payload, "not helpful"); err != nil {
		t.Fatalf("recordFeedback: %v", err)
	}

	fb := receiveFeedbackEvent(t, brk, q)
	checks := map[string]any{
		"event":           "created",
		"task_id":         taskID,
		"session_id":      sessionID,
		"feedback_type":   "down",
		"feedback_text":   "not helpful",
		"entrypoint_type": store.EntrypointTypeSlack,
	}
	for key, want := range checks {
		if got := fb[key]; got != want {
			t.Errorf("broker payload %s: expected %v, got %v", key, want, got)
		}
	}
}

func TestRecordFeedback_PublishingDisabledByDefault(t *testing.T) {
	a := storeBackedFeedbackAdapter(t)
	brk, q := subscribeFeedback(t, a.core, "test-slack-feedback-off")

	taskID := "33333333-3333-3333-3333-333333333333"
	sessionID := "44444444-4444-4444-4444-444444444444"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")
	var cb goslack.InteractionCallback
	cb.User.ID = "U1"
	cb.User.TeamID = "T1"

	if _, err := a.recordFeedback(cb, FeedbackPayload{TaskID: taskID, SessionID: sessionID, Rating: "up"},
		""); err != nil {
		t.Fatalf("recordFeedback: %v", err)
	}

	assertNoFeedbackEvent(t, brk, q)
}

func TestB10_SlackFeedbackRatingThenDetailsOnce(t *testing.T) {
	a := storeBackedFeedbackAdapter(t)
	a.core.Config().FeedbackPublishingRaw = map[string]any{"enabled": true}
	brk, q := subscribeFeedback(t, a.core, "test-slack-feedback-b10")

	taskID := "55555555-5555-5555-5555-555555555555"
	sessionID := "66666666-6666-6666-6666-666666666666"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")
	var cb goslack.InteractionCallback
	cb.User.ID = "U1"
	cb.User.TeamID = "T1"
	up := FeedbackPayload{TaskID: taskID, SessionID: sessionID, Rating: "up"}
	down := FeedbackPayload{TaskID: taskID, SessionID: sessionID, Rating: "down"}

	if _, err := a.recordFeedback(cb, up, ""); err != nil {
		t.Fatalf("first: %v", err)
	}
	first := receiveFeedbackEvent(t, brk, q)
	if first["event"] != "created" {
		t.Errorf("first event = %v, want created", first["event"])
	}

	t.Run("B10_identical_first_redelivery_idempotent", func(t *testing.T) {
		if _, err := a.recordFeedback(cb, up, ""); err != nil {
			t.Fatalf("redelivered first: %v", err)
		}
		assertNoFeedbackEvent(t, brk, q)
	})

	t.Run("B10_details_added_once", func(t *testing.T) {
		if _, err := a.recordFeedback(cb, up, "great answer"); err != nil {
			t.Fatalf("details: %v", err)
		}
		details := receiveFeedbackEvent(t, brk, q)
		if details["event"] != "updated" || details["feedback_text"] != "great answer" {
			t.Errorf("details event = %v text = %v, want updated / great answer", details["event"], details["feedback_text"])
		}
		if details["created_at"] != first["created_at"] {
			t.Errorf("details created_at = %v, want the first write's %v", details["created_at"], first["created_at"])
		}
	})

	t.Run("B10_identical_details_redelivery_idempotent", func(t *testing.T) {
		if _, err := a.recordFeedback(cb, up, "great answer"); err != nil {
			t.Fatalf("redelivered details: %v", err)
		}
		if _, err := a.recordFeedback(cb, up, ""); err != nil {
			t.Fatalf("late first: %v", err)
		}
		assertNoFeedbackEvent(t, brk, q)
	})

	for _, tc := range []struct {
		name    string
		payload FeedbackPayload
		comment string
	}{
		{"B10_changed_comment_conflicts", up, "actually fine"},
		{"B10_changed_rating_conflicts", down, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := a.recordFeedback(cb, tc.payload, tc.comment); !errors.Is(err, store.ErrFeedbackConflict) {
				t.Fatalf("err = %v, want ErrFeedbackConflict", err)
			}
			assertNoFeedbackEvent(t, brk, q)
		})
	}

	got := onlyFeedbackRow(t, a, taskID)
	if got.Rating != "up" || got.Comment == nil || *got.Comment != "great answer" {
		t.Fatalf("stored feedback = (%s, %v), want the first valid submission", got.Rating, got.Comment)
	}
}

func receiveFullFeedbackPayload(t *testing.T, brk broker.Broker, q broker.Queue) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("expected broker message, got error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("unmarshal broker payload: %v", err)
	}
	return payload
}

func TestRecordFeedback_IncludeTaskInfoSummary_OmitsTaskInfoWithoutConsent(t *testing.T) {
	a := storeBackedFeedbackAdapter(t)
	a.core.Config().FeedbackPublishingRaw = map[string]any{
		"enabled": true, "include_task_info": "summary",
	}
	brk, q := subscribeFeedback(t, a.core, "test-slack-feedback-summary")

	taskID := "12121212-1212-1212-1212-121212121212"
	sessionID := "13131313-1313-1313-1313-131313131313"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")
	var cb goslack.InteractionCallback
	cb.User.ID = "U1"
	cb.User.TeamID = "T1"

	if _, err := a.recordFeedback(cb, FeedbackPayload{TaskID: taskID, SessionID: sessionID, Rating: "up"},
		""); err != nil {
		t.Fatalf("recordFeedback: %v", err)
	}

	payload := receiveFullFeedbackPayload(t, brk, q)
	if _, ok := payload["feedback"]; !ok {
		t.Fatalf("payload missing the feedback object, so the absence checks below prove nothing: %v", payload)
	}
	for _, key := range []string{"task_summary", "task_stim_data"} {
		if _, ok := payload[key]; ok {
			t.Errorf("%s present on Slack feedback with no consent recorded: %v", key, payload[key])
		}
	}
}

func TestHandleNativeFeedbackClick_PersistsNothing(t *testing.T) {
	for _, rating := range []string{feedbackRatingUp, feedbackRatingDown} {
		t.Run(rating, func(t *testing.T) {
			a, calls := storeBackedFeedbackAdapterWithCalls(t)
			taskID := "11111111-1111-1111-1111-111111111111"
			seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")

			acked := 0
			cb, action := clickCallback(t, taskID, rating)
			a.handleNativeFeedbackClick(cb, action, func() { acked++ })
			a.wg.Wait()

			fbs, err := store.NewFeedbackRepo(a.core.Store()).Search(t.Context(), "*", 0, 0, taskID, "", "", 10, 0)
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			if len(fbs) != 0 {
				t.Errorf("click persisted %d rows, want 0 before Submit", len(fbs))
			}
			if got := calls.count("views.open"); got != 1 {
				t.Errorf("views.open called %d times, want 1", got)
			}
			if acked != 1 {
				t.Errorf("acked %d times, want 1: nothing is written, so Slack has nothing to redeliver for", acked)
			}
			if got := calls.count("chat.update"); got != 0 {
				t.Errorf("chat.update called %d times, want 0: there is no capture to acknowledge", got)
			}
		})
	}
}

func TestHandleNativeFeedbackClick_ModalFailureNotifiesAndStoresNothing(t *testing.T) {
	a, calls := storeBackedFeedbackAdapterWithCalls(t, withFailingViewsOpen())
	taskID := "11111111-1111-1111-1111-111111111111"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")

	acked := 0
	cb, action := clickCallback(t, taskID, feedbackRatingDown)
	a.handleNativeFeedbackClick(cb, action, func() { acked++ })
	a.wg.Wait()

	fbs, err := store.NewFeedbackRepo(a.core.Store()).Search(t.Context(), "*", 0, 0, taskID, "", "", 10, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(fbs) != 0 {
		t.Errorf("a failed modal persisted %d rows, want 0", len(fbs))
	}
	if got := calls.count("chat.postEphemeral"); got != 1 {
		t.Errorf("chat.postEphemeral called %d times, want 1", got)
	}
	if acked != 1 {
		t.Errorf("acked %d times, want 1", acked)
	}
	if got := calls.count("chat.update"); got != 0 {
		t.Errorf("chat.update called %d times, want 0: the buttons must stay live for a retry", got)
	}
}

func TestHandleNativeFeedbackClick_NotAckedWhenStopping(t *testing.T) {
	a, calls := storeBackedFeedbackAdapterWithCalls(t, withFailingViewsOpen())
	taskID := "11111111-1111-1111-1111-111111111111"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")
	ctx, cancel := context.WithCancel(t.Context())
	a.ctx = ctx
	cancel()

	acked := 0
	cb, action := clickCallback(t, taskID, feedbackRatingDown)
	a.handleNativeFeedbackClick(cb, action, func() { acked++ })
	a.wg.Wait()

	if acked != 0 {
		t.Errorf("acked %d times while stopping, want 0 so Slack redelivers to another GWE", acked)
	}
	if got := calls.count("chat.postEphemeral"); got != 0 {
		t.Errorf("chat.postEphemeral called %d times while stopping, want 0: the live GWE owns the notice", got)
	}
}

func TestNativeFeedback_MisclickCorrectedBeforeSubmitting(t *testing.T) {
	a, calls := storeBackedFeedbackAdapterWithCalls(t)
	taskID := "11111111-1111-1111-1111-111111111111"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")

	cbUp, actionUp := clickCallback(t, taskID, feedbackRatingUp)
	a.handleNativeFeedbackClick(cbUp, actionUp, func() {})
	a.wg.Wait()
	cbDown, actionDown := clickCallback(t, taskID, feedbackRatingDown)
	a.handleNativeFeedbackClick(cbDown, actionDown, func() {})
	a.wg.Wait()

	a.handleNativeFeedbackSubmit(submitCallbackRated(t, taskID, "wrong document", feedbackRatingDown), func() {})
	a.wg.Wait()

	got := onlyFeedbackRow(t, a, taskID)
	if got.Rating != feedbackRatingDown {
		t.Errorf("rating = %q, want down: a correction before Submit must still land", got.Rating)
	}
	if n := calls.count("chat.postEphemeral"); n != 0 {
		t.Errorf("chat.postEphemeral called %d times, want 0: correcting before Submit is not a conflict", n)
	}
}

func TestHandleInteractive_RoutesClickToFeedback(t *testing.T) {
	a, calls := storeBackedFeedbackAdapterWithCalls(t)
	taskID := "11111111-1111-1111-1111-111111111111"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")

	client, srv := newTestSocketClient(t)
	defer srv.Close()

	cb, action := clickCallback(t, taskID, feedbackRatingDown)
	cb.Type = goslack.InteractionTypeBlockActions
	cb.ActionCallback.BlockActions = []*goslack.BlockAction{action}
	a.handleInteractive(client, socketmode.Event{
		Type:    socketmode.EventTypeInteractive,
		Data:    cb,
		Request: &socketmode.Request{EnvelopeID: "env-click"},
	})
	a.wg.Wait()

	if got := calls.count("views.open"); got != 1 {
		t.Errorf("views.open called %d times, want 1: the click did not route", got)
	}
}

func TestNativeFeedbackSubmit_AckedAfterTheWrite(t *testing.T) {
	a, _ := storeBackedFeedbackAdapterWithCalls(t)
	taskID := "11111111-1111-1111-1111-111111111111"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")
	repo := store.NewFeedbackRepo(a.core.Store())

	var atSubmitAck []*store.Feedback
	a.handleNativeFeedbackSubmit(submitCallback(t, taskID, "kept"), func() {
		atSubmitAck, _ = repo.Search(t.Context(), "*", 0, 0, taskID, "", "", 10, 0)
	})
	a.wg.Wait()

	if len(atSubmitAck) != 1 || atSubmitAck[0].Comment == nil || *atSubmitAck[0].Comment != "kept" {
		t.Fatalf("feedback when the submit was acknowledged = %v, want the comment", atSubmitAck)
	}
}

func TestNativeFeedback_NotAckedWhenStopping(t *testing.T) {
	a, calls := storeBackedFeedbackAdapterWithCalls(t)
	taskID := "11111111-1111-1111-1111-111111111111"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")
	ctx, cancel := context.WithCancel(t.Context())
	a.ctx = ctx
	cancel()

	acked := 0
	a.handleNativeFeedbackSubmit(submitCallback(t, taskID, "kept"), func() { acked++ })
	a.wg.Wait()
	if acked != 0 {
		t.Fatalf("acks while stopping = %d, want 0 so Slack delivers the feedback to another GWE", acked)
	}
	if got := calls.count("chat.postEphemeral"); got != 0 {
		t.Errorf("chat.postEphemeral called %d times while stopping, want 0: another GWE stores the feedback", got)
	}
}

func TestHandleNativeFeedbackSubmit_IgnoresAStaleConsentValue(t *testing.T) {
	a, _ := storeBackedFeedbackAdapterWithCalls(t)
	taskID := "11111111-1111-1111-1111-111111111111"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")

	cb := submitCallback(t, taskID, "detail")
	cb.View.State.Values["feedback_share_block"] = map[string]goslack.BlockAction{
		"feedback_share_input": {SelectedOptions: []goslack.OptionBlockObject{{Value: "share_task_content"}}},
	}

	a.handleNativeFeedbackSubmit(cb, func() {})
	a.wg.Wait()

	if got := onlyFeedbackRow(t, a, taskID); got.ShareContent {
		t.Error("shareContent = true; the consent checkbox is retired and must not be read back")
	}
}

func TestHandleNativeFeedbackSubmit_BareSubmitStoresTheRating(t *testing.T) {
	a, _ := storeBackedFeedbackAdapterWithCalls(t)
	taskID := "11111111-1111-1111-1111-111111111111"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")

	a.handleNativeFeedbackSubmit(submitCallbackRated(t, taskID, "", feedbackRatingUp), func() {})
	a.wg.Wait()

	got := onlyFeedbackRow(t, a, taskID)
	if got.Rating != feedbackRatingUp {
		t.Errorf("rating = %q, want up", got.Rating)
	}
	if got.Comment != nil {
		t.Errorf("comment = %v, want nil on a bare submit", got.Comment)
	}
}

func TestHandleNativeFeedbackSubmit_MaxLengthCommentRoundTrips(t *testing.T) {
	a, _ := storeBackedFeedbackAdapterWithCalls(t)
	taskID := "11111111-1111-1111-1111-111111111111"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")

	comment := strings.Repeat("x", feedbackCommentMaxLength)
	a.handleNativeFeedbackSubmit(submitCallback(t, taskID, comment), func() {})
	a.wg.Wait()

	got := onlyFeedbackRow(t, a, taskID)
	if got.Comment == nil || len(*got.Comment) != feedbackCommentMaxLength {
		t.Fatalf("comment length = %v, want %d", got.Comment, feedbackCommentMaxLength)
	}
	if *got.Comment != comment {
		t.Error("comment was altered in transit")
	}
}

func TestHandleNativeFeedbackSubmit_InvalidRatingIsRefused(t *testing.T) {
	for _, rating := range []string{"", "sideways"} {
		t.Run("rating="+rating, func(t *testing.T) {
			a, calls := storeBackedFeedbackAdapterWithCalls(t)
			taskID := "11111111-1111-1111-1111-111111111111"
			seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")

			a.handleNativeFeedbackSubmit(submitCallbackRated(t, taskID, "detail", rating), func() {})
			a.wg.Wait()

			fbs, err := store.NewFeedbackRepo(a.core.Store()).Search(t.Context(), "*", 0, 0, taskID, "", "", 10, 0)
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			if len(fbs) != 0 {
				t.Errorf("persisted %d rows, want 0 for rating %q", len(fbs), rating)
			}
			if got := calls.count("chat.postEphemeral"); got != 1 {
				t.Errorf("chat.postEphemeral called %d times, want 1", got)
			}
			if got := calls.count("chat.update"); got != 0 {
				t.Errorf("chat.update called %d times, want 0 on a refused capture", got)
			}
		})
	}
}
