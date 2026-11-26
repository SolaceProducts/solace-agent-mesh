// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/hilsurface"
)

func teamsApprovalSurface(t *testing.T, args map[string]any) json.RawMessage {
	t.Helper()
	raw, err := hilsurface.Generate(hilsurface.Request{
		RequestID:     "req-approval",
		ToolName:      "salesforce_rest_request",
		RequesterKind: hilsurface.RequesterAgent,
		RequesterName: "Support",
		ToolArgs:      args,
		Message:       "Send {{.method}} {{.path}} to Salesforce?",
		ShowArgs:      true,
		Timeout:       45 * time.Minute,
	})
	if err != nil {
		t.Fatalf("generate approval surface: %v", err)
	}
	return raw
}

type cardText struct{ elementType, text string }

func cardTexts(t *testing.T, card Attachment) []cardText {
	t.Helper()
	content, _ := card.Content.(map[string]any)
	body, _ := content["body"].([]any)
	var out []cardText
	for _, raw := range body {
		el, _ := raw.(map[string]any)
		typ, _ := el["type"].(string)
		if s, ok := el["text"].(string); ok {
			out = append(out, cardText{typ, s})
		}
		inlines, _ := el["inlines"].([]any)
		for _, in := range inlines {
			run, _ := in.(map[string]any)
			s, _ := run["text"].(string)
			out = append(out, cardText{typ, s})
		}
	}
	return out
}

func buildApprovalCard(t *testing.T, args map[string]any) (Attachment, bool) {
	t.Helper()
	s, err := a2ui.Parse(teamsApprovalSurface(t, args))
	if err != nil {
		t.Fatal(err)
	}
	return (&TeamsAdapter{}).buildHILCard(s, "req-approval", "task-approval", "Support", false)
}

func TestTeamsApprovalCard_ShowsWhatIsBeingApproved(t *testing.T) {
	card, ok := buildApprovalCard(t, map[string]any{
		"method": "POST",
		"path":   "/sobjects/CaseComment",
		"body":   map[string]any{"IsPublished": true},
	})
	if !ok {
		t.Fatal("buildHILCard ok=false for a small approval")
	}

	var shown strings.Builder
	for _, ct := range cardTexts(t, card) {
		shown.WriteString(ct.text + "\n")
	}
	at := 0
	for _, want := range []string{
		"Approval Required",
		`Agent "Support" wants to run tool: salesforce_rest_request`,
		"body", `"IsPublished": true`,
		"method", "POST",
		"path", "/sobjects/CaseComment",
		"Send POST /sobjects/CaseComment to Salesforce?",
		"Expires in 45 minutes",
	} {
		i := strings.Index(shown.String()[at:], want)
		if i < 0 {
			t.Fatalf("card lacks %q in the web UI's order after offset %d:\n%s", want, at, shown.String())
		}
		at += i + len(want)
	}
	_ = submitActionData(t, card, HILSubmitAction)
}

func TestTeamsApprovalCard_ArgumentsAreNotMarkdown(t *testing.T) {
	card, _ := buildApprovalCard(t, map[string]any{"method": "[your bank](https://evil.example)"})
	found := false
	for _, ct := range cardTexts(t, card) {
		if !strings.Contains(ct.text, "evil.example") {
			continue
		}
		found = true
		if ct.elementType != "RichTextBlock" {
			t.Errorf("argument text is in a %s, which Teams renders as markdown", ct.elementType)
		}
	}
	if !found {
		t.Error("the card does not show the argument at all")
	}
}

func TestTeamsApprovalCard_ClipsALongValue(t *testing.T) {
	card, ok := buildApprovalCard(t, map[string]any{"body": strings.Repeat("a", 3*teamsMaxHILValueRunes)})
	if !ok {
		t.Fatal("a card with one long value should still be answerable in Teams")
	}
	found := false
	for _, ct := range cardTexts(t, card) {
		if !strings.HasPrefix(ct.text, "aaa") {
			continue
		}
		found = true
		if len([]rune(ct.text)) > teamsMaxHILValueRunes {
			t.Errorf("value is %d characters, over %d", len([]rune(ct.text)), teamsMaxHILValueRunes)
		}
		if !strings.Contains(ct.text, "more characters; open the web UI") {
			t.Errorf("a clipped value carries no notice that it goes on")
		}
	}
	if !found {
		t.Error("the card does not show the long value at all")
	}
}

const (
	approvalTaskID    = "task-approval"
	approvalRequestID = "req-approval"
	approvalConv      = "conv-approval"
)

func newApprovalTeamsAdapter(t *testing.T) (*TeamsAdapter, *fakeBotConnector) {
	t.Helper()
	a, _, _ := newHILTeamsAdapter(t)
	bot := newFakeBotConnector(t)
	a.botClient.apiBaseURL = bot.srv.URL + "/i0"

	db, err := store.Open(store.TestSQLiteDBURL(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := store.NewTaskRecordRepo(db, "test-gw").Upsert(t.Context(), &store.TaskRecord{TaskID: approvalTaskID, AgentName: "Support", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	a.items = store.NewEntrypointTaskItemRepo(db)

	_ = a.core.Tasks().Register(approvalTaskID, "Support", "u1", entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(approvalTaskID, &TeamsTaskMeta{ConversationID: approvalConv, ServiceURL: a.botClient.apiBaseURL, Activity: &Activity{ID: "act-1"}})
	return a, bot
}

func requestTeamsApproval(a *TeamsAdapter, surface json.RawMessage) {
	a.OnStatusUpdate(a.ctx, approvalTaskID, &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{
			State: a2a.TaskStateInputNeeded,
			Message: &a2a.Message{Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
				"type": "user_input_request", "request_id": approvalRequestID, "surface": surface,
			})}},
		},
	}, nil)
}

func teamsPromptRecorded(a *TeamsAdapter) bool {
	return a.hasItem(a.ctx, approvalTaskID, itemHILPromptedPrefix+approvalRequestID)
}

func requireOneWebUINotice(t *testing.T, bot *fakeBotConnector) Activity {
	t.Helper()
	posted := bot.postedActivities()
	if len(posted) != 1 {
		t.Fatalf("posted %d activities, want the one web-UI notice", len(posted))
	}
	notice := posted[0]
	if len(notice.Attachments) != 0 {
		t.Errorf("the notice carries %d attachments; an Approve button must not be offered", len(notice.Attachments))
	}
	if !strings.Contains(notice.Text, "web UI") {
		t.Errorf("notice = %q, want a pointer to the web UI", notice.Text)
	}
	return notice
}

func tooBigForTeams() map[string]any {
	args := map[string]any{}
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		args[k] = strings.Repeat(k, teamsMaxHILValueRunes)
	}
	return args
}

