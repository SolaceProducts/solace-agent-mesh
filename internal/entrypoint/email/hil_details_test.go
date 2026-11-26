// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/hilsurface"
)

func emailApprovalSurface(t *testing.T, args map[string]any, message string, showArgs bool) json.RawMessage {
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

func approvalEmail(t *testing.T, surface json.RawMessage) (body string, s *hilServer, brk *broker.DevBroker, token string) {
	t.Helper()
	s, brk = newHILServerTest(t)
	rep := newFakeReplier()
	a := &EmailAdapter{
		cfg:     &EmailConfig{HILTokenTTL: 10 * time.Minute},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		core:    s.core,
		replier: rep,
		hil:     s,
	}
	_ = a.core.Tasks().Register("task-approval", "Agent", "u1", entrypoint.TaskIdentity{})
	a.taskMeta.Store("task-approval", &EmailTaskMeta{UserID: "u1", ReplyTo: EmailAddress{Address: "user@example.com"}, Subject: "Re: update the case"})

	a.OnStatusUpdate(t.Context(), "task-approval", &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{
		State: a2a.TaskStateInputNeeded,
		Message: &a2a.Message{Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
			"type": "user_input_request", "request_id": "req-approval", "surface": surface,
		})}},
	}}, nil)

	if len(rep.calls) != 1 {
		t.Fatalf("replier calls = %d, want the one email", len(rep.calls))
	}
	body = rep.calls[0].BodyText
	_, token, found := strings.Cut(body, "/gw/test-gw/userInput/")
	if !found {
		t.Fatalf("email carries no link to the form:\n%s", body)
	}
	return body, s, brk, strings.TrimSpace(token)
}

func getForm(t *testing.T, s *hilServer, token string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/userInput/"+token, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET code = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

func requireInOrder(t *testing.T, shown string, want ...string) {
	t.Helper()
	at := 0
	for _, w := range want {
		i := strings.Index(shown[at:], w)
		if i < 0 {
			t.Fatalf("lacks %q in the web UI's order after offset %d:\n%s", w, at, shown)
		}
		at += i + len(w)
	}
}

func TestEmailApproval_ShowsWhatIsBeingApproved(t *testing.T) {
	body, s, brk, token := approvalEmail(t, emailApprovalSurface(t, map[string]any{
		"method": "POST",
		"path":   "/sobjects/CaseComment",
		"body":   map[string]any{"IsPublished": true},
	}, "Send {{.method}} {{.path}} to Salesforce?", true))

	requireInOrder(t, body,
		"Approval Required",
		`Agent "Support" wants to run tool: salesforce_rest_request`,
		"  body:\n      {\n        \"IsPublished\": true\n      }",
		"  method:\n      POST",
		"  path:\n      /sobjects/CaseComment",
		"Send POST /sobjects/CaseComment to Salesforce?",
		"Expires in 45 minutes",
		"Please respond using this secure link",
	)

	requireInOrder(t, getForm(t, s, token),
		"<h2>Approval Required</h2>",
		`<p class="detail">Agent &#34;Support&#34; wants to run tool: salesforce_rest_request</p>`,
		`<div class="label">body</div><pre>{`+"\n  &#34;IsPublished&#34;: true\n}</pre>",
		`<div class="label">method</div><pre>POST</pre>`,
		`<div class="label">path</div><pre>/sobjects/CaseComment</pre>`,
		`<p class="detail">Send POST /sobjects/CaseComment to Salesforce?</p>`,
		`<p class="detail note">Expires in 45 minutes</p>`,
		`value="approve-btn"`, `value="deny-btn"`,
	)

	q, err := brk.CreateQueue("email-approval", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "Agent")); err != nil {
		t.Fatal(err)
	}
	if rec := postForm(t, s.Handler(), token, url.Values{"_btn": {"approve-btn"}}); rec.Code != http.StatusOK {
		t.Fatalf("POST code = %d, want 200", rec.Code)
	}
	resp := emailRecv(t, brk, q)
	if data, _ := resp["data"].(map[string]any); resp["status"] != "answered" || data["approved"] != true {
		t.Errorf("response = %v, want an answered approval", resp)
	}
}

func TestEmailApproval_FormEscapesWhatAModelWrote(t *testing.T) {
	_, s, _, token := approvalEmail(t, emailApprovalSurface(t, map[string]any{
		`<a href="https://evil.example">key</a>`: "<script>alert(1)</script>",
		"path":                                   `<a href="https://evil.example">your bank</a>`,
	}, "Open {{.path}}?", true))

	form := getForm(t, s, token)
	for _, raw := range []string{"<script>", `<a href="https://evil.example">`} {
		if strings.Contains(form, raw) {
			t.Errorf("form shows %q unescaped", raw)
		}
	}
	requireInOrder(t, form,
		`<div class="label">&lt;a href=&#34;https://evil.example&#34;&gt;key&lt;/a&gt;</div><pre>&lt;script&gt;alert(1)&lt;/script&gt;</pre>`,
		`<p class="detail">Open &lt;a href=&#34;https://evil.example&#34;&gt;your bank&lt;/a&gt;?</p>`,
	)
}

