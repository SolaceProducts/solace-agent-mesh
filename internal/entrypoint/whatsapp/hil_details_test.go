// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/hilsurface"
)

const approvalPhone = "15551230400"

func waApprovalSurface(t *testing.T, args map[string]any, message string, showArgs bool) json.RawMessage {
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

type sentMessage struct {
	Type string `json:"type"`
	Text struct {
		Body string `json:"body"`
	} `json:"text"`
	Interactive interactivePayload `json:"interactive"`
}

func (m sentMessage) body() string {
	if m.Type == "interactive" {
		return m.Interactive.Body.Text
	}
	return m.Text.Body
}

func askApproval(t *testing.T, env *promptEnv, a *Adapter, surface json.RawMessage) []sentMessage {
	t.Helper()
	_ = a.core.Tasks().Register(taskT1, promptAgent, "whatsapp:"+approvalPhone, entrypoint.TaskIdentity{SessionID: "sess-approval"})
	a.core.Tasks().SetMetadata(taskT1, &taskMeta{FromPhone: approvalPhone, SessionID: "sess-approval"})
	if err := a.taskItems().writeDestination(t.Context(), taskT1, approvalPhone, "sess-approval"); err != nil {
		t.Fatalf("writeDestination: %v", err)
	}
	a.OnStatusUpdate(t.Context(), taskT1, inputNeededEvent(t, "req-approval", surface), nil)
	waitQueueDrained(t, a)
	var out []sentMessage
	for _, raw := range env.mock.all() {
		var m sentMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("decode sent message: %v", err)
		}
		out = append(out, m)
	}
	return out
}

func requireOneMessage(t *testing.T, sent []sentMessage) sentMessage {
	t.Helper()
	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent))
	}
	return sent[0]
}

func requireInOrder(t *testing.T, shown string, want ...string) {
	t.Helper()
	at := 0
	for _, w := range want {
		i := strings.Index(shown[at:], w)
		if i < 0 {
			t.Fatalf("prompt lacks %q in the web UI's order after offset %d:\n%s", w, at, shown)
		}
		at += i + len(w)
	}
}

func TestWhatsAppApproval_ShowsWhatIsBeingApprovedAndApproves(t *testing.T) {
	env := newPromptEnv(t)
	agent := subscribeAgentRequests(t, env.brk, "wa-approval-q")
	a := env.start(t)

	prompt := requireOneMessage(t, askApproval(t, env, a, waApprovalSurface(t, map[string]any{
		"method": "POST",
		"path":   "/sobjects/CaseComment",
	}, "Send {{.method}} {{.path}} to Salesforce?", true)))

	if prompt.Type != "interactive" || prompt.Interactive.Type != "button" {
		t.Fatalf("prompt type = %q/%q, want interactive buttons", prompt.Type, prompt.Interactive.Type)
	}
	requireInOrder(t, prompt.body(),
		"Approval Required",
		"```Agent \"Support\" wants to run tool: salesforce_rest_request```",
		"method:\n```POST```",
		"path:\n```/sobjects/CaseComment```",
		"Send POST /sobjects/CaseComment to Salesforce?",
		"_Expires in 45 minutes_",
	)
	var titles, ids []string
	for _, b := range prompt.Interactive.Action.Buttons {
		titles = append(titles, b.Reply.Title)
		ids = append(ids, b.Reply.ID)
	}
	if strings.Join(titles, ",") != "Approve,Deny" || strings.Join(ids, ",") != "act:0,act:1" {
		t.Fatalf("buttons = %v %v, want Approve and Deny", titles, ids)
	}

	pending := waitPendingPrompt(t, a, approvalPhone)
	value, msg := interactiveReply(approvalPhone, "wamid.approve", "act:0")
	msg.Context = &messageContext{From: approvalPhone, ID: pending.PromptWamid}
	value.Messages[0].Context = msg.Context
	a.deliver(value, msg)

	resumed := receiveWithin(t, env.brk, agent, 2*time.Second)
	if resumed == nil {
		t.Fatal("the tap on Approve did not reach the agent")
	}
	if payload := string(resumed.Payload); !strings.Contains(payload, `"approved":true`) || !strings.Contains(payload, `"answered"`) {
		t.Errorf("response = %s, want an answered approval", payload)
	}
}

