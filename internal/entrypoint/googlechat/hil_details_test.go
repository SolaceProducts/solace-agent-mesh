// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	chat "google.golang.org/api/chat/v1"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/hilsurface"
)

func chatApprovalSurface(t *testing.T, args map[string]any, message string, showArgs bool) json.RawMessage {
	t.Helper()
	raw, err := hilsurface.Generate(hilsurface.Request{
		RequestID:     "req-approval",
		ToolName:      "salesforce_rest_request",
		RequesterKind: hilsurface.RequesterAgent,
		RequesterName: "Support",
		ToolArgs:      args,
		Message:       message,
		ShowArgs:      showArgs,
		Timeout:       45 * time.Minute,
	})
	if err != nil {
		t.Fatalf("generate approval surface: %v", err)
	}
	return raw
}

const chatTestWebUI = "https://sam.example.com"

func attachWebUICore(t *testing.T, a *Adapter) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{
		"namespace": "test-ns", "gateway_id": a.gatewayID, "frontend_redirect_url": chatTestWebUI + "/api/v1/auth/callback",
	})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core init: %v", err)
	}
	a.core = core
}

func postedPrompt(t *testing.T, surface json.RawMessage) *chat.CardWithId {
	t.Helper()
	fake := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), fake)
	attachWebUICore(t, a)
	submitForTest(t, a, tid("task-approval"), dmMeta())

	a.OnStatusUpdate(t.Context(), tid("task-approval"), &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{
		State: a2a.TaskStateInputNeeded,
		Message: &a2a.Message{Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
			"type": "user_input_request", "request_id": "req-approval", "surface": surface,
		})}},
	}}, nil)

	var prompt *chat.CardWithId
	fake.waitFor(t, func(w []fakeWrite, _ []string) bool {
		for _, x := range w {
			if x.op != "create" {
				continue
			}
			for _, c := range fake.currentCards(x.name) {
				if c.CardId == "sam-hil-req-approval" {
					prompt = c
					return true
				}
			}
		}
		return false
	})
	return prompt
}

func paragraphs(c *chat.CardWithId) []string {
	var out []string
	for _, sec := range c.Card.Sections {
		for _, w := range sec.Widgets {
			if w.TextParagraph != nil {
				out = append(out, w.TextParagraph.Text)
			}
		}
	}
	return out
}

func buttonLabels(c *chat.CardWithId) []string {
	var out []string
	for _, sec := range c.Card.Sections {
		for _, w := range sec.Widgets {
			if w.ButtonList == nil {
				continue
			}
			for _, b := range w.ButtonList.Buttons {
				out = append(out, b.Text)
			}
		}
	}
	return out
}

func requireInOrder(t *testing.T, shown string, want ...string) {
	t.Helper()
	at := 0
	for _, w := range want {
		i := strings.Index(shown[at:], w)
		if i < 0 {
			t.Fatalf("card lacks %q in the web UI's order after offset %d:\n%s", w, at, shown)
		}
		at += i + len(w)
	}
}

func TestGoogleChatApprovalCard_ShowsWhatIsBeingApproved(t *testing.T) {
	card := postedPrompt(t, chatApprovalSurface(t, map[string]any{
		"method": "POST",
		"path":   "/sobjects/CaseComment",
		"body":   map[string]any{"IsPublished": true},
	}, "Send {{.method}} {{.path}} to Salesforce?", true))

	if card.Card.Header == nil || card.Card.Header.Title != "Approval Required" {
		t.Errorf("header = %+v, want the surface title", card.Card.Header)
	}
	requireInOrder(t, strings.Join(paragraphs(card), "\n"),
		`Agent "Support" wants to run tool: salesforce_rest_request`,
		"<b>body</b>", "{<br>"+noBreakSpace+noBreakSpace+`"IsPublished": true<br>}`,
		"<b>method</b>", "POST",
		"<b>path</b>", "/sobjects/CaseComment",
		"Send POST /sobjects/CaseComment to Salesforce?",
		"<i>Expires in 45 minutes</i>",
	)
	if got := buttonLabels(card); strings.Join(got, ",") != "Approve,Deny" {
		t.Errorf("buttons = %v, want Approve and Deny after the details", got)
	}
}

func TestGoogleChatApprovalCard_EscapesWhatAModelWrote(t *testing.T) {
	card := postedPrompt(t, chatApprovalSurface(t, map[string]any{
		`<a href="https://evil.example">key</a>`: "<script>alert(1)</script> & <b>bold</b>",
		"path":                                   `<a href="https://evil.example">your bank</a>`,
	}, "Open {{.path}}?", true))

	shown := strings.Join(paragraphs(card), "\n")
	for _, raw := range []string{"<a ", "<script>", "<b>bold</b>", "& <"} {
		if strings.Contains(shown, raw) {
			t.Errorf("card shows %q unescaped:\n%s", raw, shown)
		}
	}
	requireInOrder(t, shown,
		"<b>&lt;a href=\"https://evil.example\"&gt;key&lt;/a&gt;</b>",
		"&lt;script&gt;alert(1)&lt;/script&gt; &amp; &lt;b&gt;bold&lt;/b&gt;",
		"Open &lt;a href=\"https://evil.example\"&gt;your bank&lt;/a&gt;?",
	)
}

