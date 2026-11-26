// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const askSurfaceJSON = `{
  "surfaceId":"req-1",
  "components":[
    {"id":"msg","component":"Text","text":{"literalString":"Choose"},"variant":"body"},
    {"id":"q0-label","component":"Text","text":{"literalString":"Favorite color?"},"variant":"h3"},
    {"id":"q0-picker","component":"ChoicePicker","label":{"literalString":"Color"},"variant":"mutuallyExclusive",
      "options":[
        {"label":{"literalString":"Red"},"value":"Red"},
        {"label":{"literalString":"Blue"},"value":"Blue"},
        {"label":{"literalString":"Other"},"value":"__other__"}],
      "value":{"path":"/answers/q0"}},
    {"id":"q0-other","component":"TextField","label":{"literalString":"Please specify"},"value":{"path":"/other_text/q0"}},
    {"id":"submit-text","component":"Text","text":{"literalString":"Submit"}},
    {"id":"submit-btn","component":"Button","child":"submit-text","variant":"primary",
      "action":{"event":{"name":"submit","context":{"answers":{"path":"/answers"},"other_text":{"path":"/other_text"}}}}},
    {"id":"cancel-text","component":"Text","text":{"literalString":"Cancel"}},
    {"id":"cancel-btn","component":"Button","child":"cancel-text","action":{"event":{"name":"cancel","context":{}}}}
  ],
  "dataModel":{"answers":{"q0":""},"other_text":{"q0":""}}
}`

func parseSurfaceT(t *testing.T) *a2ui.Surface {
	t.Helper()
	s, err := a2ui.Parse(json.RawMessage(askSurfaceJSON))
	if err != nil {
		t.Fatalf("parse surface: %v", err)
	}
	return s
}

func submitButtonValue(t *testing.T, blocks []goslack.Block, buttonID string) string {
	t.Helper()
	for _, b := range blocks {
		ab, ok := b.(*goslack.ActionBlock)
		if !ok || ab.Elements == nil {
			continue
		}
		for _, el := range ab.Elements.ElementSet {
			btn, ok := el.(*goslack.ButtonBlockElement)
			if !ok {
				continue
			}
			if strings.HasSuffix(btn.ActionID, buttonID) {
				return btn.Value
			}
		}
	}
	t.Fatalf("no button ending in %q found", buttonID)
	return ""
}

func TestSlackBuildHILBlocks(t *testing.T) {
	a := &SlackGatewayAdapter{}
	s := parseSurfaceT(t)

	blocks, ok := a.buildHILBlocks(s, "req-1", "task-1", "TestAgent", false)
	if !ok {
		t.Fatal("buildHILBlocks returned ok=false")
	}

	var inputs, actions int
	for _, b := range blocks {
		switch b.(type) {
		case *goslack.InputBlock:
			inputs++
		case *goslack.ActionBlock:
			actions++
		}
	}
	if inputs != 2 {
		t.Errorf("input blocks = %d, want 2", inputs)
	}
	if actions != 1 {
		t.Errorf("action blocks = %d, want 1", actions)
	}

	var val slackHILValue
	if err := json.Unmarshal([]byte(submitButtonValue(t, blocks, "submit-btn")), &val); err != nil {
		t.Fatalf("unmarshal submit value: %v", err)
	}
	if val.RequestID != "req-1" || val.TaskID != "task-1" || val.AgentName != "TestAgent" {
		t.Errorf("correlation tuple wrong: %+v", val)
	}
	if val.Cancel {
		t.Error("submit button marked cancel")
	}
	if len(val.Fields) != 2 {
		t.Errorf("fields = %d, want 2", len(val.Fields))
	}
	if _, ok := val.Context["answers"]; !ok {
		t.Errorf("submit context missing answers binding: %v", val.Context)
	}

	var cancelVal slackHILValue
	if err := json.Unmarshal([]byte(submitButtonValue(t, blocks, "cancel-btn")), &cancelVal); err != nil {
		t.Fatalf("unmarshal cancel value: %v", err)
	}
	if !cancelVal.Cancel {
		t.Error("cancel button not flagged cancel")
	}
}

func q2recv(t *testing.T, brk *broker.DevBroker, q broker.Queue) map[string]any {
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

func TestSlackHandleHILInput_Answered(t *testing.T) {
	a, _, brk := newHandlerTestAdapter(t)

	s := parseSurfaceT(t)
	blocks, ok := a.buildHILBlocks(s, "req-ans", "task-ans", "TestAgent", false)
	if !ok {
		t.Fatal("buildHILBlocks ok=false")
	}
	submitVal := submitButtonValue(t, blocks, "submit-btn")

	q, err := brk.CreateQueue("hil-ans", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "TestAgent")); err != nil {
		t.Fatal(err)
	}

	callback := goslack.InteractionCallback{
		User:      goslack.User{ID: "U1", Name: "alice"},
		Container: goslack.Container{ChannelID: "C123", MessageTs: "1700000000.1"},
		BlockActionState: &goslack.BlockActionStates{
			Values: map[string]map[string]goslack.BlockAction{
				"q0-picker": {"q0-picker-input": {SelectedOption: goslack.OptionBlockObject{Value: "Blue"}}},
			},
		},
	}
	a.handleHILInput(callback, &goslack.BlockAction{ActionID: HILInputActionPrefix + "submit-btn", Value: submitVal})

	uir := q2recv(t, brk, q)
	if uir["status"] != "answered" {
		t.Errorf("status = %v, want answered (uir=%v)", uir["status"], uir)
	}
	respData, _ := uir["data"].(map[string]any)
	answers, _ := respData["answers"].(map[string]any)
	if answers["q0"] != "Blue" {
		t.Errorf("answers.q0 = %v, want Blue (data=%v)", answers["q0"], respData)
	}
}

func TestSlackHandleHILInput_AnswersAsTheChannelPolicyPrincipal(t *testing.T) {
	for name, tc := range map[string]struct {
		channelID      string
		userInChannels bool
		wantRunAs      bool
	}{
		"channel":                    {channelID: "C123", wantRunAs: true},
		"direct message":             {channelID: "D123"},
		"channel, own identity kept": {channelID: "C123", userInChannels: true},
	} {
		t.Run(name, func(t *testing.T) {
			a, _, brk := newHandlerTestAdapter(t)
			a.cfg.RunAs = "ops"
			a.cfg.UseUserIdentityInChannels = tc.userInChannels
			blocks, ok := a.buildHILBlocks(parseSurfaceT(t), "req-"+name, "task-"+name, "TestAgent", false)
			if !ok {
				t.Fatal("buildHILBlocks ok=false")
			}
			q, err := brk.CreateQueue("hil-runas-"+name, broker.QueueOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "TestAgent")); err != nil {
				t.Fatal(err)
			}

			a.handleHILInput(goslack.InteractionCallback{
				User:      goslack.User{ID: "U1", Name: "alice"},
				Container: goslack.Container{ChannelID: tc.channelID, MessageTs: "1700000000.1"},
			}, &goslack.BlockAction{ActionID: HILInputActionPrefix + "cancel-btn", Value: submitButtonValue(t, blocks, "cancel-btn")})

			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			msg, err := brk.Receive(ctx, q)
			if err != nil {
				t.Fatalf("no broker message: %v", err)
			}
			want := "test@test.com"
			if tc.wantRunAs {
				want = rbac.SystemUserPrincipal("ops")
			}
			if got := a2a.DecodeUserProperties(msg.UserProperties).UserID; got != want {
				t.Errorf("answered as %q, want %q", got, want)
			}
		})
	}
}

func TestSlackHandleHILInput_Cancelled(t *testing.T) {
	a, _, brk := newHandlerTestAdapter(t)
	s := parseSurfaceT(t)
	blocks, _ := a.buildHILBlocks(s, "req-cxl", "task-cxl", "TestAgent", false)
	cancelVal := submitButtonValue(t, blocks, "cancel-btn")

	q, err := brk.CreateQueue("hil-cxl", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "TestAgent")); err != nil {
		t.Fatal(err)
	}

	callback := goslack.InteractionCallback{
		User:      goslack.User{ID: "U1", Name: "bob"},
		Container: goslack.Container{ChannelID: "C123", MessageTs: "1700000000.2"},
	}
	a.handleHILInput(callback, &goslack.BlockAction{ActionID: HILInputActionPrefix + "cancel-btn", Value: cancelVal})

	uir := q2recv(t, brk, q)
	if uir["status"] != "cancelled" {
		t.Errorf("status = %v, want cancelled", uir["status"])
	}
}

func TestSlackHandleHILInput_Dedup(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	s := parseSurfaceT(t)
	blocks, _ := a.buildHILBlocks(s, "req-dup", "task-dup", "TestAgent", false)
	submitVal := submitButtonValue(t, blocks, "submit-btn")

	callback := goslack.InteractionCallback{
		User:      goslack.User{ID: "U1", Name: "alice"},
		Container: goslack.Container{ChannelID: "C123", MessageTs: "1700000000.3"},
		BlockActionState: &goslack.BlockActionStates{
			Values: map[string]map[string]goslack.BlockAction{
				"q0-picker": {"q0-picker-input": {SelectedOption: goslack.OptionBlockObject{Value: "Red"}}},
			},
		},
	}
	action := &goslack.BlockAction{ActionID: HILInputActionPrefix + "submit-btn", Value: submitVal}
	a.handleHILInput(callback, action)
	a.handleHILInput(callback, action)

	if got := calls.Updates(); got != 1 {
		t.Errorf("chat.update calls = %d, want 1 (second click deduped)", got)
	}
}

func TestSlackCollectHILState(t *testing.T) {
	a := &SlackGatewayAdapter{}
	fields := []slackHILField{
		{Block: "q0", Action: "q0-input", Path: "/answers/q0", Kind: a2ui.FieldMultiChoice},
		{Block: "q1", Action: "q1-input", Path: "/answers/q1", Kind: a2ui.FieldSingleChoice},
		{Block: "q1-other", Action: "q1-other-input", Path: "/other_text/q1", Kind: a2ui.FieldText},
	}
	cb := goslack.InteractionCallback{
		BlockActionState: &goslack.BlockActionStates{
			Values: map[string]map[string]goslack.BlockAction{
				"q0":       {"q0-input": {SelectedOptions: []goslack.OptionBlockObject{{Value: "a"}, {Value: "b"}}}},
				"q1":       {"q1-input": {SelectedOption: goslack.OptionBlockObject{Value: "__other__"}}},
				"q1-other": {"q1-other-input": {Value: "freeform"}},
			},
		},
	}

	dm := a.collectHILState(cb, fields)

	multi, ok := a2ui.GetPath(dm, "/answers/q0").([]any)
	if !ok || len(multi) != 2 || multi[0] != "a" || multi[1] != "b" {
		t.Errorf("multi-select = %v, want [a b]", a2ui.GetPath(dm, "/answers/q0"))
	}
	if got := a2ui.GetPath(dm, "/answers/q1"); got != "__other__" {
		t.Errorf("single-select = %v, want __other__", got)
	}
	if got := a2ui.GetPath(dm, "/other_text/q1"); got != "freeform" {
		t.Errorf("other-text = %v, want freeform", got)
	}
}

func TestSlackBuildHILBlocks_DegradesOnLongOptionValue(t *testing.T) {
	long := strings.Repeat("x", 80)
	surfaceJSON := `{
      "surfaceId":"req-long",
      "components":[
        {"id":"q0-label","component":"Text","text":{"literalString":"Pick"},"variant":"h3"},
        {"id":"q0-picker","component":"ChoicePicker","variant":"mutuallyExclusive",
          "options":[{"label":{"literalString":"long"},"value":"` + long + `"}],
          "value":{"path":"/answers/q0"}},
        {"id":"submit-text","component":"Text","text":{"literalString":"Submit"}},
        {"id":"submit-btn","component":"Button","child":"submit-text",
          "action":{"event":{"name":"submit","context":{"answers":{"path":"/answers"}}}}}
      ],
      "dataModel":{"answers":{"q0":""}}
    }`
	s, err := a2ui.Parse(json.RawMessage(surfaceJSON))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	a := &SlackGatewayAdapter{}
	if _, ok := a.buildHILBlocks(s, "req-long", "task-long", "Agent", false); ok {
		t.Error("buildHILBlocks ok=true for a >75-char option value; want degrade (false)")
	}
}

func TestSlackOnStatusUpdate_InputRequired_PostsPrompt(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	taskID := "task-os-1"
	_ = a.core.Tasks().Register(taskID, "TestAgent", "u1", entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskID, &SlackTaskMeta{ChannelID: "C123", ThreadTS: "1700000000.1"})

	ev := &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{
			State: a2a.TaskStateInputNeeded,
			Message: &a2a.Message{Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
				"type":       "user_input_request",
				"request_id": "req-os",
				"surface":    json.RawMessage(askSurfaceJSON),
			})}},
		},
	}
	a.OnStatusUpdate(a.ctx, taskID, ev, nil)

	if calls.Posts() != 1 {
		t.Errorf("postMessage calls = %d, want 1 (input-required must post a prompt)", calls.Posts())
	}
}
