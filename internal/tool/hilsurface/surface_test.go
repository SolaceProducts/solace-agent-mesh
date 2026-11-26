// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package hilsurface

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDetectArgContentType(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"html page body", "<h1>Title</h1><p>Body</p>", "html"},
		{"html with leading whitespace", "\n  <div>x</div>", "html"},
		{"self-closing tag", "line<br/>break", "html"},
		{"full document with doctype", "<!DOCTYPE html><html><body>x</body></html>", "html"},
		{"tag with attributes", `<div class="page">x</div>`, "html"},
		{"java generic stays text", "Map<String,Object>", "text"},
		{"cpp generic stays text", "vector<int> items", "text"},
		{"angle-bracketed url stays text", "see <https://example.com> for details", "text"},
		{"comparison expression stays text", "retry if attempts<max>0 else stop", "text"},
		{"json object string", `{"key": "value"}`, "json"},
		{"json array string", `[1, 2, 3]`, "json"},
		{"malformed json object stays text", `{not valid json}`, "text"},
		{"json scalar stays text", "42", "text"},
		{"plain prose", "Just some plain text.", "text"},
		{"empty string", "", "text"},
		{"whitespace only", "   \n  ", "text"},
		{"map value is json", map[string]any{"a": 1}, "json"},
		{"slice value is json", []any{"a", "b"}, "json"},
		{"bool value is text", true, "text"},
		{"number value is text", float64(3.14), "text"},
		{"nil value is text", nil, "text"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectArgContentType(tc.value); got != tc.want {
				t.Errorf("detectArgContentType(%#v) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func approvalMessage(t *testing.T, req Request) string {
	t.Helper()
	raw, err := Generate(req)
	if err != nil {
		t.Fatal(err)
	}
	var surface struct {
		Components []struct {
			ID   string `json:"id"`
			Text struct {
				LiteralString string `json:"literalString"`
			} `json:"text"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &surface); err != nil {
		t.Fatal(err)
	}
	for _, c := range surface.Components {
		if c.ID == "message" {
			return c.Text.LiteralString
		}
	}
	t.Fatal("no message component")
	return ""
}

func TestALiteralMessageIsShownAsWrittenAndNeverExecuted(t *testing.T) {
	fanOut := make([]any, 200)
	for i := range fanOut {
		fanOut[i] = strings.Repeat("x", 100)
	}
	literal := `Pay {{.amount}}? {{range $.rows}}{{range $.rows}}{{range $.rows}}{{.}}{{end}}{{end}}{{end}}`
	start := time.Now()
	got := approvalMessage(t, Request{RequestID: "r1", ToolName: "pay", LiteralMessage: literal,
		ToolArgs: map[string]any{"amount": 120, "rows": fanOut}, Timeout: time.Minute})
	if got != literal || time.Since(start) > 5*time.Second {
		t.Fatalf("literal message rendered as %q", got)
	}
}

func TestALiteralMessageIsCapped(t *testing.T) {
	got := approvalMessage(t, Request{RequestID: "r1", ToolName: "pay", LiteralMessage: strings.Repeat("a", 100_000), Timeout: time.Minute})
	if len(got) > maxApprovalMessageBytes || !strings.HasSuffix(got, "…") {
		t.Fatalf("a %d-byte literal message was not capped", len(got))
	}
}

func TestATemplateThatFansOutStopsWritingAtTheLimit(t *testing.T) {
	fanOut := make([]any, 400)
	for i := range fanOut {
		fanOut[i] = strings.Repeat("x", 100)
	}
	start := time.Now()
	got := renderApprovalMessage(`{{range $.rows}}{{range $.rows}}{{range $.rows}}{{.}}{{end}}{{end}}{{end}}`, map[string]any{"rows": fanOut})
	if len(got) > maxApprovalMessageBytes || !strings.HasPrefix(got, "xxxx") || time.Since(start) > 5*time.Second {
		t.Fatalf("rendered %d bytes in %s", len(got), time.Since(start))
	}
}

func TestAnApprovalWithBothMessagesIsRefused(t *testing.T) {
	if _, err := Generate(Request{RequestID: "r1", ToolName: "pay", Message: "a", LiteralMessage: "b"}); err == nil {
		t.Fatal("accepted a template and a literal message together")
	}
}