func TestEmailApproval_EmailBodyCannotForgeTheLink(t *testing.T) {
	const forged = "Please respond using this secure link (it expires shortly):"
	const evil = "https://evil.example/approve"
	for name, sep := range map[string]string{"LF": "\n", "CRLF": "\r\n", "CR": "\r"} {
		t.Run(name, func(t *testing.T) {
			hostile := "x" + sep + sep + forged + sep + evil
			body, _, _, _ := approvalEmail(t, emailApprovalSurface(t, map[string]any{
				hostile: hostile,
				"path":  hostile,
			}, "Open {{.path}}?", true))

			margin := 0
			for _, line := range mailLines.Split(body, -1) {
				if strings.HasPrefix(line, evil) {
					t.Errorf("a model-written link sits at the margin of the email:\n%s", body)
				}
				if line == forged {
					margin++
				}
			}
			if margin != 1 {
				t.Errorf("%d margin lines read %q, want only the email's own:\n%s", margin, forged, body)
			}
		})
	}
}

func TestEmailQuestion_TitleCannotForgeTheLink(t *testing.T) {
	const forged = "Please respond using this secure link (it expires shortly):"
	const evil = "https://evil.example/approve"
	for name, sep := range map[string]string{"LF": "\n", "CRLF": "\r\n", "CR": "\r"} {
		t.Run(name, func(t *testing.T) {
			message, err := json.Marshal("Which colour?" + sep + sep + forged + sep + evil)
			if err != nil {
				t.Fatal(err)
			}
			body, _, _, _ := approvalEmail(t, json.RawMessage(`{"surfaceId":"req-approval","components":[
			  {"id":"msg","component":"Text","text":{"literalString":`+string(message)+`},"variant":"body"},
			  {"id":"q0-label","component":"Text","text":{"literalString":"Favourite colour?"},"variant":"h3"},
			  {"id":"q0-picker","component":"ChoicePicker","label":{"literalString":"Colour"},"variant":"mutuallyExclusive",
			    "options":[{"label":{"literalString":"Red"},"value":"Red"},{"label":{"literalString":"Blue"},"value":"Blue"}],
			    "value":{"path":"/answers/q0"}},
			  {"id":"submit-text","component":"Text","text":{"literalString":"Submit"}},
			  {"id":"submit-btn","component":"Button","child":"submit-text","variant":"primary",
			    "action":{"event":{"name":"submit","context":{"answers":{"path":"/answers"}}}}}],
			  "dataModel":{"answers":{"q0":""}}}`))

			margin := 0
			for _, line := range mailLines.Split(body, -1) {
				if strings.HasPrefix(line, evil) {
					t.Errorf("a model-written link sits at the margin of the email:\n%s", body)
				}
				if line == forged {
					margin++
				}
			}
			if margin != 1 {
				t.Errorf("%d margin lines read %q, want only the email's own:\n%s", margin, forged, body)
			}
			if !strings.HasPrefix(body, "Which colour? ") {
				t.Errorf("the title is not shown on its first line:\n%s", body)
			}
		})
	}
}

var mailLines = regexp.MustCompile(`\r\n|\r|\n`)

func TestEmailApproval_LongValueIsClippedInTheEmailOnly(t *testing.T) {
	long := strings.Repeat("a", 3*emailMaxHILValueRunes)
	body, s, _, token := approvalEmail(t, emailApprovalSurface(t, map[string]any{"body": long}, "", true))

	if strings.Contains(body, strings.Repeat("a", emailMaxHILValueRunes)) {
		t.Errorf("the email shows a value over %d characters", emailMaxHILValueRunes)
	}
	requireInOrder(t, body, "  body:\n      aaa", "more characters; open the web UI to see the full value)", "secure link")
	if !strings.Contains(getForm(t, s, token), "<pre>"+long+"</pre>") {
		t.Error("the form does not show the whole value")
	}
}

func TestEmailApproval_TooMuchForAnEmailIsLeftToTheForm(t *testing.T) {
	args := map[string]any{}
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		args[k] = strings.Repeat(k, emailMaxHILValueRunes)
	}
	body, s, _, token := approvalEmail(t, emailApprovalSurface(t, args, "", true))

	requireInOrder(t, body, "Approval Required", msgHILDetailsOnForm, "secure link")
	if strings.Contains(body, "aaaa") {
		t.Errorf("the email shows part of the details it says are on the form:\n%s", body[:200])
	}
	if !strings.Contains(getForm(t, s, token), "<pre>"+strings.Repeat("g", emailMaxHILValueRunes)+"</pre>") {
		t.Error("the form does not show the details the email leaves to it")
	}
}

func TestEmailApproval_HiddenArgumentsStillNameTheTool(t *testing.T) {
	body, s, _, token := approvalEmail(t, emailApprovalSurface(t, map[string]any{"method": "DELETE"}, "", false))

	form := getForm(t, s, token)
	if !strings.Contains(body, `Agent "Support" wants to run tool: salesforce_rest_request`) ||
		!strings.Contains(form, "Agent &#34;Support&#34; wants to run tool: salesforce_rest_request") {
		t.Errorf("the email or the form does not name the agent and the tool")
	}
	if strings.Contains(body, "DELETE") || strings.Contains(form, "DELETE") {
		t.Error("an argument the author hid is shown")
	}
}