type promptRig struct {
	env    *promptEnv
	a      *Adapter
	agent  broker.Queue
	prompt *store.WhatsAppPendingPrompt
}

func newPromptRig(t *testing.T, surface json.RawMessage) *promptRig {
	t.Helper()
	env := newPromptEnv(t)
	agent := subscribeAgentRequests(t, env.brk, "wa-reply-q")
	a := env.start(t)
	askApproval(t, env, a, surface)
	return &promptRig{env: env, a: a, agent: agent, prompt: waitPendingPrompt(t, a, approvalPhone)}
}

func (r *promptRig) typeReply(wamid, text string) {
	value, msg := textMessage(wamid, approvalPhone, text)
	r.a.deliver(value, msg)
}

func (r *promptRig) tap(wamid, replyID string) {
	value, msg := interactiveReply(approvalPhone, wamid, replyID)
	msg.Context = &messageContext{From: approvalPhone, ID: r.prompt.PromptWamid}
	value.Messages[0].Context = msg.Context
	r.a.deliver(value, msg)
}

func (r *promptRig) response(t *testing.T) string {
	t.Helper()
	msg := receiveWithin(t, r.env.brk, r.agent, 2*time.Second)
	if msg == nil {
		t.Fatal("the reply did not reach the agent")
	}
	return string(msg.Payload)
}

func (r *promptRig) requireNoResponse(t *testing.T) {
	t.Helper()
	if msg := receiveWithin(t, r.env.brk, r.agent, 300*time.Millisecond); msg != nil {
		t.Fatalf("the agent was sent %s, want nothing", msg.Payload)
	}
}

func (r *promptRig) requireStillPending(t *testing.T) {
	t.Helper()
	row := pendingPromptRow(t, r.a, approvalPhone)
	if row == nil || row.RequestID != r.prompt.RequestID {
		t.Fatalf("pending prompt = %+v, want the prompt to stay live", row)
	}
}

func (r *promptRig) requireCleared(t *testing.T) {
	t.Helper()
	if row := pendingPromptRow(t, r.a, approvalPhone); row != nil {
		t.Errorf("pending prompt %+v, want an answered prompt cleared", row)
	}
}

func (r *promptRig) sentCount(t *testing.T, want string) int {
	t.Helper()
	n := 0
	for _, raw := range r.env.mock.all() {
		var m sentMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("decode sent message: %v", err)
		}
		if strings.Contains(m.body(), want) {
			n++
		}
	}
	return n
}

func requireAnswered(t *testing.T, payload string, approved bool) {
	t.Helper()
	wantApproved := `"approved":` + strconv.FormatBool(approved)
	if !strings.Contains(payload, `"status":"answered"`) || !strings.Contains(payload, wantApproved) {
		t.Errorf("response = %s, want an answered response with %s", payload, wantApproved)
	}
	if strings.Contains(payload, "cancelled") {
		t.Errorf("response = %s, must not cancel the request", payload)
	}
}

func TestWhatsAppApproval_TypedReplyChoosesAButton(t *testing.T) {
	surface := waApprovalSurface(t, map[string]any{"method": "POST"}, "", true)
	for _, tc := range []struct {
		reply    string
		approved bool
	}{{"1", true}, {"approve", true}, {" APPROVE ", true}, {"2", false}, {"deny", false}, {" Deny ", false}} {
		t.Run(fmt.Sprintf("%q", tc.reply), func(t *testing.T) {
			rig := newPromptRig(t, surface)

			rig.typeReply("wamid.typed", tc.reply)

			requireAnswered(t, rig.response(t), tc.approved)
			rig.requireCleared(t)
		})
	}
}