func TestTeamsApprovalCard_TooMuchToShowGoesToTheWebUI(t *testing.T) {
	if _, ok := buildApprovalCard(t, tooBigForTeams()); ok {
		t.Fatal("buildHILCard ok=true for details past the card budget")
	}
	a, bot := newApprovalTeamsAdapter(t)

	requestTeamsApproval(a, teamsApprovalSurface(t, tooBigForTeams()))

	requireOneWebUINotice(t, bot)
	if !teamsPromptRecorded(a) {
		t.Error("prompt not recorded after its notice was delivered")
	}
}

func TestTeamsApprovalCard_RefusedCardGoesToTheWebUI(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	a, bot := newApprovalTeamsAdapter(t)
	bot.failNextPost()

	requestTeamsApproval(a, teamsApprovalSurface(t, map[string]any{"method": "POST"}))

	notice := requireOneWebUINotice(t, bot)
	if !strings.HasSuffix(notice.Text, "https://sam.example.com") {
		t.Errorf("notice = %q, want it to link the web UI", notice.Text)
	}
	if notice.Conversation.ID != approvalConv {
		t.Errorf("notice posted to %q, want the task's conversation %q", notice.Conversation.ID, approvalConv)
	}
	if !teamsPromptRecorded(a) {
		t.Error("prompt not recorded after its notice was delivered; a redelivery would post it again")
	}
}

func TestTeamsApprovalCard_UndeliveredNoticeLeavesThePromptUnrecorded(t *testing.T) {
	a, bot := newApprovalTeamsAdapter(t)
	bot.failNextPost()
	bot.failNextPost()

	requestTeamsApproval(a, teamsApprovalSurface(t, map[string]any{"method": "POST"}))

	if posted := bot.postedActivities(); len(posted) != 0 {
		t.Errorf("posted %d activities, want none", len(posted))
	}
	if teamsPromptRecorded(a) {
		t.Error("prompt recorded as posted although nothing reached Teams")
	}
}

func TestTeamsProactiveApproval_PointsAtTheWebUIWhenItCannotBeShown(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	for name, tc := range map[string]struct {
		args   map[string]any
		refuse bool
	}{
		"refused":     {args: map[string]any{"method": "POST"}, refuse: true},
		"over budget": {args: tooBigForTeams()},
	} {
		t.Run(name, func(t *testing.T) {
			a, bot := newApprovalTeamsAdapter(t)
			if tc.refuse {
				bot.failNextPost()
			}
			a.deliverProactiveHIL(
				a2a.NotifyDeliver{RequestID: "req-notify", Mode: a2a.NotifyModeInput, Surface: teamsApprovalSurface(t, tc.args)},
				&TeamsTaskMeta{ConversationID: approvalConv, ServiceURL: a.botClient.apiBaseURL},
			)

			if notice := requireOneWebUINotice(t, bot); !strings.HasSuffix(notice.Text, "https://sam.example.com") {
				t.Errorf("notice = %q, want it to link the web UI", notice.Text)
			}
		})
	}
}

func TestTeamsProactiveApproval_ActivityTextIsTheTitle(t *testing.T) {
	a, bot := newApprovalTeamsAdapter(t)

	a.deliverProactiveHIL(
		a2a.NotifyDeliver{RequestID: "req-notify", Mode: a2a.NotifyModeInput, Surface: teamsApprovalSurface(t, map[string]any{"method": "POST"})},
		&TeamsTaskMeta{ConversationID: approvalConv, ServiceURL: a.botClient.apiBaseURL},
	)

	posted := bot.postedActivities()
	if len(posted) != 1 || len(posted[0].Attachments) != 1 {
		t.Fatalf("posted %d activities, want one card", len(posted))
	}
	if posted[0].Text != "Approval Required" {
		t.Errorf("activity text = %q, want the title alone", posted[0].Text)
	}
}

func TestTeamsApprovalCard_ActivityTextIsTheTitle(t *testing.T) {
	a, bot := newApprovalTeamsAdapter(t)

	requestTeamsApproval(a, teamsApprovalSurface(t, map[string]any{"method": "POST"}))

	posted := bot.postedActivities()
	if len(posted) != 1 || len(posted[0].Attachments) != 1 {
		t.Fatalf("posted %d activities, want one card", len(posted))
	}
	if posted[0].Text != "Approval Required" {
		t.Errorf("activity text = %q, want the title alone", posted[0].Text)
	}
}

func TestTeamsHILWebUINotice(t *testing.T) {
	if got := hilWebUINotice("https://sam.example.com"); !strings.HasSuffix(got, "https://sam.example.com") {
		t.Errorf("notice = %q, want it to end with the web UI link", got)
	}
	if got := hilWebUINotice(""); strings.Contains(got, "http") {
		t.Errorf("notice without a URL = %q", got)
	}
}
