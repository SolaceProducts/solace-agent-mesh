// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2ui

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const askSurfaceSingleChoice = `{
  "surfaceId": "req-1",
  "catalogId": "https://a2ui.org/specification/v0_9/basic_catalog.json",
  "components": [
    {"id":"msg","component":"Text","text":{"literalString":"Pick one"},"variant":"body"},
    {"id":"q0-label","component":"Text","text":{"literalString":"Favorite color?"},"variant":"h3"},
    {"id":"q0-picker","component":"ChoicePicker","label":{"literalString":"Color"},"variant":"mutuallyExclusive",
      "options":[
        {"label":{"literalString":"Red"},"value":"Red","description":{"literalString":""}},
        {"label":{"literalString":"Blue"},"value":"Blue","description":{"literalString":""}},
        {"label":{"literalString":"Other"},"value":"__other__"}
      ],
      "value":{"path":"/answers/q0"}},
    {"id":"q0-other","component":"TextField","label":{"literalString":"Please specify"},"value":{"path":"/other_text/q0"}},
    {"id":"submit-text","component":"Text","text":{"literalString":"Submit"}},
    {"id":"submit-btn","component":"Button","child":"submit-text","variant":"primary",
      "action":{"event":{"name":"submit","context":{"answers":{"path":"/answers"},"other_text":{"path":"/other_text"}}}}},
    {"id":"cancel-text","component":"Text","text":{"literalString":"Cancel"}},
    {"id":"cancel-btn","component":"Button","child":"cancel-text",
      "action":{"event":{"name":"cancel","context":{}}}}
  ],
  "dataModel": {"answers":{"q0":""},"other_text":{"q0":""}}
}`

func TestParse_AskSingleChoice(t *testing.T) {
	s, err := Parse(json.RawMessage(askSurfaceSingleChoice))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Title != "Pick one" {
		t.Errorf("Title = %q, want %q", s.Title, "Pick one")
	}
	if s.Question != "Favorite color?" {
		t.Errorf("Question = %q, want %q", s.Question, "Favorite color?")
	}
	if len(s.Fields) != 2 {
		t.Fatalf("Fields = %d, want 2", len(s.Fields))
	}
	picker := s.Fields[0]
	if picker.Kind != FieldSingleChoice {
		t.Errorf("picker.Kind = %v, want SingleChoice", picker.Kind)
	}
	if picker.Label != "Color" {
		t.Errorf("picker.Label = %q, want Color", picker.Label)
	}
	if !picker.HasOther() {
		t.Errorf("picker.HasOther() = false, want true")
	}
	if len(picker.Options) != 3 {
		t.Errorf("picker options = %d, want 3", len(picker.Options))
	}
	other := s.Fields[1]
	if !other.IsOtherTextField() {
		t.Errorf("field 1 should be the other-text field (path %q)", other.Path)
	}

	if len(s.Buttons) != 2 {
		t.Fatalf("Buttons = %d, want 2", len(s.Buttons))
	}
	if s.Buttons[0].Label != "Submit" || s.Buttons[0].Event != "submit" {
		t.Errorf("submit button = %+v", s.Buttons[0])
	}
	if !s.Buttons[1].IsCancel() {
		t.Errorf("button 1 should be cancel")
	}

	dm := s.BuildDataModel()
	SetPath(dm, picker.Path, "Blue")
	status, data := s.BuildResponse(s.Buttons[0], dm)
	if status != "answered" {
		t.Fatalf("status = %q, want answered", status)
	}
	wantAnswers := map[string]any{"q0": "Blue"}
	gotAnswers, _ := data["answers"].(map[string]any)
	if !reflect.DeepEqual(gotAnswers, wantAnswers) {
		t.Errorf("answers = %v, want %v", gotAnswers, wantAnswers)
	}

	status, data = s.BuildResponse(s.Buttons[1], s.BuildDataModel())
	if status != "cancelled" || data != nil {
		t.Errorf("cancel = (%q, %v), want (cancelled, nil)", status, data)
	}
}

const approvalSurface = `{
  "surfaceId":"hil-approval-1",
  "components":[
    {"id":"info","component":"Text","text":{"literalString":"Agent wants to run a tool"},"variant":"body"},
    {"id":"approve-text","component":"Text","text":{"literalString":"Approve"}},
    {"id":"approve-btn","component":"Button","child":"approve-text","variant":"primary",
      "action":{"event":{"name":"submit","context":{"approved":true}}}},
    {"id":"deny-text","component":"Text","text":{"literalString":"Deny"}},
    {"id":"deny-btn","component":"Button","child":"deny-text",
      "action":{"event":{"name":"submit","context":{"approved":false}}}}
  ],
  "dataModel":{}
}`