func TestWhatsAppApproval_UnmatchedReplyAsksAgain(t *testing.T) {
	rig := newPromptRig(t, waApprovalSurface(t, map[string]any{"method": "POST"}, "", true))

	for i, reply := range []string{"0", "99", "maybe", "cancel"} {
		rig.typeReply(fmt.Sprintf("wamid.unmatched.%d", i), reply)

		rig.requireNoResponse(t)
		rig.requireStillPending(t)
		if got := rig.sentCount(t, "Sorry, I didn't understand."); got != i+1 {
			t.Fatalf("after %q: asked again %d times, want %d", reply, got, i+1)
		}
	}
	if rig.sentCount(t, "1. Approve\n2. Deny") != 4 {
		t.Error("each unmatched reply should be answered with the numbered choices")
	}

	rig.typeReply("wamid.valid", "1")

	requireAnswered(t, rig.response(t), true)
	rig.requireCleared(t)
}

func TestWhatsAppReply_ActionTapsAreBoundsChecked(t *testing.T) {
	approval := waApprovalSurface(t, map[string]any{"method": "POST"}, "", true)
	question := json.RawMessage(askSurfaceSingleChoiceWA)
	for _, tc := range []struct {
		name    string
		surface json.RawMessage
		id      string
		denied  bool
		notice  string
	}{
		{name: "Deny", surface: approval, id: "act:1", denied: true},
		{name: "past the last button", surface: approval, id: "act:5", notice: msgAnswerUnmatched},
		{name: "negative", surface: approval, id: "act:-1", notice: msgReplyToQuestion},
		{name: "not a number", surface: approval, id: "act:x", notice: msgReplyToQuestion},
		{name: "prompt with inputs", surface: question, id: "act:0", notice: msgAnswerUnmatched},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newPromptRig(t, tc.surface)

			rig.tap("wamid.tap", tc.id)

			if tc.denied {
				requireAnswered(t, rig.response(t), false)
				rig.requireCleared(t)
				return
			}
			rig.requireNoResponse(t)
			rig.requireStillPending(t)
			if got := rig.sentCount(t, tc.notice); got != 1 {
				t.Errorf("sent %q %d times, want the user told once", tc.notice, got)
			}
		})
	}
}

func TestActionIndexValid(t *testing.T) {
	t.Parallel()
	approval, err := a2ui.Parse(waApprovalSurface(t, map[string]any{"method": "POST"}, "", true))
	if err != nil {
		t.Fatal(err)
	}
	question, err := a2ui.Parse(json.RawMessage(askSurfaceSingleChoiceWA))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		surface *a2ui.Surface
		idx     int
		want    bool
	}{
		{"before the first button", approval, -1, false},
		{"first button", approval, 0, true},
		{"last button", approval, 1, true},
		{"past the last button", approval, 2, false},
		{"a surface with inputs has no action buttons", question, 0, false},
	} {
		if got := actionIndexValid(tc.surface, tc.idx); got != tc.want {
			t.Errorf("%s: actionIndexValid(%d) = %v, want %v", tc.name, tc.idx, got, tc.want)
		}
	}
}

func parkedPrefixRunes() int { return utf8.RuneCountInString(msgParkedReplyPrefix + "\n\n") }

func TestFitsParked_ReservesTheParkedPrefixInRunes(t *testing.T) {
	t.Parallel()
	for _, limit := range []int{waBodyTextMax, waTextBodyMax} {
		for _, r := range []string{"é", "世"} {
			fits := strings.Repeat(r, limit-parkedPrefixRunes())
			over := fits + r
			parked := func(body string) string { return msgParkedReplyPrefix + "\n\n" + body }
			if !fitsParked(fits, limit) {
				t.Errorf("limit %d, %q: a body of %d runes does not fit", limit, r, limit-parkedPrefixRunes())
			}
			if fitsParked(over, limit) {
				t.Errorf("limit %d, %q: a body one rune longer fits", limit, r)
			}
			if truncate(parked(fits), limit) != parked(fits) {
				t.Errorf("limit %d, %q: the body that fits is cut once parked", limit, r)
			}
			if truncate(parked(over), limit) == parked(over) {
				t.Errorf("limit %d, %q: the body that does not fit survives once parked", limit, r)
			}
		}
	}
}

