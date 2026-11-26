// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

const mixedStatusesFixture = `{"tools":[
 {"ref":{"toolName":"get_current_time"},"status":"ok","kind":"builtin","name":"get_current_time","parameters":{"type":"object","properties":{}},"result":{"type":"object","properties":{"timezone":{"type":"string"},"time":{"type":"string"}}},"toolNode":{"usable":true}},
 {"ref":{"connector":"jira","tool":"search_issues"},"status":"ok","kind":"connector","connectorName":"jira","connectorType":"mcp","name":"search_issues","parameters":{"type":"object","properties":{"max_results":{"type":"integer"},"jql":{"type":"string"}},"required":["jql"]},"result":null,"toolNode":{"usable":true}},
 {"ref":{"toolName":"send_email"},"status":"not_found","reason":"no such tool on this platform","toolNode":{"usable":false},"suggestions":["send_mail","notify_user"]},
 {"ref":{"connector":"crm_api","tool":"createLead"},"status":"unavailable","reason":"parameters unknown: no stored specification","toolNode":{"usable":false}},
 {"ref":{"toolName":"platform_list_components"},"status":"ok","kind":"builtin","name":"platform_list_components","parameters":{"type":"object","properties":{"component_type":{"type":"string"}}},"result":{"type":"object","properties":{"count":{"type":"integer"}}},"toolNode":{"usable":false,"reason":"not bindable by built agents"}},
 {"ref":{"toolName":"hr__lookup"},"status":"not_permitted","reason":"not permitted","toolNode":{"usable":false}}
]}`

func decodeDescribed(t *testing.T, body string) []describedTool {
	t.Helper()
	var resp describeToolsResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return resp.Tools
}

func realisticDescribedTool(i int) describedTool {
	name := fmt.Sprintf("tool_%03d", i)
	return describedTool{
		Ref:        describeToolRef{ToolName: name},
		Status:     describeStatusOK,
		Kind:       "builtin",
		Name:       name,
		Parameters: json.RawMessage(`{"type":"object","properties":{"artifact":{},"image_url":{},"include_crops":{},"labels":{},"max_detections":{},"output_filename":{},"threshold":{}},"required":["image_url"]}`),
		Result:     json.RawMessage(`{"type":"object","properties":{"object_count":{},"objects":{},"summary":{}}}`),
		ToolNode:   describeToolNodeVerdict{Usable: true},
	}
}

func TestDescribedToolsPreview_MixedStatuses(t *testing.T) {
	got := describedToolsPreview(decodeDescribed(t, mixedStatusesFixture))

	const want = "get_current_time: in() out(time, timezone) toolNode=yes\n" +
		"jira.search_issues: in(jql*, max_results) out(?) toolNode=yes\n" +
		"send_email: not_found (did you mean: send_mail, notify_user)\n" +
		"crm_api.createLead: unavailable — parameters unknown: no stored specification\n" +
		"platform_list_components: in(component_type) out(count) toolNode=no (not bindable by built agents)\n" +
		"hr__lookup: not_permitted — not permitted\n"
	if got != want {
		t.Errorf("preview =\n%s\nwant\n%s", got, want)
	}
	if len(got) > previewBudgetChars {
		t.Errorf("preview is %d chars, over the %d-char budget a six-tool answer should fit", len(got), previewBudgetChars)
	}
}

func TestDescribedToolsPreview_NotFoundWithoutSuggestionsSaysWhy(t *testing.T) {
	tools := []describedTool{{
		Ref: describeToolRef{ToolName: "send_email"}, Status: describeStatusNotFound, Reason: "no such tool on this platform",
	}}
	if got, want := describedToolsPreview(tools), "send_email: not_found — no such tool on this platform\n"; got != want {
		t.Errorf("preview = %q, want %q", got, want)
	}
}

func TestDescribedToolsPreview_ListsNotes(t *testing.T) {
	tool := realisticDescribedTool(1)
	tool.Notes = []string{"not yet announced by a tool runtime"}
	got := describedToolsPreview([]describedTool{tool})
	if !strings.Contains(got, "toolNode=yes — notes: not yet announced by a tool runtime") {
		t.Errorf("preview omits the note: %s", got)
	}
}

