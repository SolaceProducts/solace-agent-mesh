// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
)

const (
	twoInstanceChannel = "D1"
	twoInstanceThread  = "1700000010.000001"
)

func newSlackInstances(t *testing.T, n int, cfg SlackConfig) ([]*SlackGatewayAdapter, *slackapi.InMemorySlackAPI) {
	t.Helper()
	db := store.TestDB(t)
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	api := slackapi.NewInMemorySlackAPI()
	api.SeedUserProfile("U1", &goslack.UserProfile{Email: "u1@example.com"})
	cfg.DefaultAgentName = "TestAgent"
	cfg.RunAs = "default"

	instances := make([]*SlackGatewayAdapter, n)
	for i := range instances {
		core := entrypoint.NewGatewayCore(&recordingGWAdapter{}, map[string]any{
			"namespace":  "test-ns",
			"gateway_id": "test-gw",
		})
		core.SetServices(runtime.NewRuntime(brk, nil).Services())
		if err := core.Init(t.Context(), nil); err != nil {
			t.Fatal(err)
		}
		core.UseDatabaseForTest(db)
		if err := core.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = core.Stop(context.Background()) })
		core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

		ctx, cancel := context.WithCancel(t.Context())
		instanceCfg := cfg
		a := &SlackGatewayAdapter{
			core:             core,
			cfg:              &instanceCfg,
			api:              api,
			emailCache:       NewEmailCache(api, 0, testLogger()),
			hilDedup:         runtime.NewMapCache(),
			inFlightMentions: make(map[string]mentionSlot),
			sessKeys:         store.NewGatewaySessionKeyRepo(core.Store()),
			items:            taskItems{repo: store.NewEntrypointTaskItemRepo(core.Store()), warn: testLogger().Warn, debug: testLogger().Debug},
			logger:           testLogger(),
			ctx:              ctx,
			cancel:           cancel,
			queues:           make(map[string]*MessageQueue),
		}
		t.Cleanup(func() { _ = a.Stop(context.Background()) })
		instances[i] = a
	}
	return instances, api
}

func submitDM(t *testing.T, a *SlackGatewayAdapter) string {
	t.Helper()
	a.HandleMessage(&slackevents.MessageEvent{
		ChannelType: "im", User: "U1", Channel: twoInstanceChannel, Text: "test 7", TimeStamp: twoInstanceThread,
	}, "T1")
	ids := a.core.Tasks().AllByAgent("TestAgent")
	if len(ids) != 1 {
		t.Fatalf("submitted tasks = %v, want one", ids)
	}
	return ids[0]
}

func textStatus(text string) *a2a.TaskStatusUpdateEvent {
	return &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{
		State:   a2a.TaskStateWorking,
		Message: &a2a.Message{Role: "agent", Parts: []a2a.Part{a2a.NewTextPart(text)}},
	}}
}

func completedTask(text string) *a2a.Task {
	return &a2a.Task{Status: a2a.TaskStatus{
		State:   a2a.TaskStateCompleted,
		Message: &a2a.Message{Role: "agent", Parts: []a2a.Part{a2a.NewTextPart(text)}},
	}}
}

func inputRequired(requestID string) *a2a.TaskStatusUpdateEvent {
	return &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{
		State: a2a.TaskStateInputNeeded,
		Message: &a2a.Message{Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
			"type":       "user_input_request",
			"request_id": requestID,
			"surface":    json.RawMessage(askSurfaceJSON),
		})}},
	}}
}

func threadTexts(api *slackapi.InMemorySlackAPI) []string {
	msgs := slices.DeleteFunc(api.Messages(), func(m slackapi.StoredMessage) bool {
		return m.Channel != twoInstanceChannel || m.ThreadTS != twoInstanceThread
	})
	slices.SortFunc(msgs, func(x, y slackapi.StoredMessage) int { return strings.Compare(x.TS, y.TS) })
	texts := make([]string, len(msgs))
	for i, m := range msgs {
		texts[i] = m.Text
	}
	return texts
}

func TestTwoInstances_AnswerPostedByTheInstanceThatDidNotSubmit(t *testing.T) {
	inst, api := newSlackInstances(t, 2, SlackConfig{InitialStatusMessage: "Working on it"})
	submitting, receiving := inst[0], inst[1]
	taskID := submitDM(t, submitting)
	if got := threadTexts(api); len(got) != 1 || got[0] != "Working on it" {
		t.Fatalf("thread after submit = %q, want the status message", got)
	}

	receiving.OnStatusUpdate(t.Context(), taskID, textStatus("Hello "), nil)
	receiving.OnStatusUpdate(t.Context(), taskID, textStatus("world"), nil)
	receiving.OnFinalResponse(t.Context(), taskID, completedTask("Hello world"), nil)

	if got := threadTexts(api); len(got) != 1 || got[0] != "Hello world" {
		t.Fatalf("thread = %q, want only the answer", got)
	}
}