func approvalSurfaceFillingText(t *testing.T, runes int) json.RawMessage {
	t.Helper()
	build := func(padLen int) json.RawMessage {
		return waApprovalSurface(t, map[string]any{
			"a":   strings.Repeat("a", waMaxHILValueRunes),
			"b":   strings.Repeat("b", waMaxHILValueRunes),
			"c":   strings.Repeat("c", waMaxHILValueRunes),
			"pad": strings.Repeat("p", padLen),
		}, "", true)
	}
	promptRunes := func(raw json.RawMessage) int {
		s, err := a2ui.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return utf8.RuneCountInString(textPromptBody(s))
	}
	padLen := 1 + runes - promptRunes(build(1))
	if padLen < 1 || padLen > waMaxHILValueRunes {
		t.Fatalf("cannot size the prompt to %d runes with one argument: pad %d", runes, padLen)
	}
	raw := build(padLen)
	if got := promptRunes(raw); got != runes {
		t.Fatalf("prompt is %d runes, want %d", got, runes)
	}
	return raw
}

func TestWhatsAppApproval_ParkedPromptKeepsItsChoices(t *testing.T) {
	t.Parallel()
	const phone = "15551230500"
	rig := newWindowRig(t)
	rig.heardFrom(t, phone, 25*time.Hour)
	rig.adapter.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})
	registerReplyTask(rig.adapter, taskT1, phone)
	fullest := waTextBodyMax - parkedPrefixRunes()

	tooFull, err := a2ui.Parse(approvalSurfaceFillingText(t, fullest+1))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := hilPromptPayload(tooFull); ok {
		t.Fatal("a prompt one rune too long for a parked send fits")
	}

	rig.adapter.handleUserInputRequest(taskT1, &a2a.UserInputRequestData{
		Type: "user_input_request", RequestID: "req-parked", Surface: approvalSurfaceFillingText(t, fullest),
	})
	rig.waitParked(t, phone)
	value, msg := textMessage("wamid.reopen", phone, "are you still there?")
	rig.adapter.deliver(value, msg)

	body := rig.mock.waitDelivered(t, 1)[0].Text.Body
	if !strings.HasPrefix(body, msgParkedReplyPrefix) {
		t.Errorf("delivered prompt does not open with %q", msgParkedReplyPrefix)
	}
	if !strings.HasSuffix(body, "1. Approve\n2. Deny\n\nReply with the number of your choice.") {
		t.Errorf("delivered prompt lost its tail:\n…%s", body[max(len(body)-120, 0):])
	}
	if got := utf8.RuneCountInString(body); got != waTextBodyMax {
		t.Errorf("delivered prompt is %d runes, want exactly %d", got, waTextBodyMax)
	}
}

var monospaceRE = regexp.MustCompile("(?s)```.*?```")

func TestWhatsAppApproval_ModelTextCannotFormatThePrompt(t *testing.T) {
	env := newPromptEnv(t)
	a := env.start(t)
	const hostile = "*FREE* _money_ ~gone~ `y` ```x```"

	prompt := requireOneMessage(t, askApproval(t, env, a, waApprovalSurface(t, map[string]any{
		"note":           hostile,
		"name_*starred*": "v",
	}, "Post {{.note}}?", true)))

	body := prompt.body()
	if strings.Contains(body, "```x```") {
		t.Errorf("a value's own fence closes the monospace block early:\n%s", body)
	}
	if !strings.Contains(body, "*FREE* _money_ ~gone~ `y`") {
		t.Errorf("the value is not shown as written:\n%s", body)
	}
	outside := strings.ReplaceAll(monospaceRE.ReplaceAllString(body, ""), "_Expires in 45 minutes_", "")
	if strings.ContainsAny(outside, waMarkupChars) {
		t.Errorf("formatting characters outside monospace: %q", outside)
	}
}