func TestDescribedToolsPreview_ShedsOutputsThenInputsAndNeverATool(t *testing.T) {
	missing := describedTool{Ref: describeToolRef{ToolName: "send_email"}, Status: describeStatusNotFound, Reason: "no such tool on this platform"}
	var shedOut, shedIn bool
	for n := 1; n <= describeMaxRefs; n++ {
		tools := make([]describedTool, 0, n+1)
		for i := range n {
			tools = append(tools, realisticDescribedTool(i))
		}
		tools = append(tools, missing)

		preview := describedToolsPreview(tools)
		hasIn, hasOut := strings.Contains(preview, "in("), strings.Contains(preview, "out(")
		switch {
		case hasOut && !hasIn:
			t.Fatalf("%d tools: result fields kept while parameters were shed:\n%s", n, preview)
		case shedOut && hasOut:
			t.Fatalf("%d tools: result fields came back after being shed", n)
		case shedIn && hasIn:
			t.Fatalf("%d tools: parameters came back after being shed", n)
		}
		shedOut, shedIn = shedOut || !hasOut, shedIn || !hasIn

		for i := range n {
			if name := fmt.Sprintf("tool_%03d", i); !strings.Contains(preview, name) {
				t.Fatalf("%d tools: preview dropped %q:\n%s", n, name, preview)
			}
		}
		if !strings.Contains(preview, "send_email: not_found") {
			t.Fatalf("%d tools: a tool that is not ok was shed:\n%s", n, preview)
		}
		if n == 1 && !hasOut {
			t.Fatalf("one tool must render in full:\n%s", preview)
		}
		if (!hasIn || !hasOut) && !strings.Contains(preview, "load_artifact described_tools.json") {
			t.Fatalf("%d tools: detail was shed without saying where it is:\n%s", n, preview)
		}
	}
	if !shedIn {
		t.Errorf("%d tools never reached the names-only tier", describeMaxRefs)
	}
}

func TestDescribedToolsPreview_HardCapped(t *testing.T) {
	tools := make([]describedTool, 0, 400)
	for i := range 400 {
		tools = append(tools, realisticDescribedTool(i))
	}
	preview := describedToolsPreview(tools)

	if len(preview) > 4000+250 {
		t.Errorf("preview is %d chars, over the 4000-char hard cap plus its notice", len(preview))
	}
	if !strings.Contains(preview, "preview cut at 4000 characters") || strings.Contains(preview, "not every") || !strings.Contains(preview, "load_artifact described_tools.json") {
		t.Errorf("a capped preview must say it is incomplete and where the rest is: %q", preview[max(0, len(preview)-200):])
	}
	if !utf8.ValidString(preview) || !strings.HasSuffix(preview, "\n") {
		t.Errorf("capped preview is not whole lines of valid UTF-8")
	}
}

func TestDescribedToolsPreview_NoElisionLineWhenATierShedsNothing(t *testing.T) {
	var tools []describedTool
	for i := range 30 {
		tools = append(tools, describedTool{
			Ref:         describeToolRef{ToolName: fmt.Sprintf("missing_tool_%02d", i)},
			Status:      describeStatusNotFound,
			Reason:      "no such tool on this platform",
			Suggestions: []string{"first_candidate", "second_candidate", "third_candidate"},
		})
	}

	got := describedToolsPreview(tools)

	if previewChars(got) <= previewBudgetChars {
		t.Fatalf("fixture is %d chars; it must exceed the %d-char budget to reach the shedding tiers", previewChars(got), previewBudgetChars)
	}
	if strings.Contains(got, "load_artifact") {
		t.Errorf("a preview of tools that are all not ok sheds nothing, so it must not say detail was left out:\n%s", got)
	}
}

