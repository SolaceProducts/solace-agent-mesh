// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const teamsAskSurfaceJSON = `{
  "surfaceId":"req-1",
  "components":[
    {"id":"msg","component":"Text","text":{"literalString":"Choose"},"variant":"body"},
    {"id":"q0-label","component":"Text","text":{"literalString":"Favorite color?"},"variant":"h3"},
    {"id":"q0-picker","component":"ChoicePicker","label":{"literalString":"Color"},"variant":"mutuallyExclusive",
      "options":[{"label":{"literalString":"Red"},"value":"Red"},{"label":{"literalString":"Blue"},"value":"Blue"},{"label":{"literalString":"Other"},"value":"__other__"}],
      "value":{"path":"/answers/q0"}},
    {"id":"q0-other","component":"TextField","value":{"path":"/other_text/q0"}},
    {"id":"submit-text","component":"Text","text":{"literalString":"Submit"}},
    {"id":"submit-btn","component":"Button","child":"submit-text","variant":"primary",
      "action":{"event":{"name":"submit","context":{"answers":{"path":"/answers"},"other_text":{"path":"/other_text"}}}}},
    {"id":"cancel-text","component":"Text","text":{"literalString":"Cancel"}},
    {"id":"cancel-btn","component":"Button","child":"cancel-text","action":{"event":{"name":"cancel","context":{}}}}
  ],
  "dataModel":{"answers":{"q0":""},"other_text":{"q0":""}}
}`

func parseTeamsSurfaceT(t *testing.T) *a2ui.Surface {
	t.Helper()
	s, err := a2ui.Parse(json.RawMessage(teamsAskSurfaceJSON))
	if err != nil {
		t.Fatalf("parse surface: %v", err)
	}
	return s
}

func newHILTeamsAdapter(t *testing.T) (*TeamsAdapter, *broker.DevBroker, *atomic.Int32) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "oauth2"):
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "expires_in": 3600})
		case strings.HasSuffix(r.URL.Path, "/v3/conversations"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "dm-conv-1"})
		default:
			posts.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "activity-1"})
		}
	}))
	t.Cleanup(srv.Close)

	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
	})
	rt := runtime.NewRuntime(brk, nil)
	core.SetServices(rt.Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := core.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	a := newTestAdapter(t)
	a.core = core
	a.ctx = t.Context()
	a.hilDedup = runtime.NewMapCache()
	a.botClient = NewBotClient(a.cfg.AppID, a.cfg.AppPassword, a.cfg.TenantID, a.logger)
	a.botClient.apiBaseURL = srv.URL
	a.emailCache = NewEmailCache("", 0, a.botClient, a.logger)
	return a, brk, &posts
}

func submitActionData(t *testing.T, card Attachment, actionName string) map[string]any {
	t.Helper()
	content, _ := card.Content.(map[string]any)
	actions, _ := content["actions"].([]any)
	for _, raw := range actions {
		act, _ := raw.(map[string]any)
		data, _ := act["data"].(map[string]any)
		if data["action"] == actionName {
			return data
		}
	}
	t.Fatalf("no action %q in card", actionName)
	return nil
}

func TestTeamsBuildHILCard(t *testing.T) {
	a := &TeamsAdapter{}
	s := parseTeamsSurfaceT(t)
	card, _ := a.buildHILCard(s, "req-1", "task-1", "TestAgent", false)

	content, _ := card.Content.(map[string]any)
	body, _ := content["body"].([]any)
	var choiceSets, textInputs int
	for _, raw := range body {
		el, _ := raw.(map[string]any)
		switch el["type"] {
		case "Input.ChoiceSet":
			choiceSets++
		case "Input.Text":
			textInputs++
		}
	}
	if choiceSets != 1 {
		t.Errorf("Input.ChoiceSet count = %d, want 1", choiceSets)
	}
	if textInputs != 1 {
		t.Errorf("Input.Text count = %d, want 1", textInputs)
	}

	submit := submitActionData(t, card, HILSubmitAction)
	if submit["qmeta"] == nil || submit["ctx"] == nil {
		t.Errorf("submit data missing qmeta/ctx: %v", submit)
	}
	if submit["requestId"] != "req-1" || submit["targetAgentName"] != "TestAgent" {
		t.Errorf("submit routing wrong: %v", submit)
	}
	_ = submitActionData(t, card, HILCancelAction)
}

func teamsRecv(t *testing.T, brk *broker.DevBroker, q broker.Queue) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("no broker message: %v", err)
	}
	var req a2a.Request
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		t.Fatal(err)
	}
	params, _ := req.Params.(map[string]any)
	msgData, _ := params["message"].(map[string]any)
	parts, _ := msgData["parts"].([]any)
	part, _ := parts[0].(map[string]any)
	data, _ := part["data"].(map[string]any)
	return data
}

func TestTeamsHandleHILSubmit_Answered(t *testing.T) {
	a, brk, _ := newHILTeamsAdapter(t)
	s := parseTeamsSurfaceT(t)
	card, _ := a.buildHILCard(s, "req-ans", "task-ans", "TestAgent", false)
	submit := submitActionData(t, card, HILSubmitAction)
	submit["hil_0"] = "o1"

	value, _ := json.Marshal(submit)
	activity := &Activity{Type: ActivityTypeInvoke, Value: value, From: Account{ID: "U1"}}

	q, err := brk.CreateQueue("teams-ans", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "TestAgent")); err != nil {
		t.Fatal(err)
	}

	a.handleHILSubmit(activity)

	uir := teamsRecv(t, brk, q)
	if uir["status"] != "answered" {
		t.Errorf("status = %v, want answered (uir=%v)", uir["status"], uir)
	}
	respData, _ := uir["data"].(map[string]any)
	answers, _ := respData["answers"].(map[string]any)
	if answers["q0"] != "Blue" {
		t.Errorf("answers.q0 = %v, want Blue (synthetic o1 must map back)", answers["q0"])
	}
}

func TestTeamsHandleHILSubmit_MultiSelectOptionContainingCommaSurvivesRoundTrip(t *testing.T) {
	a, brk, _ := newHILTeamsAdapter(t)
	const surfaceJSON = `{
      "surfaceId":"req-multi",
      "components":[
        {"id":"q0-label","component":"Text","text":{"literalString":"Teams?"},"variant":"h3"},
        {"id":"q0-picker","component":"ChoicePicker","variant":"multipleSelection",
          "options":[
            {"label":{"literalString":"Marketing, Sales"},"value":"Marketing, Sales"},
            {"label":{"literalString":"Eng"},"value":"Eng"}],
          "value":{"path":"/answers/q0"}},
        {"id":"submit-text","component":"Text","text":{"literalString":"Submit"}},
        {"id":"submit-btn","component":"Button","child":"submit-text",
          "action":{"event":{"name":"submit","context":{"answers":{"path":"/answers"}}}}}
      ],
      "dataModel":{"answers":{"q0":[]}}
    }`
	s, err := a2ui.Parse(json.RawMessage(surfaceJSON))
	if err != nil {
		t.Fatal(err)
	}
	card, _ := a.buildHILCard(s, "req-multi", "task-multi", "TestAgent", false)
	submit := submitActionData(t, card, HILSubmitAction)
	submit["hil_0"] = "o0,o1"
	value, _ := json.Marshal(submit)

	q, err := brk.CreateQueue("teams-multi", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "TestAgent")); err != nil {
		t.Fatal(err)
	}
	a.handleHILSubmit(&Activity{Type: ActivityTypeInvoke, Value: value, From: Account{ID: "U1"}})

	uir := teamsRecv(t, brk, q)
	respData, _ := uir["data"].(map[string]any)
	answers, _ := respData["answers"].(map[string]any)
	got, _ := answers["q0"].([]any)
	if len(got) != 2 || got[0] != "Marketing, Sales" || got[1] != "Eng" {
		t.Errorf("answers.q0 = %v, want [\"Marketing, Sales\" \"Eng\"] (comma in value must survive)", answers["q0"])
	}
}

func TestTeamsHandleHILSubmit_OtherFreeText(t *testing.T) {
	a, brk, _ := newHILTeamsAdapter(t)
	s := parseTeamsSurfaceT(t)
	card, _ := a.buildHILCard(s, "req-other", "task-other", "TestAgent", false)
	submit := submitActionData(t, card, HILSubmitAction)
	submit["hil_0"] = "o2"
	submit["hil_1"] = "Chartreuse"
	value, _ := json.Marshal(submit)

	q, err := brk.CreateQueue("teams-other", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "TestAgent")); err != nil {
		t.Fatal(err)
	}
	a.handleHILSubmit(&Activity{Type: ActivityTypeInvoke, Value: value, From: Account{ID: "U1"}})

	uir := teamsRecv(t, brk, q)
	respData, _ := uir["data"].(map[string]any)
	answers, _ := respData["answers"].(map[string]any)
	if answers["q0"] != "__other__" {
		t.Errorf("answers.q0 = %v, want __other__", answers["q0"])
	}
	other, _ := respData["other_text"].(map[string]any)
	if other["q0"] != "Chartreuse" {
		t.Errorf("other_text.q0 = %v, want Chartreuse", other["q0"])
	}
}

func TestTeamsHandleHILCancel(t *testing.T) {
	a, brk, _ := newHILTeamsAdapter(t)
	s := parseTeamsSurfaceT(t)
	card, _ := a.buildHILCard(s, "req-cxl", "task-cxl", "TestAgent", false)
	cancel := submitActionData(t, card, HILCancelAction)
	value, _ := json.Marshal(cancel)
	activity := &Activity{Type: ActivityTypeInvoke, Value: value, From: Account{ID: "U1"}}

	q, err := brk.CreateQueue("teams-cxl", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "TestAgent")); err != nil {
		t.Fatal(err)
	}

	a.handleHILCancel(activity)

	uir := teamsRecv(t, brk, q)
	if uir["status"] != "cancelled" {
		t.Errorf("status = %v, want cancelled", uir["status"])
	}
}

func TestTeamsHandleHILSubmit_Dedup(t *testing.T) {
	a, brk, _ := newHILTeamsAdapter(t)
	s := parseTeamsSurfaceT(t)
	card, _ := a.buildHILCard(s, "req-dup", "task-dup", "TestAgent", false)
	submit := submitActionData(t, card, HILSubmitAction)
	submit["hil_0"] = "o0"
	value, _ := json.Marshal(submit)
	activity := &Activity{Type: ActivityTypeInvoke, Value: value, From: Account{ID: "U1"}}

	q, err := brk.CreateQueue("teams-dup", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "TestAgent")); err != nil {
		t.Fatal(err)
	}

	a.handleHILSubmit(activity)
	a.handleHILSubmit(activity)

	_ = teamsRecv(t, brk, q)
	ctx, cancel := context.WithTimeout(t.Context(), 700*time.Millisecond)
	defer cancel()
	if _, err := brk.Receive(ctx, q); err == nil {
		t.Error("second (duplicate) submit should not publish a response")
	}
}

func TestTeamsCapabilities(t *testing.T) {
	a := &TeamsAdapter{}
	if !a.Capabilities()[tool.CapInteractiveUserInput] {
		t.Error("Teams must advertise interactive_user_input:true")
	}
}

func TestTeamsOnStatusUpdate_InputRequired_PostsCard(t *testing.T) {
	a, _, posts := newHILTeamsAdapter(t)
	taskID := "task-os"
	_ = a.core.Tasks().Register(taskID, "TestAgent", "u1", entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskID, &TeamsTaskMeta{
		ConversationID: "conv-1",
		ServiceURL:     a.botClient.apiBaseURL,
		Activity:       &Activity{ID: "act-1"},
	})

	ev := &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{
			State: a2a.TaskStateInputNeeded,
			Message: &a2a.Message{Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
				"type":       "user_input_request",
				"request_id": "req-os",
				"surface":    json.RawMessage(teamsAskSurfaceJSON),
			})}},
		},
	}
	a.OnStatusUpdate(a.ctx, taskID, ev, nil)

	if posts.Load() == 0 {
		t.Error("input-required OnStatusUpdate must post an Adaptive Card")
	}
}

func TestTeamsHandleAdaptiveCardAction_RoutesHILSubmit(t *testing.T) {
	a, brk, _ := newHILTeamsAdapter(t)
	s := parseTeamsSurfaceT(t)
	card, _ := a.buildHILCard(s, "req-route", "task-route", "TestAgent", false)
	submit := submitActionData(t, card, HILSubmitAction)
	submit["hil_0"] = "o1"
	value, _ := json.Marshal(submit)

	q, err := brk.CreateQueue("teams-route", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "TestAgent")); err != nil {
		t.Fatal(err)
	}

	a.handleAdaptiveCardAction(&Activity{Type: ActivityTypeInvoke, Value: value, From: Account{ID: "U1"}})

	uir := teamsRecv(t, brk, q)
	if uir["status"] != "answered" {
		t.Errorf("status = %v, want answered (routed via handleAdaptiveCardAction)", uir["status"])
	}
}