func TestWhatsAppApproval_LongValueIsClippedIntoATextPrompt(t *testing.T) {
	env := newPromptEnv(t)
	a := env.start(t)

	prompt := requireOneMessage(t, askApproval(t, env, a, waApprovalSurface(t, map[string]any{
		"body": strings.Repeat("a", 3*waMaxHILValueRunes),
	}, "", true)))

	if prompt.Type != "text" {
		t.Fatalf("prompt type = %q; details past an interactive body go in a text prompt", prompt.Type)
	}
	body := prompt.body()
	if strings.Contains(body, strings.Repeat("a", waMaxHILValueRunes)) {
		t.Errorf("the value is not clipped to %d characters", waMaxHILValueRunes)
	}
	requireInOrder(t, body, "body:\n```aaa", "more characters; open the web UI to see the full value)```",
		"1. Approve\n2. Deny", "Reply with the number of your choice.")
	waitPendingPrompt(t, a, approvalPhone)
}

func TestWhatsAppApproval_TooMuchToShowGoesToTheWebUI(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	env := newPromptEnv(t)
	a := env.start(t)
	args := map[string]any{}
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		args[k] = strings.Repeat(k, waMaxHILValueRunes)
	}

	notice := requireOneMessage(t, askApproval(t, env, a, waApprovalSurface(t, args, "", true)))

	if notice.Type != "text" {
		t.Errorf("notice type = %q, want plain text with no buttons", notice.Type)
	}
	if want := "Approval Required from Builder: " + waCannotShow + " Please respond in the web UI: https://sam.example.com"; notice.body() != want {
		t.Errorf("notice = %q, want %q", notice.body(), want)
	}
	if row := pendingPromptRow(t, a, approvalPhone); row != nil {
		t.Error("a prompt the user was not shown must not take their next message as its answer")
	}
}

func TestWhatsAppApproval_HiddenArgumentsStillNameTheTool(t *testing.T) {
	env := newPromptEnv(t)
	a := env.start(t)

	body := requireOneMessage(t, askApproval(t, env, a, waApprovalSurface(t, map[string]any{"method": "DELETE"}, "", false))).body()

	if !strings.Contains(body, `Agent "Support" wants to run tool: salesforce_rest_request`) {
		t.Errorf("prompt = %q, want it to name the agent and the tool", body)
	}
	if strings.Contains(body, "DELETE") {
		t.Errorf("prompt = %q shows an argument the author hid", body)
	}
}

const waCannotShow = "This agent needs input that can't be shown in WhatsApp."