func TestDescribedToolsPreview_BudgetCountsCharactersNotBytes(t *testing.T) {
	name := strings.Repeat("é", 300)
	tool := describedTool{Ref: describeToolRef{ToolName: name}, Status: describeStatusOK, Kind: "builtin", Name: name,
		Parameters: json.RawMessage(`{"type":"object","properties":{}}`), ToolNode: describeToolNodeVerdict{Usable: true}}

	got := describedToolsPreview([]describedTool{tool})

	if len(got) <= previewBudgetChars || previewChars(got) > previewBudgetChars {
		t.Fatalf("fixture is %d bytes and %d characters; it needs more bytes than the budget and no more characters", len(got), previewChars(got))
	}
	if !strings.Contains(got, "out(") {
		t.Errorf("a preview inside the budget in characters was shed because it is over in bytes:\n%s", got)
	}
}

func TestCapPreview(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in       string
		wantKept string
		cut      bool
	}{
		{"under the cap is verbatim", "a\nb\n", "a\nb\n", false},
		{"counted in characters, so multi-byte text at the cap is not cut", strings.Repeat("é", previewHardCapChars), strings.Repeat("é", previewHardCapChars), false},
		{"cut at a whole line", strings.Repeat("x\n", previewHardCapChars), strings.Repeat("x\n", previewHardCapChars/2), true},
		{"a single line over the cap is cut and terminated", strings.Repeat("é", previewHardCapChars+50), strings.Repeat("é", previewHardCapChars) + "\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := capPreview(tc.in, "things.json")
			if !utf8.ValidString(got) {
				t.Fatalf("cut a rune in half")
			}
			if !tc.cut {
				if got != tc.in {
					t.Errorf("an uncut preview changed")
				}
				return
			}
			notice := fmt.Sprintf("(preview cut at %d characters) load_artifact things.json for the rest.\n", previewHardCapChars)
			if got != tc.wantKept+notice {
				t.Errorf("got %q\nwant %q", got[max(0, len(got)-120):], (tc.wantKept + notice)[max(0, len(tc.wantKept+notice)-120):])
			}
			if strings.Contains(got, "not every") {
				t.Errorf("the notice must say the preview was cut, not that items are missing: %s", got[len(got)-120:])
			}
		})
	}
}

func TestSchemaSignature(t *testing.T) {
	for _, tc := range []struct {
		name      string
		raw       string
		marks     bool
		want      string
		wantKnown bool
	}{
		{"sorted with required marked", `{"type":"object","properties":{"zeta":{},"alpha":{}},"required":["zeta","ghost"]}`, true, "alpha, zeta*", true},
		{"required not marked for results", `{"type":"object","properties":{"zeta":{},"alpha":{}},"required":["zeta"]}`, false, "alpha, zeta", true},
		{"no properties", `{"type":"object"}`, true, "", true},
		{"empty properties", `{"type":"object","properties":{}}`, true, "", true},
		{"null is unknown", `null`, true, "?", false},
		{"absent is unknown", ``, true, "?", false},
		{"not an object is unknown", `[1,2]`, true, "?", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := schemaSignature(json.RawMessage(tc.raw), tc.marks); got != tc.want {
				t.Errorf("schemaSignature = %q, want %q", got, tc.want)
			}
			if _, _, known := schemaProperties(json.RawMessage(tc.raw)); known != tc.wantKnown {
				t.Errorf("known = %v, want %v", known, tc.wantKnown)
			}
		})
	}
}

func TestDescribeToolsTool_ExecuteSetsPreview(t *testing.T) {
	q := &describeQuerier{response: json.RawMessage(mixedStatusesFixture)}
	res := runDescribe(t, q, map[string]any{"tools": []any{map[string]any{"tool_name": "get_current_time"}}})

	if len(res.DataObjects) != 1 || res.DataObjects[0].Preview == "" {
		t.Fatal("Preview is empty, so the framework falls back to cutting the raw JSON")
	}
	for _, want := range []string{"get_current_time: in()", "jira.search_issues: in(jql*, max_results)", "send_email: not_found"} {
		if !strings.Contains(res.DataObjects[0].Preview, want) {
			t.Errorf("Preview omits %q: %s", want, res.DataObjects[0].Preview)
		}
	}
}