func TestGoogleChatQuestionCard_ShowsTheQuestionEscaped(t *testing.T) {
	card := postedPrompt(t, json.RawMessage(`{"surfaceId":"req-approval","components":[
	  {"id":"msg","component":"Text","text":{"literalString":"Pick one"},"variant":"body"},
	  {"id":"q0-label","component":"Text","text":{"literalString":"<a href=\"https://evil.example\">your bank</a> & y\nsecond line"},"variant":"h3"},
	  {"id":"q0-picker","component":"ChoicePicker","label":{"literalString":"Colour"},"variant":"mutuallyExclusive",
	    "options":[{"label":{"literalString":"Red"},"value":"Red"},{"label":{"literalString":"Blue"},"value":"Blue"}],
	    "value":{"path":"/answers/q0"}},
	  {"id":"submit-text","component":"Text","text":{"literalString":"Submit"}},
	  {"id":"submit-btn","component":"Button","child":"submit-text","variant":"primary",
	    "action":{"event":{"name":"submit","context":{"answers":{"path":"/answers"}}}}}],
	  "dataModel":{"answers":{"q0":""}}}`))

	shown := strings.Join(paragraphs(card), "\n")
	if strings.Contains(shown, "<a ") {
		t.Errorf("card shows the question's link as HTML:\n%s", shown)
	}
	const want = `&lt;a href="https://evil.example"&gt;your bank&lt;/a&gt; &amp; y<br>second line`
	if !strings.Contains(shown, want) {
		t.Errorf("card lacks the escaped question %q:\n%s", want, shown)
	}
}

func TestGoogleChatApprovalCard_ClipsALongValue(t *testing.T) {
	card := postedPrompt(t, chatApprovalSurface(t, map[string]any{"body": strings.Repeat("a", 3*chatMaxHILValueRunes)}, "", true))

	var value string
	for _, p := range paragraphs(card) {
		if strings.HasPrefix(p, "<b>body</b><br>") {
			value = strings.TrimPrefix(p, "<b>body</b><br>")
		}
	}
	if value == "" {
		t.Fatal("the card does not show the long value at all")
	}
	if n := len([]rune(value)); n > chatMaxHILValueRunes {
		t.Errorf("value is %d characters, over %d", n, chatMaxHILValueRunes)
	}
	if !strings.HasSuffix(value, "more characters; open the web UI to see the full value)") {
		t.Errorf("a clipped value does not end with the notice that it goes on")
	}
	if got := buttonLabels(card); len(got) != 2 {
		t.Errorf("buttons = %v, want the card still answerable", got)
	}
}

func tooBigForChat() map[string]any {
	args := map[string]any{}
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		args[k] = strings.Repeat(k, chatMaxHILValueRunes)
	}
	return args
}

func TestGoogleChatApprovalCard_TooMuchToShowGoesToTheWebUI(t *testing.T) {
	card := postedPrompt(t, chatApprovalSurface(t, tooBigForChat(), "", true))

	if got := buttonLabels(card); len(got) != 0 {
		t.Errorf("buttons = %v; an Approve button must not be offered without what it approves", got)
	}
	shown := strings.Join(paragraphs(card), "\n")
	if !strings.Contains(shown, "Please respond in the web UI: "+chatTestWebUI) {
		t.Errorf("card = %q, want a linked pointer to the web UI", shown)
	}
	if card.Card.Header == nil || card.Card.Header.Title != "Approval Required" {
		t.Errorf("header = %+v, want the surface title", card.Card.Header)
	}
}

func TestGoogleChatApprovalCard_HiddenArgumentsStillNameTheTool(t *testing.T) {
	card := postedPrompt(t, chatApprovalSurface(t, map[string]any{"method": "DELETE"}, "", false))

	shown := strings.Join(paragraphs(card), "\n")
	if !strings.Contains(shown, `Agent "Support" wants to run tool: salesforce_rest_request`) {
		t.Errorf("card = %q, want it to name the agent and the tool", shown)
	}
	if strings.Contains(shown, "DELETE") {
		t.Errorf("card = %q shows an argument the author hid", shown)
	}
}

func TestGoogleChatHILWebUINotice(t *testing.T) {
	if got := hilWebUINotice("https://sam.example.com"); !strings.HasSuffix(got, "https://sam.example.com") {
		t.Errorf("notice = %q, want it to end with the web UI link", got)
	}
	if got := hilWebUINotice(""); strings.Contains(got, "http") {
		t.Errorf("notice without a URL = %q", got)
	}
}