func TestWhatsAppHILWebUINotice(t *testing.T) {
	t.Parallel()
	const link = "https://sam.example.com"
	for _, tc := range []struct{ name, title, agent, url, want string }{
		{"title, agent and link", "Approval Required", "Support", link,
			"Approval Required from Support: " + waCannotShow + " Please respond in the web UI: " + link},
		{"no link", "Approval Required", "Support", "",
			"Approval Required from Support: " + waCannotShow + " Please use the web UI to respond."},
		{"title only", "Approval Required", "", link,
			"Approval Required: " + waCannotShow + " Please respond in the web UI: " + link},
		{"agent only", "", "Support", link,
			"Request from Support: " + waCannotShow + " Please respond in the web UI: " + link},
		{"neither", "", "", link, waCannotShow + " Please respond in the web UI: " + link},
		{"line breaks in the title", "Approval\r\nRequired\nnow", "Support", link,
			"Approval Required now from Support: " + waCannotShow + " Please respond in the web UI: " + link},
		{"formatting in the title", "*FREE*", "Support", link,
			"```*FREE*``` from Support: " + waCannotShow + " Please respond in the web UI: " + link},
	} {
		if got := hilWebUINotice(tc.title, tc.agent, tc.url); got != tc.want {
			t.Errorf("%s: notice = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func oversizedApprovalSurface(t *testing.T) json.RawMessage {
	t.Helper()
	args := map[string]any{}
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		args[k] = strings.Repeat(k, waMaxHILValueRunes)
	}
	return waApprovalSurface(t, args, "", true)
}

type failingEnqueue struct{ senderQueue }

func (failingEnqueue) Enqueue(context.Context, dbutil.Querier, *store.WhatsAppOutbound) error {
	return errors.New("ledger unavailable")
}

func TestWhatsAppApproval_WebUIPointerThatCannotBeQueuedIsLogged(t *testing.T) {
	t.Parallel()
	logs := &recordCollector{}
	a, _ := newHILRig(t, slog.New(logs))
	registerHILTasks(a, approvalPhone, taskT1)
	a.ledger = failingEnqueue{a.ledger}

	a.handleUserInputRequest(taskT1, &a2a.UserInputRequestData{
		Type: "user_input_request", RequestID: "req-oversized", Surface: oversizedApprovalSurface(t),
	})

	for _, rec := range logs.all() {
		if rec.Message != "failed to queue the web UI pointer for an interactive prompt" {
			continue
		}
		attrs := map[string]string{}
		rec.Attrs(func(at slog.Attr) bool { attrs[at.Key] = at.Value.String(); return true })
		if rec.Level != slog.LevelError || attrs["taskID"] != taskT1 || attrs["requestID"] != "req-oversized" {
			t.Errorf("logged %v %v, want an Error naming the task and the request", rec.Level, attrs)
		}
		return
	}
	t.Error("a pointer that was never queued left the user with nothing and the log silent")
}

func TestWhatsAppApproval_WebUIPointerLeavesAnOlderLivePromptAlone(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	env := newPromptEnv(t)
	agent := subscribeAgentRequests(t, env.brk, "wa-pointer-q")
	a := env.start(t)
	askQuestion(t, a, taskT1, approvalPhone, "req-older")
	_ = a.core.Tasks().Register(taskT2, promptAgent, "whatsapp:"+approvalPhone, entrypoint.TaskIdentity{SessionID: "sess-2"})
	a.core.Tasks().SetMetadata(taskT2, &taskMeta{FromPhone: approvalPhone, SessionID: "sess-2"})

	a.handleUserInputRequest(taskT2, &a2a.UserInputRequestData{
		Type: "user_input_request", RequestID: "req-newer", Surface: oversizedApprovalSurface(t),
	})
	waitQueueDrained(t, a)

	row := pendingPromptRow(t, a, approvalPhone)
	if row == nil || row.TaskID != taskT1 || row.RequestID != "req-older" {
		t.Errorf("pending prompt = %+v, want the older question still live", row)
	}
	if msg := receiveWithin(t, env.brk, agent, 300*time.Millisecond); msg != nil {
		t.Errorf("the older task was sent %s, want it left waiting", msg.Payload)
	}
}

func TestWhatsAppApproval_ArgumentNameCannotFakeALine(t *testing.T) {
	env := newPromptEnv(t)
	a := env.start(t)

	body := requireOneMessage(t, askApproval(t, env, a, waApprovalSurface(t, map[string]any{
		"x\nfake: value":   "v",
		"y\r\nforged: row": "w",
		"z\rfaked: too":    "u",
	}, "", true))).body()

	requireInOrder(t, body, "x fake: value:\n```v```", "y forged: row:\n```w```", "z faked: too:\n```u```")
	for _, line := range strings.Split(body, "\n") {
		for _, forged := range []string{"fake:", "forged:", "faked:"} {
			if strings.HasPrefix(line, forged) {
				t.Errorf("line %q is a label the arguments faked:\n%s", line, body)
			}
		}
	}
}

func TestDetailText_LineEndings(t *testing.T) {
	t.Parallel()
	got := detailText([]a2ui.Detail{
		{Kind: a2ui.DetailParagraph, Text: "one\r\ntwo\rthree"},
		{Kind: a2ui.DetailValue, Label: "k", Text: "p\r\nq"},
		{Kind: a2ui.DetailNote, Text: "n\r\nm"},
	})
	if want := "one\ntwo\nthree\n\nk:\n```p\nq```\n\n_n\nm_"; got != want {
		t.Errorf("detailText = %q, want %q", got, want)
	}
}