func TestTwoInstances_TakeoverMidAnswerEditsTheSameMessage(t *testing.T) {
	inst, api := newSlackInstances(t, 3, SlackConfig{InitialStatusMessage: "Working on it"})
	submitting, first, takeover := inst[0], inst[1], inst[2]
	taskID := submitDM(t, submitting)

	first.OnStatusUpdate(t.Context(), taskID, textStatus("Hello "), nil)
	first.queues[taskID].Flush()

	takeover.OnStatusUpdate(t.Context(), taskID, textStatus("wor"), nil)
	takeover.OnFinalResponse(t.Context(), taskID, completedTask("Hello world"), nil)

	if got := threadTexts(api); len(got) != 1 || got[0] != "Hello world" {
		t.Fatalf("thread = %q, want one message with the whole answer", got)
	}
	if posts := countCalls(api.Calls(), "PostMessage"); posts != 2 {
		t.Errorf("chat.postMessage calls = %d, want 2: the status message and the answer, edited after", posts)
	}
}

func TestTwoInstances_SubmitterTakesOverMidAnswerEditsTheSameMessage(t *testing.T) {
	inst, api := newSlackInstances(t, 2, SlackConfig{InitialStatusMessage: "Working on it"})
	submitting, receiving := inst[0], inst[1]
	taskID := submitDM(t, submitting)

	receiving.OnStatusUpdate(t.Context(), taskID, textStatus("Hello "), nil)
	receiving.queues[taskID].Flush()

	submitting.OnStatusUpdate(t.Context(), taskID, textStatus("wor"), nil)
	submitting.OnFinalResponse(t.Context(), taskID, completedTask("Hello world"), nil)

	if got := threadTexts(api); len(got) != 1 || got[0] != "Hello world" {
		t.Fatalf("thread = %q, want one message with the whole answer", got)
	}
}

func TestTwoInstances_SubmitterTakesOverMidAnswerStreamsTheWholeAnswer(t *testing.T) {
	inst, api := newSlackInstances(t, 2, SlackConfig{ShowActivityTimeline: true})
	submitting, receiving := inst[0], inst[1]
	taskID := submitDM(t, submitting)

	receiving.OnStatusUpdate(t.Context(), taskID, textStatus("Hello "), nil)
	receiving.queues[taskID].Flush()

	submitting.OnStatusUpdate(t.Context(), taskID, textStatus("wor"), nil)
	submitting.OnFinalResponse(t.Context(), taskID, completedTask("Hello world"), nil)

	var answers []string
	for _, c := range api.Calls() {
		if c.Method == "AppendStream" && strings.Contains(c.Params["chunks"], "markdown_text") {
			answers = append(answers, c.Params["chunks"])
		}
	}
	if len(answers) != 1 || !strings.Contains(answers[0], `"text":"Hello world"`) {
		t.Fatalf("answer chunks = %q, want one with the whole answer", answers)
	}
	if n := countCalls(api.Calls(), "StartStream"); n != 1 {
		t.Errorf("chat.startStream calls = %d, want 1", n)
	}
}

