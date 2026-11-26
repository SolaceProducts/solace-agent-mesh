// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/hilsurface"
)

func approvalSurfaceFor(t *testing.T, args map[string]any) json.RawMessage {
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

type postedPrompt struct {
	blocks       string
	texts        []string
	notification string
}

func (p postedPrompt) shown() string { return strings.Join(p.texts, "\n") }

func (p postedPrompt) hasButtons() bool { return strings.Contains(p.blocks, HILInputActionPrefix) }

const (
	approvalTaskID    = "task-approval"
	approvalRequestID = "req-approval"
	approvalThreadTS  = "1700000000.1"
)

func newApprovalAdapter(t *testing.T) (*SlackGatewayAdapter, *slackAPICalls) {
	t.Helper()
	a, calls, _ := newHandlerTestAdapter(t)
	db, err := store.Open(store.TestSQLiteDBURL(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := store.NewTaskRecordRepo(db, "test-gw").Upsert(t.Context(), &store.TaskRecord{TaskID: approvalTaskID, AgentName: "Support", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	a.items = taskItems{repo: store.NewEntrypointTaskItemRepo(db), warn: testLogger().Warn, debug: testLogger().Debug}
	_ = a.core.Tasks().Register(approvalTaskID, "Support", "u1", entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(approvalTaskID, &SlackTaskMeta{ChannelID: "C123", ThreadTS: approvalThreadTS})
	return a, calls
}

func requestApproval(a *SlackGatewayAdapter, surface json.RawMessage) {
	a.OnStatusUpdate(a.ctx, approvalTaskID, &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{
			State: a2a.TaskStateInputNeeded,
			Message: &a2a.Message{Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
				"type":       "user_input_request",
				"request_id": approvalRequestID,
				"surface":    surface,
			})}},
		},
	}, nil)
}

func lastPosted(t *testing.T, calls *slackAPICalls) postedPrompt {
	t.Helper()
	texts := calls.PostTexts()
	p := postedPrompt{blocks: calls.LastPostBlocks(), notification: texts[len(texts)-1]}
	if p.blocks != "" {
		var decoded any
		if err := json.Unmarshal([]byte(p.blocks), &decoded); err != nil {
			t.Fatalf("decode blocks: %v", err)
		}
		collectTexts(decoded, &p.texts)
	}
	return p
}

func promptRecorded(a *SlackGatewayAdapter) bool {
	_, ok := a.items.get(a.ctx, approvalTaskID, itemHILPromptedPrefix+approvalRequestID)
	return ok
}

func postApprovalPrompt(t *testing.T, surface json.RawMessage) postedPrompt {
	t.Helper()
	a, calls := newApprovalAdapter(t)
	requestApproval(a, surface)
	if calls.Posts() != 1 {
		t.Fatalf("postMessage calls = %d, want 1", calls.Posts())
	}
	return lastPosted(t, calls)
}

func collectTexts(v any, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		if s, ok := t["text"].(string); ok {
			*out = append(*out, s)
		}
		for k, child := range t {
			if k != "text" || !isString(child) {
				collectTexts(child, out)
			}
		}
	case []any:
		for _, child := range t {
			collectTexts(child, out)
		}
	}
}

func isString(v any) bool { _, ok := v.(string); return ok }

func TestSlackApprovalPrompt_ShowsWhatIsBeingApproved(t *testing.T) {
	p := postApprovalPrompt(t, approvalSurfaceFor(t, map[string]any{
		"method": "POST",
		"path":   "/sobjects/CaseComment",
		"body":   map[string]any{"IsPublished": true, "CommentBody": "Fixed in 2.5"},
	}))

	shown := p.shown()
	at := 0
	for _, want := range []string{
		"Approval Required",
		`Agent "Support" wants to run tool: salesforce_rest_request`,
		"*body*",
		`"IsPublished": true`,
		"*method*",
		"POST",
		"*path*",
		"/sobjects/CaseComment",
		"Send POST /sobjects/CaseComment to Salesforce?",
		"Expires in 45 minutes",
		"Approve",
	} {
		i := strings.Index(shown[at:], want)
		if i < 0 {
			t.Fatalf("card lacks %q in the web UI's order after offset %d:\n%s", want, at, shown)
		}
		at += i + len(want)
	}
	if !p.hasButtons() {
		t.Error("card offers no buttons")
	}
	if !strings.Contains(p.notification, "salesforce_rest_request") {
		t.Errorf("notification text = %q, want it to name the tool", p.notification)
	}
}

func TestSlackApprovalPrompt_EscapesArgumentText(t *testing.T) {
	p := postApprovalPrompt(t, approvalSurfaceFor(t, map[string]any{
		"method":  "<!channel> <https://evil.example|bank> & co",
		"path":    "``````\n*not bold*",
		"<!here>": "an argument key is written by the model too",
	}))
	shown := p.shown()

	for _, raw := range []string{"<!channel>", "<https://evil.example", "<!here>"} {
		if strings.Contains(shown, raw) {
			t.Errorf("card carries %q unescaped, which Slack would act on:\n%s", raw, shown)
		}
	}
	for _, escaped := range []string{"&lt;!channel&gt;", "&amp; co", "*&lt;!here&gt;*"} {
		if !strings.Contains(shown, escaped) {
			t.Errorf("card lacks the escaped text %q:\n%s", escaped, shown)
		}
	}
	found := false
	for _, txt := range p.texts {
		if !strings.HasPrefix(txt, "*path*") {
			continue
		}
		found = true
		if strings.Count(txt, codeFence) != 2 {
			t.Errorf("a fence inside the value ends the code block early: %q", txt)
		}
	}
	if !found {
		t.Error("the card does not show the path argument at all")
	}
}

func TestSlackApprovalPrompt_EscapesEntitiesInsideACodeBlockLikeEveryOtherText(t *testing.T) {
	p := postApprovalPrompt(t, approvalSurfaceFor(t, map[string]any{"reason": "a & b > c &amp; d"}))

	for _, txt := range p.texts {
		if !strings.HasPrefix(txt, "*reason*") {
			continue
		}
		if !strings.Contains(txt, "a &amp; b &gt; c &amp;amp; d") {
			t.Errorf("the code block does not escape & and >: %q", txt)
		}
		return
	}
	t.Errorf("the card does not show the reason argument:\n%s", p.shown())
}

func TestSlackApprovalPrompt_ClipsALongValue(t *testing.T) {
	p := postApprovalPrompt(t, approvalSurfaceFor(t, map[string]any{"body": strings.Repeat("a", 2*slackMaxHILValueChars)}))

	if !strings.Contains(p.shown(), "more characters; open the web UI to see the full value") {
		t.Errorf("a clipped value carries no notice that it goes on:\n%.400s", p.shown())
	}
	if !p.hasButtons() {
		t.Error("a card with one clipped value should still be answerable in Slack")
	}
	for _, txt := range p.texts {
		if n := utf8.RuneCountInString(txt); n > slackMaxSectionChars {
			t.Errorf("a text is %d characters, over Slack's section limit of %d", n, slackMaxSectionChars)
		}
	}
}

func TestSlackApprovalPrompt_TooMuchToShowGoesToTheWebUI(t *testing.T) {
	args := map[string]any{}
	for _, k := range []string{"a", "b", "c", "d", "e", "f"} {
		args[k] = strings.Repeat(k, slackMaxHILValueChars)
	}
	p := postApprovalPrompt(t, approvalSurfaceFor(t, args))

	if p.hasButtons() {
		t.Errorf("an approval too large to show still offers buttons:\n%.400s", p.blocks)
	}
	if !strings.Contains(p.notification, "web UI") {
		t.Errorf("text = %q, want a pointer to the web UI", p.notification)
	}
}

func TestSlackApprovalPrompt_OverTheBlockLimitGoesToTheWebUI(t *testing.T) {
	args := map[string]any{}
	for i := range 60 {
		args[fmt.Sprintf("k%02d", i)] = "v"
	}
	p := postApprovalPrompt(t, approvalSurfaceFor(t, args))

	if p.hasButtons() {
		t.Errorf("an approval past the block limit still offers buttons:\n%.400s", p.blocks)
	}
	if !strings.Contains(p.notification, "web UI") {
		t.Errorf("text = %q, want a pointer to the web UI", p.notification)
	}
}

func TestSlackApprovalPrompt_RefusedPromptGoesToTheWebUI(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	a, calls := newApprovalAdapter(t)
	calls.failBlockPosts.Store(true)

	requestApproval(a, approvalSurfaceFor(t, map[string]any{"method": "POST"}))

	if calls.Posts() != 1 {
		t.Fatalf("delivered posts = %d, want the one web-UI notice", calls.Posts())
	}
	p := lastPosted(t, calls)
	if p.hasButtons() || p.blocks != "" {
		t.Errorf("the notice carries blocks: %.400s", p.blocks)
	}
	if !strings.HasSuffix(p.notification, "https://sam.example.com") {
		t.Errorf("notice = %q, want it to link the web UI", p.notification)
	}
	if ts := calls.PostThreadTSs(); ts[0] != approvalThreadTS {
		t.Errorf("notice posted to thread %q, want the task's thread %q", ts[0], approvalThreadTS)
	}
	if !promptRecorded(a) {
		t.Error("prompt not recorded after its notice was delivered; a redelivery would post it again")
	}
}

func TestSlackApprovalPrompt_UndeliveredNoticeLeavesThePromptUnrecorded(t *testing.T) {
	a, calls := newApprovalAdapter(t)
	calls.failAllPosts.Store(true)

	requestApproval(a, approvalSurfaceFor(t, map[string]any{"method": "POST"}))

	if calls.Posts() != 0 {
		t.Errorf("delivered posts = %d, want 0", calls.Posts())
	}
	if promptRecorded(a) {
		t.Error("prompt recorded as posted although nothing reached Slack")
	}
}

func TestSlackProactiveApproval_PointsAtTheWebUIWhenItCannotBeShown(t *testing.T) {
	big := map[string]any{}
	for _, k := range []string{"a", "b", "c", "d", "e", "f"} {
		big[k] = strings.Repeat(k, slackMaxHILValueChars)
	}
	for name, tc := range map[string]struct {
		args   map[string]any
		refuse bool
	}{
		"refused":     {args: map[string]any{"method": "POST"}, refuse: true},
		"over budget": {args: big},
	} {
		t.Run(name, func(t *testing.T) {
			a, calls, _ := newHandlerTestAdapter(t)
			calls.failBlockPosts.Store(tc.refuse)
			a.handleNotifyDeliver(a2a.NotifyDeliver{
				RequestID: "req-notify", Mode: a2a.NotifyModeInput, ChannelType: "slack",
				Address: slackAddr(t), Surface: approvalSurfaceFor(t, tc.args),
			})

			if calls.Posts() != 1 {
				t.Fatalf("delivered posts = %d, want the one web-UI notice", calls.Posts())
			}
			p := lastPosted(t, calls)
			if p.hasButtons() || !strings.Contains(p.notification, "web UI") {
				t.Errorf("posted %q with blocks %.200s; want a web-UI notice with no buttons", p.notification, p.blocks)
			}
		})
	}
}

func TestSlackApprovalPrompt_WithoutArgsStillNamesTheTool(t *testing.T) {
	raw, err := hilsurface.Generate(hilsurface.Request{
		RequestID: "req-approval", ToolName: "delete_record", RequesterKind: hilsurface.RequesterAgent,
		RequesterName: "Support", ToolArgs: map[string]any{"id": "500X"}, ShowArgs: false, Timeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	shown := postApprovalPrompt(t, raw).shown()

	if !strings.Contains(shown, "delete_record") {
		t.Errorf("card does not name the tool:\n%s", shown)
	}
	if strings.Contains(shown, "500X") {
		t.Errorf("card shows an argument the author hid with show_args: false:\n%s", shown)
	}
}

func TestHILWebUINotice_LinksTheWebUIWhenKnown(t *testing.T) {
	if got := hilWebUINotice("https://sam.example.com"); !strings.HasSuffix(got, "https://sam.example.com") {
		t.Errorf("notice = %q, want it to end with the web UI link", got)
	}
	if got := hilWebUINotice(""); strings.Contains(got, "http") || !strings.Contains(got, "web UI") {
		t.Errorf("notice without a URL = %q", got)
	}
}

func TestEscapeClipped_NeverSplitsAnEscape(t *testing.T) {
	got := escapeClipped(strings.Repeat("&", 1000), 500)
	if n := utf8.RuneCountInString(got); n > 500 {
		t.Errorf("clipped to %d characters, over 500", n)
	}
	body, _, _ := strings.Cut(got, "…")
	if strings.ReplaceAll(body, "&amp;", "") != "" {
		t.Errorf("clipped text splits an escape: %q", body)
	}
}