func TestParse_Approval(t *testing.T) {
	s, err := Parse(json.RawMessage(approvalSurface))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(s.Fields) != 0 {
		t.Errorf("Fields = %d, want 0 (approval has no inputs)", len(s.Fields))
	}
	if len(s.Buttons) != 2 {
		t.Fatalf("Buttons = %d, want 2", len(s.Buttons))
	}
	_, data := s.BuildResponse(s.Buttons[0], s.BuildDataModel())
	if approved, _ := data["approved"].(bool); !approved {
		t.Errorf("approve data = %v, want approved:true", data)
	}
	_, data = s.BuildResponse(s.Buttons[1], s.BuildDataModel())
	if approved, ok := data["approved"].(bool); !ok || approved {
		t.Errorf("deny data = %v, want approved:false", data)
	}
}

const volumeSurface = `{
  "surfaceId":"volume-prompt-ws",
  "components":[
    {"id":"title","component":"Text","text":{"literalString":"Volume Required"},"variant":"h3"},
    {"id":"volume-picker","component":"ChoicePicker","label":{"literalString":"Available Volumes"},"variant":"mutuallyExclusive",
      "options":[
        {"label":{"literalString":"vol-a"},"value":"id-a"},
        {"label":{"literalString":"Other (enter ID manually)"},"value":"__other__"}
      ],
      "value":{"path":"/selected_volume"}},
    {"id":"manual-id","component":"TextField","label":{"literalString":"Volume ID"},"value":{"path":"/manual_volume_id"}},
    {"id":"submit-text","component":"Text","text":{"literalString":"Select"}},
    {"id":"submit-btn","component":"Button","child":"submit-text","variant":"primary",
      "action":{"event":{"name":"submit","context":{"selected_volume":{"path":"/selected_volume"},"manual_volume_id":{"path":"/manual_volume_id"}}}}},
    {"id":"cancel-text","component":"Text","text":{"literalString":"Cancel"}},
    {"id":"cancel-btn","component":"Button","child":"cancel-text","action":{"event":{"name":"cancel","context":{}}}}
  ],
  "dataModel":{"selected_volume":"","manual_volume_id":""}
}`

func TestParse_VolumePrompt(t *testing.T) {
	s, err := Parse(json.RawMessage(volumeSurface))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(s.Fields) != 2 {
		t.Fatalf("Fields = %d, want 2", len(s.Fields))
	}
	dm := s.BuildDataModel()
	SetPath(dm, "/selected_volume", "id-a")
	_, data := s.BuildResponse(s.Buttons[0], dm)
	if got, _ := data["selected_volume"].(string); got != "id-a" {
		t.Errorf("selected_volume = %q, want id-a", got)
	}
	if _, ok := data["manual_volume_id"]; !ok {
		t.Errorf("manual_volume_id key missing from %v", data)
	}
}

func TestExtractRequest(t *testing.T) {
	event := &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{
			State: a2a.TaskStateInputNeeded,
			Message: &a2a.Message{
				Parts: []a2a.Part{
					{Kind: "text", Text: "ignore"},
					{Kind: "data", Data: map[string]any{
						"type":       "user_input_request",
						"request_id": "req-9",
						"surface":    json.RawMessage(askSurfaceSingleChoice),
					}},
				},
			},
		},
	}
	req, ok := ExtractRequest(event)
	if !ok {
		t.Fatal("ExtractRequest ok = false, want true")
	}
	if req.RequestID != "req-9" {
		t.Errorf("RequestID = %q, want req-9", req.RequestID)
	}

	event.Status.State = a2a.TaskStateWorking
	if _, ok := ExtractRequest(event); ok {
		t.Error("ExtractRequest ok = true for working state, want false")
	}
}

func TestParse_QuestionOnlyWhenDistinct(t *testing.T) {
	t.Run("no second heading", func(t *testing.T) {
		s, err := Parse(json.RawMessage(approvalSurface))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if s.Question != "" {
			t.Errorf("Question = %q, want empty", s.Question)
		}
	})

	t.Run("heading repeats the title", func(t *testing.T) {
		raw := `{"surfaceId":"s","components":[
      {"id":"a","component":"Text","text":{"literalString":"Same text"},"variant":"body"},
      {"id":"b","component":"Text","text":{"literalString":"Same text"},"variant":"h3"}
    ]}`
		s, err := Parse(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if s.Question != "" {
			t.Errorf("Question = %q, want empty for a duplicate heading", s.Question)
		}
	})

	t.Run("title is an h3", func(t *testing.T) {
		raw := `{"surfaceId":"s","components":[
      {"id":"a","component":"Text","text":{"literalString":"Heading one"},"variant":"h3"},
      {"id":"b","component":"Text","text":{"literalString":"Heading two"},"variant":"h3"}
    ]}`
		s, err := Parse(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if s.Title != "Heading one" || s.Question != "Heading two" {
			t.Errorf("Title/Question = %q/%q, want %q/%q", s.Title, s.Question, "Heading one", "Heading two")
		}
	})
}
