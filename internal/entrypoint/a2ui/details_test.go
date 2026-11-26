// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2ui_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/hilsurface"
)

func TestParse_ApprovalKeepsWhatIsBeingApproved(t *testing.T) {
	raw, err := hilsurface.Generate(hilsurface.Request{
		RequestID:     "req-1",
		ToolName:      "salesforce_rest_request",
		RequesterKind: hilsurface.RequesterAgent,
		RequesterName: "Support",
		ToolArgs:      map[string]any{"method": "POST", "body": map[string]any{"IsPublished": true}},
		Message:       "Send {{.method}}?",
		ShowArgs:      true,
		Timeout:       45 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}

	s, err := a2ui.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Title != "Approval Required" {
		t.Errorf("Title = %q", s.Title)
	}
	want := []a2ui.Detail{
		{Kind: a2ui.DetailParagraph, Text: `Agent "Support" wants to run tool: salesforce_rest_request`},
		{Kind: a2ui.DetailValue, Label: "body", Text: "{\n  \"IsPublished\": true\n}"},
		{Kind: a2ui.DetailValue, Label: "method", Text: "POST"},
		{Kind: a2ui.DetailParagraph, Text: "Send POST?"},
		{Kind: a2ui.DetailNote, Text: "Expires in 45 minutes"},
	}
	if !reflect.DeepEqual(s.Details, want) {
		t.Errorf("Details =\n%#v\nwant\n%#v", s.Details, want)
	}
	if len(s.Buttons) != 2 || len(s.Fields) != 0 {
		t.Errorf("buttons = %d, fields = %d; want 2 and 0", len(s.Buttons), len(s.Fields))
	}
	if got := s.Summary(); got != `Approval Required: Agent "Support" wants to run tool: salesforce_rest_request` {
		t.Errorf("Summary = %q", got)
	}
}

func TestParse_QuestionSurfaceHasNoDetails(t *testing.T) {
	s, err := a2ui.Parse(json.RawMessage(`{
	  "surfaceId":"req-1",
	  "components":[
	    {"id":"msg","component":"Text","text":{"literalString":"Pick one"},"variant":"body"},
	    {"id":"q0-label","component":"Text","text":{"literalString":"Favorite color?"},"variant":"h3"},
	    {"id":"q0-picker","component":"ChoicePicker","variant":"mutuallyExclusive","options":[{"label":{"literalString":"Red"},"value":"Red"}],"value":{"path":"/answers/q0"}},
	    {"id":"submit-text","component":"Text","text":{"literalString":"Submit"}},
	    {"id":"submit-btn","component":"Button","child":"submit-text","action":{"event":{"name":"submit","context":{}}}},
	    {"id":"main-col","component":"Column","children":{"explicitList":["msg","q0-label","q0-picker","submit-btn"]}},
	    {"id":"root","component":"Card","child":"main-col"}
	  ]}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(s.Details) != 0 {
		t.Errorf("Details = %#v, want none", s.Details)
	}
	if s.Summary() != "Pick one" {
		t.Errorf("Summary = %q, want the title alone", s.Summary())
	}
}

func TestParse_ChildrenShapes(t *testing.T) {
	for name, children := range map[string]string{
		"explicitList": `{"explicitList":["k","v"]}`,
		"array":        `["k","v"]`,
		"template":     `{"template":{"componentId":"x","dataBinding":"/rows"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			s, err := a2ui.Parse(json.RawMessage(`{"surfaceId":"s","components":[
			  {"id":"k","component":"Text","text":{"literalString":"region"},"variant":"caption"},
			  {"id":"v","component":"Text","text":{"literalString":"eu-west"},"variant":"body"},
			  {"id":"pair","component":"Column","children":` + children + `}]}`))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if name == "template" {
				for _, d := range s.Details {
					if d.Kind == a2ui.DetailValue {
						t.Errorf("an unrecognised children shape produced a labelled value: %#v", d)
					}
				}
				return
			}
			want := []a2ui.Detail{{Kind: a2ui.DetailValue, Label: "region", Text: "eu-west"}}
			if !reflect.DeepEqual(s.Details, want) {
				t.Errorf("Details = %#v, want %#v", s.Details, want)
			}
		})
	}
}

func TestClipNotice(t *testing.T) {
	if got := a2ui.ClipNotice(1200); got != "… (1200 more characters; open the web UI to see the full value)" {
		t.Errorf("ClipNotice = %q", got)
	}
}

func TestClip(t *testing.T) {
	if got := a2ui.Clip("héllo", 5); got != "héllo" {
		t.Errorf("Clip of a short value = %q, want it unchanged", got)
	}
	got := a2ui.Clip(strings.Repeat("é", 300), 100)
	if n := len([]rune(got)); n > 100 {
		t.Errorf("Clip kept %d characters, over 100", n)
	}
	kept := strings.Count(got, "é")
	if want := strings.Repeat("é", kept) + a2ui.ClipNotice(300-kept); got != want {
		t.Errorf("Clip = %q, want the kept characters then the count of the rest", got)
	}
}