func TestTwoInstances_ReceiverFinishesTheSubmittersStream(t *testing.T) {
	inst, api := newSlackInstances(t, 2, SlackConfig{ShowActivityTimeline: true})
	submitting, receiving := inst[0], inst[1]
	taskID := submitDM(t, submitting)

	receiving.OnStatusUpdate(t.Context(), taskID, textStatus("Hello"), nil)
	receiving.OnFinalResponse(t.Context(), taskID, completedTask("Hello"), nil)

	if n := countCalls(api.Calls(), "StartStream"); n != 1 {
		t.Fatalf("chat.startStream calls = %d, want 1", n)
	}
	var streamTS string
	for _, c := range api.Calls() {
		switch c.Method {
		case "StartStream":
			streamTS = c.ResponseTS
		case "StopStream":
			if c.ThreadTS != streamTS && c.Params["ts"] != streamTS {
				t.Errorf("stopped stream %+v, want %s", c, streamTS)
			}
		}
	}
	if n := countCalls(api.Calls(), "StopStream"); n != 1 {
		t.Errorf("chat.stopStream calls = %d, want 1", n)
	}

	submitting.OnTaskEndedElsewhere(taskID)
	deadline := time.Now().Add(5 * time.Second)
	for {
		submitting.queuesMu.Lock()
		left := len(submitting.queues)
		submitting.queuesMu.Unlock()
		if left == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the submitting instance kept the queue of a task that ended elsewhere")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTwoInstances_InputPromptPostedOnce(t *testing.T) {
	inst, api := newSlackInstances(t, 3, SlackConfig{})
	submitting, first, second := inst[0], inst[1], inst[2]
	taskID := submitDM(t, submitting)
	before := countCalls(api.Calls(), "PostMessage")

	first.OnStatusUpdate(t.Context(), taskID, inputRequired("req-1"), nil)
	second.OnStatusUpdate(t.Context(), taskID, inputRequired("req-1"), nil)

	if posted := countCalls(api.Calls(), "PostMessage") - before; posted != 1 {
		t.Fatalf("prompts posted = %d, want 1", posted)
	}
}

func TestTwoInstances_AuthorizePromptPostedOnceAndRetired(t *testing.T) {
	inst, api := newSlackInstances(t, 3, SlackConfig{})
	submitting, first, second := inst[0], inst[1], inst[2]
	taskID := submitDM(t, submitting)
	authEvent := authRequiredEvent("https://idp.example/authorize?state=s1")
	authEvent.Status.Message.Parts[0].Data.(map[string]any)["auth_request_function_call_id"] = "call-1"
	before := countCalls(api.Calls(), "PostMessage")

	first.OnStatusUpdate(t.Context(), taskID, authEvent, nil)
	second.OnStatusUpdate(t.Context(), taskID, authEvent, nil)
	if posted := countCalls(api.Calls(), "PostMessage") - before; posted != 1 {
		t.Fatalf("authorize prompts posted = %d, want 1", posted)
	}

	second.OnFinalResponse(t.Context(), taskID, completedTask("done"), nil)
	if !slices.ContainsFunc(threadTexts(api), func(s string) bool { return s == "Authorized" }) {
		t.Fatalf("thread = %q, want the prompt retired as authorized", threadTexts(api))
	}
}

func TestTwoInstances_SubmitterTakesOverRetiresTheOtherInstancesPrompt(t *testing.T) {
	inst, api := newSlackInstances(t, 2, SlackConfig{})
	submitting, receiving := inst[0], inst[1]
	taskID := submitDM(t, submitting)
	authEvent := authRequiredEvent("https://idp.example/authorize?state=s1")
	authEvent.Status.Message.Parts[0].Data.(map[string]any)["auth_request_function_call_id"] = "call-1"

	receiving.OnStatusUpdate(t.Context(), taskID, authEvent, nil)
	submitting.OnFinalResponse(t.Context(), taskID, completedTask("done"), nil)

	if !slices.ContainsFunc(threadTexts(api), func(s string) bool { return s == "Authorized" }) {
		t.Fatalf("thread = %q, want the prompt retired as authorized", threadTexts(api))
	}
}

func TestTwoInstances_InputAnsweredOnce(t *testing.T) {
	inst, api := newSlackInstances(t, 2, SlackConfig{})
	first, second := inst[0], inst[1]
	taskID := submitDM(t, first)
	first.OnStatusUpdate(t.Context(), taskID, inputRequired("req-2"), nil)

	blocks, ok := first.buildHILBlocks(parseSurfaceT(t), "req-2", taskID, "TestAgent", false)
	if !ok {
		t.Fatal("buildHILBlocks ok=false")
	}
	click := func(a *SlackGatewayAdapter, button string) {
		a.handleHILInput(goslack.InteractionCallback{
			User:      goslack.User{ID: "U1", Name: "alice"},
			Container: goslack.Container{ChannelID: twoInstanceChannel, MessageTs: "1700000000.5"},
		}, &goslack.BlockAction{ActionID: HILInputActionPrefix + button, Value: submitButtonValue(t, blocks, button)})
	}
	api.SeedMessage(twoInstanceChannel, "1700000000.5", "prompt")
	click(first, "submit-btn")
	click(second, "cancel-btn")

	if n := countCalls(api.Calls(), "UpdateMessage"); n != 1 {
		t.Fatalf("prompt card updates = %d, want 1: the second click must be ignored", n)
	}
}

func TestSubmit_RefusedWhenTheDestinationCannotBeWritten(t *testing.T) {
	inst, api := newSlackInstances(t, 1, SlackConfig{})
	a := inst[0]
	closed, err := store.Open("sqlite:///"+filepath.Join(t.TempDir(), "closed.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = closed.Close()
	a.items.repo = store.NewEntrypointTaskItemRepo(closed)

	a.HandleMessage(&slackevents.MessageEvent{
		ChannelType: "im", User: "U1", Channel: twoInstanceChannel, Text: "test 7", TimeStamp: twoInstanceThread,
	}, "T1")

	if ids := a.core.Tasks().AllByAgent("TestAgent"); len(ids) != 0 {
		t.Fatalf("tasks left after the refusal: %v", ids)
	}
	if got := threadTexts(api); len(got) != 1 {
		t.Fatalf("thread = %q, want the refusal message", got)
	}
}

func TestEmailCache_FailedLookupNotCached(t *testing.T) {
	cache, api := newTestCache(t)
	api.SeedUserProfile("U100", &goslack.UserProfile{Email: "u100@example.com"})
	api.SetNextError("GetUserProfile", errors.New("ratelimited"))

	if id := cache.ResolveIdentity("U100", "T200"); id.UserID() != "slack:T200:U100" {
		t.Fatalf("UserID during the failure = %q, want the fallback", id.UserID())
	}
	if id := cache.ResolveIdentity("U100", "T200"); id.UserID() != "u100@example.com" {
		t.Fatalf("UserID after the failure = %q, want the email", id.UserID())
	}
}

func TestTaskItems_UnreachableDatabase(t *testing.T) {
	t.Parallel()
	closed, err := store.Open("sqlite:///"+filepath.Join(t.TempDir(), "closed.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = closed.Close()
	items := taskItems{repo: store.NewEntrypointTaskItemRepo(closed), warn: testLogger().Warn, debug: testLogger().Debug}

	ctx := t.Context()
	items.put(ctx, "t1", itemStatusMsgTS, "1.1")
	items.delete(ctx, "t1", itemArtifactMsgTSPrefix+"report.pdf")
	if _, posted := items.get(ctx, "t1", itemHILPromptedPrefix+"r1"); posted {
		t.Error("an unreadable item must read as not posted")
	}
	if won, claim := items.claimAnswer(ctx, "t1", itemHILAnsweredPrefix+"r1"); !won || claim != "" {
		t.Error("an unwritable claim must be left to the local guard, with no claim to release")
	}
	items.releaseAnswer(ctx, "t1", itemHILAnsweredPrefix+"r1", "1")
	items.answerForwarded(ctx, "t1", itemHILAnsweredPrefix+"r1", "1")
	if meta, err := items.load(ctx, "t1"); meta != nil || err == nil {
		t.Error("an unreadable task must report the read error")
	}
	if itemKind(itemArtifactMsgTSPrefix+"report.pdf") != "artifact-msg-ts" || itemKind(itemStatusMsgTS) != itemStatusMsgTS {
		t.Error("itemKind must drop the per-element part")
	}
}

type slowStreamAPI struct {
	*slackapi.InMemorySlackAPI
	release chan struct{}
}

func (s *slowStreamAPI) StartStream(ctx context.Context, channelID string, opts ...goslack.MsgOption) (string, string, error) {
	<-s.release
	return s.InMemorySlackAPI.StartStream(ctx, channelID, opts...)
}

func TestTwoInstances_SubmitterStreamOpensLate(t *testing.T) {
	inst, api := newSlackInstances(t, 2, SlackConfig{ShowActivityTimeline: true})
	submitting, receiving := inst[0], inst[1]
	slow := &slowStreamAPI{InMemorySlackAPI: api, release: make(chan struct{})}
	submitting.api = slow

	taskID := submitDM(t, submitting)
	receiving.OnStatusUpdate(t.Context(), taskID, textStatus("Hello"), nil)
	receiving.OnFinalResponse(t.Context(), taskID, completedTask("Hello"), nil)
	close(slow.release)
	submitting.queues[taskID].Flush()

	started := map[string]bool{}
	stopped := map[string]bool{}
	for _, c := range api.Calls() {
		switch c.Method {
		case "StartStream":
			started[c.ResponseTS] = true
		case "StopStream":
			stopped[c.ThreadTS] = true
		}
	}
	answers := 0
	for _, c := range api.Calls() {
		if c.Method == "AppendStream" && strings.Contains(c.Params["chunks"], `"text":"Hello"`) {
			answers++
		}
	}
	if answers != 1 {
		t.Errorf("answer written %d times, want once", answers)
	}
	for ts := range started {
		if !stopped[ts] {
			t.Errorf("stream %s was started and never stopped", ts)
		}
	}
}

func TestTwoInstances_SubmitterFinishesTheOtherStreamAfterALateStart(t *testing.T) {
	inst, api := newSlackInstances(t, 2, SlackConfig{ShowActivityTimeline: true})
	submitting, receiving := inst[0], inst[1]
	slow := &slowStreamAPI{InMemorySlackAPI: api, release: make(chan struct{})}
	submitting.api = slow

	taskID := submitDM(t, submitting)
	receiving.OnStatusUpdate(t.Context(), taskID, textStatus("Hello"), nil)
	receiving.queues[taskID].Flush()
	close(slow.release)
	submitting.queues[taskID].Flush()
	submitting.OnFinalResponse(t.Context(), taskID, completedTask("Hello"), nil)

	started := map[string]bool{}
	stopped := map[string]bool{}
	for _, c := range api.Calls() {
		switch c.Method {
		case "StartStream":
			started[c.ResponseTS] = true
		case "StopStream":
			stopped[c.ThreadTS] = true
		}
	}
	for ts := range started {
		if !stopped[ts] {
			t.Errorf("stream %s was started and never stopped", ts)
		}
	}
}
